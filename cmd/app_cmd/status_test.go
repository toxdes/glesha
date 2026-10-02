package app_cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"glesha/config"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/pricing"
)

func TestStatusDoesNotCreateMissingCatalog(t *testing.T) {
	ctx := context.Background()
	registry, err := repository.NewRegistryRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	set := model.Set{ID: "set", Name: "music", CatalogTo: "local"}
	if err = registry.Create(ctx, set); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	r := &runtime{ctx: ctx, registry: registry, config: config.Defaults(), root: t.TempDir(), env: &AppCmdEnv{}, out: &output}
	for _, args := range [][]string{nil, {"music"}} {
		output.Reset()
		if err = status(r, args); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "music") || strings.Contains(output.String(), "unknown") || !strings.Contains(output.String(), " - ") {
			t.Fatal(output.String())
		}
		if strings.Contains(output.String(), "Storage estimate:") || strings.Contains(output.String(), "Excludes requests") {
			t.Fatal("status footer restored")
		}
		if _, err = os.Stat(filepath.Join(r.root, set.ID, "catalog.db")); !os.IsNotExist(err) {
			t.Fatal("status created catalog", err)
		}
	}
}

func TestHumanHelpOmitsRoutineExplanations(t *testing.T) {
	for _, name := range []string{"", "create", "run", "status", "help", "version"} {
		text := Usage()
		if name != "" {
			text = CommandUsage(name)
		}
		for _, redundant := range []string{"Omitted options", "without reading configuration", "There is no default provider.", "never approves cloud charges"} {
			if strings.Contains(text, redundant) {
				t.Fatalf("%s: redundant help: %s", name, redundant)
			}
		}
	}
}

func TestStatusCountsProviderCopiesAndPreservesJSONFields(t *testing.T) {
	ctx := context.Background()
	registry, err := repository.NewRegistryRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	remote, err := registry.BindRemote(ctx, model.Remote{Name: "b2", Kind: "b2", Bucket: "test"})
	if err != nil {
		t.Fatal(err)
	}
	set := model.Set{ID: "set", Name: "music", CatalogTo: "local", To: []string{remote.ID}}
	if err = registry.Create(ctx, set); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	r := &runtime{ctx: ctx, registry: registry, config: config.Defaults(), root: t.TempDir(), env: &AppCmdEnv{JSON: true}, out: &output}
	if err = r.openSet(set.ID); err != nil {
		t.Fatal(err)
	}
	c, err := r.service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	date := time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)
	for _, id := range []string{"first", "second", "duplicate"} {
		v := model.Snapshot{Manifest: model.Manifest{ID: id, Set: set.ID, Full: true}, Size: pricing.DecimalGB, Status: model.STATUS_COMPLETED, Created: date}
		if err = c.PutSnapshot(ctx, v); err != nil {
			t.Fatal(err)
		}
		key := id
		if id == "first" || id == "duplicate" {
			key = "quote'; DROP TABLE copies;--"
		}
		if err = c.PutLocation(ctx, model.Location{Snapshot: id, Provider: remote.ID, Key: key, Status: model.STATUS_COMPLETED, UploadedAt: &date}); err != nil {
			t.Fatal(err)
		}
	}
	if err = c.SetMeta(ctx, "head", "duplicate"); err != nil {
		t.Fatal(err)
	}
	c.Close()
	if err = status(r, []string{"music"}); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Version int
		Kind    string
		Data    statusData
	}
	if err = json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	d := envelope.Data
	if envelope.Version != 2 || envelope.Kind != "status" || d.Latest != "duplicate" || len(d.Locations) != 1 || !d.CatalogAvailable || d.ArchiveBytes != 2*pricing.DecimalGB || d.Snapshots != 3 || d.ArchiveAnnualUSD != pricing.Annual(2*.00695) || !d.LastUpload.Equal(date) {
		t.Fatal(output.String())
	}
}

func TestStatusTablesEscapeNamesAndPreserveLongValues(t *testing.T) {
	text := asciiTableRow([]string{"é\n|name", strings.Repeat("a", 100)}, []int{20, 30})
	if strings.Contains(text, "é") || !strings.Contains(text, `\u00e9\n\x7cname`) || strings.Count(text, "a") != 101 {
		t.Fatal(text)
	}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if len(line) != 57 {
			t.Fatal("inconsistent table width", len(line), line)
		}
	}
}
