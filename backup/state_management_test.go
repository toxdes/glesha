package backup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"glesha/database/model"
)

func importManagedFixture(t *testing.T) (*Service, *memoryStore, model.Snapshot) {
	t.Helper()
	s, store, _ := setupService(t)
	s.Set.Roots = nil
	if err := s.Registry.Save(context.Background(), s.Set); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "archive", "testdata", "encb-fixture-stream-20260704120000.tar.gz.gpg"))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(s.Directory, "import-00112233445566778899aabbccddeeff.gpg")
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	object, err := store.Put(context.Background(), "legacy.gpg", file, "DEEP_ARCHIVE")
	if err != nil {
		t.Fatal(err)
	}
	remote := s.Set.To[0]
	v, err := s.Import(context.Background(), ImportOptions{File: file, Password: []byte("encb fixture password"), Remote: remote, Object: Location("", remote, object), Baseline: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, store, v
}

func TestImportPublicationFailureResumesWithoutReadingArchive(t *testing.T) {
	ctx := context.Background()
	s, store, v := importManagedFixture(t)
	store.fail = "batches/"
	if err := s.PublishImport(ctx); !errors.Is(err, ErrPending) {
		t.Fatalf("expected pending publication: %v", err)
	}
	if !s.Pending() {
		t.Fatal("publication not persisted")
	}
	moved := v.File + ".retained"
	if err := os.Rename(v.File, moved); err != nil {
		t.Fatal(err)
	}
	store.fail = ""
	if err := s.ResumeImportPublication(ctx, "legacy.gpg", ""); err != nil {
		t.Fatal(err)
	}
	if s.Pending() {
		t.Fatal("publication still pending")
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if dirty, _ := c.GetMeta(ctx, "dirty"); dirty != "" {
		t.Fatal("import not published")
	}
	if store.puts["legacy.gpg"] != 1 {
		t.Fatal("payload rewritten")
	}
}

func TestCleanupEligibilityAndRevalidation(t *testing.T) {
	for _, scenario := range []string{"published", "unpublished", "pending", "retrieval-job", "missing-remote", "changed-local", "changed-after-preview", "outside-state", "symlink", "metadata-only", "wrong-version", "legacy-receipt"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			s, store, v := importManagedFixture(t)
			if scenario != "unpublished" {
				if err := s.PublishImport(ctx); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "metadata-only", "legacy-receipt":
				object := store.objects["legacy.gpg"]
				object.Hash = ""
				store.objects["legacy.gpg"] = object
				c, err := s.Catalog(ctx)
				if err != nil {
					t.Fatal(err)
				}
				locations, err := c.Locations(ctx, v.ID)
				if err != nil {
					t.Fatal(err)
				}
				locations[0].Verification = "metadata"
				if err = c.PutLocation(ctx, locations[0]); err != nil {
					t.Fatal(err)
				}
				c.Close()
				if scenario == "legacy-receipt" {
					if err := s.saveJob(v.File+".json", importJob{Remote: s.Set.To[0], Object: object, Hash: v.Hash}); err != nil {
						t.Fatal(err)
					}
				}
			case "wrong-version":
				object := store.objects["legacy.gpg"]
				object.Version = "other-version"
				store.objects["legacy.gpg"] = object
			case "pending":
				p, err := s.stageOperation(ctx, "run")
				if err != nil {
					t.Fatal(err)
				}
				p.Close()
			case "retrieval-job":
				if err := os.WriteFile(filepath.Join(s.Directory, "restore-pending.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-remote":
				delete(store.objects, "legacy.gpg")
			case "changed-local":
				if err := os.WriteFile(v.File, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "outside-state":
				outside := filepath.Join(t.TempDir(), "user.gpg")
				data, _ := os.ReadFile(v.File)
				os.WriteFile(outside, data, 0600)
				os.Remove(v.File)
				v.File = outside
				c, _ := s.Catalog(ctx)
				c.PutSnapshot(ctx, v)
				c.Close()
			case "symlink":
				target := v.File + ".original"
				os.Rename(v.File, target)
				os.Symlink(target, v.File)
			}
			plan, err := s.PlanCleanup(ctx)
			switch scenario {
			case "unpublished", "pending", "retrieval-job":
				if err != nil || plan.Skipped == "" || len(plan.Files) != 0 {
					t.Fatalf("unsafe plan: %+v %v", plan, err)
				}
			case "missing-remote", "changed-local", "symlink", "metadata-only", "wrong-version":
				if err == nil {
					t.Fatal("unsafe cleanup accepted")
				}
			case "outside-state":
				if err != nil || len(plan.Files) != 0 {
					t.Fatalf("explicit file selected: %v", err)
				}
			default:
				if err != nil || len(plan.Files) != 1 {
					t.Fatalf("plan: %+v %v", plan, err)
				}
				if scenario == "changed-after-preview" {
					os.WriteFile(v.File, []byte("changed"), 0600)
					if err = s.RemoveCleanupFile(ctx, plan.Files[0]); err == nil {
						t.Fatal("changed file removed")
					}
					return
				}
				payload := bytes.Clone(store.files["legacy.gpg"])
				if err = s.RemoveCleanupFile(ctx, plan.Files[0]); err != nil {
					t.Fatal(err)
				}
				if _, err = os.Stat(v.File); !os.IsNotExist(err) {
					t.Fatal("local file retained")
				}
				if !bytes.Equal(payload, store.files["legacy.gpg"]) {
					t.Fatal("remote changed")
				}
				if _, err = os.Stat(s.path()); err != nil {
					t.Fatal("catalog removed")
				}
			}
		})
	}
}

func TestSyncPublishesObservationsAndNoopDoesNotPublishAgain(t *testing.T) {
	ctx := context.Background()
	s, store, v := importManagedFixture(t)
	if err := s.PublishImport(ctx); err != nil {
		t.Fatal(err)
	}
	h := store.objects["legacy.gpg"]
	h.Class = "DEEP_ARCHIVE"
	h.Cold = true
	store.objects["legacy.gpg"] = h
	if err := s.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	locations, err := c.Locations(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) != 1 || !locations[0].Cold {
		t.Fatal("observations not persisted")
	}
	revision, _ := c.GetMeta(ctx, "remote_revision")
	c.Close()
	if err = s.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	c, err = s.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	again, _ := c.GetMeta(ctx, "remote_revision")
	if again != revision {
		t.Fatal("unchanged sync published another batch")
	}
	if store.puts["retrieval:legacy.gpg"] != 0 {
		t.Fatal("sync requested retrieval")
	}
	peer := *s
	peer.Directory = filepath.Join(t.TempDir(), s.Set.ID)
	os.MkdirAll(peer.Directory, 0700)
	if err = peer.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	remote, err := peer.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	copies, err := remote.Locations(ctx, v.ID)
	if err != nil || !copies[0].Cold {
		t.Fatalf("observations not remotely published: %v", err)
	}
}

func TestSetConditions(t *testing.T) {
	for _, state := range []SetState{STATE_NEW, STATE_SYNCED, STATE_RUNNING, STATE_PAUSED, STATE_LOCAL, STATE_PUBLISHING, STATE_WAITING, STATE_STALE, STATE_CONFLICT, STATE_FAILED} {
		if got, err := ParseSetState(state.String()); err != nil || got != state {
			t.Fatal(state, err)
		}
	}
	if _, err := ParseSetState("future"); err == nil {
		t.Fatal("unknown state accepted")
	}
	ctx := context.Background()
	s, _, _ := importManagedFixture(t)
	condition, err := s.Condition(ctx)
	if err != nil || condition.State != STATE_LOCAL {
		t.Fatal(condition, err)
	}
	if err = s.RecordResult(ctx, "import", ErrPending); err != nil {
		t.Fatal(err)
	}
	condition, err = s.Condition(ctx)
	if err != nil || condition.State != STATE_PAUSED {
		t.Fatal(condition, err)
	}
	if err = s.RecordResult(ctx, "sync", ErrConflict); err != nil {
		t.Fatal(err)
	}
	condition, err = s.Condition(ctx)
	if err != nil || condition.State != STATE_CONFLICT {
		t.Fatal(condition, err)
	}
}

func TestSyncMergesIndependentImportsAndRequiresBaselineResolution(t *testing.T) {
	for _, keep := range []string{"", "local", "remote"} {
		t.Run("keep="+keep, func(t *testing.T) {
			ctx := context.Background()
			s, _, v := importManagedFixture(t)
			if err := s.PublishImport(ctx); err != nil {
				t.Fatal(err)
			}
			peer := *s
			peer.Directory = filepath.Join(t.TempDir(), s.Set.ID)
			os.MkdirAll(peer.Directory, 0700)
			if err := peer.Pull(ctx); err != nil {
				t.Fatal(err)
			}
			// Independent catalog additions do not replace the existing archive.
			add := func(service *Service, id string, dirty string) {
				c, err := service.Catalog(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				snap := v
				snap.ID = id
				snap.Parent = ""
				snap.Hash = id
				snap.File = ""
				snap.Size = 0
				if err = c.Commit(ctx, snap, "", true); err != nil {
					t.Fatal(err)
				}
				if err = c.SetMeta(ctx, "dirty", dirty); err != nil {
					t.Fatal(err)
				}
			}
			add(&peer, "local-addition", "import")
			if keep != "" {
				c, err := peer.Catalog(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err = c.SetMeta(ctx, "baseline_changed", "true"); err != nil {
					t.Fatal(err)
				}
				c.Close()
			}
			add(s, "remote-addition", "import")
			if err := s.Push(ctx); err != nil {
				t.Fatal(err)
			}
			err := peer.Sync(ctx)
			if keep == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("baseline conflict not reported: %v", err)
				}
				differences := 0
				if err = peer.ResolutionDifferences(ctx, func(d ResolutionDifference) error {
					differences++
					if d.Local == d.Remote {
						t.Fatal("spurious difference")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if differences == 0 {
					t.Fatal("conflict preview missing")
				}
				if err = peer.Resolve(ctx, keep); err != nil {
					t.Fatal(err)
				}
			}
			c, err := peer.Catalog(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, id := range []string{v.ID, "local-addition", "remote-addition"} {
				if _, err = c.Snapshot(ctx, id); err != nil {
					t.Fatal("history lost", id, err)
				}
			}
			if keep != "" {
				head, _ := c.GetMeta(ctx, "head")
				want := "remote-addition"
				if keep == "local" {
					want = "local-addition"
				}
				if head != want {
					t.Fatal("wrong resolution", head, want)
				}
			}
		})
	}
}

func TestCompletedImportWithoutIncrementalStartingPointIsSynced(t *testing.T) {
	ctx := context.Background()
	s, _, _ := importManagedFixture(t)
	if err := s.PublishImport(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.SetMeta(ctx, "head", ""); err != nil {
		t.Fatal(err)
	}
	c.Close()
	condition, err := s.Condition(ctx)
	if err != nil || condition.State != STATE_SYNCED {
		t.Fatal(condition, err)
	}
}

func TestPendingRetrievalSurvivesClearedOperationResult(t *testing.T) {
	ctx := context.Background()
	s, _, _ := importManagedFixture(t)
	if err := s.PublishImport(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Directory, "import-pending.gpg")
	if err := s.saveJob(path+".json", importJob{Requested: true}); err != nil {
		t.Fatal(err)
	}
	condition, err := s.Condition(ctx)
	if err != nil || condition.State != STATE_WAITING {
		t.Fatal(condition, err)
	}
	plan, err := s.PlanCleanup(ctx)
	if err != nil || plan.Skipped == "" {
		t.Fatal("retrieval not excluded", plan, err)
	}
}
