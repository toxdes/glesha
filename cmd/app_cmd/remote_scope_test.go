package app_cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"glesha/config"
)

func TestCommandsRespectRemoteBoundary(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	selected, selectedServer := newTestBucket(t)
	excluded, excludedServer := newTestBucket(t)
	r := portableRuntime(t, selectedServer.URL)
	r.config.Remotes = map[string]config.Provider{
		"other": {Kind: "aws", Bucket: "bucket", Region: "us-east-1", Endpoint: excludedServer.URL},
	}
	for _, name := range []string{"aws", "other"} {
		r.env = &AppCmdEnv{To: destinations{name}}
		if err := create(r, []string{name, t.TempDir()}); err != nil {
			t.Fatal(err)
		}
	}
	requests := func(bucket *testBucket) int {
		bucket.mu.Lock()
		defer bucket.mu.Unlock()
		return bucket.requests
	}
	for _, command := range []string{"catalog", "configure", "run", "move", "import"} {
		t.Run(command, func(t *testing.T) {
			r.out.(*bytes.Buffer).Reset()
			r.env = &AppCmdEnv{From: destinations{"aws"}, To: destinations{"aws"}, Key: "legacy.gpg"}
			before := requests(excluded)
			var err error
			if command == "catalog" {
				err = catalogSync(r, []string{"other"})
			} else {
				err = handlers[command](r, []string{"other"})
			}
			if !errors.Is(err, ErrRemoteExcluded) {
				t.Fatalf("expected boundary error, got %v", err)
			}
			detail := err.Error()
			if command == "catalog" {
				detail = r.out.(*bytes.Buffer).String()
			}
			if !strings.Contains(detail, "other") {
				t.Fatalf("failure should identify configured remote: %s", detail)
			}
			if got := requests(excluded); got != before {
				t.Fatalf("excluded bucket received %d requests", got-before)
			}
		})
	}
	t.Run("bare sync skips unrelated sets", func(t *testing.T) {
		r.env = &AppCmdEnv{From: destinations{"aws"}}
		before := requests(excluded)
		if err := catalogSync(r, nil); err != nil {
			t.Fatal(err)
		}
		if got := requests(excluded); got != before {
			t.Fatalf("excluded bucket received %d requests", got-before)
		}
	})
	t.Run("union permits catalog and replication", func(t *testing.T) {
		r.env = &AppCmdEnv{From: destinations{"other"}, To: destinations{"aws"}}
		before := requests(excluded)
		if err := catalogSync(r, []string{"other"}); err != nil {
			t.Fatal(err)
		}
		if requests(excluded) <= before {
			t.Fatal("selected catalog bucket was not contacted")
		}
	})
	t.Run("invalid selection fails before network", func(t *testing.T) {
		r.env = &AppCmdEnv{From: destinations{"aws", "missing"}}
		before := requests(selected) + requests(excluded)
		if err := catalogSync(r, nil); err == nil {
			t.Fatal("expected invalid selection error")
		}
		if got := requests(selected) + requests(excluded); got != before {
			t.Fatalf("invalid selection caused %d requests", got-before)
		}
	})
	t.Run("cached clients cannot bypass selection", func(t *testing.T) {
		r.env = &AppCmdEnv{}
		if err := r.openSet("other"); err != nil {
			t.Fatal(err)
		}
		ids, err := r.remoteIDs([]string{"other"})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.stores(ids); err != nil {
			t.Fatal(err)
		}
		r.env = &AppCmdEnv{From: destinations{"aws"}}
		before := requests(excluded)
		if err := r.stores(ids); !errors.Is(err, ErrRemoteExcluded) {
			t.Fatalf("expected cached client boundary error, got %v", err)
		}
		if r.service.Stores[ids[0]] != nil {
			t.Fatal("excluded client remains available to backup operations")
		}
		if got := requests(excluded); got != before {
			t.Fatalf("cached client caused %d excluded requests", got-before)
		}
	})
	t.Run("no selectors permits all providers", func(t *testing.T) {
		r.env = &AppCmdEnv{}
		before := requests(excluded)
		if err := catalogSync(r, nil); err != nil {
			t.Fatal(err)
		}
		if requests(excluded) <= before {
			t.Fatal("unrestricted discovery did not contact other bucket")
		}
	})
	t.Run("stale discovery cannot follow an excluded redirect", func(t *testing.T) {
		selected.mu.Lock()
		original := map[string][]byte{}
		for key, data := range selected.objects {
			if strings.Contains(key, "/directory/") {
				original[key] = data
			}
		}
		selected.mu.Unlock()
		r.env = &AppCmdEnv{From: destinations{"aws"}, To: destinations{"other"}}
		if err := move(r, []string{"aws"}); err != nil {
			t.Fatal(err)
		}
		selected.mu.Lock()
		for key := range selected.objects {
			if strings.Contains(key, "/directory/") {
				delete(selected.objects, key)
			}
		}
		for key, data := range original {
			selected.objects[key] = data
		}
		selected.mu.Unlock()
		fresh := portableRuntime(t, selectedServer.URL)
		fresh.config.Remotes = r.config.Remotes
		fresh.env = &AppCmdEnv{From: destinations{"aws"}}
		before := requests(excluded)
		if err := catalogSync(fresh, nil); !errors.Is(err, ErrRemoteExcluded) {
			t.Fatalf("expected excluded redirect error, got %v", err)
		}
		if got := requests(excluded); got != before {
			t.Fatalf("redirect caused %d excluded requests", got-before)
		}
	})
}
