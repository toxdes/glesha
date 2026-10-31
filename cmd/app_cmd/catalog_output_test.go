package app_cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	L "glesha/logger"
)

func TestRepeatedCatalogSyncReportsUpToDate(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("GLESHA_CATALOG_PASSPHRASE", "catalog secret")
	for _, encrypted := range []bool{false, true} {
		t.Run(fmt.Sprintf("encrypted=%t", encrypted), func(t *testing.T) {
			bucket, server := newTestBucket(t)
			writer := portableRuntime(t, server.URL)
			writer.config.Catalog.Encrypted = encrypted
			writer.env.To = destinations{"aws"}
			if err := create(writer, []string{"music", t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			reader := portableRuntime(t, server.URL)
			if err := catalogSync(reader, nil); err != nil {
				t.Fatal(err)
			}
			if output := reader.out.(*bytes.Buffer).String(); !strings.Contains(output, "Updated") {
				t.Fatalf("initial download was not reported: %s", output)
			}
			bucket.mu.Lock()
			reads, writes, requests := bucket.catalogReads, bucket.metadataWrites, bucket.requests
			bucket.mu.Unlock()
			reader.out.(*bytes.Buffer).Reset()
			var progress bytes.Buffer
			renderer := L.NewProgressRenderer(&progress, false)
			reader.ctx = L.WithProgress(reader.ctx, renderer)
			err := catalogSync(reader, nil)
			renderer.Close()
			reader.ctx = L.WithProgress(reader.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(progress.String(), "Checking remote catalogs") || strings.Contains(progress.String(), "Verifying") || strings.Contains(progress.String(), "Uploading") || strings.Contains(progress.String(), "Hashing") {
				t.Fatalf("unexpected unchanged sync activity: %s", progress.String())
			}
			expected := "Everything is up to date. 1 catalog checked."
			if encrypted {
				expected = "Everything is up to date. 1 catalog checked; 1 locked.\n  Open a cached catalog: glesha catalog unlock SET"
			}
			if output := reader.out.(*bytes.Buffer).String(); strings.TrimSpace(output) != expected {
				t.Fatalf("unexpected repeat output: %s", output)
			}
			bucket.mu.Lock()
			if bucket.catalogReads != reads || bucket.metadataWrites != writes || bucket.payloadReads != 0 || bucket.payloadWrites != 0 {
				t.Error("unchanged sync transferred catalog or archive data")
			}
			if bucket.requests <= requests {
				t.Error("sync did not check remote changes")
			}
			bucket.mu.Unlock()
			reader.out.(*bytes.Buffer).Reset()
			reader.env.Format = "json"
			if err := catalogSync(reader, nil); err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(reader.out.(*bytes.Buffer))
			var record struct {
				Version int
				Kind    string
				Data    json.RawMessage
			}
			if err := decoder.Decode(&record); err != nil {
				t.Fatal(err)
			}
			var result catalogSyncResult
			if err := json.Unmarshal(record.Data, &result); err != nil {
				t.Fatal(err)
			}
			if record.Version != 3 || record.Kind != "catalog_state" || result.Result != CatalogSyncUpToDate || result.Locked != encrypted {
				t.Fatalf("incorrect JSON result: %+v", result)
			}
			if err := decoder.Decode(&record); err != nil {
				t.Fatal(err)
			}
			if record.Version != 3 || record.Kind != "catalog_sync_summary" {
				t.Fatalf("missing JSON summary: %s", record.Kind)
			}
			// missing cache files must be repaired even if the revision is unchanged
			files, err := filepath.Glob(filepath.Join(reader.service.Directory, "metadata-*"))
			if err != nil || len(files) == 0 {
				t.Fatalf("metadata cache missing: %v", err)
			}
			if err := os.Remove(files[0]); err != nil {
				t.Fatal(err)
			}
			reader.env.Format = "plain"
			reader.out.(*bytes.Buffer).Reset()
			if err := catalogSync(reader, nil); err != nil {
				t.Fatal(err)
			}
			if output := reader.out.(*bytes.Buffer).String(); !strings.Contains(output, "Updated") || strings.Contains(output, "Everything is up to date") {
				t.Fatalf("cache repair reported no changes: %s", output)
			}
			if encrypted {
				reader.out.(*bytes.Buffer).Reset()
				if err := catalogSync(reader, []string{reader.service.Set.ID}); err != nil {
					t.Fatal(err)
				}
				if output := reader.out.(*bytes.Buffer).String(); (!strings.Contains(output, "music") || !strings.Contains(output, "Updated")) || strings.Contains(output, "1 locked") {
					t.Fatalf("unlock outcome was not reported: %s", output)
				}
				reader.out.(*bytes.Buffer).Reset()
				if err := catalogSync(reader, nil); err != nil {
					t.Fatal(err)
				}
				if output := reader.out.(*bytes.Buffer).String(); strings.TrimSpace(output) != "Everything is up to date. 1 catalog checked." {
					t.Fatalf("unlocked catalog was not up to date: %s", output)
				}
			}
		})
	}
}
