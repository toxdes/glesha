//go:build !linux && !darwin && !windows

package file_io

import (
	"fmt"
	"os"
)

func fileIdentity(_ *os.Root, name string) (string, error) {
	return "", fmt.Errorf("file_io: resumable directory publication is unsupported on this platform: %s", name)
}
