package archive

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"glesha/cmd"
	"glesha/file_io"
	L "glesha/logger"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
)

type TarGzArchive struct {
	ID                   string
	InputPath            string
	OutputPath           string
	Info                 *file_io.FilesInfo
	Progress             *Progress
	StatusChannel        chan ArchiveStatus
	closeOnce            sync.Once
	abortReq             chan struct{}
	abortDone            chan struct{}
	GleshaWorkDir        string
	IgnoredDirs          map[string]bool
	archiveAlreadyExists bool
}

type MetaProgress struct {
	ID            string
	InputFilePath string
}

func getExistingUUIDOrDefault(inputPath string, gleshaWorkDir string, fallbackUUID string) string {
	metaFilePath := getMetaProgressFilePath(gleshaWorkDir)
	exists := file_io.Exists(metaFilePath)
	if !exists {
		return fallbackUUID
	}
	metaFile, err := os.Open(metaFilePath)
	if err != nil {
		return fallbackUUID
	}
	defer metaFile.Close()
	decoder := json.NewDecoder(metaFile)
	var metaProgress MetaProgress
	err = decoder.Decode(&metaProgress)
	if err != nil || metaProgress.InputFilePath != inputPath {
		return fallbackUUID
	}
	args := cmd.Get()
	if !args.AssumeYes {
		fmt.Printf("Existing archive exists for input path: %s  Use that instead? (y/n) (default: yes)", inputPath)
		var answer string
		fmt.Scanf("%s", &answer)
		if strings.ToLower(answer) == "no" || strings.ToLower(answer) == "n" {
			return fallbackUUID
		}
	}
	return metaProgress.ID
}

func NewTarGzArchiver(inputPath string, outputPath string, statusChannel chan ArchiveStatus) (*TarGzArchive, error) {
	err := file_io.IsReadable(inputPath)
	if err != nil {
		return nil, err
	}

	err = file_io.IsWritable(outputPath)

	if err != nil {
		return nil, err
	}

	GleshaWorkDir := filepath.Join(outputPath, ".glesha-cache")

	err = os.MkdirAll(GleshaWorkDir, os.ModePerm)
	if err != nil {
		return nil, err
	}

	progress := &Progress{0, 0, STATUS_IN_QUEUE}
	newUUID := uuid.NewString()
	id := getExistingUUIDOrDefault(inputPath, GleshaWorkDir, newUUID)
	archiveAlreadyExists := id != newUUID

	abortReq := make(chan struct{})
	abortDone := make(chan struct{})
	absGleshaWorkDir, err := filepath.Abs(GleshaWorkDir)
	if err != nil {
		return nil, err
	}
	ignoredDirs := map[string]bool{
		absGleshaWorkDir: true,
	}
	return &TarGzArchive{
		ID:                   id,
		InputPath:            inputPath,
		OutputPath:           outputPath,
		Info:                 nil,
		Progress:             progress,
		StatusChannel:        statusChannel,
		abortReq:             abortReq,
		abortDone:            abortDone,
		GleshaWorkDir:        GleshaWorkDir,
		archiveAlreadyExists: archiveAlreadyExists,
		IgnoredDirs:          ignoredDirs}, nil
}

func (tgz *TarGzArchive) UpdateStatus(newStatus ArchiveStatus) error {
	tgz.Progress.Status = newStatus
	tgz.StatusChannel <- newStatus
	return nil
}

func (tgz *TarGzArchive) Plan() error {
	L.Info("Computing files info")
	tgz.UpdateStatus(STATUS_PLANNING)
	fileInfo, err := file_io.ComputeFilesInfo(tgz.InputPath, tgz.IgnoredDirs)
	if err != nil {
		return err
	}
	tgz.Info = fileInfo
	tgz.Progress.Done = 0
	tgz.Progress.Total = fileInfo.TotalFileCount
	tgz.UpdateStatus(STATUS_PLANNED)
	return nil
}

func (tgz *TarGzArchive) GetInfo() (*file_io.FilesInfo, error) {
	return tgz.Info, nil
}

func getMetaProgressFilePath(gleshaWorkDir string) string {
	return filepath.Join(gleshaWorkDir, ".progress.meta")
}

func (tgz *TarGzArchive) getTarFile() string {
	return filepath.Join(tgz.GleshaWorkDir, fmt.Sprintf("glesha-%s.tar.gz", tgz.ID))
}

func (tgz *TarGzArchive) archive() error {
	if tgz.archiveAlreadyExists {
		fmt.Printf("Archive already exists for path %s: %s\n", tgz.InputPath, tgz.getTarFile())
		tgz.StatusChannel <- STATUS_COMPLETED
		tgz.CloseStatusChannel()
		return nil
	}
	tgz.UpdateStatus(STATUS_RUNNING)
	tarFile, err := os.OpenFile(tgz.getTarFile(), os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	var prevText string
	var completedBytes uint64 = 0
	var shouldAbort bool = false
	gzipWriter := gzip.NewWriter(tarFile)
	tarGzWriter := tar.NewWriter(gzipWriter)
	verbose := cmd.Get().Verbose
	err = filepath.Walk(tgz.InputPath, func(path string, info fs.FileInfo, err error) error {
		select {
		case <-tgz.abortReq:
			{
				L.Debug("Received abort signal inside filepath.Walk")
				shouldAbort = true
				return fs.SkipAll
			}
		default:
		}

		_, ignore := tgz.IgnoredDirs[path]

		if ignore {
			return fs.SkipDir
		}
		if err != nil {
			return err
		}
		L.Debug(fmt.Sprintf("Processing: %s", path))
		var link string
		relPath, err := filepath.Rel(filepath.Dir(tgz.InputPath), path)
		if err != nil {
			return err
		}

		if info.Mode()&os.ModeSymlink == os.ModeSymlink {
			link, err = os.Readlink(path)
		}
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = relPath
		err = tarGzWriter.WriteHeader(header)
		if err != nil {
			return err
		}

		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				// skip files that are not readable
				return nil
			}
			defer file.Close()
			bufferedFileReader := bufio.NewReader(file)
			hash := sha256.New()
			teeReader := io.TeeReader(bufferedFileReader, hash)
			var progressPercentage float64 = 100.0
			if tgz.Progress.Total > 0 {
				progressPercentage = float64(completedBytes) * 100.0 / float64(tgz.Info.SizeInBytes)
			}
			if !verbose {
				fmt.Print("\r" + strings.Repeat(" ", len(prevText)) + "\r")
			}
			prevText = fmt.Sprintf("Archiving: %.2f%% (%d/%d) [%s - %s]", progressPercentage, tgz.Progress.Done, tgz.Progress.Total, info.Name(), L.HumanReadableBytes(uint64(info.Size())))
			fmt.Printf("%s", prevText)
			_, err = io.Copy(tarGzWriter, teeReader)
			if err != nil {
				return err
			}
			tgz.Progress.Done++
			completedBytes += uint64(info.Size())
			if verbose {
				fmt.Println()
				L.Debug(fmt.Sprintf("Processed: %s (%s)", path, L.HumanReadableBytes(uint64(bufferedFileReader.Size()))))
			}
		}
		return nil
	})

	if err != nil {
		tarGzWriter.Close()
		gzipWriter.Close()
		tarFile.Close()
		return err
	}

	if shouldAbort {
		tarGzWriter.Close()
		gzipWriter.Close()
		tarFile.Close()
		os.RemoveAll(tgz.GleshaWorkDir)
		tgz.abortDone <- struct{}{}
		return err
	}
	if !verbose {
		fmt.Print("\r" + strings.Repeat(" ", len(prevText)) + "\r")
	}

	size, err := file_io.FileSizeInBytes(tgz.getTarFile())
	if err != nil {
		return err
	}
	fmt.Printf("Archiving: Done (%d/%d) (%s -> %s)\n", tgz.Progress.Done, tgz.Progress.Total, L.HumanReadableBytes(tgz.Info.SizeInBytes), L.HumanReadableBytes(size))
	tarGzWriter.Close()
	gzipWriter.Close()
	tarFile.Close()
	tgz.saveProgress()
	tgz.StatusChannel <- STATUS_COMPLETED
	tgz.CloseStatusChannel()
	return nil
}

func (tgz *TarGzArchive) Start() error {
	return tgz.archive()
}

func (tgz *TarGzArchive) GetProgress() (*Progress, error) {
	if tgz.Progress == nil {
		return nil, fmt.Errorf("progress is nil, this should be unreachable")
	}
	return tgz.Progress, nil
}

func (tgz *TarGzArchive) saveProgress() error {
	metaProgress := MetaProgress{
		ID: tgz.ID, InputFilePath: tgz.InputPath,
	}
	metaProgressData, err := json.MarshalIndent(metaProgress, "", "  ")
	if err != nil {
		return err
	}
	err = os.WriteFile(getMetaProgressFilePath(tgz.GleshaWorkDir), metaProgressData, 0644)
	return err
}

func (tgz *TarGzArchive) Abort() error {
	if tgz.Progress.Status != STATUS_RUNNING {
		return fmt.Errorf("Abort() called when archiver is not running")
	}
	tgz.abortReq <- struct{}{}
	select {
	case <-tgz.abortDone:
		tgz.StatusChannel <- STATUS_ABORTED
		return nil
	}
}

func (tgz *TarGzArchive) Pause() error {
	return fmt.Errorf("Unimplmented")
}

func (tgz *TarGzArchive) GetStatusChannel() chan ArchiveStatus {
	return tgz.StatusChannel
}

func (tgz *TarGzArchive) HandleKillSignal() error {
	err := tgz.Abort()
	if err != nil {
		L.Error(err)
	}
	return nil
}

func (tgz *TarGzArchive) CloseStatusChannel() error {
	tgz.closeOnce.Do(func() {
		close(tgz.GetStatusChannel())
	})
	return nil
}

func (tgz *TarGzArchive) GetArchiveFilePath() string {
	return filepath.Join(tgz.OutputPath, tgz.getTarFile())
}
