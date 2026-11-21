//go:build !linux && !darwin

package fsutil

import (
	"fmt"
	"os"
)

func PublishDir(temp, dest string) error {
	if _, e := os.Lstat(dest); !os.IsNotExist(e) {
		return fmt.Errorf("file_io: output exists")
	}
	return os.Rename(temp, dest)
}
