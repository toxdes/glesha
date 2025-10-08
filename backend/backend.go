package backend

import (
	"context"
)

type CreateUploadResult struct {
	StorageBackendMetadataJson string
}
type StorageBackend interface {
	CreateResourceContainer(ctx context.Context) error
	CreateUploadResource(ctx context.Context, taskKey string, resourceFilePath string) (*CreateUploadResult, error)
	UploadResource(ctx context.Context, uploadID int64) error
}

type StorageFactory interface {
	NewStorageBackend() (StorageBackend, error)
}
