package file_io

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	L "glesha/logger"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type FilesInfo struct {
	TotalFileCount    uint64
	SizeInBytes       uint64
	ReadableFileCount uint64
	ContentHash       string
}

func ComputeFilesInfo(ctx context.Context, inputPath string, ignorePaths map[string]bool) (*FilesInfo, error) {
	filesInfo := &FilesInfo{TotalFileCount: 0, SizeInBytes: 0, ReadableFileCount: 0, ContentHash: ""}
	contentHashWriter := sha256.New()
	err := filepath.WalkDir(inputPath, func(path string, d fs.DirEntry, walkError error) error {
		select {
		case <-ctx.Done():
			return fs.SkipAll
		default:
		}

		if walkError != nil {
			return fs.SkipDir
		}
		_, exists := ignorePaths[path]

		if exists {
			L.Debug(fmt.Sprintf("ComputeFileInfo: Ignoring %s", path))
			return fs.SkipDir
		}

		isSpecialPath := strings.HasPrefix(path, "/proc") ||
			strings.HasPrefix(path, "/dev") ||
			strings.HasPrefix(path, "/sys")

		if isSpecialPath {
			info, _ := d.Info()
			if info.IsDir() {
				L.Debug(fmt.Sprintf("Archive: skipping potentially problematic dir: %s", path))
				return fs.SkipDir
			} else {
				L.Debug(fmt.Sprintf("Archive: skipping potentially problematic file: %s", path))
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			filesInfo.TotalFileCount++
			readable, err := IsReadable(path)
			if err != nil || !readable {
				L.Debug(fmt.Errorf("could not read: %s", path))
				return nil
			}

			filesInfo.SizeInBytes += uint64(info.Size())
			filesInfo.ReadableFileCount++
		}
		contentHashWriter.Write([]byte(path))
		contentHashWriter.Write([]byte(strconv.FormatInt(info.Size(), 10)))
		return nil
	})
	if err != nil {
		return nil, err
	}
	filesInfo.ContentHash = hex.EncodeToString(contentHashWriter.Sum([]byte{}))
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

	info, err := os.Stat(inputPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, fmt.Errorf("path does not exist: %s", inputPath)
		}
		return false, fmt.Errorf("failed to stat path: %s", inputPath)
	}

	if info.IsDir() {
		return isDirWritable(inputPath)
	} else {
		return isFileWritable(inputPath)
	}
}

func isDirWritable(inputDirPath string) (bool, error) {
	tempFilePath := filepath.Join(inputDirPath, ".write-test-"+strconv.Itoa(int(time.Now().UnixNano())))
	tempFile, err := os.Create(tempFilePath)
	if err != nil {
		return false, err
	}
	_ = tempFile.Close()
	_ = os.Remove(tempFilePath)
	return true, nil
}

func isFileWritable(inputFilePath string) (bool, error) {
	inputFile, err := os.OpenFile(inputFilePath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return false, err
	}
	_ = inputFile.Close()
	return true, nil
}

func Exists(inputFilePath string) (bool, error) {
	info, err := os.Stat(inputFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if info.IsDir() {
		return false, fmt.Errorf("%s is a directory", inputFilePath)
	}
	return true, nil
}

type FileInfo struct {
	Size       uint64
	ModifiedAt time.Time
}

// return filesize in bytes and last modified timestamp
func GetFileInfo(inputFilePath string) (*FileInfo, error) {
	stat, err := os.Stat(inputFilePath)
	if err != nil {
		return nil, err
	}
	if stat.IsDir() {
		return nil, fmt.Errorf("could not find size: %s is a directory", inputFilePath)
	}
	return &FileInfo{Size: uint64(stat.Size()), ModifiedAt: stat.ModTime()}, nil
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

func GetGlobalWorkDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(filepath.Join(homeDir, ".glesha-cache"))
	if err != nil {
		return "", err
	}
	err = os.MkdirAll(absPath, os.ModePerm)
	if err != nil {
		return "", err
	}
	return absPath, nil
}
