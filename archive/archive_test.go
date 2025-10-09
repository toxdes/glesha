package archive

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestArchiveStatus_String(t *testing.T) {
	tests := []struct {
		name   string
		status ArchiveStatus
		want   string
	}{
		{
			name:   "Test STATUS_IN_QUEUE",
			status: STATUS_IN_QUEUE,
			want:   "IN_QUEUE",
		},
		{
			name:   "Test STATUS_PLANNING",
			status: STATUS_PLANNING,
			want:   "PLANNING",
		},
		{
			name:   "Test STATUS_PLANNED",
			status: STATUS_PLANNED,
			want:   "PLANNED",
		},
		{
			name:   "Test STATUS_RUNNING",
			status: STATUS_RUNNING,
			want:   "RUNNING",
		},
		{
			name:   "Test STATUS_PAUSED",
			status: STATUS_PAUSED,
			want:   "PAUSED",
		},
		{
			name:   "Test STATUS_ABORTED",
			status: STATUS_ABORTED,
			want:   "ABORTED",
		},
		{
			name:   "Test STATUS_COMPLETED",
			status: STATUS_COMPLETED,
			want:   "COMPLETE",
		},
		{
			name:   "Test Unknown Status",
			status: ArchiveStatus(99),
			want:   "UNKNOWN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.status.String())
		})
	}
}
