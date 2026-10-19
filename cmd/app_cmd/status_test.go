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

	"glesha/backup"
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
		if !strings.Contains(output.String(), "music") || strings.Contains(output.String(), "unknown") || !strings.Contains(output.String(), "-") {
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
	r := &runtime{ctx: ctx, registry: registry, config: config.Defaults(), root: t.TempDir(), env: &AppCmdEnv{Format: "json"}, out: &output}
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
	text := renderColumns([][]string{{"SET", "VALUE"}, {"é\n|name", strings.Repeat("a", 100)}}, "table", false)
	if strings.Contains(text, "é") || !strings.Contains(text, `\u00e9\n\x7cname`) || strings.Count(text, "a") != 101 {
		t.Fatal(text)
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
	if !strings.Contains(text, "DEEP_ARCHIVE") || !strings.Contains(text, "33.33%") {
		t.Fatal(text)
	}
}

func TestStatusFormatsAndDominantClass(t *testing.T) {
	ctx := context.Background()
	registry, err := repository.NewRegistryRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	set := model.Set{ID: "set", Name: "docs", CatalogTo: "local"}
	if err = registry.Create(ctx, set); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	r := &runtime{ctx: ctx, registry: registry, config: config.Defaults(), root: t.TempDir(), env: &AppCmdEnv{}, out: &output}
	for _, format := range []string{"plain", "table", "csv", "json"} {
		t.Run(format, func(t *testing.T) {
			output.Reset()
			r.env = &AppCmdEnv{Format: format}
			if err := status(r, nil); err != nil {
				t.Fatal(err)
			}
			text := output.String()
			switch format {
			case "plain":
				if strings.Contains(text, "+---") || !strings.Contains(text, "EST/YR") {
					t.Fatal(text)
				}
			case "table":
				if !strings.Contains(text, "+---") {
					t.Fatal(text)
				}
			case "csv":
				if !strings.HasPrefix(text, "set,state,archive_bytes,") || strings.Contains(text, "TOTAL") {
					t.Fatal(text)
				}
			case "json":
				var envelope map[string]any
				if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope["version"] != float64(2) {
					t.Fatal(text)
				}
			}
		})
	}
	if got := dominantClass(map[string]int64{"STANDARD": 10, "DEEP_ARCHIVE": 100}); got != "DEEP_ARCHIVE" {
		t.Fatal(got)
	}
	if got := dominantClass(map[string]int64{"STANDARD": 10, "DEEP_ARCHIVE": 10}); got != "DEEP_ARCHIVE" {
		t.Fatal("unstable tie", got)
	}
	for size, want := range map[int64]string{512: "512B", 1024: "1.00K", 1 << 20: "1.00M", 1 << 30: "1.00G"} {
		if got := compactSize(size); got != want {
			t.Fatal(got, want)
		}
	}
}

func TestStatusColorOnlyAppliesToStateCells(t *testing.T) {
	rows := [][]string{{"SET", "STATE", "SIZE"}, {"docs", "SYNCED", "1.00G"}, {"music", "PAUSED", "2.00G"}}
	plain := renderColumns(rows, "plain", false)
	color := renderColumns(rows, "plain", true)
	if !strings.Contains(color, "\x1b[32mSYNCED\x1b[0m") || !strings.Contains(color, "\x1b[33mPAUSED\x1b[0m") {
		t.Fatal(color)
	}
	stripped := strings.NewReplacer("\x1b[32m", "", "\x1b[33m", "", "\x1b[0m", "").Replace(color)
	if stripped != plain {
		t.Fatal("color changed alignment")
	}
}

func TestCleanupAndSyncSelection(t *testing.T) {
	ctx := context.Background()
	registry, err := repository.NewRegistryRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if err = registry.Create(ctx, model.Set{ID: "set", Name: "docs", CatalogTo: "local"}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	r := &runtime{ctx: ctx, registry: registry, config: config.Defaults(), root: t.TempDir(), env: &AppCmdEnv{}, out: &output}
	if _, err = r.selectedSets(nil); err == nil {
		t.Fatal("implicit all allowed")
	}
	r.env.All = true
	if _, err = r.selectedSets([]string{"docs"}); err == nil {
		t.Fatal("ambiguous selection accepted")
	}
	if err = catalogSync(r, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "SKIPPED") {
		t.Fatal(output.String())
	}
	if err = cleanup(r, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(r.root, "set", "catalog.db")); !os.IsNotExist(err) {
		t.Fatal("cleanup created a missing catalog")
	}
}

func TestStatusTotalDeduplicatesSharedArchiveVersions(t *testing.T) {
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
	var out bytes.Buffer
	r := &runtime{ctx: ctx, registry: registry, config: config.Defaults(), root: t.TempDir(), env: &AppCmdEnv{}, out: &out}
	for _, name := range []string{"first", "second"} {
		set := model.Set{ID: name, Name: name, CatalogTo: "local", To: []string{remote.ID}}
		if err = registry.Create(ctx, set); err != nil {
			t.Fatal(err)
		}
		if err = r.openSet(name); err != nil {
			t.Fatal(err)
		}
		c, err := r.service.Catalog(ctx)
		if err != nil {
			t.Fatal(err)
		}
		v := model.Snapshot{Manifest: model.Manifest{Version: 1, ID: name + "-snapshot", Set: name, Full: true}, Size: 1 << 30, Status: model.STATUS_COMPLETED}
		if err = c.PutSnapshot(ctx, v); err != nil {
			t.Fatal(err)
		}
		if err = c.SetMeta(ctx, "head", v.ID); err != nil {
			t.Fatal(err)
		}
		if err = c.PutLocation(ctx, model.Location{Snapshot: v.ID, Provider: remote.ID, Key: "shared-key", Version: "shared-version", Status: model.STATUS_COMPLETED}); err != nil {
			t.Fatal(err)
		}
		c.Close()
	}
	if err = status(r, nil); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "TOTAL") {
			fields := strings.Fields(line)
			if len(fields) != 4 || fields[1] != "1.00G" || fields[2] != "2" {
				t.Fatal(line)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("total missing", out.String())
	}
}

func TestNewRemoteBackedSetReportsNew(t *testing.T) {
	ctx := context.Background()
	registry, err := repository.NewRegistryRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	cfg := config.Defaults()
	cfg.B2.Bucket = "test"
	var out bytes.Buffer
	r := &runtime{ctx: ctx, registry: registry, config: cfg, root: t.TempDir(), env: &AppCmdEnv{To: destinations{"b2"}}, out: &out}
	if err = create(r, []string{"docs", t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	condition, err := r.service.Condition(ctx)
	if err != nil || condition.State != backup.STATE_NEW {
		t.Fatal(condition, err)
	}
}
