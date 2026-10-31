//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package file_io

import (
	"context"
	"os"

	"golang.org/x/term"
)

func ReadPassword(ctx context.Context, input *os.File) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return term.ReadPassword(int(input.Fd()))
}
