package database

import (
	"context"
	"errors"
	"fmt"
	"glesha/config"
	"glesha/file_io"
	L "glesha/logger"
	"time"
)

type GleshaTaskStatus string

const (
	STATUS_QUEUED            GleshaTaskStatus = "QUEUED"
	STATUS_ARCHIVE_RUNNING                    = "ARCHIVING"
	STATUS_ARCHIVE_PAUSED                     = "ARCHIVE_PAUSED"
	STATUS_ARCHIVE_ABORTED                    = "ARCHIVE_ABORTED"
	STATUS_ARCHIVE_COMPLETED                  = "ARCHIVE_COMPLETED"
	STATUS_UPLOAD_RUNNING                     = "UPLOADING"
	STATUS_UPLOAD_PAUSED                      = "UPLOAD_PAUSED"
	STATUS_UPLOAD_ABORTED                     = "UPLOAD_ABORTED"
	STATUS_UPLOAD_COMPLETED                   = "UPLOAD_COMPLETED"
)

type GleshaTask struct {
	ID             int64
	InputPath      string
	OutputPath     string
	ConfigPath     string
	Status         GleshaTaskStatus
	Provider       config.Provider
	ArchiveFormat  config.ArchiveFormat
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ContentHash    string
	TotalSize      int64
	TotalFileCount int64
}

func (t GleshaTask) String() string {
	return fmt.Sprintf("Task:\n\tID: %d\n\tInputPath: %s\n\tOutputPath: %s\n\tConfigPath: %s\n\tProvider: %s\n\tArchiveFormat: %s\n\tSize: %s\n\tTotalFileCount: %d\n",
		t.ID,
		t.InputPath,
		t.OutputPath,
		t.ConfigPath,
		t.Provider.String(),
		t.ArchiveFormat.String(),
		L.HumanReadableBytes(uint64(t.TotalSize)),
		t.TotalFileCount)
}

var ErrNoExistingTask error = errors.New("No similar task exists in the database")

func (db *DB) FindSimilarTask(
	ctx context.Context,
	inputPath string,
	provider config.Provider,
	filesInfo *file_io.FilesInfo,
	archiveFormat config.ArchiveFormat,
) (*GleshaTask, error) {
	rows, err := db.D.QueryContext(
		ctx,
		`SELECT
         id,input_path, output_path, config_path, provider,
         status, created_at, updated_at, content_hash, size, file_count
    FROM tasks WHERE input_path=? and provider=? and content_hash=? and archive_format=?
    ORDER BY created_at DESC LIMIT 1`,
		inputPath, provider, filesInfo.ContentHash, archiveFormat,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	taskExists := false
	task := GleshaTask{}
	err = rows.Err()
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		taskExists = true
		L.Debug("Task exists")
		var createdAtStr string
		var updatedAtStr string
		err := rows.Scan(&task.ID, &task.InputPath,
			&task.OutputPath, &task.ConfigPath, &task.Provider, &task.Status,
			&createdAtStr, &updatedAtStr, &task.ContentHash, &task.TotalSize, &task.TotalFileCount)
		if err != nil {
			return nil, err
		}
		task.CreatedAt = FromTimeStr(createdAtStr)
		task.UpdatedAt = FromTimeStr(updatedAtStr)
	}
	if !taskExists {
		return nil, ErrNoExistingTask
	}
	L.Debug(fmt.Sprintf("FindSimilarTask() result: %v", task))
	return &task, nil
}

func ToTimeStr(t time.Time) string {
	return t.Local().Format(DateTimeFormat)
}

func FromTimeStr(ts string) time.Time {
	t, err := time.Parse(DateTimeFormat, ts)
	if err != nil {
		L.Error(fmt.Errorf("couldnt parse time for %s: %w", ts, err))
		return time.Now()
	}
	return t
}

// returns task_id upon succeful task creation
func (db *DB) CreateTask(
	ctx context.Context,
	inputPath string,
	outputPath string,
	configPath string,
	archiveFormat config.ArchiveFormat,
	provider config.Provider,
	createdAt time.Time,
	updatedAt time.Time,
	filesInfo *file_io.FilesInfo,
) (int64, error) {
	result, err := db.D.ExecContext(ctx,
		`INSERT INTO tasks (input_path, output_path, config_path, archive_format, provider,status,created_at,updated_at,content_hash,size,file_count)
        VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		inputPath,
		outputPath,
		configPath,
		archiveFormat,
		provider,
		STATUS_QUEUED,
		ToTimeStr(createdAt),
		ToTimeStr(updatedAt),
		filesInfo.ContentHash,
		filesInfo.SizeInBytes,
		filesInfo.TotalFileCount,
	)
	if err != nil {
		return -1, err
	}
	lastInsertId, err := result.LastInsertId()
	return lastInsertId, err
}

func (db *DB) GetTaskById(ctx context.Context, id int64) (*GleshaTask, error) {
	var task GleshaTask
	row := db.D.QueryRowContext(ctx, "SELECT id, input_path, output_path, config_path, status, provider, archive_format, created_at, updated_at, content_hash, size, file_count FROM tasks WHERE id=?", id)
	var createdAtStr string
	var updatedAtStr string
	var providerStr string
	var archiveFormatStr string
	err := row.Scan(
		&task.ID,
		&task.InputPath,
		&task.OutputPath,
		&task.ConfigPath,
		&task.Status,
		&providerStr,
		&archiveFormatStr,
		&createdAtStr,
		&updatedAtStr,
		&task.ContentHash,
		&task.TotalSize,
		&task.TotalFileCount,
	)
	if err != nil {
		return nil, err
	}
	task.CreatedAt = FromTimeStr(createdAtStr)
	task.UpdatedAt = FromTimeStr(updatedAtStr)
	task.Provider, err = config.ParseProvider(providerStr)
	task.ArchiveFormat, err = config.ParseArchiveFormat(archiveFormatStr)
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func (db *DB) UpdateTaskStatus(ctx context.Context, id int64, status GleshaTaskStatus) error {
	res, err := db.D.ExecContext(ctx,
		"UPDATE tasks SET status=?, updated_at=? WHERE id=?",
		status,
		ToTimeStr(time.Now()),
		id)

	if err != nil {
		return fmt.Errorf("Couldn't update status %s for task %d: %w", status, id, err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return fmt.Errorf("Was expecting %d row updates, but %d rows were updated", 1, rowsAffected)
	}
	L.Debug(fmt.Sprintf("Updated task(%d) status to: %s", id, status))
	return err
}

func (db *DB) UpdateTaskContentInfo(ctx context.Context, id int64, info *file_io.FilesInfo) error {
	res, err := db.D.ExecContext(ctx,
		"UPDATE tasks SET size = ?, content_hash = ?, file_count = ?, updated_at = ? WHERE id = ?",
		info.SizeInBytes,
		info.ContentHash,
		info.TotalFileCount,
		ToTimeStr(time.Now()),
		id,
	)
	if err != nil {
		return fmt.Errorf("Couldn't update content info for task %d: %w", id, err)
	}
	rowsAffected, err := res.RowsAffected()
	if rowsAffected != 1 {
		return fmt.Errorf("Was expecting %d row updates, but %d rows were updated", 1, rowsAffected)
	}
	L.Debug(fmt.Sprintf("Updated task(%d) contents: content_hash, file_count", id))
	return err
}
