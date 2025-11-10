package file_io

import (
	"context"
	"fmt"
	"glesha/checksum"
	L "glesha/logger"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type FilesInfo struct {
	TotalFileCount    uint64
	SizeInBytes       uint64
	ReadableFileCount uint64
	ContentHash       string
}

type ProgressReader struct {
	R          io.ReadSeeker
	Sent       int64
	Total      int64
	Position   int64
	OnProgress func(sent int64, total int64)
}

func (pr *ProgressReader) Read(p []byte) (n int, err error) {
	n, err = pr.R.Read(p)
	atomic.AddInt64(&pr.Sent, int64(n))
	atomic.AddInt64(&pr.Position, int64(n))
	sent := atomic.LoadInt64(&pr.Sent)
	pr.OnProgress(sent, pr.Total)
	return n, err
}

func (pr *ProgressReader) Seek(offset int64, whence int) (int64, error) {
	newOffset, err := pr.R.Seek(offset, whence)
	if err == nil {
		atomic.StoreInt64(&pr.Position, int64(newOffset))
	}
	return newOffset, err
}

func ComputeFilesInfo(ctx context.Context, inputPath string, ignorePaths map[string]bool) (*FilesInfo, error) {
	filesInfo := &FilesInfo{TotalFileCount: 0, SizeInBytes: 0, ReadableFileCount: 0, ContentHash: ""}
	contentHashWriter := checksum.NewSha256()
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
	filesInfo.ContentHash = checksum.Base64EncodeStr(contentHashWriter.Sum([]byte{}))
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

func ReadFromOffset(ctx context.Context, filePath string, offset int64, buf []byte) (readBytes int64, err error) {
	readBytes = -1
	select {
	case <-ctx.Done():
		return -1, ctx.Err()
	default:
	}
	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		return -1, fmt.Errorf("could not get abs path for path %s: %w", filePath, err)
	}

	file, err := os.Open(absFilePath)

	if err != nil {
		return -1, fmt.Errorf("could not open file %s:%w", absFilePath, err)
	}
	defer file.Close()

	type result struct {
		readCnt int
		err     error
	}

	resultChannel := make(chan result, 1)
	defer close(resultChannel)
	go func() {
		readCnt, err := file.ReadAt(buf, offset)
		resultChannel <- result{readCnt, err}
	}()

	select {
	case res := <-resultChannel:
		return int64(res.readCnt), res.err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
