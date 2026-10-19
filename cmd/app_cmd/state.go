package app_cmd

import (
	"fmt"
	"path/filepath"

	"glesha/config"
	"glesha/file_io"
	"glesha/state"
)

func migrateState(r *runtime, args []string) error {
	if len(args) != 1 || args[0] != "migrate" {
		return fmt.Errorf("cli: use glesha state migrate")
	}
	source, err := config.LegacyStatePath()
	if err != nil {
		return err
	}
	target := r.config.Catalog.Directory
	if target == "" {
		target, err = config.DefaultStatePath()
	} else {
		target, err = file_io.Expand(target)
	}
	if err != nil {
		return err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	if err = state.Migrate(r.ctx, source, target); err != nil {
		return err
	}
	return r.completed(target, "", fmt.Sprintf("State moved to %s", target))
}
