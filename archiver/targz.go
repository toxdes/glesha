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
}

type MetaProgress struct {
	ID            string
	InputFilePath string
}

func getExistingUUIDOrDefault(inputPath string, fallbackUUID string) string {
	metaFilePath := getMetaProgressFilePath()
	info, err := os.Stat(metaFilePath)
	exists := err == nil && !info.IsDir()
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
	return &TarGzArchive{ID: id, InputPath: inputPath, OutputPath: outputPath, Info: nil, Progress: progress, StatusChannel: statusChannel}, nil
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

func (tgz *TarGzArchive) archive() error {
	if archiveProgress.TempFilePath == "" {
		ts := time.Now().UnixMicro()
		archiveName := fmt.Sprintf("glesha-ar-%d.tar.gz", ts)
		archiveProgress.TempFilePath = filepath.Join(os.TempDir(), archiveName)
	}
	tarFile, err := os.OpenFile(archiveProgress.TempFilePath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer tarFile.Close()
	gzipWriter := gzip.NewWriter(tarFile)
	defer gzipWriter.Close()
	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()
	return filepath.Walk(tgz.InputPath, func(path string, info fs.FileInfo, err error) error {
		_, exists := archiveProgress.Files[path]
		L.Debug(fmt.Sprintf("Processing: %s", path))
		if exists {
			return nil
		}
		if err != nil {
			return err
		}
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
			tgz.Progress.Done = uint64(len(archiveProgress.Files))
			var progressPercentage float32 = 100.0
			if tgz.Progress.Total > 0 {
				progressPercentage = float32(tgz.Progress.Done) * 100.0 / float32(tgz.Progress.Total)
			}
			fmt.Printf("\rArchiving: %.2f%% (%d/%d)", progressPercentage, tgz.Progress.Done, tgz.Progress.Total)
			L.Debug(fmt.Sprintf("Processed: %s (%s)", path, archiveProgress.Files[path]))
		}

		return nil
	})
}

func (tgz *TarGzArchive) copyArchiveToOutputDir() error {
	defer tgz.CloseStatusChannel()
	defer os.Remove(archiveProgress.TempFilePath)
	defer os.Remove(archiveProgress.ProgressFilePath)
	defer os.Remove(getMetaProgressFilePath())
	tarFilePath := filepath.Join(tgz.OutputPath, filepath.Base(archiveProgress.TempFilePath))
	tarFile, err := os.OpenFile(tarFilePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer tarFile.Close()
	tempFile, err := os.Open(archiveProgress.TempFilePath)
	if err != nil {
		return err
	}
	defer tempFile.Close()
	fmt.Printf("\n")
	io.Copy(tarFile, tempFile)
	tgz.UpdateStatus(STATUS_COMPLETED)
	return nil
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

	if uint64(len(archiveProgress.Files)) == uint64(archiveProgress.TotalFileCount) {
		err = tgz.copyArchiveToOutputDir()
	}
	return err
}

func (tgz *TarGzArchive) GetProgress() (*Progress, error) {
	if tgz.Progress == nil {
		return nil, fmt.Errorf("progress is nil, this should be unreachable")
	}
	return tgz.Progress, nil
}

func (tgz *TarGzArchive) Pause() error {
	archiveProgressData, err := json.MarshalIndent(archiveProgress, "", "  ")
	if err != nil {
		return err
	}

	if tgz.Progress.Status != STATUS_RUNNING {
		return fmt.Errorf("Pause() called when archiver is not running")
	}
	metaProgress := MetaProgress{
		ID: tgz.ID, InputFilePath: tgz.InputPath,
	}
	metaProgressData, err := json.MarshalIndent(metaProgress, "", "  ")
	err = os.WriteFile(getMetaProgressFilePath(), metaProgressData, 0644)
	err = os.WriteFile(archiveProgress.ProgressFilePath, archiveProgressData, 0644)
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
