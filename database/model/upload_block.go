package model

import (
	"fmt"
	"time"
)

const CREATE_UPLOAD_BLOCKS_TABLE = `CREATE TABLE IF NOT EXISTS upload_blocks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,

			upload_id INTEGER NOT NULL,

			file_offset INTEGER NOT NULL,
			size INTEGER NOT NULL,

			status TEXT NOT NULL DEFAULT "UB_QUEUED",
			etag TEXT,
			checksum TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			uploaded_at TEXT,
			error_message TEXT,
			error_count INTEGER NOT NULL DEFAULT 0,

			FOREIGN KEY(upload_id) REFERENCES uploads(id) ON DELETE CASCADE
);`

// TODO: maybe update completed_at automatically as well?
// currently we are doing it manually
const CREATE_UPDATE_UPLOAD_PROGRESS_TRIGGER = `CREATE TRIGGER IF NOT EXISTS 
update_upload_progress
AFTER UPDATE ON upload_blocks
FOR EACH ROW
WHEN NEW.status = 'UB_COMPLETE'
BEGIN
	UPDATE uploads
	SET uploaded_blocks = uploaded_blocks + 1,
			uploaded_bytes = uploaded_bytes + NEW.size
	WHERE id = NEW.upload_id;
END;`

type UploadBlockStatus string

const (
	UB_STATUS_QUEUED   UploadBlockStatus = "UB_QUEUED"
	UB_STATUS_RUNNING  UploadBlockStatus = "UB_RUNNING"
	UB_STATUS_COMPLETE UploadBlockStatus = "UB_COMPLETE"
	UB_STATUS_ERROR    UploadBlockStatus = "UB_ERROR"
)

type UploadBlock struct {
	Id         int64
	UploadId   int64
	FileOffset int64
	Size       int64
	Status     UploadBlockStatus
	Etag       string
	Checksum   string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	UploadedAt *time.Time
	ErrorMsg   string
	ErrorCount int64
}

func (ub *UploadBlock) String() string {
	return fmt.Sprintf("UploadBlock:\n\tId:%d\n\tOffset:%d\n\tSize:%d\n\tStatus:%s\n\tErrorMsg:%v\n\tErrorCount:%d\n",
		ub.Id,
		ub.FileOffset,
		ub.Size,
		ub.Status,
		ub.ErrorMsg,
		ub.ErrorCount,
	)
}
