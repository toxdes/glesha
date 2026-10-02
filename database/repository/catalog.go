package repository

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"

	"glesha/database/model"
)

type Cataloger interface {
	Close() error
	GetMeta(context.Context, string) (string, error)
	SetMeta(context.Context, string, string) error
	PutSnapshot(context.Context, model.Snapshot) error
	Snapshot(context.Context, string) (model.Snapshot, error)
	Latest(context.Context) (model.Snapshot, error)
	EachSnapshot(context.Context, func(model.Snapshot) error) error
	EachSnapshotReverse(context.Context, func(model.Snapshot) error) error
	PutEntry(context.Context, string, model.Entry) error
	PutEntries(context.Context, string, []model.Entry) error
	Entry(context.Context, string, string) (model.Entry, error)
	EachEntry(context.Context, string, func(model.Entry) error) error
	EachDirectory(context.Context, string, func(model.Entry) error) error
	FirstIdentity(context.Context, string, string) (model.Entry, error)
	DeleteEntries(context.Context, string) error
	CopyEntries(context.Context, string, string) error
	PutLocation(context.Context, model.Location) error
	Locations(context.Context, string) ([]model.Location, error)
	Vacuum(context.Context, string) error
	Commit(context.Context, model.Snapshot, string, bool) error
	Export(context.Context, string, bool) error
	Apply(context.Context, string) error
	Stats(context.Context) (map[string]int64, error)
}
type catalog struct {
	db       *sql.DB
	view     string
	views    map[string]string
	nextView int64
}

func NewCatalogRepository(ctx context.Context, p string) (Cataloger, error) {
	db, err := sql.Open("sqlite", p)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if p != ":memory:" {
		if info, statErr := os.Stat(p); statErr == nil && info.Size() > 0 {
			var version string
			if err = db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='version'").Scan(&version); err != nil || version != "2" {
				db.Close()
				return nil, fmt.Errorf("catalog: existing database is not schema version 2; it was left unchanged")
			}
		} else if statErr != nil && !os.IsNotExist(statErr) {
			db.Close()
			return nil, statErr
		}
	}
	if _, err = db.ExecContext(ctx, model.CatalogSchema); err != nil {
		db.Close()
		return nil, err
	}
	c := &catalog{db: db}
	v, err := c.GetMeta(ctx, "version")
	if err != nil || v != "2" {
		db.Close()
		return nil, fmt.Errorf("catalog: unsupported schema %q", v)
	}
	return c, nil
}
func (c *catalog) Close() error { return c.db.Close() }
func (c *catalog) GetMeta(ctx context.Context, k string) (string, error) {
	var v string
	err := c.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key=?", k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return v, err
}
func (c *catalog) SetMeta(ctx context.Context, k, v string) error {
	_, err := c.db.ExecContext(ctx, "INSERT OR REPLACE INTO meta VALUES(?,?)", k, v)
	return err
}
func (c *catalog) PutSnapshot(ctx context.Context, v model.Snapshot) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeChanged(ctx, "snapshot", v.ID, "", "INSERT INTO snapshots(id,parent,status,data) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET parent=excluded.parent,status=excluded.status,data=excluded.data", v.ID, v.Parent, v.Status, string(b))
}
func (c *catalog) Snapshot(ctx context.Context, id string) (model.Snapshot, error) {
	var v model.Snapshot
	var b string
	err := c.db.QueryRowContext(ctx, "SELECT data FROM snapshots WHERE id=?", id).Scan(&b)
	if err == nil {
		err = json.Unmarshal([]byte(b), &v)
	}
	return v, err
}
func (c *catalog) Latest(ctx context.Context) (model.Snapshot, error) {
	head, err := c.GetMeta(ctx, "head")
	if err != nil {
		return model.Snapshot{}, err
	}
	if head == "" {
		return model.Snapshot{}, sql.ErrNoRows
	}
	return c.Snapshot(ctx, head)
}
func (c *catalog) EachSnapshot(ctx context.Context, fn func(model.Snapshot) error) error {
	last := int64(0)
	for {
		var n int64
		var b string
		err := c.db.QueryRowContext(ctx, "SELECT seq,data FROM snapshots WHERE seq>? ORDER BY seq LIMIT 1", last).Scan(&n, &b)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var v model.Snapshot
		if err = json.Unmarshal([]byte(b), &v); err != nil {
			return err
		}
		if err = fn(v); err != nil {
			return err
		}
		last = n
	}
}

func (c *catalog) EachSnapshotReverse(ctx context.Context, fn func(model.Snapshot) error) error {
	last := int64(1<<63 - 1)
	for {
		var seq int64
		var data string
		err := c.db.QueryRowContext(ctx, "SELECT seq,data FROM snapshots WHERE seq<? ORDER BY seq DESC LIMIT 1", last).Scan(&seq, &data)
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
		if err = fn(v); err != nil {
			return err
		}
		last = seq
	}
}

const inventoryColumns = "path,parent,name,kind,size,mode,mtime,uid,gid,link,hash,archive,member,source,identity,source_identity,pax"

func entryValues(v model.Entry) []any {
	h, _ := hex.DecodeString(v.Hash)
	p, _ := json.Marshal(v.PAX)
	return []any{v.Path, v.Parent, v.Name, v.Type, v.Size, v.Mode, v.ModTime, v.UID, v.GID, v.Link, h, v.Archive, v.Member, v.Source, v.Identity, v.SourceIdentity, string(p)}
}
func scanEntry(row interface{ Scan(...any) error }) (model.Entry, error) {
	var v model.Entry
	var h []byte
	var p string
	err := row.Scan(&v.Path, &v.Parent, &v.Name, &v.Type, &v.Size, &v.Mode, &v.ModTime, &v.UID, &v.GID, &v.Link, &h, &v.Archive, &v.Member, &v.Source, &v.Identity, &v.SourceIdentity, &p)
	v.Hash = hex.EncodeToString(h)
	if err == nil {
		err = json.Unmarshal([]byte(p), &v.PAX)
	}
	return v, err
}
func (c *catalog) PutEntry(ctx context.Context, id string, v model.Entry) error {
	args := append([]any{id}, entryValues(v)...)
	_, err := c.db.ExecContext(ctx, "INSERT OR REPLACE INTO inventory VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", args...)
	if err == nil {
		err = c.SetMeta(ctx, "inventory:"+id, "true")
	}
	return err
}
func (c *catalog) PutEntries(ctx context.Context, id string, entries []model.Entry) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "INSERT OR REPLACE INTO inventory VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, entry := range entries {
		args := append([]any{id}, entryValues(entry)...)
		if _, err = stmt.ExecContext(ctx, args...); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO meta VALUES(?,?)", "inventory:"+id, "true"); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *catalog) isInventory(ctx context.Context, id string) bool {
	v, _ := c.GetMeta(ctx, "inventory:"+id)
	return v == "true"
}
func (c *catalog) Entry(ctx context.Context, id, p string) (model.Entry, error) {
	if !c.isInventory(ctx, id) {
		if err := c.materialize(ctx, id); err != nil {
			return model.Entry{}, err
		}
		return scanEntry(c.db.QueryRowContext(ctx, "SELECT "+inventoryColumns+" FROM "+c.view+" WHERE path=?", p))
	}
	return scanEntry(c.db.QueryRowContext(ctx, "SELECT "+inventoryColumns+" FROM inventory WHERE snapshot=? AND path=?", id, p))
}
func (c *catalog) EachEntry(ctx context.Context, id string, fn func(model.Entry) error) error {
	return c.each(ctx, id, false, fn)
}
func (c *catalog) EachDirectory(ctx context.Context, id string, fn func(model.Entry) error) error {
	return c.each(ctx, id, true, fn)
}
func (c *catalog) each(ctx context.Context, id string, dirs bool, fn func(model.Entry) error) error {
	table := "inventory"
	filter := "snapshot=? AND path>?"
	last := ""
	first := true
	if !c.isInventory(ctx, id) {
		if err := c.materialize(ctx, id); err != nil {
			return err
		}
		table = c.view
		filter = "path>?"
	}
	for {
		if table != "inventory" {
			if err := c.materialize(ctx, id); err != nil {
				return err
			}
			table = c.view
		}
		args := []any{id, last}
		where := filter
		if table != "inventory" {
			args = []any{last}
		}
		order := "ASC"
		if dirs {
			where = strings.Replace(where, "path>?", "path<?", 1) + " AND kind='directory'"
			order = "DESC"
			if first {
				where = strings.Replace(where, " AND path<?", "", 1)
				if table != "inventory" {
					where = "kind='directory'"
					args = nil
				} else {
					args = []any{id}
				}
			}
		}
		rows, err := c.db.QueryContext(ctx, "SELECT "+inventoryColumns+" FROM "+table+" WHERE "+where+" ORDER BY path "+order+" LIMIT 128", args...)
		if err != nil {
			return err
		}
		batch := make([]model.Entry, 0, 128)
		for rows.Next() {
			v, err := scanEntry(rows)
			if err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, v := range batch {
			if err = fn(v); err != nil {
				return err
			}
			last = v.Path
		}
		first = false
	}
}
func (c *catalog) materialize(ctx context.Context, id string) error {
	if table, ok := c.views[id]; ok {
		c.view = table
		return nil
	}
	var seq, base int64
	if err := c.db.QueryRowContext(ctx, "SELECT seq,baseline FROM snapshots WHERE id=? AND status='COMPLETED'", id).Scan(&seq, &base); err != nil {
		return err
	}
	if c.views == nil {
		c.views = map[string]string{}
	}
	if len(c.views) >= 2 {
		for old, table := range c.views {
			if table == c.view {
				continue
			}
			if _, err := c.db.ExecContext(ctx, "DROP TABLE temp."+table); err != nil {
				return err
			}
			delete(c.views, old)
			break
		}
	}
	c.nextView++
	table := fmt.Sprintf("tree_%d", c.nextView)
	_, err := c.db.ExecContext(ctx, "CREATE TEMP TABLE "+table+` AS WITH RECURSIVE paths(node,path,parent,name) AS (
 SELECT n.id,n.name,'',n.name FROM nodes n JOIN membership m ON m.node=n.id WHERE n.parent=0 AND m.begin<=? AND (m.end IS NULL OR m.end>?)
 UNION ALL SELECT n.id,p.path||'/'||n.name,p.path,n.name FROM paths p JOIN nodes n ON n.parent=p.node JOIN membership m ON m.node=n.id WHERE m.begin<=? AND (m.end IS NULL OR m.end>?)
 ) SELECT p.path,p.parent,p.name,v.kind,v.size,v.mode,v.mtime,v.uid,v.gid,v.link,v.hash,s.id AS archive,
 CASE WHEN s.wrapper='' THEN p.path ELSE s.wrapper||'/'||p.path END AS member,'' AS source,'' AS identity,'' AS source_identity,v.pax,m.node,m.version
 FROM paths p JOIN membership m ON m.node=p.node JOIN versions v ON v.id=m.version JOIN snapshots s ON s.seq=CASE WHEN v.owner<? THEN ? ELSE v.owner END
 WHERE m.begin<=? AND (m.end IS NULL OR m.end>?)`, seq, seq, seq, seq, base, base, seq, seq)
	if err != nil {
		return err
	}
	if _, err = c.db.ExecContext(ctx, "CREATE UNIQUE INDEX temp."+table+"_path ON "+table+"(path)"); err != nil {
		return err
	}
	c.view = table
	c.views[id] = table
	return nil
}
func (c *catalog) FirstIdentity(ctx context.Context, id, identity string) (model.Entry, error) {
	return scanEntry(c.db.QueryRowContext(ctx, "SELECT "+inventoryColumns+" FROM inventory WHERE snapshot=? AND identity=? ORDER BY path LIMIT 1", id, identity))
}
func (c *catalog) DeleteEntries(ctx context.Context, id string) error {
	_, err := c.db.ExecContext(ctx, "DELETE FROM inventory WHERE snapshot=?", id)
	if err == nil {
		err = c.SetMeta(ctx, "inventory:"+id, "")
	}
	return err
}
func (c *catalog) CopyEntries(ctx context.Context, to, from string) error {
	return c.EachEntry(ctx, from, func(v model.Entry) error { return c.PutEntry(ctx, to, v) })
}
func (c *catalog) PutLocation(ctx context.Context, v model.Location) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeChanged(ctx, "location", v.Snapshot, v.Provider, "INSERT OR REPLACE INTO locations VALUES(?,?,?)", v.Snapshot, v.Provider, string(b))
}
func (c *catalog) Locations(ctx context.Context, id string) ([]model.Location, error) {
	rows, err := c.db.QueryContext(ctx, "SELECT data FROM locations WHERE snapshot=? ORDER BY provider", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Location
	for rows.Next() {
		var b string
		var v model.Location
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (c *catalog) Vacuum(ctx context.Context, p string) error {
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		return fmt.Errorf("catalog: output exists")
	}
	_, err := c.db.ExecContext(ctx, "VACUUM INTO ?", p)
	return err
}
func (c *catalog) Stats(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	for _, t := range []string{"nodes", "versions", "membership", "snapshots"} {
		var n int64
		if err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+t).Scan(&n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, nil
}

func (c *catalog) clearViews(ctx context.Context) error {
	for _, table := range c.views {
		if _, err := c.db.ExecContext(ctx, "DROP TABLE IF EXISTS temp."+table); err != nil {
			return err
		}
	}
	c.views = nil
	c.view = ""
	return nil
}

func (c *catalog) writeChanged(ctx context.Context, kind, snapshot, provider, query string, args ...any) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO changes VALUES(?,?,?)", kind, snapshot, provider); err != nil {
		return err
	}
	return tx.Commit()
}
