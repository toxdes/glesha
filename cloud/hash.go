package cloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"time"

	"glesha/file_io"
	L "glesha/logger"
)

const cleanupTimeout = 30 * time.Second

func fileHash(ctx context.Context, p string) (string, int64, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	return readerHash(ctx, f)
}

func readerHash(ctx context.Context, r io.Reader) (string, int64, error) {
	total := int64(-1)
	if file, ok := r.(*os.File); ok {
		info, err := file.Stat()
		if err != nil {
			return "", 0, err
		}
		total = info.Size()
	}
	progress := L.StartProgress(ctx, "Hashing archive for upload", total)
	defer progress.Finish()
	r = progress.Reader(r)
	h := sha256.New()
	n, e := io.CopyBuffer(h, file_io.ContextReader{Ctx: ctx, Reader: r}, make([]byte, 64*1024))
	return hex.EncodeToString(h.Sum(nil)), n, e
}
