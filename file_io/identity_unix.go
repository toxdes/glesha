//go:build linux || darwin

package file_io

import (
	"fmt"
	"os"
	"syscall"
)

func fileIdentity(root *os.Root, name string) (string, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("file_io: filesystem identity unavailable for %s", name)
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
