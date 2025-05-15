package archive

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
	"time"

	"github.com/google/uuid"
)

type TarGzArchive struct {
	ID         string
	InputPath  string
	OutputPath string
	Info       *file_io.FilesInfo
	Progress   *Progress
}

func NewTarGzArchive(inputPath string, outputPath string) (*TarGzArchive, error) {
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
	id := uuid.NewString()
	return &TarGzArchive{ID: id, InputPath: inputPath, OutputPath: outputPath, Info: nil, Progress: progress}, nil
}

func (tgz *TarGzArchive) Plan() error {
	L.Info("Computing files info")
	tgz.Progress.Status = STATUS_PLANNING
	fileInfo, err := file_io.ComputeFilesInfo(tgz.InputPath)
	if err != nil {
		return err
	}
	tgz.Info = fileInfo
	tgz.Progress.Status = STATUS_PLANNED
	tgz.Progress.Done = 0
	tgz.Progress.Total = fileInfo.TotalFileCount
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

func (tgz *TarGzArchive) Start() error {
	progressFilePath := filepath.Join(os.TempDir(), tgz.ID+".progress")
	progressFile, err := os.OpenFile(progressFilePath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer progressFile.Close()
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
	if uint64(len(archiveProgress.Files)) <= uint64(archiveProgress.TotalFileCount) {
		tgz.Progress.Status = STATUS_RUNNING
		// create tar file
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
		tarWriter := tar.NewWriter(tarFile)
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
			if info.IsDir() {
				header := &tar.Header{
					Name:     path,
					Mode:     int64(info.Mode()),
					Size:     0, // Symlinks don't have content size
					Typeflag: tar.TypeDir,
				}
				return tarWriter.WriteHeader(header)
			}
			var link string
			var header *tar.Header
			if info.Mode()&os.ModeSymlink == os.ModeSymlink {
				if link, err = os.Readlink(path); err != nil {
					return err
				}
				header = &tar.Header{
					Name:     path,
					Mode:     int64(info.Mode()),
					Size:     0, // Symlinks don't have content size
					Typeflag: tar.TypeSymlink,
					Linkname: link, // Symlink target path
				}
				return tarWriter.WriteHeader(header)
			}
			header, err = tar.FileInfoHeader(info, "")

			if err != nil {
				return err
			}
			relPath, err := filepath.Rel(tgz.InputPath, path)
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
			if uint64(len(archiveProgress.Files)) == uint64(archiveProgress.TotalFileCount) {
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
				fmt.Printf("\n")
				defer tempFile.Close()
				io.Copy(tarFile, tempFile)
				os.Remove(archiveProgress.TempFilePath)
				os.Remove(archiveProgress.ProgressFilePath)
			}
			return nil
		})
	}
	return nil
}

func (tgz *TarGzArchive) GetProgress() (*Progress, error) {
	if tgz.Progress == nil {
		return nil, fmt.Errorf("progress is nil, this should be unreachable")
	}
	return tgz.Progress, nil
}

func (tgz *TarGzArchive) Pause() error {
	data, err := json.MarshalIndent(archiveProgress, "", "  ")
	if err != nil {
		return err
	}
	err = os.WriteFile(".progress", data, 0644)
	if err != nil {
		return err
	}
	L.Debug("Saved progress.")
	return nil
}

func (tgz *TarGzArchive) Abort() error {
	return fmt.Errorf("Unimplmented")
}
