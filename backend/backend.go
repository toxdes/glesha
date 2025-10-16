package backend

import (
	"context"
)

type StorageMetadata struct {
	Json          string
	SchemaVersion int64
}
type CreateUploadResult struct {
	Metadata         StorageMetadata
	BlockSizeInBytes int64
}

type StorageBackend interface {
	CreateResourceContainer(ctx context.Context) error
	CreateUploadResource(ctx context.Context, taskKey string, resourceFilePath string) (*CreateUploadResult, error)
	UploadResource(ctx context.Context, uploadId int64) error
	IsBlockSizeOK(blockSize int64, fileSize int64) error
}

type StorageFactory interface {
	NewStorageBackend() (StorageBackend, error)
}
