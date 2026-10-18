//go:build !windows

package file_io

import (
	"fmt"

	"github.com/gofrs/flock"
)

func TryMoveLock(path string) (Unlocker, error) {
	lock := flock.New(path)
	ok, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("file_io: lock is in use: %s", path)
	}
	return lock, nil
}
