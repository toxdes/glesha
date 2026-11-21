//go:build !linux && !darwin && !windows

package fsutil

import (
	"fmt"
	"os"
)

func fileIdentity(_ *os.Root, name string) (string, error) {
	return "", fmt.Errorf("file_io: resumable directory publication is unsupported on this platform: %s", name)
}
