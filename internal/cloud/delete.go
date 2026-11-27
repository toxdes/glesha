package cloud

import (
	"context"
	"errors"
	"fmt"
)

// Freed counts the stored versions a deletion removed.
type Freed struct {
	Objects int
	Bytes   int64
}

func (f *Freed) Add(o Freed) {
	f.Objects += o.Objects
	f.Bytes += o.Bytes
}

var ErrVersionsUnsupported = errors.New("cloud: remote cannot list and delete object versions")

type versionStore interface {
	VersionLister
	VersionDeleter
}

// CountAll counts every stored version under prefix that keep, when not nil,
// rejects, without deleting it.
func CountAll(ctx context.Context, store Store, prefix string, keep func(Object) bool) (Freed, error) {
	found, err := listVersions(ctx, store, prefix, keep)
	var all Freed
	for _, o := range found {
		all.Add(Freed{Objects: 1, Bytes: o.Size})
	}
	return all, err
}

// DeleteAll deletes every stored version and delete marker under prefix that
// keep, when not nil, rejects. A plain delete on a versioned bucket keeps the
// data, and it stays billed.
func DeleteAll(ctx context.Context, store Store, prefix string, keep func(Object) bool) (Freed, error) {
	found, err := listVersions(ctx, store, prefix, keep)
	if err != nil {
		return Freed{}, err
	}
	versions := store.(versionStore)
	var freed Freed
	for _, o := range found {
		if err := versions.DeleteVersion(ctx, o.Key, o.Version); err != nil && !NotFound(err) {
			return freed, fmt.Errorf("cloud: delete %s (%s): %w", o.Key, o.Version, err)
		}
		freed.Add(Freed{Objects: 1, Bytes: o.Size})
	}
	return freed, nil
}

func listVersions(ctx context.Context, store Store, prefix string, keep func(Object) bool) ([]Object, error) {
	versions, ok := store.(versionStore)
	if !ok {
		return nil, ErrVersionsUnsupported
	}
	var found []Object
	if err := versions.ListVersions(ctx, prefix, func(o Object) error {
		if keep == nil || !keep(o) {
			found = append(found, o)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("cloud: list versions of %s: %w", prefix, err)
	}
	return found, nil
}
