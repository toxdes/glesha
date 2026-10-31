package app_cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"glesha/backup"
	L "glesha/logger"
)

func TestEmptyInventoriesExplainNextStepWithoutPollutingJSON(t *testing.T) {
	r := portableRuntime(t, "")
	for _, command := range []string{"list", "status"} {
		t.Run(command, func(t *testing.T) {
			r.env = &AppCmdEnv{}
			r.out.(*bytes.Buffer).Reset()
			if err := handlers[command](r, nil); err != nil {
				t.Fatal(err)
			}
			output := r.out.(*bytes.Buffer).String()
			if !strings.Contains(output, "No sets found locally") || !strings.Contains(output, "glesha catalog sync") || strings.Contains(output, "TOTAL") {
				t.Fatalf("empty output is not useful: %s", output)
			}
			r.env.Format = "json"
			r.out.(*bytes.Buffer).Reset()
			if err := handlers[command](r, nil); err != nil {
				t.Fatal(err)
			}
			if r.out.(*bytes.Buffer).Len() != 0 {
				t.Fatal("empty JSON inventory contains prose")
			}
		})
	}
}

func TestActivityUsesStderrAndInformationIsNeutral(t *testing.T) {
	r := portableRuntime(t, "")
	if err := L.SetColorModeFromString("yes"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { L.SetColorModeFromString("auto") })
	if err := L.SetLevelFromString("info"); err != nil {
		t.Fatal(err)
	}
	if err := r.beginCatalogCheck(); err != nil {
		t.Fatal(err)
	}
	r.finishCatalogCheck()
	if r.out.(*bytes.Buffer).Len() != 0 || !strings.Contains(r.errOut.(*bytes.Buffer).String(), "Checking remote catalogs") {
		t.Fatal("activity mixed with stdout results")
	}
	if err := r.result("No changes."); err != nil {
		t.Fatal(err)
	}
	if got := r.out.(*bytes.Buffer).String(); got != "No changes.\n" {
		t.Fatalf("information is colored or malformed: %q", got)
	}
	r.env.Format = "json"
	r.out.(*bytes.Buffer).Reset()
	r.errOut.(*bytes.Buffer).Reset()
	if err := r.beginCatalogCheck(); err != nil {
		t.Fatal(err)
	}
	if err := r.result("No changes."); err != nil {
		t.Fatal(err)
	}
	if r.out.(*bytes.Buffer).Len() != 0 || r.errOut.(*bytes.Buffer).Len() != 0 {
		t.Fatal("JSON received activity or prose")
	}
}

func TestUnavailableCatalogDoesNotClaimZeroSnapshots(t *testing.T) {
	r := portableRuntime(t, "")
	set, err := backup.NewSet("locked", nil, r.config)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := r.remoteIDs([]string{"aws"})
	if err != nil {
		t.Fatal(err)
	}
	set.CatalogTo = ids[0]
	if err := r.registry.Create(r.ctx, set); err != nil {
		t.Fatal(err)
	}
	if err := status(r, nil); err != nil {
		t.Fatal(err)
	}
	output := r.out.(*bytes.Buffer).String()
	found := false
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == "locked" {
			found = true
			if fields[2] != "-" || fields[3] != "-" {
				t.Fatalf("unavailable data shown as zero: %s", line)
			}
		}
	}
	if !found || !strings.Contains(output, "Totals cover 0 of 1") || !strings.Contains(output, "catalog unlock SET") {
		t.Fatalf("missing catalog guidance: %s", output)
	}
}

func TestPortableCatalogActivitySupportsStructuredProgress(t *testing.T) {
	for _, mode := range []string{"auto", "off"} {
		t.Run(mode, func(t *testing.T) {
			out, diagnostic, err := captureOutput(t, func() error {
				L.SetStructuredDiagnostics(true)
				defer L.SetStructuredDiagnostics(false)
				renderer := L.NewEventProgressRenderer(L.EmitProgress)
				defer renderer.Close()
				r := &runtime{ctx: L.WithProgress(context.Background(), renderer), env: &AppCmdEnv{Format: "json", Progress: mode}}
				if err := r.beginCatalogCheck(); err != nil {
					return err
				}
				r.finishCatalogCheck()
				return renderer.Err()
			})
			if err != nil || out != "" {
				t.Fatalf("unexpected result output: %q: %v", out, err)
			}
			rows := decodeRecords(t, diagnostic)
			if mode == "off" {
				if len(rows) != 0 {
					t.Fatalf("progress off emitted records: %s", diagnostic)
				}
				return
			}
			if len(rows) != 2 {
				t.Fatalf("missing phase start or stop: %s", diagnostic)
			}
			for _, row := range rows {
				if row["version"] != float64(1) || row["kind"] != "progress" {
					t.Fatalf("invalid diagnostic envelope: %s", diagnostic)
				}
			}
		})
	}
}

func TestPortableActionHelpSupportsStructuredFormat(t *testing.T) {
	out, diagnostic, err := captureOutput(t, func() error {
		return Execute(context.Background(), []string{"catalog", "unlock", "--help", "--format=json", "--config", "/missing", "--env", "/missing"}, "test", "sha")
	})
	if err != nil || diagnostic != "" {
		t.Fatalf("help loaded configuration or emitted diagnostics: %s: %v", diagnostic, err)
	}
	rows := decodeRecords(t, out)
	if len(rows) != 1 || rows[0]["kind"] != "help" {
		t.Fatalf("missing help result: %s", out)
	}
	data := rows[0]["data"].(map[string]any)
	text := strings.Join(strings.Fields(data["text"].(string)), " ")
	if data["command"] != "catalog unlock" || !strings.Contains(text, "no cloud requests") || !strings.Contains(text, "--format FORMAT") || strings.Contains(text, "--json") {
		t.Fatalf("action help lost merged options: %s", out)
	}
}

func TestHelpIncludesArgumentsAndActionSpecificOptions(t *testing.T) {
	for command, arguments := range map[string]string{"create": "SET PATH...", "run": "SET", "restore": "SET (--into DIR | --cancel)", "archive": "PATH...", "download": "KEY --from REMOTE..."} {
		t.Run(command, func(t *testing.T) {
			if text := CommandUsage(command); !strings.Contains(text, "glesha "+command+" "+arguments) {
				t.Fatalf("required arguments missing: %s", text)
			}
		})
	}
	text := strings.Join(strings.Fields(CommandActionUsage("catalog", "unlock")), " ")
	for _, required := range []string{"catalog unlock SET", "no cloud requests", "--catalog-passphrase-file"} {
		if !strings.Contains(text, required) {
			t.Fatalf("unlock help missing %q", required)
		}
	}
	for _, irrelevant := range []string{"--keep", "--authority", "--all", "--from"} {
		if strings.Contains(text, irrelevant) {
			t.Fatalf("unlock help contains irrelevant flag %s", irrelevant)
		}
	}
}

func TestCatalogUnlockReadsCacheWithoutBucketRequests(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("GLESHA_CATALOG_PASSPHRASE", "test catalog key")
	bucket, server := newTestBucket(t)
	writer := portableRuntime(t, server.URL)
	writer.config.Catalog.Encrypted = true
	writer.env.To = destinations{"aws"}
	if err := create(writer, []string{"music", t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	id := writer.service.Set.ID
	reader := portableRuntime(t, server.URL)
	if err := catalogSync(reader, nil); err != nil {
		t.Fatal(err)
	}
	bucket.mu.Lock()
	requests := bucket.requests
	bucket.mu.Unlock()
	reader.out.(*bytes.Buffer).Reset()
	if err := catalogUnlock(reader, []string{id}); err != nil {
		t.Fatal(err)
	}
	if output := reader.out.(*bytes.Buffer).String(); !strings.Contains(output, `Catalog open for "music"`) || !strings.Contains(output, "glesha history") {
		t.Fatalf("missing unlock result: %s", output)
	}
	reader.env.Format = "json"
	reader.out.(*bytes.Buffer).Reset()
	if err := catalogUnlock(reader, []string{id}); err != nil {
		t.Fatal(err)
	}
	var record struct {
		Kind string
		Data struct {
			AlreadyOpen bool `json:"already_open"`
		}
	}
	if err := json.NewDecoder(reader.out.(*bytes.Buffer)).Decode(&record); err != nil {
		t.Fatal(err)
	}
	if record.Kind != "catalog_unlocked" || !record.Data.AlreadyOpen {
		t.Fatalf("missing unchanged unlock result: %+v", record)
	}
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	if bucket.requests != requests {
		t.Fatal("local catalog unlock contacted a bucket")
	}
}

func TestInvalidCatalogActionDoesNotLoadConfiguration(t *testing.T) {
	err := Execute(context.Background(), []string{"catalog", "typo", "SET", "--config", "/not/read/config.toml", "--env", "/not/read/credentials"}, "test", "test")
	if err == nil || !strings.Contains(err.Error(), "unknown catalog action") || !strings.Contains(err.Error(), "glesha help catalog") {
		t.Fatalf("invalid action did not fail with help before reading files: %v", err)
	}
}

func TestTableValuesCannotInjectLinesOrColors(t *testing.T) {
	output := renderColumns([][]string{{"SET", "STATE"}, {"name\n\x1b[32m", "FAILED"}}, "plain", true)
	if strings.Count(output, "\n") != 2 || strings.Contains(output, "\x1b[32m") || !strings.Contains(output, "\x1b[31mFAILED\x1b[0m") {
		t.Fatalf("table values changed layout or state colors: %q", output)
	}
	if path := L.Value(`C:\Users\name`); path != `C:\Users\name` {
		t.Fatalf("Windows path was double escaped: %s", path)
	}
}
