package database

import (
	"context"
	"testing"
	"time"

	"glesha/config"
	"glesha/file_io"

	"github.com/stretchr/testify/assert"
	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *DB {
	db, err := NewDB(":memory:")
	assert.NoError(t, err)
	err = db.Init(context.Background())
	assert.NoError(t, err)
	return db
}

func TestCreateAndGetTask(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close(context.Background())

	filesInfo := &file_io.FilesInfo{
		TotalFileCount: 10,
		SizeInBytes:    1024,
		ContentHash:    "test-hash",
	}

	taskID, err := db.CreateTask(
		context.Background(),
		"/input",
		"/output",
		"/config",
		config.AF_TARGZ,
		config.PROVIDER_AWS,
		time.Now(),
		time.Now(),
		filesInfo,
	)
	assert.NoError(t, err)

	task, err := db.GetTaskById(context.Background(), taskID)
	assert.NoError(t, err)
	assert.NotNil(t, task)
	assert.Equal(t, taskID, task.ID)
	assert.Equal(t, "/input", task.InputPath)
	assert.Equal(t, int64(1024), task.TotalSize)
}

func TestFindSimilarTask(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close(context.Background())

	filesInfo := &file_io.FilesInfo{
		TotalFileCount: 10,
		SizeInBytes:    1024,
		ContentHash:    "test-hash",
	}

	t.Run("NoSimilarTask", func(t *testing.T) {
		_, err := db.FindSimilarTask(context.Background(), "/input", config.PROVIDER_AWS, filesInfo, config.AF_TARGZ)
		assert.ErrorIs(t, err, ErrNoExistingTask)
	})

	t.Run("SimilarTaskExists", func(t *testing.T) {
		taskID, err := db.CreateTask(
			context.Background(),
			"/input",
			"/output",
			"/config",
			config.AF_TARGZ,
			config.PROVIDER_AWS,
			time.Now(),
			time.Now(),
			filesInfo,
		)
		assert.NoError(t, err)

		task, err := db.FindSimilarTask(context.Background(), "/input", config.PROVIDER_AWS, filesInfo, config.AF_TARGZ)
		assert.NoError(t, err)
		assert.NotNil(t, task)
		assert.Equal(t, taskID, task.ID)
	})
}

func TestUpdateTaskStatus(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close(context.Background())

	filesInfo := &file_io.FilesInfo{
		TotalFileCount: 10,
		SizeInBytes:    1024,
		ContentHash:    "test-hash",
	}

	taskID, err := db.CreateTask(
		context.Background(),
		"/input",
		"/output",
		"/config",
		config.AF_TARGZ,
		config.PROVIDER_AWS,
		time.Now(),
		time.Now(),
		filesInfo,
	)
	assert.NoError(t, err)

	err = db.UpdateTaskStatus(context.Background(), taskID, STATUS_ARCHIVE_COMPLETED)
	assert.NoError(t, err)

	task, err := db.GetTaskById(context.Background(), taskID)
	assert.NoError(t, err)
	assert.Equal(t, STATUS_ARCHIVE_COMPLETED, task.Status)
}

func TestUpdateTaskContentInfo(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close(context.Background())

	filesInfo := &file_io.FilesInfo{
		TotalFileCount: 10,
		SizeInBytes:    1024,
		ContentHash:    "test-hash",
	}

	taskID, err := db.CreateTask(
		context.Background(),
		"/input",
		"/output",
		"/config",
		config.AF_TARGZ,
		config.PROVIDER_AWS,
		time.Now(),
		time.Now(),
		filesInfo,
	)
	assert.NoError(t, err)

	newFilesInfo := &file_io.FilesInfo{
		TotalFileCount: 20,
		SizeInBytes:    2048,
		ContentHash:    "new-test-hash",
	}

	err = db.UpdateTaskContentInfo(context.Background(), taskID, newFilesInfo)
	assert.NoError(t, err)

	task, err := db.GetTaskById(context.Background(), taskID)
	assert.NoError(t, err)
	assert.Equal(t, int64(2048), task.TotalSize)
	assert.Equal(t, "new-test-hash", task.ContentHash)
}
