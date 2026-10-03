package cloud

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/smithy-go"
)

type checkStore struct {
	data                                    []byte
	object                                  Object
	retention, writeErr, readErr, deleteErr error
	corrupt, exists, deleted                bool
	deletedVersion                          string
}

func (s *checkStore) CheckTemporaryWrites(context.Context) error { return s.retention }
func (s *checkStore) Head(context.Context, string, string) (Object, error) {
	if !s.exists {
		return Object{}, &smithy.GenericAPIError{Code: "NoSuchKey"}
	}
	return s.object, nil
}
func (s *checkStore) Put(ctx context.Context, key, file, class string) (Object, error) {
	if s.writeErr != nil {
		return Object{}, s.writeErr
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return Object{}, err
	}
	s.data = data
	s.exists = true
	s.object = Object{Key: key, Version: "owned-version", ETag: "etag", Size: int64(len(data))}
	return s.object, nil
}
func (s *checkStore) Get(context.Context, string, string) (io.ReadCloser, Object, error) {
	if s.readErr != nil {
		return nil, Object{}, s.readErr
	}
	data := bytes.Clone(s.data)
	if s.corrupt {
		data[0] ^= 1
	}
	return io.NopCloser(bytes.NewReader(data)), s.object, nil
}
func (s *checkStore) DeleteVersion(ctx context.Context, key, version string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.deletedVersion = version
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deleted = true
	return nil
}
func TestCheckAccessReportsEvidenceAndAlwaysAttemptsOwnedCleanup(t *testing.T) {
	denied := errors.New("permission denied")
	for _, test := range []struct {
		name                 string
		store                checkStore
		wantErr              bool
		write, read, cleanup CheckStatus
	}{
		{"success", checkStore{}, false, CHECK_PASSED, CHECK_PASSED, CHECK_PASSED},
		{"write denied", checkStore{writeErr: denied}, true, CHECK_FAILED, CHECK_SKIPPED, CHECK_SKIPPED},
		{"read denied", checkStore{readErr: denied}, true, CHECK_PASSED, CHECK_FAILED, CHECK_PASSED},
		{"corrupt read", checkStore{corrupt: true}, true, CHECK_PASSED, CHECK_FAILED, CHECK_PASSED},
		{"cleanup denied", checkStore{deleteErr: denied}, true, CHECK_PASSED, CHECK_PASSED, CHECK_FAILED},
		{"retention blocks upload", checkStore{retention: denied}, true, CHECK_SKIPPED, CHECK_SKIPPED, CHECK_SKIPPED},
		{"existing key retained", checkStore{exists: true}, true, CHECK_FAILED, CHECK_SKIPPED, CHECK_SKIPPED},
		{"canceled read still cleans up", checkStore{readErr: context.Canceled}, true, CHECK_PASSED, CHECK_FAILED, CHECK_PASSED},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "public-test-data")
			if err := os.WriteFile(file, []byte("test bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			report, err := CheckAccess(context.Background(), &test.store, "glesha/check/owned", file)
			if (err != nil) != test.wantErr {
				t.Fatal(err)
			}
			if report.Steps[1].Status != test.write || report.Steps[3].Status != test.read || report.Steps[4].Status != test.cleanup {
				t.Fatalf("unexpected report: %+v", report)
			}
			if test.cleanup == CHECK_PASSED && (!test.store.deleted || test.store.deletedVersion != "owned-version") {
				t.Fatal("cleanup did not target the owned version")
			}
			if test.cleanup == CHECK_SKIPPED && test.store.deleted {
				t.Fatal("deleted an unowned object")
			}
		})
	}
}
