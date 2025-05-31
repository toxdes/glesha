package upload

import (
	"fmt"
	"glesha/backend"
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
	fmt.Println("Create Bucket: OK")
	return nil
}

func (u *Uploader) Start() error {
	return fmt.Errorf("Not implemented yet")
}

func (u *Uploader) Pause() error {
	return fmt.Errorf("Not implemented yet")
}

func (u *Uploader) Abort() error {
	return fmt.Errorf("Not implemented yet")
}
