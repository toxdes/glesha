package state

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"glesha/database/model"
	"glesha/database/repository"
)

func TestDefaultStateWithEmptyLegacyLocation(t *testing.T) {
	for _, scenario := range []string{"absent", "directory", "registry", "historical registry"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			base := t.TempDir()
			source, target := filepath.Join(base, "legacy"), filepath.Join(base, "current")
			if scenario != "absent" {
				if err := os.Mkdir(source, 0700); err != nil {
					t.Fatal(err)
				}
			}
			var original []byte
			if strings.Contains(scenario, "registry") {
				r, err := repository.NewRegistryRepository(ctx, filepath.Join(source, "registry.db"))
				if err != nil {
					t.Fatal(err)
				}
				r.Close()
				if scenario == "historical registry" {
					db, err := sql.Open("sqlite", filepath.Join(source, "registry.db"))
					if err != nil {
						t.Fatal(err)
					}
					if _, err = db.Exec("DROP TABLE discovery; DROP TABLE discovery_replicas"); err != nil {
						t.Fatal(err)
					}
					db.Close()
				}
				original, _ = os.ReadFile(filepath.Join(source, "registry.db"))
			}
			if err := os.Mkdir(target, 0700); err != nil {
				t.Fatal(err)
			}
			r, err := repository.NewRegistryRepository(ctx, filepath.Join(target, "registry.db"))
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Create(ctx, model.Set{ID: "set", Name: "docs"}); err != nil {
				t.Fatal(err)
			}
			r.Close()
			current, _ := os.ReadFile(filepath.Join(target, "registry.db"))
			if err := CheckDefault(ctx, source, target); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(ctx, source, target); !errors.Is(err, ErrNoMigrationNeeded) {
				t.Fatalf("expected no migration: %v", err)
			}
			if after, _ := os.ReadFile(filepath.Join(target, "registry.db")); string(after) != string(current) {
				t.Fatal("current registry changed")
			}
			if original != nil {
				if after, _ := os.ReadFile(filepath.Join(source, "registry.db")); string(after) != string(original) {
					t.Fatal("legacy registry changed")
				}
			}
		})
	}
}

func TestDefaultStatePreservesNonemptyAndUnknownLegacyState(t *testing.T) {
	for _, scenario := range []string{"sets", "remotes", "unknown table", "extra file", "corrupt database"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			base := t.TempDir()
			source, target := filepath.Join(base, "legacy"), filepath.Join(base, "current")
			if err := os.Mkdir(source, 0700); err != nil {
				t.Fatal(err)
			}
			r, err := repository.NewRegistryRepository(ctx, filepath.Join(source, "registry.db"))
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "sets":
				err = r.Create(ctx, model.Set{ID: "old-set", Name: "docs"})
			case "remotes":
				_, err = r.BindRemote(ctx, model.Remote{Name: "aws", Kind: "aws", Bucket: "old-bucket"})
			}
			if err != nil {
				t.Fatal(err)
			}
			r.Close()
			switch scenario {
			case "unknown table":
				db, err := sql.Open("sqlite", filepath.Join(source, "registry.db"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec("CREATE TABLE future_state(version INTEGER)"); err != nil {
					t.Fatal(err)
				}
				db.Close()
			case "extra file":
				if err := os.WriteFile(filepath.Join(source, "pending.db"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "corrupt database":
				if err := os.WriteFile(filepath.Join(source, "registry.db"), []byte("invalid sqlite"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(target, 0700); err != nil {
				t.Fatal(err)
			}
			original, _ := os.ReadFile(filepath.Join(source, "registry.db"))
			for _, operation := range []func() error{func() error { return CheckDefault(ctx, source, target) }, func() error { return Migrate(ctx, source, target) }} {
				if err := operation(); err == nil || errors.Is(err, ErrNoMigrationNeeded) {
					t.Fatal("unrecognized or meaningful state was ignored")
				} else if scenario != "corrupt database" && (!strings.Contains(err.Error(), "--state-dir") || !strings.Contains(err.Error(), target)) {
					t.Fatalf("no actionable destination choice: %v", err)
				}
				if after, _ := os.ReadFile(filepath.Join(source, "registry.db")); string(after) != string(original) {
					t.Fatal("legacy registry changed")
				}
			}
		})
	}
}
