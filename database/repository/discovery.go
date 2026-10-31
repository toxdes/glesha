package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"glesha/database/model"
)

func (r *registry) LocatorReplicated(ctx context.Context, locator, remote string) (bool, error) {
	var found int
	err := r.db.QueryRowContext(ctx, "SELECT 1 FROM discovery_replicas WHERE locator=? AND remote=?", locator, remote).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *registry) MarkLocatorReplicated(ctx context.Context, locator, remote string) error {
	_, err := r.db.ExecContext(ctx, "INSERT INTO discovery_replicas(locator,remote) VALUES(?,?) ON CONFLICT DO NOTHING", locator, remote)
	return err
}

func (r *registry) ForgetLocatorReplicas(ctx context.Context, remote string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM discovery_replicas WHERE remote=?", remote)
	return err
}

func (r *registry) PutLocator(ctx context.Context, id string, v model.Locator) error {
	if err := v.Version.Validate(); err != nil {
		return fmt.Errorf("registry: store discovery record: %w", err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, "INSERT INTO discovery(id,set_id,data) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING", id, v.Set, string(b))
	return err
}

func (r *registry) Locators(ctx context.Context, set string) (map[string]model.Locator, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT id,data FROM discovery WHERE set_id=? ORDER BY id", set)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]model.Locator{}
	for rows.Next() {
		var id, data string
		var value model.Locator
		if err = rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(data), &value); err != nil {
			return nil, err
		}
		if err = value.Version.Validate(); err != nil {
			return nil, fmt.Errorf("registry: read discovery record: %w", err)
		}
		values[id] = value
	}
	return values, rows.Err()
}
