package app_cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"glesha/backup"
	L "glesha/logger"
)

func captureOutput(t *testing.T, f func() error) (string, string, error) {
	t.Helper()
	stdout, stderr := os.Stdout, os.Stderr
	out, err := os.CreateTemp(t.TempDir(), "stdout-")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	diagnostic, err := os.CreateTemp(t.TempDir(), "stderr-")
	if err != nil {
		t.Fatal(err)
	}
	defer diagnostic.Close()
	os.Stdout, os.Stderr = out, diagnostic
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()
	result := f()
	if _, err = out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = diagnostic.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	a, err := io.ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(diagnostic)
	if err != nil {
		t.Fatal(err)
	}
	return string(a), string(b), result
}
func decodeRecords(t *testing.T, text string) []map[string]any {
	t.Helper()
	if strings.ContainsAny(text, "\x1b\r") {
		t.Fatalf("terminal controls in JSON: %q", text)
	}
	var rows []map[string]any
	d := json.NewDecoder(strings.NewReader(text))
	for {
		var row map[string]any
		err := d.Decode(&row)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid JSON: %v: %q", err, text)
		}
		rows = append(rows, row)
	}
	return rows
}
func TestEveryCommandAcceptsJSONFormat(t *testing.T) {
	for command := range commandDescriptions {
		t.Run(command, func(t *testing.T) {
			out, diagnostic, err := captureOutput(t, func() error {
				return Execute(context.Background(), []string{command, "--format=json", "--help", "--config", "/missing", "--env", "/missing"}, "1.2.3", "abc")
			})
			if err != nil {
				t.Fatal(err)
			}
			rows := decodeRecords(t, out)
			if len(rows) != 1 || rows[0]["kind"] != "help" || rows[0]["version"] != float64(2) || diagnostic != "" {
				t.Fatal(out, diagnostic)
			}
		})
	}
}
func TestStructuredVersionForms(t *testing.T) {
	for _, args := range [][]string{{"version", "--format=json"}, {"--format", "json", "--version"}, {"--format=json", "version"}, {"list", "--format=json", "--version"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, _, err := captureOutput(t, func() error { return Execute(context.Background(), args, "1.2.3", "abc") })
			if err != nil {
				t.Fatal(err)
			}
			rows := decodeRecords(t, out)
			if len(rows) != 1 || rows[0]["kind"] != "version" {
				t.Fatal(out)
			}
			data := rows[0]["data"].(map[string]any)
			if data["version"] != "1.2.3" || data["revision"] != "abc" {
				t.Fatal(out)
			}
		})
	}
}
func TestInvalidOutputFailsBeforeConfig(t *testing.T) {
	for _, args := range [][]string{{"run", "--format=csv"}, {"version", "--format=invalid"}, {"archive", "--format=json", "--json", "--format=plain"}, {"check", "--progress=invalid"}} {
		args = append(args, "--config", "/missing")
		if err := Execute(context.Background(), args, "test", "sha"); err == nil || strings.Contains(err.Error(), "/missing") {
			t.Fatal(args, err)
		}
	}
}
func TestRequestedJSONDoesNotInspectValuesOrPositionals(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"archive", "--output", "--format=json"}, false},
		{[]string{"help", "--", "--format=json"}, false},
		{[]string{"version", "--json=false"}, false},
		{[]string{"unknown", "--format=json"}, true},
		{[]string{"run", "--format", "json", "--bad"}, true},
	} {
		if got := requestedJSON(tc.args); got != tc.want {
			t.Fatal(tc.args, got)
		}
	}
}
func TestStructuredErrorsPreserveExitCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
		exit int
	}{
		{backup.ErrPending, "PENDING", 3}, {backup.ErrConflict, "CONFLICT", 4},
		{backup.ErrConfirmationRequired, "CONFIRMATION_REQUIRED", 1}, {context.Canceled, "CANCELED", 130},
		{errors.New("cli: unsupported flag"), "INVALID_ARGUMENT", 2},
	} {
		t.Run(tc.code, func(t *testing.T) {
			out, diagnostic, _ := captureOutput(t, func() error { PrintError([]string{"run", "--format=json", "--color=yes"}, tc.err); return nil })
			rows := decodeRecords(t, diagnostic)
			if out != "" || len(rows) != 1 {
				t.Fatal(out, diagnostic)
			}
			data := rows[0]["data"].(map[string]any)
			if data["code"] != tc.code || data["exit_code"] != float64(tc.exit) {
				t.Fatal(diagnostic)
			}
		})
	}
}
func TestJSONLocalRoundTripAndProgressOff(t *testing.T) {
	t.Setenv("PATH", "")
	root := t.TempDir()
	source := filepath.Join(root, "docs")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "quoted\"name.txt"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	password := filepath.Join(root, "password")
	if err := os.WriteFile(password, []byte("test password"), 0600); err != nil {
		t.Fatal(err)
	}
	// use a dedicated configuration so tests never open user state
	config := filepath.Join(root, "config.toml")
	if err := os.WriteFile(config, []byte("[memory]\nmax='128MiB'\n[catalog]\ndirectory='"+filepath.ToSlash(filepath.Join(root, "state"))+"'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "test.tar.gz.gpg")
	plain := filepath.Join(root, "test.tar.gz")
	into := filepath.Join(root, "restored")
	for _, args := range [][]string{
		{"archive", source, "--compression=gzip", "--output", archive},
		{"decrypt", archive, "--output", plain},
		{"extract", plain, "--into", into},
	} {
		args = append(args, "--format=json", "--progress=off", "--color=yes", "--config", config, "--passphrase-file", password)
		out, diagnostic, err := captureOutput(t, func() error { return Execute(context.Background(), args, "test", "sha") })
		if err != nil {
			t.Fatal(err)
		}
		if diagnostic != "" {
			t.Fatal(diagnostic)
		}
		rows := decodeRecords(t, out)
		if len(rows) != 1 || rows[0]["kind"] != "result" {
			t.Fatal(out)
		}
	}
	data, err := os.ReadFile(filepath.Join(into, "test", "docs", "quoted\"name.txt"))
	if err != nil || !bytes.Equal(data, []byte("hello")) {
		t.Fatal(string(data), err)
	}
}
func TestJSONProgressAndDiagnostics(t *testing.T) {
	_, text, _ := captureOutput(t, func() error {
		L.SetStructuredDiagnostics(true)
		defer L.SetStructuredDiagnostics(false)
		L.SetLevelFromString("debug")
		defer L.SetLevelFromString("info")
		L.SetColorModeFromString("yes")
		defer L.SetColorModeFromString("auto")
		r := L.NewEventProgressRenderer(L.EmitProgress)
		p := L.StartProgress(L.WithProgress(context.Background(), r), "upload", 10)
		p.Add(10)
		p.Finish()
		r.Close()
		L.Debug("debug details")
		L.Warn("warning details")
		return r.Err()
	})
	rows := decodeRecords(t, text)
	if len(rows) != 5 {
		t.Fatal(text)
	}
	for _, row := range rows {
		if row["version"] != float64(1) {
			t.Fatal(text)
		}
	}
}

func TestJSONCatalogCleanupAndEmptyLists(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "docs")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "config.toml")
	if err := os.WriteFile(config, []byte("[memory]\nmax='128MiB'\n[catalog]\ndirectory='"+filepath.ToSlash(filepath.Join(root, "state"))+"'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func(args ...string) string {
		t.Helper()
		args = append(args, "--format=json", "--progress=off", "--config", config)
		out, diagnostic, err := captureOutput(t, func() error { return Execute(context.Background(), args, "test", "sha") })
		if err != nil {
			t.Fatal(err)
		}
		if diagnostic != "" {
			t.Fatal(diagnostic)
		}
		decodeRecords(t, out)
		return out
	}
	if out := invoke("list"); out != "" {
		t.Fatalf("empty listing changed: %s", out)
	}
	invoke("create", "docs", source, "--catalog-to=local")
	out := invoke("catalog", "sync", "--all")
	if !strings.Contains(out, `"kind":"catalog_state"`) || !strings.Contains(out, `"state":"SKIPPED"`) {
		t.Fatal(out)
	}
	out = invoke("cleanup", "--all", "--dry-run")
	if !strings.Contains(out, `"kind":"cleanup_summary"`) {
		t.Fatal(out)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("cleanup touched sources", err)
	}
}

func TestRemovedJSONFlagIsRejected(t *testing.T) {
	for command := range commandDescriptions {
		t.Run(command, func(t *testing.T) {
			err := Execute(context.Background(), []string{command, "--json", "--help", "--config", "/missing"}, "test", "sha")
			if err == nil || !strings.Contains(err.Error(), "unsupported flag --json") {
				t.Fatal(err)
			}
		})
	}
}
