package model

import (
	"fmt"
	"glesha/checksum"
	"glesha/config"
	L "glesha/logger"
	"time"
)

type TaskStatus string

const (
	TASK_STATUS_QUEUED            TaskStatus = "QUEUED"
	TASK_STATUS_ARCHIVE_RUNNING   TaskStatus = "ARCHIVING"
	TASK_STATUS_ARCHIVE_PAUSED    TaskStatus = "ARCHIVE_PAUSED"
	TASK_STATUS_ARCHIVE_ABORTED   TaskStatus = "ARCHIVE_ABORTED"
	TASK_STATUS_ARCHIVE_COMPLETED TaskStatus = "ARCHIVE_COMPLETED"
	TASK_STATUS_UPLOAD_RUNNING    TaskStatus = "UPLOADING"
	TASK_STATUS_UPLOAD_PAUSED     TaskStatus = "UPLOAD_PAUSED"
	TASK_STATUS_UPLOAD_ABORTED    TaskStatus = "UPLOAD_ABORTED"
	TASK_STATUS_UPLOAD_COMPLETED  TaskStatus = "UPLOAD_COMPLETED"
)

const CREATE_TASKS_TABLE = `CREATE TABLE IF NOT EXISTS tasks (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        
				input_path TEXT NOT NULL,
        output_path TEXT NOT NULL,
        config_path TEXT NOT NULL,
        
				provider TEXT NOT NULL,
        archive_format TEXT NOT NULL,
        
				status TEXT NOT NULL,
        
				created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        
				content_hash TEXT NOT NULL,
        size INTEGER NOT NULL,
        file_count INTEGER NOT NULL
);`

type Task struct {
	Id             int64
	InputPath      string
	OutputPath     string
	ConfigPath     string
	Status         TaskStatus
	Provider       config.Provider
	ArchiveFormat  config.ArchiveFormat
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ContentHash    string
	TotalSize      int64
	TotalFileCount int64
}

func (t *Task) String() string {
	return fmt.Sprintf("Task:\n\tId: %d\n\tInputPath: %s\n\tOutputPath: %s\n\tConfigPath: %s\n\tProvider: %s\n\tArchiveFormat: %s\n\tSize: %s\n\tTotalFileCount: %d\n",
		t.Id,
		t.InputPath,
		t.OutputPath,
		t.ConfigPath,
		t.Provider.String(),
		t.ArchiveFormat.String(),
		L.HumanReadableBytes(uint64(t.TotalSize), 2),
		t.TotalFileCount)
}

func (t *Task) Key() string {
	return fmt.Sprintf("%d-%s-%d", t.Id, checksum.HexEncodeStr([]byte(t.ContentHash)), t.CreatedAt.UnixMilli())
}
