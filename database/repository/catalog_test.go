package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"glesha/database/model"
)

func testEntry(path, parent, name, value string) model.Entry {
	h := sha256.Sum256([]byte(value))
	return model.Entry{Path: path, Parent: parent, Name: name, Type: "file", Size: int64(len(value)), Hash: hex.EncodeToString(h[:]), Mode: 0600}
}
func TestHistoricalMembershipAndCompactVersions(t *testing.T) {
	ctx := context.Background()
	c, err := NewCatalogRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	root := model.Entry{Path: "docs", Name: "docs", Type: "directory", Mode: 0700}
	name := "O'Brien; DROP TABLE nodes;--"
	file := testEntry("docs/"+name, "docs", name, "first")
	commit := func(id, parent string, entries ...model.Entry) {
		t.Helper()
		for _, e := range entries {
			if err := c.PutEntry(ctx, id, e); err != nil {
				t.Fatal(err)
			}
		}
		v := model.Snapshot{Manifest: model.Manifest{ID: id, Parent: parent, Full: parent == ""}, Status: model.STATUS_RUNNING}
		if err := c.Commit(ctx, v, id, true); err != nil {
			t.Fatal(err)
		}
	}
	commit("one", "", root, file)
	commit("two", "one", root, file)
	stats, err := c.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats["nodes"] != 2 || stats["versions"] != 2 || stats["membership"] != 2 {
		t.Fatalf("unchanged inventory duplicated: %v", stats)
	}
	var oldID int64
	concrete := c.(*catalog)
	if err = concrete.db.QueryRow("SELECT id FROM nodes WHERE name=?", name).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	commit("three", "two", root)
	if _, err = c.Entry(ctx, "one", file.Path); err != nil {
		t.Fatal("historical file disappeared", err)
	}
	if _, err = c.Entry(ctx, "three", file.Path); err == nil {
		t.Fatal("deleted file present")
	}
	commit("four", "three", root, file)
	var newID int64
	if err = concrete.db.QueryRow("SELECT MAX(id) FROM nodes WHERE name=?", name).Scan(&newID); err != nil {
		t.Fatal(err)
	}
	if newID <= oldID {
		t.Fatal("node id reused")
	}
	out := filepath.Join(t.TempDir(), "a' ; DROP TABLE nodes; --.db")
	if err = c.Vacuum(ctx, out); err != nil {
		t.Fatal(err)
	}
	if err = ValidateCatalog(ctx, out); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointAndDelta(t *testing.T) {
	ctx := context.Background()
	c, _ := NewCatalogRepository(ctx, ":memory:")
	defer c.Close()
	other, _ := NewCatalogRepository(ctx, ":memory:")
	defer other.Close()
	dir := t.TempDir()
	root := model.Entry{Path: "docs", Name: "docs", Type: "directory", Mode: 0700}
	for i, id := range []string{"a", "b", "c"} {
		parent := ""
		if i > 0 {
			parent = []string{"a", "b"}[i-1]
		}
		for _, e := range []model.Entry{root, testEntry("docs/f", "docs", "f", id)} {
			if err := c.PutEntry(ctx, id, e); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.Commit(ctx, model.Snapshot{Manifest: model.Manifest{ID: id, Parent: parent, Full: i == 0}}, id, true); err != nil {
			t.Fatal(err)
		}
		batch := filepath.Join(dir, id+".db")
		if err := c.Export(ctx, batch, i == 0); err != nil {
			t.Fatal(err)
		}
		if err := other.Apply(ctx, batch); err != nil {
			t.Fatal(err)
		}
		if err := MarkSynced(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b", "c"} {
		got, err := other.Entry(ctx, id, "docs/f")
		if err != nil {
			t.Fatal(err)
		}
		if got.Hash != testEntry("", "", "", id).Hash {
			t.Fatalf("wrong historical hash at %s", id)
		}
	}
}

func TestNestedHistoricalLookupsAcrossBatches(t *testing.T) {
	ctx := context.Background()
	c, _ := NewCatalogRepository(ctx, ":memory:")
	defer c.Close()
	for _, id := range []string{"one", "two"} {
		for i := 0; i < 300; i++ {
			name := fmt.Sprintf("file%04d", i)
			e := testEntry(name, "", name, id)
			if err := c.PutEntry(ctx, id, e); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.Commit(ctx, model.Snapshot{Manifest: model.Manifest{ID: id, Full: true}}, id, true); err != nil {
			t.Fatal(err)
		}
	}
	seen := 0
	if err := c.EachEntry(ctx, "one", func(e model.Entry) error {
		other, err := c.Entry(ctx, "two", e.Path)
		if err != nil {
			return err
		}
		if other.Hash == e.Hash {
			return fmt.Errorf("wrong snapshot tree")
		}
		seen++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen != 300 {
		t.Fatalf("iteration changed snapshots: %d", seen)
	}
}

func TestLegacyDatabaseIsLeftUnchanged(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE old_tasks(id INTEGER PRIMARY KEY,name TEXT); INSERT INTO old_tasks VALUES(1,'old backup')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := NewCatalogRepository(ctx, file); err == nil {
		c.Close()
		t.Fatal("legacy catalog modified")
	}
	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("legacy bytes changed", err)
	}
}

func TestMetadataDeltaContainsOnlyChangedRecords(t *testing.T) {
	ctx := context.Background()
	c, err := NewCatalogRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, id := range []string{"one", "two"} {
		if err = c.PutSnapshot(ctx, model.Snapshot{Manifest: model.Manifest{ID: id}}); err != nil {
			t.Fatal(err)
		}
		if err = c.PutLocation(ctx, model.Location{Snapshot: id, Provider: "aws", CurrentClass: "STANDARD"}); err != nil {
			t.Fatal(err)
		}
	}
	if err = MarkSynced(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err = c.PutLocation(ctx, model.Location{Snapshot: "one", Provider: "aws", CurrentClass: "DEEP_ARCHIVE"}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "delta.db")
	if err = c.Export(ctx, file, false); err != nil {
		t.Fatal(err)
	}
	db, err := openReadOnly(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var snapshots, locations int
	if err = db.QueryRow("SELECT COUNT(*) FROM snapshots").Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT COUNT(*) FROM locations").Scan(&locations); err != nil {
		t.Fatal(err)
	}
	if snapshots != 0 || locations != 1 {
		t.Fatalf("unchanged metadata repeated: snapshots=%d locations=%d", snapshots, locations)
	}
}
