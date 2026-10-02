package model

import "time"

type Set struct {
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
