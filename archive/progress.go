package archive

import (
	"context"
	"fmt"
	"math"

	"glesha/database/model"
)

func includedEntry(ctx context.Context, o Options, v model.Entry) (bool, error) {
	changed, err := Changed(ctx, o.Catalog, o.Manifest.ID, o.Manifest.Parent, v)
	return changed || v.Archive == o.Manifest.ID, err
}

func archiveInputBytes(ctx context.Context, o Options) (int64, error) {
	var total int64
	err := o.Catalog.EachEntry(ctx, o.Manifest.ID, func(v model.Entry) error {
		if v.Type != "file" {
			return nil
		}
		included, err := includedEntry(ctx, o, v)
		if err != nil || !included {
			return err
		}
		if v.Size < 0 || v.Size > math.MaxInt64-total {
			return fmt.Errorf("archive: invalid total source size")
		}
		total += v.Size
		return nil
	})
	return total, err
}
