package archive

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"glesha/database/model"
	"glesha/database/repository"
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
	Id                   int64
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

func NewTarGzArchiver(t *model.Task) (*TarGzArchive, error) {
	readable, err := file_io.IsReadable(t.InputPath)
	if err != nil || !readable {
		return nil, fmt.Errorf("no read permission on input path: %s", t.InputPath)
	}

	err = os.MkdirAll(t.OutputPath, os.ModePerm)
	if err != nil {
		return nil, err
	}

	writable, err := file_io.IsWritable(t.OutputPath)

	if err != nil || !writable {
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
		Id:            t.Id,
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
		fmt.Sprintf("glesha-%d.tar.gz", tgz.Id))
}

func (tgz *TarGzArchive) archive(
	ctx context.Context,
	catalogRepo repository.FileCatalogRepository,
	taskRepo repository.TaskRepository,
) error {
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

	var catalogBatch []model.FileCatalogRow

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

		L.Debug(fmt.Sprintf("Processing: %s", L.TruncateString(path, 48, L.TRUNC_LEFT)))

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

		fileType := "file"
		if info.IsDir() {
			fileType = "dir"
		}
		catalogBatch = append(catalogBatch, model.FileCatalogRow{
			TaskId:     tgz.Id,
			FullPath:   relPath,
			Name:       info.Name(),
			ParentPath: filepath.Dir(relPath),
			FileType:   fileType,
			SizeBytes:  info.Size(),
			ModifiedAt: info.ModTime(),
		})

		// TODO: make this configurable from config.json
		const CATALOG_BATCH_SIZE int = 1000
		if len(catalogBatch) >= CATALOG_BATCH_SIZE {
			catalogRepo.AddMany(ctx, catalogBatch)
			catalogBatch = nil
		}

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
			L.Footer(L.NORMAL, fmt.Sprintf("Archiving: %.2f%% %s (%d/%d) [%s - %s]",
				progressPercentage,
				L.ProgressBar(progressPercentage, -1),
				tgz.Progress.Done,
				tgz.Progress.Total,
				L.TruncateString(filepath.Base(path), 24, L.TRUNC_CENTER),
				L.HumanReadableBytes(uint64(info.Size()), 2)))
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
			// update progress more frequently, because now we will have a tui dashboard
			if tgz.Progress.Done%10 == 0 {
				_ = taskRepo.UpdateArchivedFileCount(ctx, tgz.Id, int64(tgz.Progress.Done))
			}
			completedBytes += uint64(info.Size())
			if L.IsVerbose() {
				L.Debug(fmt.Sprintf("Processed: %s (%s)",
					L.TruncateString(path, 40, L.TRUNC_LEFT),
					L.HumanReadableBytes(uint64(bufferedFileReader.Size()), 2)))
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

	if len(catalogBatch) > 0 {
		catalogRepo.AddMany(ctx, catalogBatch)
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
	L.Footer(L.NORMAL, "")
	L.Printf("Archiving: Done (%d/%d) (%s -> %s)\n",
		tgz.Progress.Done,
		tgz.Progress.Total,
		L.HumanReadableBytes(tgz.Info.SizeInBytes, 2),
		L.HumanReadableBytes(tarFileInfo.Size, 2))
	L.Printf("Archiving took %s\n", L.HumanReadableTime(time.Now().UnixMilli()-startTime.UnixMilli()))
	tarGzWriter.Close()
	gzipWriter.Close()
	tarFile.Close()

	return nil
}

func (tgz *TarGzArchive) Start(
	ctx context.Context,
	catalogRepo repository.FileCatalogRepository,
	taskRepo repository.TaskRepository,
) error {
	return tgz.archive(ctx, catalogRepo, taskRepo)
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
