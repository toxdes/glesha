package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func restoreTaskFixture(t *testing.T, s *Service, output string) []string {
	t.Helper()
	stage := filepath.Join(output, ".glesha-restore-test")
	if err := os.MkdirAll(stage, 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{filepath.Join(output, "restored.txt"), filepath.Join(stage, "staged.txt")} {
		if err := os.WriteFile(file, []byte("preserve user data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.saveJob(s.restoreJobPath(output), restoreJob{Snapshot: "snapshot", Output: output}); err != nil {
		t.Fatal(err)
	}
	if err := s.saveJob(s.restorePublicationPath(output), restorePublication{Version: restorePublicationVersion, Snapshot: "snapshot", Output: output, Stage: stage}); err != nil {
		t.Fatal(err)
	}
	return []string{s.restoreJobPath(output), s.restorePublicationPath(output)}
}

func TestCancelRestoreClearsStatusAndPreservesFiles(t *testing.T) {
	ctx := context.Background()
	s, _, _ := setupService(t)
	c, err := s.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	output := t.TempDir()
	files := restoreTaskFixture(t, s, output)
	condition, err := s.Condition(ctx)
	if err != nil || condition.Operation != "restore" || !strings.Contains(condition.Action, "--cancel") {
		t.Fatalf("missing cancellation action: %+v %v", condition, err)
	}
	if err := s.RecordResult(ctx, "restore", context.Canceled); err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join(s.Directory, "operation-result.json"))
	original := map[string][]byte{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		original[file] = data
	}
	result, err := s.CancelRestore(ctx, "")
	if err != nil || result.Tasks != 1 || len(result.Records) != 3 {
		t.Fatalf("cancel failed: %+v %v", result, err)
	}
	for _, record := range result.Records {
		base := strings.Split(record, ".cancelled-")[0]
		if data, err := os.ReadFile(record); err != nil || string(data) != string(original[base]) {
			t.Fatal("task backup changed", err)
		}
		if _, err := os.Stat(base); !os.IsNotExist(err) {
			t.Fatal("task remained active", err)
		}
	}
	for _, file := range []string{filepath.Join(output, "restored.txt"), filepath.Join(output, ".glesha-restore-test", "staged.txt")} {
		if data, err := os.ReadFile(file); err != nil || string(data) != "preserve user data" {
			t.Fatal("user data changed", err)
		}
	}
	condition, err = s.Condition(ctx)
	if err != nil || condition.Operation == "restore" {
		t.Fatalf("cancelled task still needs attention: %+v %v", condition, err)
	}
	if result, err := s.CancelRestore(ctx, ""); err != nil || result.Tasks != 0 {
		t.Fatalf("repeat cancellation not harmless: %+v %v", result, err)
	}
}

func TestCancelRestoreSelectsDestinationAndPreservesOtherWork(t *testing.T) {
	ctx := context.Background()
	s, _, _ := setupService(t)
	first, second := t.TempDir(), t.TempDir()
	firstFiles := restoreTaskFixture(t, s, first)
	secondFiles := restoreTaskFixture(t, s, second)
	if err := s.RecordResult(ctx, "run", context.Canceled); err != nil {
		t.Fatal(err)
	}
	advisory, err := os.ReadFile(filepath.Join(s.Directory, "operation-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.pending(), []byte("preserve pending backup"), 0600); err != nil {
		t.Fatal(err)
	}
	if result, err := s.CancelRestore(ctx, first); err != nil || result.Tasks != 1 {
		t.Fatalf("destination cancellation failed: %+v %v", result, err)
	}
	for _, file := range firstFiles {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatal("selected task remained", err)
		}
	}
	for _, file := range secondFiles {
		if _, err := os.Stat(file); err != nil {
			t.Fatal("other destination changed", err)
		}
	}
	if _, err := s.CancelRestore(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(s.pending()); err != nil || string(data) != "preserve pending backup" {
		t.Fatal("pending backup changed", err)
	}
	if data, err := os.ReadFile(filepath.Join(s.Directory, "operation-result.json")); err != nil || string(data) != string(advisory) {
		t.Fatal("unrelated operation result changed", err)
	}
}

func TestCancelRestoreRefusesUnknownRecordsWithoutChanges(t *testing.T) {
	for _, scenario := range []string{"publication", "legacy", "advisory"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _ := setupService(t)
			output := t.TempDir()
			files := restoreTaskFixture(t, s, output)
			path := s.restorePublicationPath(output)
			data := `{"Version":99}`
			if scenario == "legacy" {
				path = s.restoreJobPath(output)
			}
			if scenario == "advisory" {
				path = filepath.Join(s.Directory, "operation-result.json")
				files = append(files, path)
			}
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			before := map[string][]byte{}
			for _, file := range files {
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				before[file] = data
			}
			if _, err := s.CancelRestore(context.Background(), ""); err == nil {
				t.Fatal("unknown record was cancelled")
			}
			for file, data := range before {
				if after, err := os.ReadFile(file); err != nil || string(after) != string(data) {
					t.Fatal("record changed before validation completed", err)
				}
			}
		})
	}
}
