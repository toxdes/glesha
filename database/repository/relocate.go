package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"glesha/database/model"
)

// ValidateStateDatabase opens existing state without creating or upgrading it.
func ValidateStateDatabase(ctx context.Context, file string, registry bool) error {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(file), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	var check string
	if err = db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		return fmt.Errorf("repository: invalid state database %s: %v", file, err)
	}
	if registry {
		for _, table := range []string{"sets", "remotes"} {
			rows, err := db.QueryContext(ctx, "SELECT id,name,data FROM "+table+" LIMIT 0")
			if err != nil {
				return fmt.Errorf("repository: unsupported registry: %w", err)
			}
			rows.Close()
		}
		return nil
	}
	var version string
	if err = db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='version'").Scan(&version); err != nil || version != "2" {
		return fmt.Errorf("repository: unsupported catalog version %q; state left unchanged", version)
	}
	rows, err := db.QueryContext(ctx, "SELECT data FROM snapshots ORDER BY seq")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		var snapshot model.Snapshot
		if err = rows.Scan(&data); err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(data), &snapshot); err != nil {
			return err
		}
		if snapshot.Version > 2 || snapshot.Layout != "" && snapshot.Layout != "single" && snapshot.Layout != "chunked" {
			return fmt.Errorf("repository: unsupported snapshot format; state left unchanged")
		}
	}
	return rows.Err()
}

// RelocateStateDatabase changes only local payload references, never source or object paths.
func RelocateStateDatabase(ctx context.Context, file, oldRoot, newRoot string) error {
	db, err := sql.Open("sqlite", file)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var seq int64
	for {
		var data string
		err = tx.QueryRowContext(ctx, "SELECT seq,data FROM snapshots WHERE seq>? ORDER BY seq LIMIT 1", seq).Scan(&seq, &data)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return err
		}
		// keep unknown serialized fields intact
		var fields map[string]json.RawMessage
		var snapshot model.Snapshot
		if err = json.Unmarshal([]byte(data), &fields); err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(data), &snapshot); err != nil {
			return err
		}
		if snapshot.Version > 2 || snapshot.Layout != "" && snapshot.Layout != "single" && snapshot.Layout != "chunked" {
			return fmt.Errorf("repository: unsupported snapshot format; state left unchanged")
		}
		changed := false
		for _, key := range []string{"file", "ready_file"} {
			var path string
			if raw, ok := fields[key]; ok {
				if err = json.Unmarshal(raw, &path); err != nil {
					return err
				}
				if moved := relocatedPath(path, oldRoot, newRoot); moved != path {
					fields[key], _ = json.Marshal(moved)
					changed = true
				}
			}
		}
		var chunks []map[string]json.RawMessage
		if raw, ok := fields["chunks"]; ok {
			if err = json.Unmarshal(raw, &chunks); err != nil {
				return err
			}
			for _, chunk := range chunks {
				var path string
				if raw, ok := chunk["file"]; ok {
					if err = json.Unmarshal(raw, &path); err != nil {
						return err
					}
					if moved := relocatedPath(path, oldRoot, newRoot); moved != path {
						chunk["file"], _ = json.Marshal(moved)
						changed = true
					}
				}
			}
			fields["chunks"], _ = json.Marshal(chunks)
		}
		if changed {
			encoded, err := json.Marshal(fields)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE snapshots SET data=? WHERE seq=?", string(encoded), seq); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func relocatedPath(path, oldRoot, newRoot string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(oldRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.Join(newRoot, rel)
}
