package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

// checkpoints and change batches use the same schema; deltas contain changed rows only
func (c *catalog) Export(ctx context.Context, file string, checkpoint bool) error {
	if checkpoint {
		return c.Vacuum(ctx, file)
	}
	out, err := NewCatalogRepository(ctx, file)
	if err != nil {
		return err
	}
	out.Close()
	if _, err = c.db.ExecContext(ctx, "ATTACH DATABASE ? AS batch", file); err != nil {
		return err
	}
	defer c.db.ExecContext(context.WithoutCancel(ctx), "DETACH DATABASE batch")
	n, _ := c.GetMeta(ctx, "synced_nodes")
	v, _ := c.GetMeta(ctx, "synced_versions")
	s, _ := c.GetMeta(ctx, "synced_snapshots")
	for _, q := range []struct {
		sql string
		arg string
	}{
		{"INSERT INTO batch.nodes SELECT * FROM main.nodes WHERE id>?", n},
		{"INSERT INTO batch.versions SELECT * FROM main.versions WHERE id>?", v},
		{"INSERT INTO batch.membership SELECT * FROM main.membership WHERE begin>? OR end>?", s},
	} {
		args := []any{q.arg}
		if q.sql == "INSERT INTO batch.membership SELECT * FROM main.membership WHERE begin>? OR end>?" {
			args = append(args, q.arg)
		}
		if _, err = c.db.ExecContext(ctx, q.sql, args...); err != nil {
			return err
		}
	}
	for _, query := range []string{
		"INSERT INTO batch.snapshots SELECT * FROM main.snapshots WHERE id IN (SELECT snapshot FROM main.changes WHERE kind='snapshot')",
		"INSERT INTO batch.locations SELECT l.* FROM main.locations l JOIN main.changes c ON c.kind='location' AND c.snapshot=l.snapshot AND c.provider=l.provider",
		"INSERT OR REPLACE INTO batch.meta SELECT * FROM main.meta",
	} {
		if _, err = c.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}
func (c *catalog) Apply(ctx context.Context, file string) error {
	if err := ValidateCatalog(ctx, file); err != nil {
		return err
	}
	if _, err := c.db.ExecContext(ctx, "ATTACH DATABASE ? AS batch", file); err != nil {
		return err
	}
	defer c.db.ExecContext(context.WithoutCancel(ctx), "DETACH DATABASE batch")
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"nodes", "versions", "membership", "snapshots", "locations", "meta"} {
		if _, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO main."+table+" SELECT * FROM batch."+table); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return c.clearViews(ctx)
}
func MarkSynced(ctx context.Context, c Cataloger) error {
	concrete, ok := c.(*catalog)
	if !ok {
		return fmt.Errorf("catalog: unsupported repository")
	}
	for _, table := range []string{"nodes", "versions", "snapshots"} {
		column := "id"
		if table == "snapshots" {
			column = "seq"
		}
		var n int64
		if err := concrete.db.QueryRowContext(ctx, "SELECT COALESCE(MAX("+column+"),0) FROM "+table).Scan(&n); err != nil {
			return err
		}
		if err := c.SetMeta(ctx, "synced_"+table, strconv.FormatInt(n, 10)); err != nil {
			return err
		}
	}
	_, err := concrete.db.ExecContext(ctx, "DELETE FROM changes")
	return err
}
func ValidateCatalog(ctx context.Context, p string) error {
	db, err := openReadOnly(p)
	if err != nil {
		return err
	}
	defer db.Close()
	var unsafe int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_schema WHERE type IN ('view','trigger')").Scan(&unsafe); err != nil || unsafe > 0 {
		return fmt.Errorf("catalog: schema contains unsupported executable objects")
	}
	var v string
	if err = db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&v); err != nil || v != "ok" {
		return fmt.Errorf("catalog: invalid SQLite database: %v", err)
	}
	if err = db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='version'").Scan(&v); err != nil || v != "2" {
		return fmt.Errorf("catalog: unsupported schema %q", v)
	}
	for _, q := range []string{"SELECT id,parent,name FROM nodes LIMIT 0", "SELECT node,version,begin,end FROM membership LIMIT 0", "SELECT seq,id,baseline,wrapper FROM snapshots LIMIT 0"} {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			return err
		}
		rows.Close()
	}
	return nil
}
func Preserve(ctx context.Context, c Cataloger, p string) error {
	if err := c.Vacuum(ctx, p); err != nil {
		return err
	}
	return os.Chmod(p, 0600)
}

func openReadOnly(p string) (*sql.DB, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: "mode=ro"}
	return sql.Open("sqlite", u.String())
}
func ReadCatalogMeta(ctx context.Context, p, key string) (string, error) {
	db, err := openReadOnly(p)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var value string
	err = db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key=?", key).Scan(&value)
	return value, err
}
