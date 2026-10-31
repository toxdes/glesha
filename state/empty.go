package state

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"glesha/database/repository"
	"glesha/file_io"
)

func emptyState(ctx context.Context, root string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("state: state path must be a real directory: %s", root)
	}
	entries, err := file_io.ReadDir(ctx, root)
	if err != nil {
		return false, err
	}
	if len(entries) == 0 {
		return true, nil
	}
	if len(entries) != 1 || entries[0].Name() != "registry.db" || !entries[0].Type().IsRegular() {
		return false, nil
	}
	return repository.StateRegistryEmpty(ctx, filepath.Join(root, "registry.db"))
}

func existingDestination(source, target string) error {
	return fmt.Errorf("state: destination already exists at %s; state at %s was left unchanged; use --state-dir %q or --state-dir %q to select a location, or migrate to an unused --state-dir destination; automatic merging is not supported", target, source, source, target)
}
