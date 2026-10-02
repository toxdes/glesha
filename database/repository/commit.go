package repository

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"glesha/database/model"
)

func (c *catalog) Commit(ctx context.Context, v model.Snapshot, wrapper string, advance bool) error {
	var already int64
	if err := c.db.QueryRowContext(ctx, "SELECT baseline FROM snapshots WHERE id=?", v.ID).Scan(&already); err == nil && already > 0 {
		return nil
	}
	if err := c.PutSnapshot(ctx, v); err != nil {
		return err
	}
	var seq, committed int64
	if err := c.db.QueryRowContext(ctx, "SELECT seq,baseline FROM snapshots WHERE id=?", v.ID).Scan(&seq, &committed); err != nil {
		return err
	}
	if committed > 0 {
		return nil
	}
	base := seq
	previous, err := c.Latest(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	compare := previous.ID
	if !v.Full {
		compare = v.Parent
		if err = c.db.QueryRowContext(ctx, "SELECT baseline FROM snapshots WHERE id=?", v.Parent).Scan(&base); err != nil {
			return err
		}
	}
	if compare != "" {
		if err = c.materialize(ctx, compare); err != nil {
			return err
		}
	}
	if _, err = c.db.ExecContext(ctx, "DROP TABLE IF EXISTS temp.desired; CREATE TEMP TABLE desired(path TEXT PRIMARY KEY,node INTEGER,version INTEGER)"); err != nil {
		return err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	last := ""
	for {
		rows, err := tx.QueryContext(ctx, "SELECT "+inventoryColumns+" FROM inventory WHERE snapshot=? AND path>? ORDER BY path LIMIT 128", v.ID, last)
		if err != nil {
			return err
		}
		batch := make([]model.Entry, 0, 128)
		for rows.Next() {
			e, err := scanEntry(rows)
			if err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, entry := range batch {
			var parent, node, version int64
			if entry.Parent != "" {
				if err = tx.QueryRowContext(ctx, "SELECT node FROM desired WHERE path=?", entry.Parent).Scan(&parent); err != nil {
					return fmt.Errorf("catalog: missing parent for %q: %w", entry.Path, err)
				}
			}
			var old model.Entry
			if compare != "" {
				old, err = scanEntry(tx.QueryRowContext(ctx, "SELECT "+inventoryColumns+" FROM "+c.view+" WHERE path=?", entry.Path))
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				if err == nil && old.Type == entry.Type {
					var oldParent int64
					if err = tx.QueryRowContext(ctx, "SELECT t.node,t.version,n.parent FROM "+c.view+" t JOIN nodes n ON n.id=t.node WHERE t.path=?", entry.Path).Scan(&node, &version, &oldParent); err != nil {
						return err
					}
					if oldParent != parent {
						node, version = 0, 0
					}
				}
			}
			if node == 0 {
				r, err := tx.ExecContext(ctx, "INSERT INTO nodes(parent,name) VALUES(?,?)", parent, entry.Name)
				if err != nil {
					return err
				}
				node, err = r.LastInsertId()
				if err != nil {
					return err
				}
			}
			if version == 0 || !sameMetadata(old, entry) || !v.Full && entry.Archive == v.ID {
				h, err := hex.DecodeString(entry.Hash)
				if err != nil {
					return err
				}
				p, err := json.Marshal(entry.PAX)
				if err != nil {
					return err
				}
				r, err := tx.ExecContext(ctx, "INSERT INTO versions(kind,size,mode,mtime,uid,gid,link,hash,pax,owner) VALUES(?,?,?,?,?,?,?,?,?,?)", entry.Type, entry.Size, entry.Mode, entry.ModTime, entry.UID, entry.GID, entry.Link, h, string(p), seq)
				if err != nil {
					return err
				}
				version, err = r.LastInsertId()
				if err != nil {
					return err
				}
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO desired VALUES(?,?,?)", entry.Path, node, version); err != nil {
				return err
			}
			last = entry.Path
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE membership SET end=? WHERE end IS NULL AND NOT EXISTS(SELECT 1 FROM desired d WHERE d.node=membership.node AND d.version=membership.version)", seq); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO membership(node,version,begin) SELECT d.node,d.version,? FROM desired d WHERE NOT EXISTS(SELECT 1 FROM membership m WHERE m.node=d.node AND m.version=d.version AND m.end IS NULL)", seq); err != nil {
		return err
	}
	v.Status = model.STATUS_COMPLETED
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE snapshots SET status=?,data=?,baseline=?,wrapper=? WHERE id=?", v.Status, string(b), base, wrapper, v.ID); err != nil {
		return err
	}
	if advance || previous.ID == "" {
		if _, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO meta VALUES('head',?)", v.ID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM inventory WHERE snapshot=?", v.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM meta WHERE key=?", "inventory:"+v.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return c.clearViews(ctx)
}
func sameMetadata(a, b model.Entry) bool {
	ap, _ := json.Marshal(a.PAX)
	bp, _ := json.Marshal(b.PAX)
	return a.Type == b.Type && a.Size == b.Size && a.Mode == b.Mode && a.ModTime == b.ModTime && a.UID == b.UID && a.GID == b.GID && a.Link == b.Link && a.Hash == b.Hash && string(ap) == string(bp)
}
