//go:build linux || darwin

package file_io

import (
	"time"

	"golang.org/x/sys/unix"
)

func SetLinkTimes(p string, t time.Time) error {
	return unix.UtimesNanoAt(unix.AT_FDCWD, p, []unix.Timespec{unix.NsecToTimespec(t.UnixNano()), unix.NsecToTimespec(t.UnixNano())}, unix.AT_SYMLINK_NOFOLLOW)
}
