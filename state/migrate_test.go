package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofrs/flock"

	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
)

func stateFixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	source, target := filepath.Join(base, "config", "state-v2"), filepath.Join(base, "data", "state")
	if err := os.MkdirAll(filepath.Join(source, "set"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r, err := repository.NewRegistryRepository(ctx, filepath.Join(source, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Create(ctx, model.Set{ID: "set", Name: "docs"}); err != nil {
		t.Fatal(err)
	}
	r.Close()
	for _, name := range []string{"catalog.db", "pending.db"} {
		c, err := repository.NewCatalogRepository(ctx, filepath.Join(source, "set", name))
		if err != nil {
			t.Fatal(err)
		}
		v := model.Snapshot{Manifest: model.Manifest{Version: 2, ID: "snapshot", Layout: "chunked", Roots: []model.Root{{Name: "docs", Path: filepath.Join(source, "source-must-not-change")}}}, File: filepath.Join(source, "set", "payload.gpg"), ReadyFile: filepath.Join(source, "set", "ready.gpg"), Chunks: []model.Chunk{{File: filepath.Join(source, "set", "chunk.gpg"), Locations: []model.Location{{Key: source + "/key-must-not-change", Version: "object-version"}}}}}
		if err = c.PutSnapshot(ctx, v); err != nil {
			t.Fatal(err)
		}
		if err = c.SetMeta(ctx, "operation", "run"); err != nil {
			t.Fatal(err)
		}
		if err = c.PutSnapshot(ctx, model.Snapshot{Manifest: model.Manifest{Version: 1, ID: "external"}, File: source + "-neighbor/backup.gpg"}); err != nil {
			t.Fatal(err)
		}
		c.Close()
		db, err := sql.Open("sqlite", filepath.Join(source, "set", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("UPDATE snapshots SET data=json_set(data,'$.future_field',?) WHERE id='snapshot'", "preserve me"); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
	for _, name := range []string{"payload.gpg", "chunk.gpg", "import.gpg.json", "writer.lock"} {
		if err := os.WriteFile(filepath.Join(source, "set", name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return source, target
}

func TestMigratePreservesPendingStateAndPayloads(t *testing.T) {
	source, target := stateFixture(t)
	ctx := context.Background()
	if err := CheckDefault(source, target); err == nil {
		t.Fatal("older state was hidden")
	}
	before, _ := os.Stat(filepath.Join(source, "set", "payload.gpg"))
	if err := Migrate(ctx, source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("old state still exists: %v", err)
	}
	after, _ := os.Stat(filepath.Join(target, "set", "payload.gpg"))
	if !os.SameFile(before, after) {
		t.Fatal("archive was copied or regenerated")
	}
	for _, name := range []string{"catalog.db", "pending.db"} {
		c, err := repository.NewCatalogRepository(ctx, filepath.Join(target, "set", name))
		if err != nil {
			t.Fatal(err)
		}
		v, err := c.Snapshot(ctx, "snapshot")
		if err != nil {
			t.Fatal(err)
		}
		if v.File != filepath.Join(target, "set", "payload.gpg") || v.ReadyFile != filepath.Join(target, "set", "ready.gpg") || v.Chunks[0].File != filepath.Join(target, "set", "chunk.gpg") {
			t.Fatal("local payload references were not relocated")
		}
		if v.Roots[0].Path != filepath.Join(source, "source-must-not-change") || v.Chunks[0].Locations[0].Key != source+"/key-must-not-change" || v.Chunks[0].Locations[0].Version != "object-version" {
			t.Fatal("source or cloud identity changed")
		}
		if op, _ := c.GetMeta(ctx, "operation"); op != "run" {
			t.Fatal("pending operation lost")
		}
		external, err := c.Snapshot(ctx, "external")
		if err != nil || external.File != source+"-neighbor/backup.gpg" {
			t.Fatal("external archive path changed:", err)
		}
		c.Close()
		db, _ := sql.Open("sqlite", filepath.Join(target, "set", name))
		var future string
		if err = db.QueryRow("SELECT json_extract(data,'$.future_field') FROM snapshots WHERE id='snapshot'").Scan(&future); err != nil || future != "preserve me" {
			t.Fatal("unknown snapshot field lost:", err)
		}
		db.Close()
		if err = repository.ValidateStateDatabase(ctx, filepath.Join(target, backupDirectory, "set", name), false); err != nil {
			t.Fatal("original database backup missing:", err)
		}
	}
	if err := CheckDefault(source, target); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationRefusesUnsafeState(t *testing.T) {
	for _, scenario := range []string{"existing destination", "busy set", "busy state", "unknown schema", "symlink", "cancelled", "overlap"} {
		t.Run(scenario, func(t *testing.T) {
			source, target := stateFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "existing destination":
				os.MkdirAll(target, 0700)
			case "busy set":
				lock := flock.New(filepath.Join(source, "set", "writer.lock"))
				if err := lock.Lock(); err != nil {
					t.Fatal(err)
				}
				defer lock.Unlock()
			case "busy state":
				lock, err := Lock(target, false)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Unlock()
			case "unknown schema":
				db, _ := sql.Open("sqlite", filepath.Join(source, "set", "pending.db"))
				if _, err := db.Exec("UPDATE meta SET value='99' WHERE key='version'"); err != nil {
					t.Fatal(err)
				}
				db.Close()
			case "symlink":
				if err := os.Symlink(t.TempDir(), filepath.Join(source, "unsafe")); err != nil {
					t.Skip(err)
				}
			case "cancelled":
				cancel()
			case "overlap":
				target = filepath.Join(source, "nested")
			}
			original, _ := os.ReadFile(filepath.Join(source, "set", "pending.db"))
			if err := Migrate(ctx, source, target); err == nil {
				t.Fatal("unsafe migration accepted")
			}
			after, err := os.ReadFile(filepath.Join(source, "set", "pending.db"))
			if err != nil || string(original) != string(after) {
				t.Fatal("original state changed", err)
			}
		})
	}
}

func TestInterruptedMigrationRecovery(t *testing.T) {
	for _, phase := range []string{"preparing", "empty preparation", "partial marker", "relocating", "unknown version"} {
		t.Run(phase, func(t *testing.T) {
			source, target := stateFixture(t)
			ctx := context.Background()
			if phase == "empty preparation" || phase == "partial marker" {
				backup := filepath.Join(source, backupDirectory)
				os.Mkdir(backup, 0700)
				if phase == "partial marker" {
					os.WriteFile(filepath.Join(backup, ".glesha-partial"), []byte("{"), 0600)
				}
				if err := Migrate(ctx, source, target); err != nil {
					t.Fatal(err)
				}
				return
			}
			m := migration{Version: 1, Source: source, Target: target, Files: []string{"registry.db", filepath.Join("set", "catalog.db"), filepath.Join("set", "pending.db")}}
			for _, rel := range m.Files {
				copy := filepath.Join(source, backupDirectory, rel)
				os.MkdirAll(filepath.Dir(copy), 0700)
				if err := file_io.CopyNew(ctx, filepath.Join(source, rel), copy); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "unknown version" {
				m.Version = 99
			}
			data, _ := json.Marshal(m)
			os.WriteFile(filepath.Join(source, backupDirectory, "migration.json"), data, 0600)
			if phase != "preparing" {
				os.MkdirAll(filepath.Dir(target), 0700)
				if err := os.Rename(source, target+".migrating"); err != nil {
					t.Fatal(err)
				}
				if err := repository.RelocateStateDatabase(ctx, filepath.Join(target+".migrating", "set", "pending.db"), source, target); err != nil {
					t.Fatal(err)
				}
			}
			err := Migrate(ctx, source, target)
			if phase == "unknown version" {
				if err == nil || !strings.Contains(err.Error(), "unsupported") {
					t.Fatal("unknown migration accepted:", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(target, "set", "pending.db")); err != nil {
				t.Fatal(err)
			}
		})
	}
}
