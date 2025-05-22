package archiver

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"glesha/file_io"
	L "glesha/logger"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type TarGzArchive struct {
	ID            string
	InputPath     string
	OutputPath    string
	Info          *file_io.FilesInfo
	Progress      *Progress
	StatusChannel chan ArchiveStatus
	closeOnce     sync.Once
	pauseChannel  chan struct{}
}

type MetaProgress struct {
	ID            string
	InputFilePath string
}

func getExistingUUIDOrDefault(inputPath string, fallbackUUID string) string {
	metaFilePath := getMetaProgressFilePath()
	exists := file_io.Exists(metaFilePath)
	if !exists {
		return fallbackUUID
	}
	metaFile, err := os.Open(metaFilePath)
	defer metaFile.Close()
	if err != nil {
		return fallbackUUID
	}
	decoder := json.NewDecoder(metaFile)
	var metaProgress MetaProgress
	err = decoder.Decode(&metaProgress)
	if err != nil || metaProgress.InputFilePath != inputPath {
		return fallbackUUID
	}
	fmt.Println("Existing progress exists for same inputPath. Continue? (y/n) (default: yes)")
	var answer string
	fmt.Scanf("%s", answer)
	if strings.ToLower(answer) == "no" || strings.ToLower(answer) == "n" {
		return fallbackUUID
	}
	return metaProgress.ID
}

func NewTarGzArchiver(inputPath string, outputPath string, statusChannel chan ArchiveStatus) (*TarGzArchive, error) {
	ok, err := file_io.IsReadable(inputPath)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("inputPath not readable: %s", inputPath)
	}
	ok, err = file_io.IsWritable(outputPath)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("output path is not writable: %s", outputPath)
	}
	progress := &Progress{0, 0, STATUS_IN_QUEUE}
	id := getExistingUUIDOrDefault(inputPath, uuid.NewString())
	pauseChannel := make(chan struct{})
	return &TarGzArchive{ID: id, InputPath: inputPath, OutputPath: outputPath, Info: nil, Progress: progress, StatusChannel: statusChannel, pauseChannel: pauseChannel}, nil
}

func (tgz *TarGzArchive) UpdateStatus(newStatus ArchiveStatus) error {
	tgz.Progress.Status = newStatus
	tgz.StatusChannel <- newStatus
	return nil
}

func (tgz *TarGzArchive) Plan() error {
	L.Info("Computing files info")
	tgz.UpdateStatus(STATUS_PLANNING)
	fileInfo, err := file_io.ComputeFilesInfo(tgz.InputPath)
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

type ArchiveProgress struct {
	Files            map[string]string
	TotalFileCount   uint64
	TempFilePath     string
	ProgressFilePath string
}

var archiveProgress ArchiveProgress

func (tgz *TarGzArchive) populateArchiveProgress() error {
	progressFilePath := filepath.Join(os.TempDir(), tgz.ID+".progress")
	progressFile, err := os.OpenFile(progressFilePath, os.O_RDWR|os.O_CREATE, 0644)
	defer progressFile.Close()
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(progressFile)
	archiveProgress = ArchiveProgress{
		Files:            map[string]string{},
		TotalFileCount:   tgz.Progress.Total,
		TempFilePath:     "",
		ProgressFilePath: progressFilePath,
	}
	var probablySavedProgress ArchiveProgress
	err = decoder.Decode(&probablySavedProgress)
	if err == nil {
		if len(probablySavedProgress.Files) > 0 {
			archiveProgress.Files = probablySavedProgress.Files
			archiveProgress.TotalFileCount = probablySavedProgress.TotalFileCount
			archiveProgress.TempFilePath = probablySavedProgress.TempFilePath
			archiveProgress.ProgressFilePath = probablySavedProgress.ProgressFilePath
		}
	}
	return nil
}

func getMetaProgressFilePath() string {
	return filepath.Join(os.TempDir(), ".progress.meta")
}

func removeTarMarker(tarFile *os.File) error {
	tarFileInfo, err := tarFile.Stat()
	if err != nil {
		return fmt.Errorf("Couldn't pause(): %w", err)
	}
	tarFileSize := tarFileInfo.Size()
	tarMarkerSize := int64(512 * 512)
	if tarFileSize < tarMarkerSize {
		return fmt.Errorf("Couldn't pause(): archive size(%d) is smaller than marker size(%d)", tarFileSize, tarMarkerSize)
	}
	newTarFileSize := tarFileSize - tarMarkerSize
	buf := make([]byte, tarMarkerSize)
	tarFile.ReadAt(buf, newTarFileSize)
	_, err = tarFile.ReadAt(buf, newTarFileSize)
	if err != nil {
		return fmt.Errorf("Couldn't pause(): %w", err)
	}
	allZeroes := true
	var res string = ""
	for _, b := range buf {
		res = fmt.Sprintf("%s, %d", res, b)
		if b != 0 {
			allZeroes = false
		}
	}
	L.Error(res)
	if !allZeroes {
		return fmt.Errorf("Couldn't pause(): archive isn't tar archive, file doesn't end with tar-end markers")
	}
	err = tarFile.Truncate(newTarFileSize)
	if err != nil {
		return fmt.Errorf("Couldn't pause(): %w", err)
	}
	_, err = tarFile.Seek(0, io.SeekEnd)
	return err
}

func (tgz *TarGzArchive) archive() error {
	if archiveProgress.TempFilePath == "" {
		ts := time.Now().UnixMicro()
		archiveName := fmt.Sprintf("glesha-ar-%d.tar", ts)
		archiveProgress.TempFilePath = filepath.Join(os.TempDir(), archiveName)
	}
	tarFile, err := os.OpenFile(archiveProgress.TempFilePath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	tarFile.Seek(0, io.SeekEnd)
	defer tarFile.Close()
	tarWriter := tar.NewWriter(tarFile)
	defer tarWriter.Close()
	var prevTextWidth int = 0
	return filepath.Walk(tgz.InputPath, func(path string, info fs.FileInfo, err error) error {
		select {
		case <-tgz.pauseChannel:
			{
				tarWriter.Close()
				err = removeTarMarker(tarFile)
				if err != nil {
					L.Panic(err)
				}
				return filepath.SkipAll
			}
		default:
		}
		_, exists := archiveProgress.Files[path]
		if exists {
			L.Debug(fmt.Printf("Skipping: %s\n", path))
			return nil
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
		err = tarWriter.WriteHeader(header)
		if err != nil {
			return err
		}

		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			hash := sha256.New()
			teeReader := io.TeeReader(file, hash)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = io.Copy(tarWriter, teeReader)
			if err != nil {
				return err
			}
			archiveProgress.Files[path] = fmt.Sprintf("%x", hash.Sum(nil))
			tgz.Progress.Done++
			var progressPercentage float32 = 100.0
			if tgz.Progress.Total > 0 {
				progressPercentage = float32(tgz.Progress.Done) * 100.0 / float32(tgz.Progress.Total)
			}
			outputLine := fmt.Sprintf("Archiving: %s [%.2f%% (%d/%d)]", info.Name(), progressPercentage, tgz.Progress.Done, tgz.Progress.Total)
			fmt.Print("\r" + strings.Repeat(" ", prevTextWidth) + "\r")
			fmt.Printf("%s", outputLine)
			prevTextWidth = len(outputLine)
			L.Debug(fmt.Sprintf("Processed: %s (%s)", path, archiveProgress.Files[path]))
		}

		var progressPercentage float32 = 100.0
		if tgz.Progress.Total > 0 {
			progressPercentage = float32(tgz.Progress.Done) * 100.0 / float32(tgz.Progress.Total)
		}
		fmt.Print("\r" + strings.Repeat(" ", prevTextWidth) + "\r")
		fmt.Printf("Archiving: [%.2f%% (%d,%d)] Done", progressPercentage, tgz.Progress.Done, tgz.Progress.Total)
		return nil
	})
}

func (tgz *TarGzArchive) compress() error {
	tarFile, err := os.Open(archiveProgress.TempFilePath)
	if err != nil {
		return fmt.Errorf("Cannot compress(): %w", err)
	}
	gzipFile, err := os.OpenFile(archiveProgress.TempFilePath+".gz", os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("Cannot compress(): %w", err)
	}
	gzipWriter := gzip.NewWriter(gzipFile)
	outputLine := fmt.Sprintf("%s", "\nCompressing: (This may take a while...)")
	fmt.Print(outputLine)
	_, err = io.Copy(gzipWriter, tarFile)
	if err != nil {
		return fmt.Errorf("Cannot compress(): %w", err)
	}
	tarFileInfo, err := tarFile.Stat()
	if err != nil {
		return fmt.Errorf("Cannot compress(): %w", err)
	}
	gzipFileInfo, err := gzipFile.Stat()
	if err != nil {
		return fmt.Errorf("Cannot compress(): %w", err)
	}
	tarFileSize := uint64(tarFileInfo.Size())
	gzipFileSize := uint64(gzipFileInfo.Size())
	fmt.Print("\r" + strings.Repeat(" ", len(outputLine)) + "\r")
	outputLine = fmt.Sprintf("Compressing: Done (%s -> %s)\n", L.HumanReadableBytes(tarFileSize), L.HumanReadableBytes(gzipFileSize))
	fmt.Print(outputLine)
	gzipWriter.Close()
	gzipFile.Close()
	tarFile.Close()
	err = os.Remove(archiveProgress.ProgressFilePath)
	if err != nil {
		return fmt.Errorf("Cannot compress(): %w", err)
	}
	err = os.Remove(archiveProgress.TempFilePath)
	if err != nil {
		return fmt.Errorf("Cannot compress(): %w", err)
	}
	if file_io.Exists(getMetaProgressFilePath()) {
		err = os.Remove(getMetaProgressFilePath())
	}
	if err != nil {
		return fmt.Errorf("Cannot compress(): %w", err)
	}
	return nil
}

func (tgz *TarGzArchive) copyArchiveToOutputDir() error {
	tarFilePath := filepath.Join(tgz.OutputPath, filepath.Base(archiveProgress.TempFilePath)+".gz")
	tarFile, err := os.OpenFile(tarFilePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	tempFile, err := os.Open(archiveProgress.TempFilePath + ".gz")
	if err != nil {
		return err
	}
	io.Copy(tarFile, tempFile)
	tgz.UpdateStatus(STATUS_COMPLETED)
	tarFile.Close()
	tempFile.Close()
	err = os.Remove(archiveProgress.TempFilePath + ".gz")
	if err != nil {
		return err
	}
	tgz.CloseStatusChannel()
	return err
}

func (tgz *TarGzArchive) Start() error {
	tgz.UpdateStatus(STATUS_RUNNING)
	err := tgz.populateArchiveProgress()
	if err != nil {
		return err
	}
	if uint64(len(archiveProgress.Files)) <= uint64(archiveProgress.TotalFileCount) {
		err = tgz.archive()
	}
	if err != nil {
		return err
	}
	err = tgz.compress()
	if err != nil {
		return err
	}
	err = tgz.copyArchiveToOutputDir()
	if err != nil {
		return err
	}
	return nil
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
	err = os.WriteFile(getMetaProgressFilePath(), metaProgressData, 0644)
	if err != nil {
		return err
	}
	archiveProgressData, err := json.MarshalIndent(archiveProgress, "", "  ")
	err = os.WriteFile(archiveProgress.ProgressFilePath, archiveProgressData, 0644)
	if err != nil {
		return err
	}
	return nil
}

func (tgz *TarGzArchive) Pause() error {
	tgz.pauseChannel <- struct{}{}
	if tgz.Progress.Status != STATUS_RUNNING {
		return fmt.Errorf("Pause() called when archiver is not running")
	}
	err := tgz.saveProgress()
	if err != nil {
		return err
	}
	L.Debug("Saved progress.")
	tgz.UpdateStatus(STATUS_PAUSED)
	return nil
}

func (tgz *TarGzArchive) Abort() error {
	return fmt.Errorf("Unimplmented")
}

func (tgz *TarGzArchive) GetStatusChannel() chan ArchiveStatus {
	return tgz.StatusChannel
}

func (tgz *TarGzArchive) HandleKillSignal() error {
	defer tgz.CloseStatusChannel()
	if tgz.Progress.Status == STATUS_RUNNING {
		err := tgz.Pause()
		if err != nil {
			L.Error(err)
		}
	}
	return nil
}

func (tgz *TarGzArchive) CloseStatusChannel() error {
	tgz.closeOnce.Do(func() {
		close(tgz.GetStatusChannel())
	})
	return nil
}
