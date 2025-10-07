package upload

import (
	"context"
	"fmt"
	"glesha/backend"
	L "glesha/logger"
)

type Uploader struct {
	archiveFilePath string
	backend         backend.Backend
}

func NewUploader(archiveFilePath string, b backend.Backend) *Uploader {
	return &Uploader{archiveFilePath: archiveFilePath, backend: b}
}

func (u *Uploader) Plan(ctx context.Context) error {
	err := u.backend.CreateResourceContainer(ctx)
	if err != nil {
		return err
	}
	L.Println("Create Bucket: OK")
	return nil
}

func (u *Uploader) Start(ctx context.Context) error {
	return u.backend.UploadResource(ctx, u.archiveFilePath)
}

func (u *Uploader) Pause(ctx context.Context) error {
	return fmt.Errorf("Not implemented yet")
}

func (u *Uploader) Abort(ctx context.Context) error {
	return fmt.Errorf("Not implemented yet")
}
