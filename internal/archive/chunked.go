package archive

import (
	"archive/tar"
	"context"
	"fmt"
	"io"

	"glesha/internal/fsutil"
	"glesha/internal/logger"
	"glesha/internal/memory"
)

// StreamTar writes a frozen tar stream without buffering an entire archive.
func StreamTar(ctx context.Context, o Options, dst io.Writer) error {
	if o.Sources == nil {
		o.Sources = fsutil.NewSourceReader()
	}
	if o.Timestamp.IsZero() {
		return fmt.Errorf("archive: chunked tar requires a frozen timestamp")
	}
	if o.Compression == "xz" {
		dictionary, err := xzDictionary(o.Level)
		if err != nil {
			return err
		}
		if _, err = memory.Reserve(o.Budget, 5*int64(dictionary)+12*memory.MiB); err != nil {
			return err
		}
	}
	if err := prepareHardlinks(ctx, o); err != nil {
		return err
	}
	total, err := archiveInputBytes(ctx, o)
	if err != nil {
		return err
	}
	progress := logger.StartWorkerProgress(ctx, "Archiving", total, 1)
	defer progress.Finish()
	tw := tar.NewWriter(dst)
	err = writeTar(ctx, o, Stem(o.Output), tw, progress)
	err = joinClose(err, tw.Close())
	if err != nil {
		return err
	}
	if err = validateSources(ctx, o.Sources, o.Catalog, o.Manifest.ID, o.Output); err != nil {
		return err
	}
	return validateTree(ctx, o.Sources, o.Catalog, o.Manifest.ID, o.Manifest.Roots, o.Exclusions)
}
