//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package fsutil

import "golang.org/x/sys/unix"

const passwordReadTermios = unix.TIOCGETA
const passwordWriteTermios = unix.TIOCSETA
