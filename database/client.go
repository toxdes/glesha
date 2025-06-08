package database

import (
	"context"
	"database/sql"
	"fmt"
	L "glesha/logger"

	_ "modernc.org/sqlite"
)

type DB struct {
	db            *sql.DB
	connectionUri string
	ctx           context.Context
}

func NewDB(dbPath string, ctx context.Context) (*DB, error) {
	d, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	return &DB{
		db:            d,
		connectionUri: dbPath,
		ctx:           ctx,
	}, nil
}

func (d *DB) createTables() error {
	res, err := d.db.ExecContext(d.ctx, `
CREATE TABLE IF NOT EXISTS tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    archive_id STRING NOT NULL,
    archive_path STRING NOT NULL,
    input_directory_path STRING NOT_NULL,
		status TEXT NOT NULL
						CHECK(status in ('archive_pending','archive_running', 'archive_paused', 'archive_aborted',
										'archive_complete', 'upload_pending', 'upload_running', 'upload_paused', 'upload_aborted',
										 'upload_complete')),
   created_at TEXT NOT NULL,
   updated_at TEXT NOT NULL,
   content_hash TEXT NOT NULL
);
`)
	L.Debug(fmt.Sprintf("TABLE CREATED: %!v", res))
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
	return d.db.Close()
}
