package model

import "time"

type Entry struct {
	SourceIdentity string            `json:"-"`
	Path           string            `json:"path"`
	Parent         string            `json:"parent"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Size           int64             `json:"size"`
	Mode           int64             `json:"mode"`
	ModTime        int64             `json:"mtime_ns"`
	UID            int               `json:"uid"`
	GID            int               `json:"gid"`
	Link           string            `json:"link,omitempty"`
	Hash           string            `json:"sha256,omitempty"`
	Archive        string            `json:"archive,omitempty"`
	Member         string            `json:"member,omitempty"`
	Source         string            `json:"-"`
	Identity       string            `json:"-"`
	PAX            map[string]string `json:"pax,omitempty"`
}
type Root struct {
	Path string `json:"path"`
	Name string `json:"name"`
}
type Manifest struct {
	Layout          string   `json:"layout,omitempty"`
	DeletionsMember string   `json:"deletions_member,omitempty"`
	Version         int      `json:"version"`
	Set             string   `json:"set,omitempty"`
	ID              string   `json:"id"`
	Parent          string   `json:"parent,omitempty"`
	Full            bool     `json:"full"`
	Roots           []Root   `json:"roots"`
	Compression     string   `json:"compression"`
	InitialClass    string   `json:"initial_storage_class"`
	Deletions       []string `json:"deletions,omitempty"`
}
type Snapshot struct {
	Chunks     []Chunk    `json:"chunks,omitempty"`
	Imported   bool       `json:"imported,omitempty"`
	BackupDate *time.Time `json:"backup_date,omitempty"`
	ReadyFile  string     `json:"ready_file,omitempty"`
	PayloadID  string     `json:"payload_snapshot_id,omitempty"`
	Manifest
	Created      time.Time `json:"created"`
	File         string    `json:"file"`
	Hash         string    `json:"sha256"`
	Size         int64     `json:"size"`
	Status       Status    `json:"status"`
	Destinations string    `json:"destinations"`
}
type Location struct {
	UploadedAt    *time.Time `json:"uploaded_at,omitempty"`
	ObservedAt    *time.Time `json:"observed_at,omitempty"`
	Cold          bool       `json:"requires_cold_restore"`
	Restore       string     `json:"restore,omitempty"`
	ArchiveStatus string     `json:"archive_status,omitempty"`
	Snapshot      string     `json:"snapshot"`
	Provider      string     `json:"provider"`
	Key           string     `json:"key"`
	Version       string     `json:"version_id"`
	ETag          string     `json:"etag"`
	InitialClass  string     `json:"initial_class"`
	CurrentClass  string     `json:"current_class"`
	ObservedClass string     `json:"observed_class"`
	Verification  string     `json:"verification"`
	Status        Status     `json:"status"`
}

type Chunk struct {
	Index     int        `json:"index"`
	Offset    int64      `json:"offset"`
	PlainSize int64      `json:"plain_size"`
	Size      int64      `json:"size"`
	Hash      string     `json:"sha256"`
	PlainHash string     `json:"plain_sha256"`
	Key       string     `json:"key"`
	File      string     `json:"file,omitempty"`
	Locations []Location `json:"locations"`
}
