package file_io

import (
	"fmt"
	L "glesha/logger"
	"io/fs"
	"os"
	"path/filepath"
)

type FilesInfo struct {
	TotalFileCount    uint64
	SizeInBytes       uint64
	ReadableFileCount uint64
}

func ComputeFilesInfo(inputPath string, ignorePaths map[string]bool) (*FilesInfo, error) {
	filesInfo := &FilesInfo{TotalFileCount: 0, SizeInBytes: 0, ReadableFileCount: 0}
	err := filepath.WalkDir(inputPath, func(path string, d fs.DirEntry, walkError error) error {

		if walkError != nil {
			return walkError
		}
		_, exists := ignorePaths[path]

		if exists {
			L.Debug(fmt.Sprintf("ComputeFileInfo: Ignoring %s", path))
			return fs.SkipDir
		}

		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			filesInfo.TotalFileCount++
			filesInfo.SizeInBytes += uint64(info.Size())
			err = IsReadable(path)
			if err != nil {
				return err
			}
			filesInfo.ReadableFileCount++
		} else {
			// account for directory sizes as well
			info, err := d.Info()
			if err != nil {
				return err
			}
			filesInfo.SizeInBytes += uint64(info.Size())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return filesInfo, nil
}

func IsReadable(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	return nil
}

func IsWritable(inputPath string) error {
	tempFile := "tempFile-123"
	file, err := os.CreateTemp(inputPath, tempFile)
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	return nil
}

func ExistsDir(inputPath string) bool {
	info, err := os.Stat(inputPath)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func Exists(inputFilePath string) bool {
	info, err := os.Stat(inputFilePath)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func FileSizeInBytes(inputFilePath string) (uint64, error) {
	info, err := os.Stat(inputFilePath)
	if err != nil {
		return 0, err
	}
	if info.IsDir() {
		return 0, fmt.Errorf("Cannot find size: %s is a directory.", inputFilePath)
	}
	return uint64(info.Size()), nil
}

type WriteMode uint8

const (
	WRITE_APPEND WriteMode = iota
	WRITE_OVERWRITE
)

func WriteToFile(filePath string, data []byte, mode WriteMode) (int, error) {
	var flags int
	switch mode {
	case WRITE_APPEND:
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	case WRITE_OVERWRITE:
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	parent := filepath.Dir(filePath)
	err := os.MkdirAll(parent, os.ModePerm)
	if err != nil {
		return 0, err
	}
	file, err := os.OpenFile(filePath, flags, 0644)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	return file.Write(data)
}
