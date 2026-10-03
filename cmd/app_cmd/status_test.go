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

func TestStatusChunkedCountsActualObjectsAndClasses(t *testing.T) {
	ctx := context.Background()
	registry, err := repository.NewRegistryRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	remote, err := registry.BindRemote(ctx, model.Remote{Name: "aws", Kind: "aws", Region: "us-east-1", Bucket: "test"})
	if err != nil {
		t.Fatal(err)
	}
	set := model.Set{ID: "set", Name: "docs", CatalogTo: "local", To: []string{remote.ID}}
	if err = registry.Create(ctx, set); err != nil {
		t.Fatal(err)
	}
	r := &runtime{ctx: ctx, registry: registry, config: config.Defaults(), root: t.TempDir(), env: &AppCmdEnv{}, out: &bytes.Buffer{}}
	if err = r.openSet(set.ID); err != nil {
		t.Fatal(err)
	}
	c, err := r.service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	v := model.Snapshot{Manifest: model.Manifest{ID: "chunked", Set: set.ID, Full: true, Layout: "chunked"}, Size: 3, Status: model.STATUS_COMPLETED}
	v.Chunks = []model.Chunk{
		{Size: 1, Locations: []model.Location{{Provider: remote.ID, Key: "chunk-a", Status: model.STATUS_COMPLETED, CurrentClass: "STANDARD_IA", Verification: "sha256"}}},
		{Size: 1, Locations: []model.Location{{Provider: remote.ID, Key: "chunk-b", Status: model.STATUS_COMPLETED, CurrentClass: "DEEP_ARCHIVE", Cold: true, Verification: "sha256"}}},
	}
	if err = c.PutSnapshot(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err = c.PutLocation(ctx, model.Location{Snapshot: v.ID, Provider: remote.ID, Key: "descriptor", Status: model.STATUS_COMPLETED, CurrentClass: "STANDARD", Verification: "sha256"}); err != nil {
		t.Fatal(err)
	}
	if err = c.SetMeta(ctx, "head", v.ID); err != nil {
		t.Fatal(err)
	}
	c.Close()
	d, err := collectStatus(r, set)
	if err != nil {
		t.Fatal(err)
	}
	var expected float64
	for _, class := range []string{"STANDARD", "STANDARD_IA", "DEEP_ARCHIVE"} {
		monthly, known := pricing.Monthly("aws", "us-east-1", class, 1)
		if !known {
			t.Fatal("unknown rate")
		}
		expected += pricing.Annual(monthly)
	}
	if d.ArchiveBytes != 3 || d.ArchiveAnnualUSD != expected || d.Snapshots != 1 {
		t.Fatalf("incorrect object accounting: %+v", d)
	}
	group := d.LatestObjects[remote.ID]
	if group.Objects != 3 || group.Class != "mixed" || !group.Cold {
		t.Fatalf("incorrect latest copy summary: %+v", group)
	}
	if len(d.Locations) != 1 {
		t.Fatal("changed existing locations envelope")
	}
	text := statusDetails(d)
	if !strings.Contains(text, "3 objects; mixed") || !strings.Contains(text, "cold restore required") {
		t.Fatal(text)
	}
}
