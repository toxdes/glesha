package file_io

import (
	"io/fs"
	"os"
	"path/filepath"
)

type FilesInfo struct {
	TotalFileCount    uint64
	SizeInBytes       uint64
	ReadableFileCount uint64
}

func ComputeFilesInfo(inputPath string) (*FilesInfo, error) {
	filesInfo := &FilesInfo{TotalFileCount: 0, SizeInBytes: 0, ReadableFileCount: 0}
	err := filepath.WalkDir(inputPath, func(path string, d fs.DirEntry, walkError error) error {
		if walkError != nil {
			return walkError
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			filesInfo.TotalFileCount++
			filesInfo.SizeInBytes += uint64(info.Size())
			readable, err := IsReadable(path)
			if err != nil {
				return err
			}
			if readable {
				filesInfo.ReadableFileCount++
			}
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

func IsReadable(filePath string) (bool, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	defer file.Close()
	return true, nil
}

func IsWritable(inputPath string) (bool, error) {
	tempFile := "tempFile-123"
	file, err := os.CreateTemp(inputPath, tempFile)
	if err != nil {
		return false, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	return true, nil
}
