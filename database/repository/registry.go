package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"glesha/database/model"
)

var ErrRemoteLocationChanged = errors.New("registry: remote location changed")
var ErrAmbiguousSetName = errors.New("registry: ambiguous set name; select a set UUID")

type Registryor interface {
	Close() error
	Create(context.Context, model.Set) error
	Save(context.Context, model.Set) error
	Set(context.Context, string) (model.Set, error)
	Each(context.Context, func(model.Set) error) error
	BindRemote(context.Context, model.Remote) (model.Remote, error)
	Remote(context.Context, string) (model.Remote, error)
	PutLocator(context.Context, string, model.Locator) error
	Locators(context.Context, string) (map[string]model.Locator, error)
	LocatorReplicated(context.Context, string, string) (bool, error)
	MarkLocatorReplicated(context.Context, string, string) error
	ForgetLocatorReplicas(context.Context, string) error
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
 CREATE TABLE IF NOT EXISTS remotes(id TEXT PRIMARY KEY,name TEXT UNIQUE NOT NULL,data TEXT NOT NULL);`+model.DiscoverySchema); err != nil {
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
	name := v.Name
	var conflictingID string
	lookupErr := r.db.QueryRowContext(ctx, "SELECT id FROM sets WHERE name=? AND id<>?", v.Name, v.ID).Scan(&conflictingID)
	if lookupErr == nil {
		name = v.ID
	} else if !errors.Is(lookupErr, sql.ErrNoRows) {
		return lookupErr
	}
	_, err = r.db.ExecContext(ctx, "UPDATE sets SET name=?,data=? WHERE id=?", name, string(b), v.ID)
	return err
}
func (r *registry) Set(ctx context.Context, name string) (model.Set, error) {
	var v model.Set
	if _, err := uuid.Parse(name); err != nil {
		var count int
		if err = r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sets WHERE json_extract(data,'$.name')=?", name).Scan(&count); err != nil {
			return v, err
		}
		if count > 1 {
			return v, fmt.Errorf("%w: %q", ErrAmbiguousSetName, name)
		}
	}
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
	identity := v.Identity()
	if existing, err := r.Remote(ctx, identity); err == nil {
		return existing, nil
	} else if err != sql.ErrNoRows {
		return model.Remote{}, err
	}
	old, err := r.Remote(ctx, v.Name)
	if err == nil {
		if old.Kind != v.Kind || old.Bucket != v.Bucket || old.Endpoint != v.Endpoint || old.Region != v.Region {
			return v, fmt.Errorf("%w: remote %q; use a new remote name and explicit move", ErrRemoteLocationChanged, v.Name)
		}
		return old, nil
	}
	if err != sql.ErrNoRows {
		return v, err
	}
	v.ID = identity
	b, err := json.Marshal(v)
	if err != nil {
		return v, err
	}
	_, err = r.db.ExecContext(ctx, "INSERT INTO remotes VALUES(?,?,?)", v.ID, v.Name, string(b))
	return v, err
}
