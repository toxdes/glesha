//go:build aix || linux || solaris

package fsutil

import "golang.org/x/sys/unix"

const passwordReadTermios = unix.TCGETS
const passwordWriteTermios = unix.TCSETS
