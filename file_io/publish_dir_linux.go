package file_io

import "golang.org/x/sys/unix"

func PublishDir(temp, dest string) error {
	return unix.Renameat2(unix.AT_FDCWD, temp, unix.AT_FDCWD, dest, unix.RENAME_NOREPLACE)
}
