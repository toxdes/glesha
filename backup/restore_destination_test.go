package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"glesha/database/model"
)

func TestRestoreIntoExistingDirectory(t *testing.T) {
	ctx := context.Background()
	s, _, source := setupService(t)
	password := []byte("archive password")
	if err := os.WriteFile(filepath.Join(source, "song.txt"), []byte("music"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(source, "song.txt"), filepath.Join(source, "hard.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("song.txt", filepath.Join(source, "link.txt")); err != nil {
		t.Fatal(err)
	}
	v, err := s.Run(ctx, RunOptions{Password: password, Compression: "gzip", Output: filepath.Join(t.TempDir(), "full.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		conflict bool
		symlink  bool
	}{
		{name: "merge"},
		{name: "existing-file", conflict: true},
		{name: "symlink-parent", conflict: true, symlink: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := t.TempDir()
			dir := filepath.Join(output, "docs")
			if test.symlink {
				if err := os.Symlink(t.TempDir(), dir); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(dir, 0750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "unrelated"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				if test.conflict {
					if err := os.WriteFile(filepath.Join(dir, "song.txt"), []byte("original"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			asked := false
			err := s.Restore(ctx, RestoreOptions{ID: v.ID, Output: output, Password: func(model.Snapshot) ([]byte, error) {
				asked = true
				return append([]byte(nil), password...), nil
			}})
			if test.conflict {
				if err == nil || asked {
					t.Fatal("conflict was not rejected before decryption", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if b, err := os.ReadFile(filepath.Join(dir, "unrelated")); err != nil || string(b) != "keep" {
				t.Fatal("unrelated file modified", err)
			}
			if b, err := os.ReadFile(filepath.Join(dir, "song.txt")); err != nil || string(b) != "music" {
				t.Fatal("restored content differs", err)
			}
			a, err := os.Stat(filepath.Join(dir, "song.txt"))
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.Stat(filepath.Join(dir, "hard.txt"))
			if err != nil || !os.SameFile(a, b) {
				t.Fatal("hardlink identity lost", err)
			}
			if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0750 {
				t.Fatal("existing directory permissions changed", err)
			}
		})
	}
}
