package repository

import (
	"context"
	"database/sql"
	"fmt"
	"glesha/backend"
	"glesha/database"
	"glesha/database/model"
	"time"
)

type UploadRepository interface {
	CreateUpload(
		ctx context.Context,
		taskId int64,
		storageBackendMetadata backend.StorageMetadata,
		filePath string,
		fileSize int64,
		fileLastModifiedAt time.Time,
		totalBlocks int64,
		blockSizeInBytes int64,
		createdAt time.Time,
		updatedAt time.Time,
	) (uploadId int64, err error)

	GetUploadByTaskId(
		ctx context.Context,
		taskId int64,
	) (*model.Upload, error)

	GetUploadById(
		ctx context.Context,
		taskId int64,
	) (*model.Upload, error)
}

type uploadRepository struct {
	db *database.DB
}

func NewUploadRepository(db *database.DB) UploadRepository {
	return uploadRepository{db: db}
}

func (u uploadRepository) CreateUpload(
	ctx context.Context,
	taskId int64,
	storageBackendMetadata backend.StorageMetadata,
	filePath string,
	fileSize int64,
	fileLastModifiedAt time.Time,
	totalBlocks int64,
	blockSizeInBytes int64,
	createdAt time.Time,
	updatedAt time.Time,
) (int64, error) {

	result, err := u.db.D.ExecContext(ctx,
		`INSERT INTO uploads (
		task_id,
		storage_backend_metadata_json,
		storage_backend_metadata_schema_version,
		file_path,
		file_size,
		file_last_modified_at,
		total_blocks,
		block_size_in_bytes,
		created_at,
		updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?) 
	 ON CONFLICT(task_id) DO NOTHING`,
		taskId,
		storageBackendMetadata.Json,
		storageBackendMetadata.SchemaVersion,
		filePath,
		fileSize,
		database.ToTimeStr(fileLastModifiedAt),
		totalBlocks,
		blockSizeInBytes,
		database.ToTimeStr(createdAt),
		database.ToTimeStr(updatedAt),
	)
	if err != nil {
		return -1, err
	}
	rowsAffected, err := result.RowsAffected()

	if err != nil {
		return -1, err
	}

	if rowsAffected == 0 {
		upload, err := u.GetUploadByTaskId(ctx, taskId)
		if err != nil {
			return -1, err
		}
		return upload.Id, nil
	}
	lastInsertId, err := result.LastInsertId()

	return lastInsertId, err
}

func (u uploadRepository) GetUploadByTaskId(ctx context.Context, taskId int64) (*model.Upload, error) {

	row := u.db.D.QueryRowContext(ctx, `SELECT
		id,
		task_id,
		storage_backend_metadata_json,
		storage_backend_metadata_schema_version,
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
	from uploads WHERE task_id=?`, taskId)

	var upload model.Upload
	var fileLastModifiedAtStr string
	var createdAtStr string
	var updatedAtStr string
	var completedAtStr sql.NullString
	err := row.Scan(
		&upload.Id,
		&upload.TaskId,
		&upload.StorageBackendMetadata.Json,
		&upload.StorageBackendMetadata.SchemaVersion,
		&upload.FilePath,
		&upload.FileSize,
		&fileLastModifiedAtStr,
		&upload.UploadedBytes,
		&upload.UploadedBlocks,
		&upload.TotalBlocks,
		&upload.BlockSizeInBytes,
		&upload.Status,
		&createdAtStr,
		&updatedAtStr,
		&completedAtStr,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, database.ErrDoesNotExist
		}
		return nil, fmt.Errorf("could not find upload for task id %d: %w", taskId, err)
	}
	upload.FileLastModifiedAt = database.FromTimeStr(fileLastModifiedAtStr)
	upload.CreatedAt = database.FromTimeStr(createdAtStr)
	upload.UpdatedAt = database.FromTimeStr(updatedAtStr)
	if completedAtStr.Valid {
		upload.CompletedAt = database.FromTimeStr(completedAtStr.String)
	}
	return &upload, nil
}

func (u uploadRepository) GetUploadById(ctx context.Context, uploadId int64) (*model.Upload, error) {

	row := u.db.D.QueryRowContext(ctx, `SELECT
		id,
		task_id,
		storage_backend_metadata_json,
		storage_backend_metadata_schema_version,
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
	from uploads WHERE id=?`, uploadId)

	var upload model.Upload
	var fileLastModifiedAtStr string
	var createdAtStr string
	var updatedAtStr string
	var completedAtStr sql.NullString
	err := row.Scan(
		&upload.Id,
		&upload.TaskId,
		&upload.StorageBackendMetadata.Json,
		&upload.StorageBackendMetadata.SchemaVersion,
		&upload.FilePath,
		&upload.FileSize,
		&upload.FileLastModifiedAt,
		&upload.UploadedBytes,
		&upload.UploadedBlocks,
		&upload.TotalBlocks,
		&upload.BlockSizeInBytes,
		&upload.Status,
		&createdAtStr,
		&updatedAtStr,
		&completedAtStr,
	)
	if err != nil {
		return nil, fmt.Errorf("could not find upload for id %d: %w", uploadId, err)
	}
	upload.FileLastModifiedAt = database.FromTimeStr(fileLastModifiedAtStr)
	upload.CreatedAt = database.FromTimeStr(createdAtStr)
	upload.UpdatedAt = database.FromTimeStr(updatedAtStr)
	if completedAtStr.Valid {
		upload.CompletedAt = database.FromTimeStr(completedAtStr.String)
	}
	return &upload, nil
}
