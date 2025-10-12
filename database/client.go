package database

import (
	"context"
	"database/sql"
	"fmt"
	"glesha/config"
	"glesha/file_io"
	L "glesha/logger"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	D             *sql.DB
	connectionUri string
}

func NewDB(dbPath string) (*DB, error) {
	d, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	return &DB{
		D:             d,
		connectionUri: dbPath,
	}, nil
}

var DateTimeFormat string = "20060102T150405Z"

const PRAGMAS = `
PRAGMA foreign_keys = ON;
`
const CREATE_TASKS_TABLE = `CREATE TABLE IF NOT EXISTS tasks (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        
				input_path TEXT NOT NULL,
        output_path TEXT NOT NULL,
        config_path TEXT NOT NULL,
        
				provider TEXT NOT NULL,
        archive_format TEXT NOT NULL,
        
				status TEXT NOT NULL,
        
				created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        
				content_hash TEXT NOT NULL,
        size INTEGER NOT NULL,
        file_count INTEGER NOT NULL
);`

const CREATE_UPLOADS_TABLE = `CREATE TABLE IF NOT EXISTS uploads(
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				
				task_id INTEGER NOT NULL UNIQUE,

				storage_backend_metadata_json TEXT, 
				storage_backend_metadata_schema_version INTEGER NOT NULL,
				file_path TEXT NOT NULL,
				file_size INTEGER NOT NULL,
				file_last_modified_at TEXT NOT NULL,

				uploaded_bytes INTEGER DEFAULT 0,
				uploaded_blocks INTEGER DEFAULT 0,
				total_blocks INTEGER NOT NULL,
				block_size_in_bytes INTEGER NOT NULL,

				status TEXT NOT NULL DEFAULT "QUEUED",
				created_at TEXT NOT NULL, 
				updated_at TEXT NOT NULL,
				completed_at TEXT,

				UNIQUE(task_id),
				FOREIGN KEY(task_id) REFERENCES tasks(id) ON DELETE CASCADE
);`

const CREATE_INDICES_ON_UPLOADS = `
CREATE INDEX IF NOT EXISTS idx_uploads_status ON uploads(status, task_id);
`

const CREATE_UPLOAD_BLOCKS_TABLE = `CREATE TABLE IF NOT EXISTS upload_blocks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			
			upload_id TEXT NOT NULL,

			block_number INTEGER NOT NULL,
			file_offset INTEGER NOT NULL,
			size INTEGER NOT NULL,

			status TEXT NOT NULL DEFAULT "QUEUED",
			etag TEXT,
			checksum TEXT,
			uploaded_at TEXT,
			error_message TEXT,

			UNIQUE(upload_id, block_number),
			FOREIGN KEY(upload_id) REFERENCES uploads(id) ON DELETE CASCADE
);`

const CREATE_INDICES_ON_UPLOAD_BLOCKS = `
CREATE INDEX IF NOT EXISTS idx_upload_block ON upload_blocks(upload_id, block_number);
CREATE INDEX IF NOT EXISTS idx_upload_status ON upload_blocks(upload_id, status);
`

const CREATE_UPDATE_UPLOAD_PROGRESS_TRIGGER = `CREATE TRIGGER IF NOT EXISTS 
update_upload_progress
AFTER UPDATE ON upload_blocks
FOR EACH ROW
WHEN NEW.status = 'COMPLETED'
BEGIN
	UPDATE uploads
	SET uploaded_blocks = uploaded_blocks + 1,
			uploaded_bytes = uploaded_bytes + NEW.size,
			updated_at = strftime('%Y%m%dT%H%M%S', 'now') || 'Z'
	WHERE upload_id = NEW.upload_id;
END;`

func (d *DB) createTables(ctx context.Context) error {
	txn, err := d.D.Begin()

	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			txn.Rollback()
		}
	}()

	stmts := []string{
		PRAGMAS,
		CREATE_TASKS_TABLE, CREATE_UPLOADS_TABLE, CREATE_UPLOAD_BLOCKS_TABLE,
		CREATE_INDICES_ON_UPLOADS, CREATE_INDICES_ON_UPLOAD_BLOCKS,
		CREATE_UPDATE_UPLOAD_PROGRESS_TRIGGER,
	}

	for _, stmt := range stmts {
		_, err = txn.ExecContext(ctx, stmt)
		if err != nil {
			return err
		}
	}

	err = txn.Commit()
	if err != nil {
		return err
	}

	L.Debug("tasks table created")
	return nil
}

func (d *DB) Init(ctx context.Context) error {
	return d.createTables(ctx)
}

func (d *DB) Close(ctx context.Context) error {
	ctx.Done()
	return d.D.Close()
}

func GetDBFilePath(ctx context.Context) (string, error) {
	configDir, err := config.GetDefaultConfigDir()
	if err != nil {
		return "", err
	}
	dbPath := filepath.Join(configDir, "glesha-db.db")
	if !file_io.IsWritable(configDir) {
		return "", fmt.Errorf("no write permissions to %s", dbPath)
	}
	return dbPath, nil
}
