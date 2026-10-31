package app_cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"glesha/config"
	"glesha/database/model"
	"glesha/database/repository"
)

func stateRegistryFixture(t *testing.T, root, name string) {
	t.Helper()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	r, err := repository.NewRegistryRepository(context.Background(), filepath.Join(root, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if name != "" {
		if err := r.Create(context.Background(), model.Set{ID: name, Name: name, CatalogTo: "local"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEmptyLegacyRegistryDoesNotBlockDefaultCommands(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("Linux XDG integration")
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	legacy, _ := config.LegacyStatePath()
	current, _ := config.DefaultStatePath()
	stateRegistryFixture(t, legacy, "")
	stateRegistryFixture(t, current, "docs")
	original, _ := os.ReadFile(filepath.Join(legacy, "registry.db"))
	for _, args := range [][]string{{"list"}, {"status"}, {"state", "migrate"}, {"list"}} {
		if err := Execute(context.Background(), args, "test", "test"); err != nil {
			t.Fatalf("%v blocked by empty legacy state: %v", args, err)
		}
	}
	out, diagnostic, err := captureOutput(t, func() error {
		return Execute(context.Background(), []string{"state", "migrate", "--format=json", "--progress=off"}, "test", "test")
	})
	if err != nil || diagnostic != "" {
		t.Fatalf("no-op migration failed: %s: %v", diagnostic, err)
	}
	rows := decodeRecords(t, out)
	if len(rows) != 1 || rows[0]["kind"] != "result" || rows[0]["data"].(map[string]any)["path"] != current {
		t.Fatalf("no-op migration lost structured result: %s", out)
	}
	if after, _ := os.ReadFile(filepath.Join(legacy, "registry.db")); string(after) != string(original) {
		t.Fatal("legacy registry changed")
	}
	r, err := repository.NewRegistryRepository(context.Background(), filepath.Join(current, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Set(context.Background(), "docs"); err != nil {
		t.Fatal("current state lost", err)
	}
}

func TestStateDirectorySelectsExistingStateAndFreshMigrationTarget(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("Linux XDG integration")
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	legacy, _ := config.LegacyStatePath()
	current, _ := config.DefaultStatePath()
	stateRegistryFixture(t, legacy, "old-docs")
	stateRegistryFixture(t, current, "new-docs")
	if err := Execute(context.Background(), []string{"list"}, "test", "test"); err == nil || !strings.Contains(err.Error(), "--state-dir") {
		t.Fatalf("missing explicit state choice: %v", err)
	}
	cfg := filepath.Join(t.TempDir(), "config.toml")
	invalidRoot := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(invalidRoot, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	contents := fmt.Sprintf("[catalog]\ndirectory=%q\n", invalidRoot)
	if err := os.WriteFile(cfg, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{legacy, current} {
		if err := Execute(context.Background(), []string{"--state-dir", root, "list", "--config", cfg}, "test", "test"); err != nil {
			t.Fatal("state-dir failed to override config:", err)
		}
	}
	target := filepath.Join(t.TempDir(), "migrated")
	if err := Execute(context.Background(), []string{"state", "migrate", "--config", cfg, "--state-dir", target, "--env", "/not/loaded"}, "test", "test"); err != nil {
		t.Fatal(err)
	}
	for root, name := range map[string]string{target: "old-docs", current: "new-docs"} {
		r, err := repository.NewRegistryRepository(context.Background(), filepath.Join(root, "registry.db"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = r.Set(context.Background(), name)
		r.Close()
		if err != nil {
			t.Fatal("selected state was lost:", err)
		}
	}
	if after, _ := os.ReadFile(cfg); string(after) != contents {
		t.Fatal("config file changed")
	}
}
