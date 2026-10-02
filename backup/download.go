package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"glesha/cloud"
	"glesha/file_io"
	L "glesha/logger"
)

func Download(ctx context.Context, stores map[string]cloud.Store, from []string, key, version, output string) (cloud.Object, error) {
	if len(from) == 0 {
		return cloud.Object{}, fmt.Errorf("backup: --from is required")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		return cloud.Object{}, fmt.Errorf("backup: output exists")
	}
	var last error
	for _, id := range from {
		store := stores[id]
		if store == nil {
			last = fmt.Errorf("backup: remote unavailable")
			continue
		}
		r, h, err := store.Get(ctx, key, version)
		if err != nil {
			last = err
			continue
		}
		temp, err := file_io.Temp(filepath.Dir(output))
		if err != nil {
			r.Close()
			return h, err
		}
		progress := L.StartProgress(ctx, "Downloading", h.Size)
		_, err = copyContext(ctx, temp, progress.Reader(r))
		progress.Finish()
		r.Close()
		closeErr := temp.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = file_io.Publish(temp.Name(), output)
		}
		os.Remove(temp.Name())
		if err == nil {
			return h, nil
		}
		last = err
	}
	return cloud.Object{}, last
}
func Upload(ctx context.Context, stores map[string]cloud.Store, to []string, key, file, class string, force bool) error {
	if len(to) == 0 {
		return fmt.Errorf("backup: --to is required")
	}
	for _, id := range to {
		if stores[id] == nil {
			return fmt.Errorf("backup: remote unavailable")
		}
		if !force {
			if _, err := stores[id].Head(ctx, key, ""); err == nil {
				return fmt.Errorf("backup: remote key exists; use --force for replacement")
			} else if !cloud.NotFound(err) {
				return err
			}
		}
	}
	var wg sync.WaitGroup
	failures := make(chan error, len(to))
	for _, id := range to {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			var err error
			if force {
				_, err = stores[id].Put(ctx, key, file, class)
			} else {
				_, err = cloud.Create(ctx, stores[id], key, file, class)
			}
			if err != nil {
				failures <- fmt.Errorf("backup: upload to %s: %w", id, err)
			}
		}(id)
	}
	wg.Wait()
	close(failures)
	var all []error
	for err := range failures {
		all = append(all, err)
	}
	return errors.Join(all...)
}
