package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"glesha/database/model"
)

type SnapshotSequencer interface {
	Close() error
	Append(context.Context, model.Snapshot) error
	EachReverse(context.Context, func(model.Snapshot) error) error
}

type snapshotSequence struct{ db *sql.DB }

func NewSnapshotSequence(ctx context.Context, path string) (SnapshotSequencer, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, `PRAGMA cache_size=-2048;
CREATE TABLE sequence(n INTEGER PRIMARY KEY,id TEXT UNIQUE NOT NULL,data TEXT NOT NULL);`); err != nil {
		db.Close()
		return nil, err
	}
	return &snapshotSequence{db: db}, nil
}

func (s *snapshotSequence) Close() error { return s.db.Close() }

func (s *snapshotSequence) Append(ctx context.Context, v model.Snapshot) error {
	var existing int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM sequence WHERE id=?", v.ID).Scan(&existing)
	if err == nil {
		return fmt.Errorf("catalog: cyclic snapshot ancestry at %s", v.ID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO sequence(id,data) VALUES(?,?)", v.ID, string(b))
	return err
}

func (s *snapshotSequence) EachReverse(ctx context.Context, visit func(model.Snapshot) error) error {
	last := int64(math.MaxInt64)
	for {
		var data string
		err := s.db.QueryRowContext(ctx, "SELECT n,data FROM sequence WHERE n<? ORDER BY n DESC LIMIT 1", last).Scan(&last, &data)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var v model.Snapshot
		if err = json.Unmarshal([]byte(data), &v); err != nil {
			return err
		}
		if err = visit(v); err != nil {
			return err
		}
	}
}
