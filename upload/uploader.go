package upload

import (
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

func (u *Uploader) Plan() error {
	err := u.backend.CreateResourceContainer()
	if err != nil {
		return err
	}
	L.Println("Create Bucket: OK")
	return nil
}

func (u *Uploader) Start() error {
	return u.backend.UploadResource(u.archiveFilePath)
}

func (u *Uploader) Pause() error {
	return fmt.Errorf("Not implemented yet")
}

func (u *Uploader) Abort() error {
	return fmt.Errorf("Not implemented yet")
}
