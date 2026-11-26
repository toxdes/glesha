// Package cloud defines the storage interface glesha uses and the table of
// supported providers. Implementations live in subpackages, such as s3store.
package cloud

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
)

type Object struct {
	ModifiedAt                                             *time.Time
	Key, Version, ETag, Class, Restore, Hash, Verification string
	ArchiveStatus                                          string
	ObjectLockMode, LegalHold                              string
	RetainUntil                                            *time.Time
	Size                                                   int64
	Cold                                                   bool
}
type Uploader interface {
	Put(context.Context, string, string, string) (Object, error)
}
type Reader interface {
	Get(context.Context, string, string) (io.ReadCloser, Object, error)
	Head(context.Context, string, string) (Object, error)
}
type Deleter interface {
	Delete(context.Context, string) error
}
type ClassChanger interface {
	CopyClass(context.Context, string, string, string) (Object, error)
}
type VersionRestorer interface {
	RestoreVersion(context.Context, string, string, int32, string) error
}
type Store interface {
	Uploader
	Reader
	// Provider describes the service behind the store.
	Provider() Provider
}

// Remote identifies one bucket of a provider.
type Remote struct {
	Kind, Bucket, Region, Endpoint string
}

// Stores report a missing object with ErrNotFound and a lost conditional write
// with ErrConflict, keeping the provider's own message.
var (
	ErrNotFound = errors.New("cloud: object not found")
	ErrConflict = errors.New("cloud: conditional write conflict")
)

func NotFound(e error) bool { return errors.Is(e, ErrNotFound) }
func Conflict(e error) bool { return errors.Is(e, ErrConflict) }

// DeepArchive reports whether o is in a Deep Archive tier, which has no expedited retrieval.
func (o Object) DeepArchive() bool {
	return o.Class == "DEEP_ARCHIVE" || strings.Contains(o.ArchiveStatus, "DEEP")
}

// CleanupTimeout bounds removing an object after its context was cancelled.
const CleanupTimeout = 30 * time.Second
