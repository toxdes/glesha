package archive

import (
	"glesha/file_io"
)

type ArchiveStatus int

const (
	STATUS_IN_QUEUE ArchiveStatus = iota
	STATUS_PLANNING
	STATUS_PLANNED
	STATUS_RUNNING
	STATUS_PAUSED
	STATUS_ABORTED
	STATUS_COMPLETED
)

type Progress struct {
	Done   uint64
	Total  uint64
	Status ArchiveStatus
}

type Archiver interface {
	Plan() error
	Start() error
	Pause() error
	Abort() error
	UpdateStatus(ArchiveStatus) error
	GetInfo() (*file_io.FilesInfo, error)
	GetProgress() (*Progress, error)
	GetArchiveFilePath() string
}

func (status ArchiveStatus) String() string {
	switch status {
	case STATUS_IN_QUEUE:
		return "IN_QUEUE"
	case STATUS_PLANNING:
		return "PLANNING"
	case STATUS_PLANNED:
		return "PLANNED"
	case STATUS_RUNNING:
		return "RUNNING"
	case STATUS_PAUSED:
		return "PAUSED"
	case STATUS_ABORTED:
		return "ABORTED"
	case STATUS_COMPLETED:
		return "COMPLETE"
	default:
		return "UNKNOWN"
	}
}
