package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"

	"glesha/file_io"
)

// StateRegistryEmpty recognizes only known, empty registry tables.
func StateRegistryEmpty(ctx context.Context, file string) (bool, error) {
	if !file_io.IsReadable(file) {
		return false, fmt.Errorf("repository: registry is not readable: %s", file)
	}
	if err := ValidateStateDatabase(ctx, file, true); err != nil {
		return false, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(file), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return false, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT name,type FROM sqlite_master WHERE type IN ('table','view','trigger') AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return false, err
		}
		if kind != "table" {
			return false, nil
		}
		switch name {
		case "sets", "remotes", "discovery", "discovery_replicas":
			tables = append(tables, name)
		default:
			return false, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for _, table := range tables {
		var populated bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+table+" LIMIT 1)").Scan(&populated); err != nil {
			return false, err
		}
		if populated {
			return false, nil
		}
	}
	return true, nil
}
