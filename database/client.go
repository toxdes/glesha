package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"glesha/config"
	"glesha/database/model"
	"glesha/file_io"

	L "glesha/logger"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	D             *sql.DB
	connectionUri string
}

func NewDB(dbPath string) (*DB, error) {
	d, err := sql.Open("sqlite", fmt.Sprintf("%s?%s", dbPath, PRAGMAS_QUERY_STRING))
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	return &DB{
		D:             d,
		connectionUri: dbPath,
	}, nil
}

const DateTimeFormat string = "20060102T150405Z"

var ErrDoesNotExist error = errors.New("could not find in database")

const PRAGMAS_QUERY_STRING = `_foreign_keys=on&_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000`

const CREATE_INDICES_ON_UPLOADS = `
CREATE INDEX IF NOT EXISTS idx_uploads_status ON uploads(status, task_id);
`

const CREATE_INDICES_ON_UPLOAD_BLOCKS = `
CREATE INDEX IF NOT EXISTS idx_upload_status ON upload_blocks(upload_id, status);
`

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
		model.CREATE_TASKS_TABLE, model.CREATE_UPLOADS_TABLE, model.CREATE_UPLOAD_BLOCKS_TABLE,
		CREATE_INDICES_ON_UPLOADS, CREATE_INDICES_ON_UPLOAD_BLOCKS,
		model.CREATE_UPDATE_UPLOAD_PROGRESS_TRIGGER,
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
	writable, err := file_io.IsWritable(configDir)
	if err != nil {
		return "", err
	}
	if !writable {
		return "", fmt.Errorf("no write permissions to %s", dbPath)
	}
	return dbPath, nil
}

func ToTimeStr(t time.Time) string {
	return t.Local().Format(DateTimeFormat)
}

func FromTimeStr(ts string) time.Time {
	t, err := time.Parse(DateTimeFormat, ts)
	if err != nil {
		L.Error(fmt.Errorf("couldnt parse time for %s: %w", ts, err))
		return time.Now()
	}
	return t
}
