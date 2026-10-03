package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"glesha/database/model"
	"glesha/database/repository"
)

func TestRunArchiveCleanup(t *testing.T) {
	for _, tc := range []struct {
		name     string
		keep     bool
		explicit bool
	}{
		{name: "automatic"},
		{name: "keep", keep: true},
		{name: "explicit output", explicit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			s, _, source := setupService(t)
			if err := os.WriteFile(filepath.Join(source, "file"), []byte("restore me"), 0600); err != nil {
				t.Fatal(err)
			}
			o := RunOptions{Password: []byte("password"), KeepArchive: tc.keep}
			if tc.explicit {
				o.Output = filepath.Join(t.TempDir(), "archive.gpg")
			}
			v, err := s.Run(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(v.File)
			if tc.keep || tc.explicit {
				if err != nil {
					t.Fatal("retained archive unavailable", err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatal("automatic archive retained", err)
			}
			out := filepath.Join(t.TempDir(), "restore")
			if err = s.Restore(context.Background(), RestoreOptions{ID: v.ID, Output: out, Password: func(model.Snapshot) ([]byte, error) { return o.Password, nil }}); err != nil {
				t.Fatal("restore after cleanup", err)
			}
			got, err := os.ReadFile(filepath.Join(out, "docs", "file"))
			if err != nil || string(got) != "restore me" {
				t.Fatal("restored contents", string(got), err)
			}
		})
	}
}

func TestPendingArchiveCleanupPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, failure                        string
		retain, resumeKeep, legacy, relocate bool
	}{
		{name: "upload retry", failure: "glesha/archives/"},
		{name: "publication retry", failure: "heads/"},
		{name: "keep on retry", failure: "heads/", retain: true},
		{name: "keep on repeated run", failure: "heads/", resumeKeep: true},
		{name: "legacy pending", failure: "heads/", legacy: true},
		{name: "relocated file", failure: "glesha/archives/", relocate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			ctx := context.Background()
			s, store, _ := setupService(t)
			store.fail = tc.failure
			v, err := s.Run(ctx, RunOptions{Password: []byte("password")})
			if err == nil || !s.Pending() {
				t.Fatal("expected pending run", err)
			}
			if _, err = os.Stat(v.File); err != nil {
				t.Fatal("failed run lost archive", err)
			}
			if tc.legacy {
				c, err := repository.NewCatalogRepository(ctx, s.pending())
				if err != nil {
					t.Fatal(err)
				}
				b, _ := c.GetMeta(ctx, "request")
				var fields map[string]json.RawMessage
				if err = json.Unmarshal([]byte(b), &fields); err != nil {
					t.Fatal(err)
				}
				delete(fields, "auto_cleanup")
				b2, _ := json.Marshal(fields)
				if err = c.SetMeta(ctx, "request", string(b2)); err != nil {
					t.Fatal(err)
				}
				c.Close()
			}
			if tc.retain {
				if err = s.RetainPendingArchive(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if tc.relocate {
				moved := filepath.Join(t.TempDir(), "relocated.gpg")
				if err = os.Rename(v.File, moved); err != nil {
					t.Fatal(err)
				}
				if err = s.Relocate(ctx, moved); err != nil {
					t.Fatal(err)
				}
				v.File = moved
			}
			store.fail = ""
			if tc.resumeKeep {
				_, err = s.Run(ctx, RunOptions{KeepArchive: true})
			} else {
				err = s.Retry(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(v.File)
			if tc.retain || tc.resumeKeep || tc.legacy || tc.relocate {
				if err != nil {
					t.Fatal("retry lost retained archive", err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatal("retry did not clean archive", err)
			}
			if got := store.puts[s.payloadKey(v)]; got != 1 {
				t.Fatalf("payload uploaded %d times", got)
			}
		})
	}
}

func TestInterruptedCreationPreservesCleanupPolicy(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy retained", true: "automatic cleanup"}[automatic], func(t *testing.T) {
			t.Chdir(t.TempDir())
			ctx := context.Background()
			s, _, _ := setupService(t)
			c, err := s.Catalog(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Vacuum(ctx, s.pending()); err != nil {
				t.Fatal(err)
			}
			c.Close()
			p, err := repository.NewCatalogRepository(ctx, s.pending())
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "restart.gpg")
			request, _ := json.Marshal(runRequest{To: s.Set.To, AutoCleanup: automatic, Compression: "gzip", Class: "STANDARD", Mode: "stream", Level: 6})
			for key, value := range map[string]string{"operation": "run", "request": string(request), "pending_snapshot": "interrupted"} {
				if err = p.SetMeta(ctx, key, value); err != nil {
					t.Fatal(err)
				}
			}
			if err = p.PutSnapshot(ctx, model.Snapshot{Manifest: model.Manifest{ID: "interrupted"}, File: file}); err != nil {
				t.Fatal(err)
			}
			p.Close()
			v, err := s.Run(ctx, RunOptions{Password: []byte("password")})
			if err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(v.File)
			if automatic && !os.IsNotExist(err) || !automatic && err != nil {
				t.Fatal("restart changed cleanup policy", err)
			}
		})
	}
}
