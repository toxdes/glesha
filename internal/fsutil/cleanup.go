package fsutil

import (
	"fmt"
	"os"
)

func RemoveUnchanged(path string, original os.FileInfo) error {
	current, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fsutil: inspect cleanup file: %w", err)
	}
	if original == nil || !current.Mode().IsRegular() || !original.Mode().IsRegular() || !os.SameFile(original, current) || current.Size() != original.Size() || !current.ModTime().Equal(original.ModTime()) {
		return fmt.Errorf("fsutil: cleanup file changed")
	}
	if err = os.Remove(path); err != nil {
		return fmt.Errorf("fsutil: remove completed archive: %w", err)
	}
	return nil
}
