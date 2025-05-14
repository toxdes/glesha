package archive

import "glesha/file_io"

type ArchiveStatus int

const (
	STATUS_IN_QUEUE ArchiveStatus = iota
	STATUS_PLANNING
	STATUS_PLANNED
	STATUS_RUNNING
	STATUS_PAUSED
	STATUS_ERROR
	STATUS_COMPLETED
)

type Progress struct {
	Done   uint64
	Total  uint64
	Status ArchiveStatus
}

type Archive interface {
	Plan() error
	GetInfo() (*file_io.FilesInfo, error)
	Start() error
	GetProgress() (*Progress, error)
	Pause() error
	Abort() error
}
