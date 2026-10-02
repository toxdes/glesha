package model

import "fmt"

type Status string

const (
	STATUS_RUNNING   Status = "RUNNING"
	STATUS_PENDING   Status = "PENDING"
	STATUS_COMPLETED Status = "COMPLETED"
	STATUS_FAILED    Status = "FAILED"
	STATUS_ABORTED   Status = "ABORTED"
)

func (s Status) String() string { return string(s) }

func ParseStatus(value string) (Status, error) {
	s := Status(value)
	switch s {
	case STATUS_RUNNING, STATUS_PENDING, STATUS_COMPLETED, STATUS_FAILED, STATUS_ABORTED:
		return s, nil
	default:
		return "", fmt.Errorf("model: unknown status %q", value)
	}
}
