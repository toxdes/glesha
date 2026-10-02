//go:build !linux && !darwin && !windows

package file_io

import (
	"fmt"
	"time"
)

func SetLinkTimes(p string, t time.Time) error {
	return fmt.Errorf("file_io: symlink timestamp preservation is unsupported on this platform")
}
