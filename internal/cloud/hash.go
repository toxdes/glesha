package cloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"

	"glesha/internal/fsutil"
	"glesha/internal/logger"
)

// HashFile returns the SHA-256 and size of the file at p.
func HashFile(ctx context.Context, p string) (string, int64, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	return HashReader(ctx, f)
}

// HashReader returns the SHA-256 and size of r, reporting progress.
func HashReader(ctx context.Context, r io.Reader) (string, int64, error) {
	total := int64(-1)
	if file, ok := r.(*os.File); ok {
		info, err := file.Stat()
		if err != nil {
			return "", 0, err
		}
		total = info.Size()
	}
	progress := logger.StartProgress(ctx, "Hashing "+TransferLabel(ctx)+" for upload", total)
	defer progress.Finish()
	r = progress.Reader(r)
	h := sha256.New()
	n, e := io.CopyBuffer(h, fsutil.ContextReader{Ctx: ctx, Reader: r}, make([]byte, 64*1024))
	return hex.EncodeToString(h.Sum(nil)), n, e
}
