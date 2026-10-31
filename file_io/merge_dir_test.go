package file_io

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeDirectoryResumesWithoutOverwriting(t *testing.T) {
	output := t.TempDir()
	stage, err := os.MkdirTemp(output, ".glesha-stage-")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err = os.WriteFile(filepath.Join(stage, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	journal := filepath.Join(t.TempDir(), "merge.json")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = MergeDir(ctx, stage, output, journal); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel did not preserve publication", err)
	}
	if err = os.Link(filepath.Join(stage, "a"), filepath.Join(output, "a")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(output, "b"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = MergeDir(context.Background(), stage, output, journal); err == nil {
		t.Fatal("existing file overwritten")
	}
	if b, err := os.ReadFile(filepath.Join(output, "b")); err != nil || string(b) != "original" {
		t.Fatal("existing content changed", err)
	}
	if err = os.Remove(filepath.Join(output, "b")); err != nil {
		t.Fatal(err)
	}
	if err = MergeDir(context.Background(), stage, output, journal); err != nil {
		t.Fatal("resume failed", err)
	}
	if b, err := os.ReadFile(filepath.Join(output, "b")); err != nil || string(b) != "b" {
		t.Fatal("resume content differs", err)
	}
}
