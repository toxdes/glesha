package file_io

import (
	"context"
	"io"
	"os"
)

// CopyNew never replaces a destination and removes incomplete copies.
func CopyNew(ctx context.Context, source, dest string) (err error) {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		out.Close()
		if err != nil {
			os.Remove(dest)
		}
	}()
	if _, err = io.Copy(out, ContextReader{Ctx: ctx, Reader: in}); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return out.Close()
}
