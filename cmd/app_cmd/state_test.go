package app_cmd

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"glesha/config"
	"glesha/database/model"
	"glesha/database/repository"
)

func TestDefaultStateMigrationCLI(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("Linux XDG integration")
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("PATH", "")
	ctx := context.Background()
	old, _ := config.LegacyStatePath()
	current, _ := config.DefaultStatePath()
	if err := os.MkdirAll(old, 0700); err != nil {
		t.Fatal(err)
	}
	registry, err := repository.NewRegistryRepository(ctx, filepath.Join(old, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.Create(ctx, model.Set{ID: "set", Name: "docs", CatalogTo: "local"}); err != nil {
		t.Fatal(err)
	}
	registry.Close()
	cfg, _ := config.DefaultPath()
	contents := []byte("[memory]\nmax='128MiB'\n")
	if err = os.WriteFile(cfg, contents, 0600); err != nil {
		t.Fatal(err)
	}
	if err = Execute(ctx, []string{"status"}, "test", "test"); err == nil || !strings.Contains(err.Error(), "state migrate") {
		t.Fatal("old state was ignored:", err)
	}
	if _, err = os.Stat(current); !os.IsNotExist(err) {
		t.Fatal("empty replacement state created")
	}
	source := filepath.Join(t.TempDir(), "docs")
	if err = os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "hello.txt"), []byte("independent recovery"), 0600); err != nil {
		t.Fatal(err)
	}
	password := filepath.Join(t.TempDir(), "password")
	if err = os.WriteFile(password, []byte("test password"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "standalone.tar.gz.gpg")
	if err = Execute(ctx, []string{"archive", source, "--compression", "gzip", "--output", archive, "--passphrase-file", password}, "test", "test"); err != nil {
		t.Fatal("standalone archive blocked by old state:", err)
	}
	output := filepath.Join(t.TempDir(), "extracted")
	if err = Execute(ctx, []string{"extract", archive, "--into", output, "--passphrase-file", password}, "test", "test"); err != nil {
		t.Fatal("standalone recovery blocked by old state:", err)
	}
	content, err := os.ReadFile(filepath.Join(output, "standalone", "docs", "hello.txt"))
	if err != nil || string(content) != "independent recovery" {
		t.Fatal("standalone recovery failed:", err)
	}
	if _, err = os.Stat(current); !os.IsNotExist(err) {
		t.Fatal("standalone archive created managed state")
	}
	// migration needs neither credentials nor cloud access
	if err = Execute(ctx, []string{"state", "migrate", "--env", "/not/a/credential/file"}, "test", "test"); err != nil {
		t.Fatal(err)
	}
	if err = Execute(ctx, []string{"status"}, "test", "test"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(cfg)
	if string(after) != string(contents) {
		t.Fatal("config changed")
	}
	entries, err := os.ReadDir(filepath.Dir(cfg))
	if err != nil || len(entries) != 1 || entries[0].Name() != "config.toml" {
		t.Fatal("config directory contains state:", entries, err)
	}
	if err = showHelp([]string{"state", "migrate"}); err != nil {
		t.Fatal(err)
	}
}
