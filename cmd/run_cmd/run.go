package run_cmd

import (
	"context"
	"flag"
	"fmt"
	"glesha/archive"
	"glesha/backend"
	"glesha/backend/aws"
	"glesha/config"
	"glesha/database"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
	L "glesha/logger"
	"strconv"
	"strings"
	"time"
)

type RunCmdEnv struct {
	DB         *database.DB
	TaskId     int64
	Task       *model.Task
	TaskRepo   repository.TaskRepository
	UploadRepo repository.UploadRepository
}

var runCmdEnv *RunCmdEnv

func Execute(ctx context.Context, args []string) error {
	// parse cli args
	err := parseFlags(args)
	if err != nil {
		return err
	}
	if runCmdEnv == nil {
		return fmt.Errorf("could not initialize env, this shouldn't happen")
	}

	// initialize db connection
	dbPath, err := database.GetDBFilePath(ctx)
	if err != nil {
		return err
	}
	db, err := database.NewDB(dbPath)
	if err != nil {
		return err
	}
	defer db.Close(ctx)
	L.Debug(fmt.Sprintf("Found database at: %s", dbPath))
	runCmdEnv.DB = db
	err = runCmdEnv.DB.Init(ctx)
	if err != nil {
		return err
	}

	runCmdEnv.TaskRepo = repository.NewTaskRepository(db)
	runCmdEnv.UploadRepo = repository.NewUploadRepository(db)

	runCmdEnv.Task, err = runCmdEnv.TaskRepo.GetTaskById(ctx, runCmdEnv.TaskId)
	if err != nil {
		if err == database.ErrDoesNotExist {
			return fmt.Errorf("task %d does not exist, for more information see 'glesha help add'", runCmdEnv.TaskId)
		}
		return err
	}
	L.Printf("%s", runCmdEnv.Task)
	err = config.Parse(runCmdEnv.Task.ConfigPath)
	if err != nil {
		return err
	}
	err = runTask(ctx)
	return err
}

func parseFlags(args []string) error {
	runCmd := flag.NewFlagSet("run", flag.ExitOnError)
	logLevel := runCmd.String("log-level", L.GetLogLevel().String(), "Set log level: debug info warn error panic")
	runCmd.StringVar(logLevel, "L", L.GetLogLevel().String(), "Set log level: debug info warn error panic")
	runCmd.Usage = func() {
		PrintUsage()
	}
	err := runCmd.Parse(args)

	if err != nil {
		return err
	}

	nArgs := len(runCmd.Args())

	if nArgs < 1 {
		return fmt.Errorf("no task Id provided. For more information check 'glesha help run'")
	}
	if nArgs > 1 {
		return fmt.Errorf("too many arguments. For more information, check 'glesha help run'")
	}
	taskId, err := strconv.ParseInt(runCmd.Arg(0), 10, 64)
	if err != nil {
		return err
	}
	if logLevel != nil {
		err = L.SetLevelFromString(*logLevel)
		if err != nil {
			return err
		}
		L.Info(fmt.Sprintf("log level set to: %s", strings.ToUpper(*logLevel)))
	}
	runCmdEnv = &RunCmdEnv{
		TaskId: taskId,
		Task:   nil,
		DB:     nil,
	}

	return err
}

func runTask(ctx context.Context) error {
	t := runCmdEnv.Task
	if t == nil {
		return fmt.Errorf("no task to run")
	}
	mustRearchive := false
	switch t.Status {
	case model.TASK_STATUS_QUEUED,
		model.TASK_STATUS_ARCHIVE_RUNNING,
		model.TASK_STATUS_ARCHIVE_ABORTED,
		model.TASK_STATUS_ARCHIVE_PAUSED:
		mustRearchive = true
	}

	var archiver archive.Archiver
	var err error
	switch t.ArchiveFormat {
	case config.AF_TARGZ:
		archiver, err = archive.NewTarGzArchiver(t)
		if err != nil {
			return err
		}
		archivePath := archiver.GetArchiveFilePath(ctx)
		L.Info("Archive: Planning archive")
		err = archiver.Plan(ctx)
		if err != nil {
			return err
		}
		L.Println("Plan Archive: OK")
		err = archive.IsValidTarGz(archivePath)
		if err != nil {
			mustRearchive = true
			L.Debug(err)
			L.Debug(fmt.Sprintf("Existing archive %s is not valid, starting fresh", archivePath))
		}
		info := archiver.GetInfo(ctx)
		if int64(info.SizeInBytes) != t.TotalSize {
			L.Info("Rearchiving because input_path contents have changed since last run")
			mustRearchive = true
		}
	default:
		return fmt.Errorf("archive format %s is not supported yet", t.ArchiveFormat.String())
	}

	if mustRearchive {
		L.Info("Starting fresh because cannot continue from previous state")
		err = archiver.Start(ctx)
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			L.Println()
			return fmt.Errorf("kill signal received, exiting")
		default:
		}
		err = runCmdEnv.TaskRepo.UpdateTaskStatus(ctx, runCmdEnv.TaskId, model.TASK_STATUS_ARCHIVE_COMPLETED)
		if err != nil {
			return err
		}
		err = runCmdEnv.TaskRepo.UpdateTaskContentInfo(ctx,
			runCmdEnv.TaskId, archiver.GetInfo(ctx))
		if err != nil {
			return err
		}
		L.Println("Create Archive: OK")
	} else {
		L.Info("Skipping Archiving because input_path contents have not changed since last run")
	}

	archivePath := archiver.GetArchiveFilePath(ctx)
	L.Printf("Archive: %s\n", archivePath)

	if runCmdEnv.Task.Provider != config.PROVIDER_AWS {
		return fmt.Errorf("unsupported provider: %v", runCmdEnv.Task.Provider.String())
	}

	var storageBackendFactory backend.StorageFactory = &aws.AWSFactory{}
	storageBackend, err := storageBackendFactory.NewStorageBackend()
	if err != nil {
		return err
	}

	err = storageBackend.CreateResourceContainer(ctx)
	if err != nil {
		return err
	}
	L.Info("Upload::CreateResourceContainer OK")

	existingUpload, err := runCmdEnv.UploadRepo.GetUploadByTaskId(ctx, runCmdEnv.TaskId)

	var uploadId int64

	if err != nil && err == database.ErrDoesNotExist {
		uploadRes, err2 := storageBackend.CreateUploadResource(ctx,
			runCmdEnv.Task.Key(), archivePath)
		if err2 != nil {
			return err2
		}
		archiveFileInfo, err2 := file_io.GetFileInfo(archivePath)
		if err2 != nil {
			return err2
		}

		blockSizeInBytes := uploadRes.BlockSizeInBytes
		archiveFileSize := int64(archiveFileInfo.Size)
		var totalBlocks int64 = 1
		if blockSizeInBytes > 0 {
			totalBlocks = (archiveFileSize + blockSizeInBytes - 1) / blockSizeInBytes
		}

		err2 = storageBackend.IsBlockSizeOK(blockSizeInBytes, archiveFileSize)
		if err2 != nil {
			return fmt.Errorf("failed to partition file: %w", err)
		}

		uploadId, err2 = runCmdEnv.UploadRepo.CreateUpload(ctx, runCmdEnv.TaskId,
			uploadRes.Metadata, archivePath,
			int64(archiveFileInfo.Size), archiveFileInfo.ModifiedAt,
			totalBlocks, blockSizeInBytes, time.Now(), time.Now())

		if err2 != nil {
			return fmt.Errorf("failed to save upload information: %w", err)
		}
		L.Printf("Upload::CreateUploadResource OK (upload_id: %d)\n", uploadId)
		err = nil
	} else if err != nil {
		return fmt.Errorf("could not get upload for task id %d: %w", runCmdEnv.TaskId, err)
	} else {
		L.Info("Skipping creating a new upload because upload already exists for a task")
		uploadId = existingUpload.Id
	}
	L.Println(fmt.Sprintf("Task(%d) now has upload Id: %d", runCmdEnv.TaskId, uploadId))

	// upload, err := runCmdEnv.UploadRepo.GetUploadById(ctx, uploadId)

	if err != nil {
		return err
	}

	// TODO: call UploadPart for each part, and call CompleteMultipartUpload after
	err = storageBackend.UploadResource(ctx, uploadId)

	if err != nil {
		return err
	}

	return fmt.Errorf("upload: UploadParts() not implemented yet")
}
