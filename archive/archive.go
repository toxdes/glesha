package archive

import (
	"context"
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
	Plan(context.Context) error
	Start(context.Context) error
	Pause(context.Context) error
	Abort(context.Context) error
	UpdateStatus(context.Context, ArchiveStatus) error
	GetInfo(context.Context) *file_io.FilesInfo
	GetProgress(context.Context) (*Progress, error)
	GetArchiveFilePath(context.Context) string
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
