package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"glesha/internal/database/model"
)

func TestInterruptedRestoreMergeResumesFromStage(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	ctx := context.Background()
	s, _, source := setupService(t)
	pw := []byte("password")
	if err := os.WriteFile(filepath.Join(source, "added"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	v, err := s.Run(ctx, RunOptions{Password: pw, Output: filepath.Join(t.TempDir(), "full.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "restore")
	locked := filepath.Join(out, "docs")
	if err = os.MkdirAll(locked, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(locked, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0700) })
	o := RestoreOptions{ID: v.ID, Output: out, Password: func(model.Snapshot) ([]byte, error) { return bytes.Clone(pw), nil }}

	if err = s.Restore(ctx, o); err == nil || !strings.Contains(err.Error(), "restore publication incomplete") {
		t.Fatalf("merge into a read-only directory: got %v", err)
	}
	stages, _ := filepath.Glob(filepath.Join(out, ".glesha-restore-*"))
	if len(stages) != 1 {
		t.Fatalf("interrupted merge kept %d stages, want 1", len(stages))
	}

	if err = os.Chmod(locked, 0700); err != nil {
		t.Fatal(err)
	}
	if err = s.Restore(ctx, o); err != nil {
		t.Fatal("resumed restore:", err)
	}
	if b, err := os.ReadFile(filepath.Join(locked, "added")); err != nil || string(b) != "new" {
		t.Fatalf("resumed restore content %q: %v", b, err)
	}
	if stages, _ = filepath.Glob(filepath.Join(out, ".glesha-restore-*")); len(stages) != 0 {
		t.Fatal("finished restore left its stage behind")
	}
}
