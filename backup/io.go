package backup

import (
	"context"
	"io"
	"time"

	"glesha/file_io"
)

func now() time.Time { return time.Now().UTC() }
func newID() string  { return now().Format("20060102T150405.000000000Z") }
func copyContext(ctx context.Context, w io.Writer, r io.Reader) (int64, error) {
	return io.CopyBuffer(w, file_io.ContextReader{Ctx: ctx, Reader: r}, make([]byte, 64*1024))
}
