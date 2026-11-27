package cli

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"glesha/internal/backup"
)

// backedUpSet creates the set music on the fake bucket and backs it up once.
func backedUpSet(t *testing.T) (*runtime, *testBucket) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	bucket, server := newTestBucket(t)
	r := portableRuntime(t, server.URL)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "song.txt"), []byte("music"), 0600); err != nil {
		t.Fatal(err)
	}
	r.command = "create"
	r.env.To = destinations{"aws"}
	if err := create(r, []string{"music", source}); err != nil {
		t.Fatal(err)
	}
	password := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(password, []byte("archive secret"), 0600); err != nil {
		t.Fatal(err)
	}
	r.command = "run"
	r.env = &AppCmdEnv{PassphraseFile: password, Output: filepath.Join(t.TempDir(), "full.gpg")}
	if err := run(r, []string{"music"}); err != nil {
		t.Fatal(err)
	}
	return r, bucket
}

func (b *testBucket) keys(fragment string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var keys []string
	for key := range b.objects {
		if strings.Contains(key, fragment) {
			keys = append(keys, key)
		}
	}
	return keys
}

func use(r *runtime, command string, env *AppCmdEnv) {
	r.command, r.env, r.service = command, env, nil
	r.out = &bytes.Buffer{}
}

func TestTrashFreezesSetUntilUndelete(t *testing.T) {
	r, bucket := backedUpSet(t)
	use(r, "delete", &AppCmdEnv{AssumeYes: true})
	if err := deleteSets(r, []string{"music"}); err != nil {
		t.Fatal(err)
	}
	if out := r.out.(*bytes.Buffer).String(); !strings.Contains(out, "Moved 1 set to trash") || !strings.Contains(out, "Undo: glesha undelete music") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if len(bucket.keys("/tombstones/v1/")) != 1 || len(bucket.keys("glesha/archives/")) == 0 {
		t.Fatal("trash must add a marker and keep the archives")
	}
	use(r, "run", &AppCmdEnv{})
	if err := run(r, []string{"music"}); err == nil || !strings.Contains(err.Error(), "music is in trash; run glesha undelete music") {
		t.Fatalf("run on a set in trash: %v", err)
	}
	use(r, "create", &AppCmdEnv{To: destinations{"aws"}})
	if err := create(r, []string{"music", t.TempDir()}); err == nil || !strings.Contains(err.Error(), "is in trash") {
		t.Fatalf("create reused the name of a set in trash: %v", err)
	}
	use(r, "undelete", &AppCmdEnv{})
	if err := undeleteSets(r, []string{"music"}); err != nil {
		t.Fatal(err)
	}
	if len(bucket.keys("/tombstones/")) != 0 {
		t.Fatal("undelete left the trash marker")
	}
	use(r, "undelete", &AppCmdEnv{})
	if err := undeleteSets(r, []string{"music"}); err == nil || !strings.Contains(err.Error(), "music is not in trash") {
		t.Fatalf("undelete of an active set: %v", err)
	}
}

func TestDeleteNowRemovesEveryObject(t *testing.T) {
	r, bucket := backedUpSet(t)
	use(r, "delete", &AppCmdEnv{Now: true, DryRun: true})
	if err := deleteSets(r, []string{"music"}); err != nil {
		t.Fatal(err)
	}
	before := len(bucket.keys(""))
	if !strings.Contains(r.out.(*bytes.Buffer).String(), "music") || before == 0 {
		t.Fatal("dry run listed nothing")
	}
	use(r, "delete", &AppCmdEnv{Now: true, AssumeYes: true})
	if err := deleteSets(r, []string{"music"}); err != nil {
		t.Fatal(err)
	}
	if out := r.out.(*bytes.Buffer).String(); !strings.Contains(out, "Deleted 1 set; freed") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if left := bucket.keys(""); len(left) != 1 || !strings.HasSuffix(left[0], "/deleted.json") {
		t.Fatalf("objects left: %v", left)
	}
	if _, err := r.registry.Set(r.ctx, "music"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("set still registered: %v", err)
	}
}

// expireLocally moves the local deadline of set into the past, as a local
// clock that runs ahead would.
func expireLocally(t *testing.T, r *runtime, name string) {
	t.Helper()
	set, err := r.registry.Set(r.ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	set.Trash.Deadline = time.Now().Add(-time.Minute)
	if err = r.registry.Save(r.ctx, set); err != nil {
		t.Fatal(err)
	}
}

func TestDueDeletionRunsBeforeNextCommand(t *testing.T) {
	r, bucket := backedUpSet(t)
	use(r, "delete", &AppCmdEnv{AssumeYes: true})
	if err := deleteSets(r, []string{"music"}); err != nil {
		t.Fatal(err)
	}
	expireLocally(t, r, "music")
	use(r, "status", &AppCmdEnv{})
	r.finishDueDeletions()
	if len(bucket.keys("glesha/archives/")) == 0 {
		t.Fatal("deleted before the trash period ended on the bucket's clock")
	}
	expireLocally(t, r, "music")
	markers := bucket.keys("/trash.json")
	bucket.mu.Lock()
	for _, key := range markers {
		bucket.modified[key] = time.Now().Add(-25 * time.Hour)
	}
	bucket.mu.Unlock()
	r.finishDueDeletions()
	if left := bucket.keys(""); len(left) != 1 {
		t.Fatalf("objects left after the trash period: %v", left)
	}
	if r.service != nil {
		t.Fatal("finishing deletions left a service for the command")
	}
}

func TestTypedConfirmationNeedsEveryName(t *testing.T) {
	ctx := context.Background()
	in := bufio.NewReader(strings.NewReader("docs\nphotos\n"))
	for _, name := range []string{"docs", "photos"} {
		if err := readTypedConfirmation(ctx, in, &bytes.Buffer{}, true, "set name", name, false); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	in = bufio.NewReader(strings.NewReader("docs photos\n"))
	if err := readTypedConfirmation(ctx, in, &bytes.Buffer{}, true, "set name", "docs", false); !errors.Is(err, backup.ErrConfirmationRequired) {
		t.Fatalf("accepted several names in one answer: %v", err)
	}
	if err := readTypedConfirmation(ctx, in, &bytes.Buffer{}, false, "set name", "docs", false); !errors.Is(err, backup.ErrConfirmationRequired) {
		t.Fatalf("accepted no terminal: %v", err)
	}
}

func TestCatalogSyncFinishesDeletionFromAnotherDevice(t *testing.T) {
	r, bucket := backedUpSet(t)
	second := portableRuntime(t, r.config.AWS.Endpoint)
	second.command = "catalog"
	if err := catalogSync(second, nil); err != nil {
		t.Fatal(err)
	}
	use(r, "delete", &AppCmdEnv{Now: true, AssumeYes: true})
	if err := deleteSets(r, []string{"music"}); err != nil {
		t.Fatal(err)
	}
	use(second, "catalog", &AppCmdEnv{})
	if err := catalogSync(second, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := second.registry.Set(second.ctx, "music"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second device kept the deleted set: %v", err)
	}
	if left := bucket.keys(""); len(left) != 1 {
		t.Fatalf("catalog sync published the deleted set again: %v", left)
	}
}
