package file_io

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveUnchanged(t *testing.T) {
	for _, kind := range []string{"unchanged", "modified", "replaced", "symlink", "missing"} {
		t.Run(kind, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "archive")
			if err := os.WriteFile(file, []byte("archive"), 0600); err != nil {
				t.Fatal(err)
			}
			original, err := os.Lstat(file)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "modified":
				err = os.WriteFile(file, []byte("changed contents"), 0600)
			case "replaced", "symlink":
				err = os.Rename(file, file+".original")
				if err == nil {
					if kind == "symlink" {
						err = os.Symlink(file+".original", file)
					} else {
						err = os.WriteFile(file, []byte("archive"), 0600)
					}
				}
			case "missing":
				err = os.Remove(file)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = RemoveUnchanged(file, original)
			if kind == "unchanged" || kind == "missing" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = os.Lstat(file); !os.IsNotExist(err) {
					t.Fatal("file remains", err)
				}
			} else {
				if err == nil {
					t.Fatal("changed file accepted")
				}
				if _, err = os.Lstat(file); err != nil {
					t.Fatal("changed file removed", err)
				}
			}
		})
	}
}
