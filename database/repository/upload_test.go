package repository

import (
	"context"
	"testing"
	"time"

	"glesha/backend"
	"glesha/config"
	"glesha/file_io"

	"github.com/stretchr/testify/assert"
	_ "modernc.org/sqlite"
)

func TestCreateAndGetUpload(t *testing.T) {
	db := setupTestDB(t)
	taskRepo := NewTaskRepository(db)
	uploadRepo := NewUploadRepository(db)
	defer db.Close(context.Background())

	filesInfo := &file_io.FilesInfo{
		TotalFileCount: 10,
		SizeInBytes:    1024,
		ContentHash:    "test-hash",
	}

	taskId, err := taskRepo.CreateTask(
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
	metadata := backend.StorageMetadata{
		Json:          "metadata",
		SchemaVersion: 1,
	}
	uploadId, err := uploadRepo.CreateUpload(
		context.Background(),
		taskId,
		metadata,
		"/path/to/file",
		2048,
		time.Now(),
		10,
		204,
		time.Now(),
		time.Now(),
	)
	assert.NoError(t, err)

	upload, err := uploadRepo.GetUploadByTaskId(context.Background(), taskId)
	assert.NoError(t, err)
	assert.NotNil(t, upload)
	assert.Equal(t, uploadId, upload.Id)
	assert.Equal(t, taskId, upload.TaskId)
	assert.Equal(t, metadata.Json, upload.StorageBackendMetadata.Json)
	assert.Equal(t, metadata.SchemaVersion, upload.StorageBackendMetadata.SchemaVersion)
	assert.True(t, upload.StorageBackendMetadata.SchemaVersion > 0)

	// Try to create again, should return existing
	newUploadId, err := uploadRepo.CreateUpload(
		context.Background(),
		taskId,
		upload.StorageBackendMetadata,
		"/path/to/file2",
		4096,
		time.Now(),
		20,
		204,
		time.Now(),
		time.Now(),
	)
	assert.NoError(t, err)
	assert.Equal(t, uploadId, newUploadId)

	newUpload, err := uploadRepo.GetUploadByTaskId(context.Background(), taskId)
	assert.NoError(t, err)
	assert.Equal(t, metadata.Json, newUpload.StorageBackendMetadata.Json)
	assert.Equal(t, metadata.SchemaVersion, newUpload.StorageBackendMetadata.SchemaVersion)
}
