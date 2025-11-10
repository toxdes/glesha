package model

import (
	"fmt"
	"glesha/backend"
	L "glesha/logger"
	"time"
)

type UploadStatus string

const (
	UPLOAD_STATUS_QUEUED    UploadStatus = "QUEUED"
	UPLOAD_STATUS_RUNNING   UploadStatus = "UPLOADING"
	UPLOAD_STATUS_ABORTED   UploadStatus = "ABORTED"
	UPLOAD_STATUS_COMPLETED UploadStatus = "COMPLETED"
	UPLOAD_STATUS_FAILED    UploadStatus = "FAILED"
)

const CREATE_UPLOADS_TABLE = `CREATE TABLE IF NOT EXISTS uploads(
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				
				task_id INTEGER NOT NULL UNIQUE,

				storage_backend_metadata_json TEXT, 
				storage_backend_metadata_schema_version INTEGER NOT NULL,
				file_path TEXT NOT NULL,
				file_size INTEGER NOT NULL,
				file_last_modified_at TEXT NOT NULL,

				uploaded_bytes INTEGER DEFAULT 0,
				uploaded_blocks INTEGER DEFAULT 0,
				total_blocks INTEGER NOT NULL,
				block_size_in_bytes INTEGER NOT NULL,

				status TEXT NOT NULL DEFAULT "QUEUED",
				created_at TEXT NOT NULL, 
				updated_at TEXT NOT NULL,
				completed_at TEXT,

				UNIQUE(task_id),
				FOREIGN KEY(task_id) REFERENCES tasks(id) ON DELETE CASCADE
);`

type Upload struct {
	Id                     int64
	TaskId                 int64
	StorageBackendMetadata backend.StorageMetadata
	FilePath               string
	FileSize               int64
	FileLastModifiedAt     time.Time
	UploadedBytes          int64
	UploadedBlocks         int64
	TotalBlocks            int64
	BlockSizeInBytes       int64
	Status                 UploadStatus
	CreatedAt              time.Time
	UpdatedAt              time.Time
	CompletedAt            time.Time
}

func (t *Upload) String() string {
	return fmt.Sprintf("Upload:\n\tId: %d\n\tTaskId: %d\n\t\tStorageBackendMetadataJson: %s\n\tFilePath: %s\n\tFileSize: %s\n\tUploadedBytes: %d\n\tUploadedBlocks: %d\n\tTotalParts: %d\n\tStatus: %s\n",
		t.Id,
		t.TaskId,
		t.StorageBackendMetadata.Json,
		t.FilePath,
		L.HumanReadableBytes(uint64(t.FileSize), 2),
		t.UploadedBytes,
		t.UploadedBlocks, t.TotalBlocks, t.Status)
}
