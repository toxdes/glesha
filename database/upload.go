package database

import (
	"context"
	"database/sql"
	"fmt"
	L "glesha/logger"
	"time"
)

type GleshaUploadStatus string

const (
	UPLOAD_STATUS_QUEUED    GleshaUploadStatus = "QUEUED"
	UPLOAD_STATUS_RUNNING   GleshaUploadStatus = "UPLOADING"
	UPLOAD_STATUS_ABORTED   GleshaUploadStatus = "ABORTED"
	UPLOAD_STATUS_COMPLETED GleshaUploadStatus = "COMPLETED"
	UPLOAD_STATUS_FAILED    GleshaUploadStatus = "FAILED"
)

type GleshaUpload struct {
	ID                         int64
	TaskID                     int64
	StorageBackendMetadataJson string
	FilePath                   string
	FileSize                   int64
	FileLastModifiedAt         time.Time
	UploadedBytes              int64
	UploadedBlocks             int64
	TotalBlocks                int64
	BlockSizeInBytes           int64
	Status                     GleshaUploadStatus
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
	CompletedAt                time.Time
}

func (t *GleshaUpload) String() string {
	return fmt.Sprintf("Upload:\n\tID: %d\n\tTaskID: %d\n\t\tStorageBackendMetadataJson: %s\n\tFilePath: %s\n\tFileSize: %s\n\tUploadedBytes: %d\n\tUploadedBlocks: %d\n\tTotalParts: %d\n\tStatus: %s\n",
		t.ID,
		t.TaskID,
		t.StorageBackendMetadataJson,
		t.FilePath,
		L.HumanReadableBytes(uint64(t.FileSize)),
		t.UploadedBytes,
		t.UploadedBlocks, t.TotalBlocks, t.Status)
}

func (db *DB) CreateUpload(
	ctx context.Context,
	taskID int64,
	storageBackendMetadataJson string,
	filePath string,
	fileSize int64,
	fileLastModifiedAt time.Time,
	totalBlocks int64,
	blockSizeInBytes int64,
	createdAt time.Time,
	updatedAt time.Time,
) (int64, error) {
	result, err := db.D.ExecContext(ctx,
		`INSERT INTO uploads (
		task_id,
		storage_backend_metadata_json,
		file_path,
		file_size,
		file_last_modified_at,
		total_blocks,
		block_size_in_bytes,
		created_at,
		updated_at
	) VALUES (?,?,?,?,?,?,?,?,?) 
	 ON CONFLICT(task_id) DO NOTHING`,
		taskID,
		storageBackendMetadataJson,
		filePath,
		fileSize,
		ToTimeStr(fileLastModifiedAt),
		totalBlocks,
		blockSizeInBytes,
		ToTimeStr(createdAt),
		ToTimeStr(updatedAt),
	)
	if err != nil {
		return -1, err
	}
	rowsAffected, err := result.RowsAffected()

	if err != nil {
		return -1, err
	}

	if rowsAffected == 0 {
		upload, err := db.GetUploadByTaskId(ctx, taskID)
		if err != nil {
			return -1, err
		}
		return upload.ID, nil
	}
	lastInsertId, err := result.LastInsertId()
	return lastInsertId, err
}

func (db *DB) GetUploadByTaskId(ctx context.Context, taskID int64) (*GleshaUpload, error) {
	row := db.D.QueryRowContext(ctx, `SELECT
		id,
		task_id,
		storage_backend_metadata_json,
		file_path,
		file_size,
		file_last_modified_at,
		uploaded_bytes,
		uploaded_blocks,
		total_blocks,
		block_size_in_bytes,
		status,
		created_at,
		updated_at,
		completed_at
	from uploads WHERE task_id=?`, taskID)

	var u GleshaUpload
	var fileLastModifiedAtStr string
	var createdAtStr string
	var updatedAtStr string
	var completedAtStr sql.NullString
	err := row.Scan(
		&u.ID,
		&u.TaskID,
		&u.StorageBackendMetadataJson,
		&u.FilePath,
		&u.FileSize,
		&fileLastModifiedAtStr,
		&u.UploadedBytes,
		&u.UploadedBlocks,
		&u.TotalBlocks,
		&u.BlockSizeInBytes,
		&u.Status,
		&createdAtStr,
		&updatedAtStr,
		&completedAtStr,
	)
	if err != nil {
		return nil, err
	}
	u.FileLastModifiedAt = FromTimeStr(fileLastModifiedAtStr)
	u.CreatedAt = FromTimeStr(createdAtStr)
	u.UpdatedAt = FromTimeStr(updatedAtStr)
	if completedAtStr.Valid {
		u.CompletedAt = FromTimeStr(completedAtStr.String)
	}
	return &u, nil
}
