package model

import (
	"time"

	"github.com/google/uuid"
)

type Set struct {
	Chunked          bool      `json:"chunked,omitempty"`
	SpoolMax         int64     `json:"spool_max,omitempty"`
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Roots            []Root    `json:"roots"`
	To               []string  `json:"destinations"`
	CatalogTo        string    `json:"catalog_remote"`
	Compression      string    `json:"compression"`
	Level            int       `json:"compression_level"`
	Prefix           string    `json:"prefix"`
	Class            string    `json:"storage_class"`
	IncrementalClass string    `json:"incremental_storage_class"`
	Authority        string    `json:"incremental_remote,omitempty"`
	Created          time.Time `json:"created"`
}
type Remote struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Bucket   string `json:"bucket"`
	Endpoint string `json:"endpoint"`
	Region   string `json:"region"`
}

func (r Remote) Identity() string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(r.Kind+"\x00"+r.Bucket+"\x00"+r.Endpoint+"\x00"+r.Region)).String()
}
