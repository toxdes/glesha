package archive

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"glesha/database"
	"glesha/file_io"
	L "glesha/logger"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type TarGzArchive struct {
	ID                   int64
	InputPath            string
	OutputPath           string
	Info                 *file_io.FilesInfo
	Progress             *Progress
	abortReq             chan struct{}
	abortDone            chan struct{}
	GleshaWorkDir        string
	IgnoredDirs          map[string]bool
	archiveAlreadyExists bool
}

func NewTarGzArchiver(t *database.GleshaTask) (*TarGzArchive, error) {
	if !file_io.IsReadable(t.InputPath) {
		return nil, fmt.Errorf("no read permission on input path: %s", t.InputPath)
	}
	err := os.MkdirAll(t.OutputPath, os.ModePerm)
	if err != nil {
		return nil, err
	}
	if !file_io.IsWritable(t.OutputPath) {
		return nil, fmt.Errorf("no write permission on output path: %s", t.OutputPath)
	}

	GleshaWorkDir := t.OutputPath
	err = os.MkdirAll(GleshaWorkDir, os.ModePerm)
	if err != nil {
		return nil, err
	}

	progress := &Progress{0, 0, STATUS_IN_QUEUE}
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
		ID:            t.ID,
		InputPath:     t.InputPath,
		OutputPath:    t.OutputPath,
		Info:          nil,
		Progress:      progress,
		abortReq:      abortReq,
		abortDone:     abortDone,
		GleshaWorkDir: absGleshaWorkDir,
		IgnoredDirs:   ignoredDirs}, nil
}

func (tgz *TarGzArchive) UpdateStatus(ctx context.Context, newStatus ArchiveStatus) error {
	tgz.Progress.Status = newStatus
	return nil
}

func (tgz *TarGzArchive) Plan(ctx context.Context) error {
	tgz.UpdateStatus(ctx, STATUS_PLANNING)
	L.Info(fmt.Sprintf("Checking if files are changed in %s", tgz.InputPath))
	fileInfo, err := file_io.ComputeFilesInfo(ctx, tgz.InputPath, tgz.IgnoredDirs)
	if err != nil {
		return err
	}
	tgz.Info = fileInfo
	tgz.Progress.Done = 0
	tgz.Progress.Total = fileInfo.TotalFileCount
	tgz.UpdateStatus(ctx, STATUS_PLANNED)
	return nil
}

func (tgz *TarGzArchive) GetInfo(ctx context.Context) *file_io.FilesInfo {
	return tgz.Info
}

func (tgz *TarGzArchive) getTarFile() string {
	return filepath.Join(tgz.GleshaWorkDir,
		fmt.Sprintf("glesha-%d.tar.gz", tgz.ID))
}

func (tgz *TarGzArchive) archive(ctx context.Context) error {
	if tgz.archiveAlreadyExists {
		L.Printf("Archive already exists for path %s: %s\n",
			tgz.InputPath, tgz.getTarFile())
		return nil
	}
	tgz.UpdateStatus(ctx, STATUS_RUNNING)
	tarFile, err := os.OpenFile(tgz.getTarFile(), os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	var completedBytes uint64 = 0
	var shouldAbort bool = false
	gzipWriter := gzip.NewWriter(tarFile)
	tarGzWriter := tar.NewWriter(gzipWriter)
	startTime := time.Now()
	err = filepath.Walk(tgz.InputPath, func(path string, info fs.FileInfo, walkErr error) error {
		select {
		case <-ctx.Done():
			{
				L.Debug("Received abort signal inside filepath.Walk")
				shouldAbort = true
				return fs.SkipAll
			}
		default:
		}
		L.Print(L.C_CLEAR_LINE)

		_, ignore := tgz.IgnoredDirs[path]

		if ignore {
			L.Warn(fmt.Sprintf("Archive: potentially conflicting file: %s", path))
			return fs.SkipDir
		}

		if walkErr != nil {
			return fs.SkipDir
		}

		isSpecialPath := strings.HasPrefix(path, "/proc") ||
			strings.HasPrefix(path, "/dev") ||
			strings.HasPrefix(path, "/sys")

		if isSpecialPath {
			if info.IsDir() {
				L.Warn(fmt.Sprintf("Archive: skipping potentially problematic dir: %s", path))
				return fs.SkipDir
			} else {
				L.Warn(fmt.Sprintf("Archive: skipping potentially problematic file: %s", path))
				return nil
			}
		}

		L.Debug(fmt.Sprintf("Processing: %s", path))

		var link string
		relPath, err := filepath.Rel(filepath.Dir(tgz.InputPath), path)
		if err != nil {
			L.Warn(fmt.Errorf("archive: skipping %s due to error: %w", path, err))
			return nil
		}

		// Skip special file types like sockets, devices, FIFOs
		if info.Mode()&os.ModeSocket != 0 ||
			info.Mode()&os.ModeDevice != 0 ||
			info.Mode()&os.ModeNamedPipe != 0 {
			L.Warn(fmt.Sprintf("archive: skipping special file type: %s (mode: %s)", path, info.Mode().String()))
			return nil
		}
		if info.Mode()&os.ModeSymlink == os.ModeSymlink {
			link, err = os.Readlink(path)
		}
		if err != nil {
			L.Warn(fmt.Errorf("archive: skipping %s due to error: %w", path, err))
			return nil
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			L.Warn(fmt.Errorf("archive: skipping %s due to error: %w", path, err))
			return nil
		}
		header.Name = relPath

		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				// skip files that are not readable
				L.Warn(fmt.Errorf("archive: skipping %s due to error: %w", path, err))
				return nil
			}
			defer file.Close()
			bufferedFileReader := bufio.NewReader(file)
			var progressPercentage float64 = 100.0
			if tgz.Progress.Total > 0 {
				progressPercentage = float64(completedBytes) * 100.0 / float64(tgz.Info.SizeInBytes)
			}
			L.Print(L.C_SAVE)
			L.Printf("%sArchiving: %.2f%% %s (%d/%d) [%s - %s]",
				L.C_CLEAR_LINE,
				progressPercentage,
				L.ProgressBar(progressPercentage),
				tgz.Progress.Done,
				tgz.Progress.Total,
				L.TruncateString(filepath.Base(path), 24, L.TRUNC_CENTER),
				L.HumanReadableBytes(uint64(info.Size())))
			L.Print(L.C_RESTORE)
			err = tarGzWriter.WriteHeader(header)
			if err != nil {
				L.Warn(fmt.Errorf("archive: skipping %s due to error: %w", path, err))
				return nil
			}
			_, err = io.Copy(tarGzWriter, bufferedFileReader)
			if err != nil {
				L.Warn(fmt.Errorf("archive: skipping %s due to error: %w", path, err))
				return nil
			}
			tgz.Progress.Done++
			completedBytes += uint64(info.Size())
			if L.IsVerbose() {
				L.Debug(fmt.Sprintf("Processed: %s (%s)",
					path,
					L.HumanReadableBytes(uint64(bufferedFileReader.Size()))))
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
		os.Remove(tgz.getTarFile())
		return err
	}

	tarFileInfo, err := file_io.GetFileInfo(tgz.getTarFile())
	if err != nil {
		return err
	}
	L.Printf("%sArchiving: Done (%d/%d) (%s -> %s)\n",
		L.C_CLEAR_LINE,
		tgz.Progress.Done,
		tgz.Progress.Total,
		L.HumanReadableBytes(tgz.Info.SizeInBytes),
		L.HumanReadableBytes(tarFileInfo.Size))
	L.Info(fmt.Sprintf("Took: %s", L.HumanReadableTime(time.Now().UnixMilli()-startTime.UnixMilli())))
	tarGzWriter.Close()
	gzipWriter.Close()
	tarFile.Close()

	return nil
}

func (tgz *TarGzArchive) Start(ctx context.Context) error {
	return tgz.archive(ctx)
}

func (tgz *TarGzArchive) GetProgress(ctx context.Context) (*Progress, error) {
	if tgz.Progress == nil {
		return nil, fmt.Errorf("progress is nil, this should be unreachable")
	}
	return tgz.Progress, nil
}

func (tgz *TarGzArchive) Abort(ctx context.Context) error {
	if tgz.Progress.Status != STATUS_RUNNING {
		return fmt.Errorf("Abort() called when archiver is not running")
	}
	tgz.abortReq <- struct{}{}
	<-tgz.abortDone
	return nil
}

func (tgz *TarGzArchive) Pause(ctx context.Context) error {
	return fmt.Errorf("Unimplmented")
}

func (tgz *TarGzArchive) GetArchiveFilePath(ctx context.Context) string {
	return filepath.Join(tgz.OutputPath, filepath.Base(tgz.getTarFile()))
}

func IsValidTarGz(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()
	gr, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("not a valid gzip stream: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	_, err = tr.Next()
	if err == io.EOF {
		return nil
	}
	return err
}
