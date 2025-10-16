package repository

import (
	"context"
	"database/sql"
	"fmt"
	"glesha/config"
	"glesha/database"
	"glesha/database/model"
	"glesha/file_io"
	L "glesha/logger"
	"time"
)

type TaskRepository interface {
	FindSimilarTask(
		ctx context.Context,
		inputPath string,
		provider config.Provider,
		filesInfo *file_io.FilesInfo,
		archiveFormat config.ArchiveFormat,
	) (*model.Task, error)

	UpdateTaskContentInfo(
		ctx context.Context,
		id int64,
		info *file_io.FilesInfo,
	) error

	CreateTask(
		ctx context.Context,
		inputPath string,
		outputPath string,
		configPath string,
		archiveFormat config.ArchiveFormat,
		provider config.Provider,
		createdAt time.Time,
		updatedAt time.Time,
		filesInfo *file_io.FilesInfo,
	) (int64, error)

	GetTaskById(
		ctx context.Context,
		id int64,
	) (*model.Task, error)

	UpdateTaskStatus(
		ctx context.Context,
		taskId int64,
		status model.TaskStatus,
	) error
}

type taskRepository struct {
	db *database.DB
}

func NewTaskRepository(db *database.DB) TaskRepository {
	return taskRepository{db: db}
}

func (t taskRepository) FindSimilarTask(
	ctx context.Context,
	inputPath string,
	provider config.Provider,
	filesInfo *file_io.FilesInfo,
	archiveFormat config.ArchiveFormat,
) (*model.Task, error) {

	rows, err := t.db.D.QueryContext(
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
	task := model.Task{}
	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("could not find a task with similar contents: %w", err)
	}
	for rows.Next() {
		taskExists = true
		L.Debug("Task exists")
		var createdAtStr string
		var updatedAtStr string
		err := rows.Scan(&task.Id, &task.InputPath,
			&task.OutputPath, &task.ConfigPath, &task.Provider, &task.Status,
			&createdAtStr, &updatedAtStr, &task.ContentHash, &task.TotalSize, &task.TotalFileCount)
		if err != nil {
			return nil, err
		}
		task.CreatedAt = database.FromTimeStr(createdAtStr)
		task.UpdatedAt = database.FromTimeStr(updatedAtStr)
	}
	if !taskExists {
		return nil, database.ErrDoesNotExist
	}
	L.Debug(fmt.Sprintf("FindSimilarTask() result: %v", task))
	return &task, nil
}

// returns task_id upon succeful task creation
func (t taskRepository) CreateTask(
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
	result, err := t.db.D.ExecContext(ctx,
		`INSERT INTO tasks (input_path, output_path, config_path, archive_format, provider,status,created_at,updated_at,content_hash,size,file_count)
        VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		inputPath,
		outputPath,
		configPath,
		archiveFormat,
		provider,
		model.UPLOAD_STATUS_QUEUED,
		database.ToTimeStr(createdAt),
		database.ToTimeStr(updatedAt),
		filesInfo.ContentHash,
		filesInfo.SizeInBytes,
		filesInfo.TotalFileCount,
	)
	if err != nil {
		return -1, fmt.Errorf("could not create task for path %s: %w", inputPath, err)
	}
	lastInsertId, err := result.LastInsertId()
	if err != nil {
		return -1, fmt.Errorf("could not get last insert id for create task: %w", err)
	}
	return lastInsertId, nil
}

func (t taskRepository) GetTaskById(ctx context.Context, taskId int64) (*model.Task, error) {
	var task model.Task
	row := t.db.D.QueryRowContext(ctx, "SELECT id, input_path, output_path, config_path, status, provider, archive_format, created_at, updated_at, content_hash, size, file_count FROM tasks WHERE id=?", taskId)
	var createdAtStr string
	var updatedAtStr string
	var providerStr string
	var archiveFormatStr string

	err := row.Scan(
		&task.Id,
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
		if err == sql.ErrNoRows {
			return nil, database.ErrDoesNotExist
		}
		return nil, fmt.Errorf("could not get task for id %d: %w", taskId, err)
	}

	task.CreatedAt = database.FromTimeStr(createdAtStr)
	task.UpdatedAt = database.FromTimeStr(updatedAtStr)
	task.Provider, err = config.ParseProvider(providerStr)
	if err != nil {
		return nil, fmt.Errorf("could not parse provider %s: %w", providerStr, err)
	}
	task.ArchiveFormat, err = config.ParseArchiveFormat(archiveFormatStr)
	if err != nil {
		return nil, fmt.Errorf("could not parse archive format %s: %w", archiveFormatStr, err)
	}
	return &task, nil
}

func (t taskRepository) UpdateTaskStatus(ctx context.Context, taskId int64, status model.TaskStatus) error {

	res, err := t.db.D.ExecContext(ctx,
		"UPDATE tasks SET status=?, updated_at=? WHERE id=?",
		status,
		database.ToTimeStr(time.Now()),
		taskId)

	if err != nil {
		return fmt.Errorf("could not update status %s for task %d: %w", status, taskId, err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("could not update status for task %d: %w", taskId, err)
	}
	if rowsAffected != 1 {
		return fmt.Errorf("was expecting %d row updates, but %d rows were updated", 1, rowsAffected)
	}
	L.Debug(fmt.Sprintf("Updated task(%d) status to: %s", taskId, status))
	return err
}

func (t taskRepository) UpdateTaskContentInfo(ctx context.Context, id int64, info *file_io.FilesInfo) error {

	res, err := t.db.D.ExecContext(ctx,
		"UPDATE tasks SET size = ?, content_hash = ?, file_count = ?, updated_at = ? WHERE id = ?",
		info.SizeInBytes,
		info.ContentHash,
		info.TotalFileCount,
		database.ToTimeStr(time.Now()),
		id,
	)
	if err != nil {
		return fmt.Errorf("could not update content info for task %d: %w", id, err)
	}
	rowsAffected, err := res.RowsAffected()
	if rowsAffected != 1 {
		return fmt.Errorf("was expecting %d row updates, but %d rows were updated", 1, rowsAffected)
	}
	L.Debug(fmt.Sprintf("Updated task(%d) contents: content_hash, file_count", id))
	return err
}
