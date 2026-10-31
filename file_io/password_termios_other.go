//go:build aix || linux || solaris

package file_io

import "golang.org/x/sys/unix"

const passwordReadTermios = unix.TCGETS
const passwordWriteTermios = unix.TCSETS
