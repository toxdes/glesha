package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"

	"glesha/database/repository"
	"glesha/file_io"
)

type migration struct {
	Version int      `json:"version"`
	Source  string   `json:"source"`
	Target  string   `json:"target"`
	Files   []string `json:"databases"`
}

const backupDirectory = ".state-migration-backup"

var ErrNoMigrationNeeded = errors.New("state: no legacy state needs migration")

// Lock coordinates migration with every command using the same state directory.
func Lock(root string, exclusive bool) (*flock.Flock, error) {
	if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		return nil, err
	}
	lock := flock.New(root + ".lock")
	var ok bool
	var err error
	if exclusive {
		ok, err = lock.TryLock()
	} else {
		ok, err = lock.TryRLock()
	}
	if err != nil || !ok {
		return nil, fmt.Errorf("state: directory is in use: %s (%v)", root, err)
	}
	return lock, nil
}

// CheckDefault refuses to hide older state behind a fresh registry.
func CheckDefault(ctx context.Context, source, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Lstat(target + ".migrating"); err == nil {
		return fmt.Errorf("state: interrupted migration; run glesha state migrate")
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Lstat(source); err == nil {
		empty, err := emptyState(ctx, source)
		if err != nil {
			return err
		}
		if empty {
			return nil
		}
		if _, err := os.Lstat(target); err == nil {
			return existingDestination(source, target)
		} else if !os.IsNotExist(err) {
			return err
		}
		return fmt.Errorf("state: existing state at %s; run glesha state migrate or select catalog.directory explicitly", source)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Migrate moves archives by rename; only database backups need extra disk space.
func Migrate(ctx context.Context, source, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(source) || !filepath.IsAbs(target) || filepath.Clean(source) != source || filepath.Clean(target) != target {
		return fmt.Errorf("state: migration requires clean absolute paths")
	}
	if source == target {
		return fmt.Errorf("state: source and destination are identical")
	}
	for _, pair := range [][2]string{{source, target}, {target, source}} {
		if rel, err := filepath.Rel(pair[0], pair[1]); err == nil && filepath.IsLocal(rel) {
			return fmt.Errorf("state: source and destination must not overlap")
		}
	}
	lock, err := Lock(target, true)
	if err != nil {
		return err
	}
	defer lock.Unlock()
	stage := target + ".migrating"
	if _, err = os.Lstat(stage); err == nil {
		if err = recoverMigration(ctx, stage, source, target); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	empty, err := emptyState(ctx, source)
	if err != nil {
		return err
	}
	if empty {
		return ErrNoMigrationNeeded
	}
	if _, err = os.Lstat(target); err == nil {
		return existingDestination(source, target)
	} else if !os.IsNotExist(err) {
		return err
	}
	if info, err := os.Lstat(source); err != nil {
		return fmt.Errorf("state: locate old state: %w", err)
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state: old state must be a real directory")
	}
	backup := filepath.Join(source, backupDirectory)
	if _, err = os.Lstat(backup); err == nil {
		// a pre-rename interruption cannot have changed the original databases
		if err = discardPreparation(source, target); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// older binaries only hold per-set locks
	var locks []file_io.Unlocker
	defer func() {
		for _, lock := range locks {
			lock.Unlock()
		}
	}()
	m := migration{Version: 1, Source: source, Target: target}
	err = file_io.Walk(ctx, source, func(path string, info fs.FileInfo) error {
		if path == filepath.Join(source, backupDirectory) {
			return fs.SkipDir
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("state: cannot migrate special file: %s", path)
		}
		if info.Name() == "writer.lock" {
			lock, err := file_io.TryMoveLock(path)
			if err != nil {
				return fmt.Errorf("state: stop active glesha operations before migrating")
			}
			locks = append(locks, lock)
		}
		if strings.HasSuffix(path, "-wal") || strings.HasSuffix(path, "-shm") || strings.HasSuffix(path, "-journal") {
			return fmt.Errorf("state: SQLite recovery required before migration: %s", path)
		}
		if strings.HasSuffix(path, ".db") {
			if err := repository.ValidateStateDatabase(ctx, path, path == filepath.Join(source, "registry.db")); err != nil {
				return err
			}
			rel, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			m.Files = append(m.Files, rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(source, "registry.db")); err != nil {
		return fmt.Errorf("state: no supported registry: %w", err)
	}
	if _, err = os.Lstat(backup); err == nil {
		return fmt.Errorf("state: migration backup already exists: %s", backup)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.Mkdir(backup, 0700); err != nil {
		return err
	}
	prepared := false
	defer func() {
		if !prepared {
			os.RemoveAll(backup)
		}
	}()
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	marker, err := file_io.Temp(backup)
	if err != nil {
		return err
	}
	if _, err = marker.Write(data); err == nil {
		err = marker.Sync()
	}
	closeErr := marker.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(marker.Name(), filepath.Join(backup, "migration.json")); err != nil {
		return err
	}
	for _, rel := range m.Files {
		copy := filepath.Join(backup, rel)
		if err = os.MkdirAll(filepath.Dir(copy), 0700); err != nil {
			return err
		}
		if err = file_io.CopyNew(ctx, filepath.Join(source, rel), copy); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(source, stage); err != nil {
		return fmt.Errorf("state: cannot move directory (same filesystem required); catalog.directory can retain the existing location: %w", err)
	}
	prepared = true
	for _, rel := range m.Files {
		if rel != "registry.db" {
			err = repository.RelocateStateDatabase(ctx, filepath.Join(stage, rel), source, target)
		}
		if err != nil {
			break
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = os.Rename(stage, target)
	}
	if err != nil {
		if rollbackErr := recoverMigration(context.WithoutCancel(ctx), stage, source, target); rollbackErr != nil {
			return fmt.Errorf("state: migration failed: %v; rollback failed: %w; repeat glesha state migrate", err, rollbackErr)
		}
		return fmt.Errorf("state: migration rolled back: %w", err)
	}
	return nil
}

func recoverMigration(ctx context.Context, stage, source, target string) error {
	m, err := readMigration(stage, source, target)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(source); !os.IsNotExist(err) {
		return fmt.Errorf("state: old state path exists; refusing rollback over it")
	}
	for _, rel := range m.Files {
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("state: invalid migration database path")
		}
		if err = repository.ValidateStateDatabase(ctx, filepath.Join(stage, backupDirectory, rel), rel == "registry.db"); err != nil {
			return err
		}
	}
	for _, rel := range m.Files {
		file := filepath.Join(stage, rel)
		for _, suffix := range []string{"-journal", "-wal", "-shm"} {
			if err = os.Remove(file + suffix); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		temp, err := file_io.Temp(filepath.Dir(file))
		if err != nil {
			return err
		}
		name := temp.Name()
		temp.Close()
		os.Remove(name)
		if err = file_io.CopyNew(ctx, filepath.Join(stage, backupDirectory, rel), name); err != nil {
			return err
		}
		if err = os.Rename(name, file); err != nil {
			os.Remove(name)
			return err
		}
	}
	if err = os.Rename(stage, source); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(source, backupDirectory))
}

func readMigration(root, source, target string) (migration, error) {
	var m migration
	for _, path := range []string{root, filepath.Join(root, backupDirectory)} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return m, fmt.Errorf("state: migration directory must be a real directory: %s", path)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, backupDirectory, "migration.json"))
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.Version != 1 || m.Source != source || m.Target != target {
		return m, fmt.Errorf("state: unsupported or mismatched migration; state left unchanged")
	}
	return m, nil
}

func discardPreparation(source, target string) error {
	backup := filepath.Join(source, backupDirectory)
	_, err := readMigration(source, source, target)
	if os.IsNotExist(err) {
		// before the marker is published, only its temporary file can exist
		entries, readErr := os.ReadDir(backup)
		if readErr != nil {
			return readErr
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), ".glesha-") || !entry.Type().IsRegular() {
				return fmt.Errorf("state: unrecognized migration preparation; state left unchanged")
			}
		}
	} else if err != nil {
		return err
	}
	return os.RemoveAll(backup)
}
