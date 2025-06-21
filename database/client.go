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
	ctx           context.Context
}

func NewDB(dbPath string, ctx context.Context) (*DB, error) {
	d, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	return &DB{
		D:             d,
		connectionUri: dbPath,
		ctx:           ctx,
	}, nil
}

var DateTimeFormat string = "20060102T150405Z"

func (d *DB) createTables() error {
	_, err := d.D.ExecContext(d.ctx,
		`CREATE TABLE IF NOT EXISTS tasks (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        input_path STRING NOT_NULL,
        output_path STRING NOT NULL,
        config_path STRING NOT NULL,
        provider STRING NOT NULL,
        archive_format STRING NOT NULL,
        status TEXT NOT NULL,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        content_hash TEXT NOT NULL,
        size INTEGER NOT NULL,
        file_count INTEGER NOT NULL
);`)

	L.Debug(fmt.Sprintf("tasks table created"))
	if err != nil {
		return err
	}
	return nil
}

func (d *DB) Init() error {
	return d.createTables()
}

func (d *DB) Close() error {
	d.ctx.Done()
	return d.D.Close()
}

func GetDBFilePath() (string, error) {
	configDir, err := config.GetDefaultConfigDir()
	if err != nil {
		return "", err
	}
	dbPath := filepath.Join(configDir, "glesha-db.db")
	if !file_io.IsWritable(configDir) {
		return "", fmt.Errorf("No write permissions to %s", dbPath)
	}
	return dbPath, nil
}
