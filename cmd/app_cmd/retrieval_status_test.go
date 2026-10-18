package app_cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"glesha/backup"
	"glesha/config"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/memory"
)

func TestRetrievalTableUsesLocalTimeAndNoExtraBlankLine(t *testing.T) {
	previous := time.Local
	time.Local = time.FixedZone("IST", 5*3600+1800)
	defer func() { time.Local = previous }()
	checked := time.Date(time.Now().Year(), 10, 15, 9, 2, 0, 0, time.UTC)
	expires := time.Date(time.Now().Year(), 10, 18, 0, 0, 0, 0, time.UTC)
	rows := []statusData{
		{Set: model.Set{Name: "music"}, Retrievals: []backup.RetrievalStatus{{Task: backup.RETRIEVAL_RESTORE, State: backup.RETRIEVAL_READY, Ready: 1, CheckedAt: &checked, ExpiresAt: &expires}}},
		{Set: model.Set{Name: "photos"}, Retrievals: []backup.RetrievalStatus{{Task: backup.RETRIEVAL_RESTORE, State: backup.RETRIEVAL_PARTIAL, Ready: 2, Waiting: 1, CheckedAt: &checked}}},
		{Set: model.Set{Name: "old-music"}, Retrievals: []backup.RetrievalStatus{{Task: backup.RETRIEVAL_IMPORT, State: backup.RETRIEVAL_QUEUED, Waiting: 1, CheckedAt: &checked}}},
	}
	for _, single := range []bool{false, true} {
		t.Run(map[bool]string{false: "overview", true: "one set"}[single], func(t *testing.T) {
			var out bytes.Buffer
			if err := renderRetrievals(&out, rows, single, "plain", false); err != nil {
				t.Fatal(err)
			}
			text := out.String()
			if !strings.Contains(text, "14:32 IST\n") || !strings.Contains(text, "EXPIRES (IST)") || !strings.Contains(text, "Oct 18 05:30") {
				t.Fatal("timestamps not local:", text)
			}
			if strings.Contains(text, "IST\n\n") || strings.Contains(text, "objects ready") || strings.Contains(text, "Available until") || strings.Contains(text, "2/3") {
				t.Fatal("unexpected prose or spacing:", text)
			}
			if single && strings.Contains(text, "SET") || !single && !strings.Contains(text, "SET") {
				t.Fatal("wrong set column:", text)
			}
		})
	}
	var empty bytes.Buffer
	if err := renderRetrievals(&empty, nil, false, "plain", false); err != nil || empty.Len() != 0 {
		t.Fatal("empty section was rendered")
	}
}

func TestRetrievalTableShowsOldestOrUnknownCheck(t *testing.T) {
	old := time.Now().Add(-24 * time.Hour)
	recent := time.Now()
	rows := []statusData{{Retrievals: []backup.RetrievalStatus{{Task: backup.RETRIEVAL_IMPORT, State: backup.RETRIEVAL_QUEUED, CheckedAt: &recent}, {Task: backup.RETRIEVAL_RESTORE, State: backup.RETRIEVAL_QUEUED, CheckedAt: &old}}}}
	var out bytes.Buffer
	if err := renderRetrievals(&out, rows, true, "plain", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Last checked: "+statusTime(&old)+"\n") {
		t.Fatal("freshness overstated:", out.String())
	}
	rows[0].Retrievals[1].CheckedAt = nil
	out.Reset()
	if err := renderRetrievals(&out, rows, true, "plain", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Last checked: -\n") {
		t.Fatal("unknown freshness invented:", out.String())
	}
}

func TestRetrievalTableColorsStateOnly(t *testing.T) {
	rows := [][]string{{"TASK", "STATE", "READY", "WAITING"}, {"IMPORT", "READY", "1", "0"}}
	text := renderColumns(rows, "plain", true)
	if !strings.Contains(text, "\x1b[32mREADY\x1b[0m") || strings.Contains(text, "\x1b[32mIMPORT") {
		t.Fatal(text)
	}
}

func TestStatusRefreshesPendingImportWithoutCatalogOrPayloadRead(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	expires := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	var heads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodHead || request.URL.Query().Get("versionId") != "pinned" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		heads.Add(1)
		w.Header().Set("Content-Length", "42")
		w.Header().Set("ETag", `"etag"`)
		w.Header().Set("x-amz-version-id", "pinned")
		w.Header().Set("x-amz-storage-class", "DEEP_ARCHIVE")
		w.Header().Set("x-amz-restore", `ongoing-request="false", expiry-date="`+expires.Format(http.TimeFormat)+`"`)
	}))
	defer server.Close()
	ctx := context.Background()
	registry, err := repository.NewRegistryRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	remote, err := registry.BindRemote(ctx, model.Remote{Name: "aws", Kind: "aws", Region: "us-east-1", Bucket: "test", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	set := model.Set{ID: "pending-set", Name: "old-music", CatalogTo: "local"}
	if err = registry.Create(ctx, set); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	directory := filepath.Join(root, set.ID)
	if err = os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	job := map[string]any{"Days": 7, "Tier": "bulk", "Requested": true, "Remote": remote.ID, "Object": map[string]any{"Key": "encb-old.tar.gz.gpg", "Version": "pinned", "ETag": `"etag"`, "Size": 42, "Class": "DEEP_ARCHIVE", "Cold": true, "Restore": `ongoing-request="true"`}}
	data, _ := json.Marshal(job)
	if err = os.WriteFile(filepath.Join(directory, "import-legacy.gpg.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.AWS = config.Provider{Region: remote.Region, Bucket: remote.Bucket, Endpoint: remote.Endpoint}
	var out bytes.Buffer
	r := &runtime{ctx: ctx, registry: registry, root: root, config: cfg, budget: memory.Budget{TransferWorkers: 1}, env: &AppCmdEnv{}, out: &out}
	if err = status(r, []string{set.Name}); err != nil {
		t.Fatal(err)
	}
	if heads.Load() != 0 || !strings.Contains(out.String(), "QUEUED") || !strings.Contains(out.String(), "Last checked: -\n") {
		t.Fatal("plain status did not use cached pending import:", out.String())
	}
	out.Reset()
	r.env.Refresh, r.env.Format = true, "json"
	if err = status(r, []string{set.Name}); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Version int        `json:"version"`
		Data    statusData `json:"data"`
	}
	if err = json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if heads.Load() != 1 || len(envelope.Data.Retrievals) != 1 || envelope.Data.Retrievals[0].State != backup.RETRIEVAL_READY || envelope.Data.Condition.State != backup.STATE_PAUSED || envelope.Data.CatalogAvailable {
		t.Fatal("refresh did not expose ready import:", out.String())
	}
	out.Reset()
	r.env.Refresh, r.env.Format, r.env.JSON = false, "plain", false
	if err = status(r, nil); err != nil {
		t.Fatal(err)
	}
	if heads.Load() != 1 || !strings.Contains(out.String(), "READY") || strings.Contains(out.String(), "Last checked: -") {
		t.Fatal("refreshed status was not cached:", out.String())
	}
	if _, err = os.Stat(filepath.Join(directory, "catalog.db")); !os.IsNotExist(err) {
		t.Fatal("status created an import catalog")
	}
}
