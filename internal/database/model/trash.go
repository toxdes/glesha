package model

import "time"

// TrashVersion is the format of the deletion markers published next to the
// discovery records.
const TrashVersion = 1

// TrashMarker puts a set in the trash on every remote, so any device can see,
// undo or finish the deletion. It holds no set names or source paths.
type TrashMarker struct {
	Version int    `json:"version"`
	Set     string `json:"set"`
	// Period in seconds; the deadline is the time the bucket stored the marker plus Period
	Period int64 `json:"period_seconds"`
	// Imported lists archives outside the set's own prefixes, so the deletion
	// can finish on a device that cannot read the catalog
	Imported []ObjectRef `json:"imported,omitempty"`
}

// DeletedMarker stays after a deletion finishes and stops devices from
// publishing the set again.
type DeletedMarker struct {
	Version int    `json:"version"`
	Set     string `json:"set"`
}

// ObjectRef is one stored version on a remote. ETag identifies the content of
// an object in a bucket without versions, where Version is empty.
type ObjectRef struct {
	Remote  string `json:"remote"`
	Key     string `json:"key"`
	Version string `json:"version_id,omitempty"`
	ETag    string `json:"etag,omitempty"`
}

// Trash is the local copy of a set's trash marker.
type Trash struct {
	Deadline time.Time   `json:"deadline"`
	Imported []ObjectRef `json:"imported,omitempty"`
	// Started is set once a remote holds the deleted marker; the deletion
	// cannot be undone from then on
	Started bool `json:"started,omitempty"`
}

// Due reports whether the set may be deleted: its trash period has ended on
// the local clock, or its deletion has started.
func (t Trash) Due(now time.Time) bool { return t.Started || !now.Before(t.Deadline) }
