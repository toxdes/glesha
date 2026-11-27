package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"glesha/internal/cloud"
	"glesha/internal/fsutil"
	"glesha/internal/logger"
)

// remoteError names the remote whose operation failed, by its configured name.
func (s *Service) remoteError(ctx context.Context, id string, err error) error {
	name := id
	if s.Registry != nil {
		if remote, lookupErr := s.Registry.Remote(context.WithoutCancel(ctx), id); lookupErr == nil && remote.Name != "" {
			name = remote.Name
		}
	}
	return fmt.Errorf("remote %q: %w", name, err)
}

// Download copies key from the first remote in from that serves it.
func (s *Service) Download(ctx context.Context, from []string, key, version, output string) (cloud.Object, error) {
	if len(from) == 0 {
		return cloud.Object{}, fmt.Errorf("backup: --from is required")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		return cloud.Object{}, fmt.Errorf("backup: output exists")
	}
	var last error
	for _, id := range from {
		store := s.Stores[id]
		if store == nil {
			last = s.remoteError(ctx, id, fmt.Errorf("backup: remote unavailable"))
			continue
		}
		r, h, err := store.Get(ctx, key, version)
		if err != nil {
			last = s.remoteError(ctx, id, err)
			continue
		}
		temp, err := fsutil.Temp(filepath.Dir(output))
		if err != nil {
			r.Close()
			return h, err
		}
		progress := logger.StartWorkerProgress(ctx, "Downloading", h.Size, 1)
		_, err = copyContext(ctx, temp, progress.Reader(r))
		progress.Finish()
		r.Close()
		closeErr := temp.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = fsutil.Publish(temp.Name(), output)
		}
		os.Remove(temp.Name())
		if err == nil {
			return h, nil
		}
		last = s.remoteError(ctx, id, err)
	}
	return cloud.Object{}, last
}

// Upload writes file to key on every remote in to. Without force, a key that
// already exists on any of them is an error.
func (s *Service) Upload(ctx context.Context, to []string, key, file, class string, force bool) error {
	if len(to) == 0 {
		return fmt.Errorf("backup: --to is required")
	}
	if err := s.checkClass(to, class); err != nil {
		return err
	}
	for _, id := range to {
		if s.Stores[id] == nil {
			return s.remoteError(ctx, id, fmt.Errorf("backup: remote unavailable"))
		}
		if !force {
			if _, err := s.Stores[id].Head(ctx, key, ""); err == nil {
				return s.remoteError(ctx, id, fmt.Errorf("backup: remote key exists; use --force for replacement"))
			} else if !cloud.NotFound(err) {
				return s.remoteError(ctx, id, err)
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
				_, err = s.Stores[id].Put(ctx, key, file, class)
			} else {
				_, err = cloud.Create(ctx, s.Stores[id], key, file, class)
			}
			if err != nil {
				failures <- s.remoteError(ctx, id, fmt.Errorf("backup: upload: %w", err))
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
