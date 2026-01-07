package model

import "time"

const CREATE_FILE_CATALOG_TABLE = `
CREATE TABLE IF NOT EXISTS file_catalog (
id INTEGER PRIMARY KEY AUTOINCREMENT,

task_id INTEGER NOT NULL,

full_path TEXT NOT NULL,
name TEXT NOT NULL,
parent_path TEXT,
file_type TEXT NOT NULL,
size_bytes INTEGER,
modified_at TEXT,

FOREIGN KEY(task_id) REFERENCES tasks(id) ON DELETE CASCADE
);`

type FileCatalogRow struct {
	Id         int64     `json:"id"`
	TaskId     int64     `json:"task_id"`
	FullPath   string    `json:"full_path"`
	Name       string    `json:"name"`
	ParentPath string    `json:"parent_path"`
	FileType   string    `json:"file_type"` // 'file' | 'dir'
	SizeBytes  int64     `json:"size_bytes"`
	ModifiedAt time.Time `json:"modified_at"`
}
