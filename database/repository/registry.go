package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"glesha/database/model"
)

type Registryor interface {
	Close() error
	Create(context.Context, model.Set) error
	Save(context.Context, model.Set) error
	Set(context.Context, string) (model.Set, error)
	Each(context.Context, func(model.Set) error) error
	BindRemote(context.Context, model.Remote) (model.Remote, error)
	Remote(context.Context, string) (model.Remote, error)
}
type registry struct{ db *sql.DB }

func NewRegistryRepository(ctx context.Context, file string) (Registryor, error) {
	db, err := sql.Open("sqlite", file)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, `PRAGMA cache_size=-2048;
 CREATE TABLE IF NOT EXISTS sets(id TEXT PRIMARY KEY,name TEXT UNIQUE NOT NULL,data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS remotes(id TEXT PRIMARY KEY,name TEXT UNIQUE NOT NULL,data TEXT NOT NULL);`); err != nil {
		db.Close()
		return nil, err
	}
	return &registry{db}, nil
}
func (r *registry) Close() error { return r.db.Close() }
func (r *registry) Create(ctx context.Context, v model.Set) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, "INSERT INTO sets VALUES(?,?,?)", v.ID, v.Name, string(b))
	return err
}
func (r *registry) Save(ctx context.Context, v model.Set) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, "UPDATE sets SET data=? WHERE id=? AND name=?", string(b), v.ID, v.Name)
	return err
}
func (r *registry) Set(ctx context.Context, name string) (model.Set, error) {
	var v model.Set
	var b string
	err := r.db.QueryRowContext(ctx, "SELECT data FROM sets WHERE name=? OR id=?", name, name).Scan(&b)
	if err == nil {
		err = json.Unmarshal([]byte(b), &v)
	}
	return v, err
}
func (r *registry) Each(ctx context.Context, fn func(model.Set) error) error {
	last := ""
	for {
		var b, name string
		err := r.db.QueryRowContext(ctx, "SELECT name,data FROM sets WHERE name>? ORDER BY name LIMIT 1", last).Scan(&name, &b)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		var v model.Set
		if err = json.Unmarshal([]byte(b), &v); err != nil {
			return err
		}
		if err = fn(v); err != nil {
			return err
		}
		last = name
	}
}
func (r *registry) Remote(ctx context.Context, name string) (model.Remote, error) {
	var v model.Remote
	var b string
	err := r.db.QueryRowContext(ctx, "SELECT data FROM remotes WHERE name=? OR id=?", name, name).Scan(&b)
	if err == nil {
		err = json.Unmarshal([]byte(b), &v)
	}
	return v, err
}
func (r *registry) BindRemote(ctx context.Context, v model.Remote) (model.Remote, error) {
	old, err := r.Remote(ctx, v.Name)
	if err == nil {
		if old.Kind != v.Kind || old.Bucket != v.Bucket || old.Endpoint != v.Endpoint || old.Region != v.Region {
			return v, fmt.Errorf("registry: remote %q location changed; use a new remote name and explicit move", v.Name)
		}
		return old, nil
	}
	if err != sql.ErrNoRows {
		return v, err
	}
	v.ID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(v.Kind+"\x00"+v.Bucket+"\x00"+v.Endpoint+"\x00"+v.Region)).String()
	b, err := json.Marshal(v)
	if err != nil {
		return v, err
	}
	_, err = r.db.ExecContext(ctx, "INSERT INTO remotes VALUES(?,?,?)", v.ID, v.Name, string(b))
	return v, err
}
