package file_io

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const directoryMergeVersion = 1

type directoryMerge struct {
	Version          int              `json:"version"`
	Source           string           `json:"source"`
	Destination      string           `json:"destination"`
	Directories      []mergeDirectory `json:"directories"`
	PendingDirectory string           `json:"pending_directory,omitempty"`
}

type mergeDirectory struct {
	Path     string `json:"path"`
	Identity string `json:"identity"`
}

// CheckDestinationEntry rejects symlink ancestors and existing non-directory entries.
func CheckDestinationEntry(root *os.Root, name string, directory bool, source *os.Root) error {
	if !fs.ValidPath(name) || name == "." {
		return fmt.Errorf("file_io: unsafe restore path %q", name)
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		current := strings.Join(parts[:i+1], "/")
		info, err := root.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		last := i == len(parts)-1
		if info.IsDir() && (!last || directory) {
			continue
		}
		if last && source != nil {
			original, err := source.Lstat(name)
			if err == nil && os.SameFile(info, original) {
				return nil
			}
		}
		return fmt.Errorf("file_io: restore would overwrite existing path %q", current)
	}
	return nil
}

func saveDirectoryMerge(file string, value directoryMerge) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return WriteAtomic(context.Background(), file, b)
}

// MergeDir publishes verified staging inside dest without replacing existing files.
func MergeDir(ctx context.Context, source, dest, journal string) error {
	rel, err := filepath.Rel(dest, source)
	if err != nil || !filepath.IsLocal(rel) || filepath.Dir(rel) != "." || rel == "." {
		return fmt.Errorf("file_io: restore staging must be a child of the destination")
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	staged, err := root.OpenRoot(rel)
	if err != nil {
		return err
	}
	defer staged.Close()
	state := directoryMerge{Version: directoryMergeVersion, Source: source, Destination: dest}
	if b, readErr := os.ReadFile(journal); readErr == nil {
		if err = json.Unmarshal(b, &state); err != nil {
			return err
		}
		if state.Version != directoryMergeVersion || state.Source != source || state.Destination != dest {
			return fmt.Errorf("file_io: unsupported or conflicting restore publication journal")
		}
	} else if !errors.Is(readErr, fs.ErrNotExist) {
		return readErr
	}
	if err = saveDirectoryMerge(journal, state); err != nil {
		return err
	}
	if state.PendingDirectory != "" {
		if _, err := root.Lstat(state.PendingDirectory); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("file_io: interrupted creation of %q cannot establish ownership; inspect preserved staging", state.PendingDirectory)
		}
		state.PendingDirectory = ""
		if err = saveDirectoryMerge(journal, state); err != nil {
			return err
		}
	}
	err = fs.WalkDir(staged.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if err := CheckDestinationEntry(root, name, entry.IsDir(), staged); err != nil {
			return err
		}
		if entry.IsDir() {
			if _, err := root.Lstat(name); err == nil {
				return nil
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			state.PendingDirectory = name
			if err := saveDirectoryMerge(journal, state); err != nil {
				return err
			}
			if err := root.Mkdir(name, 0700); err != nil {
				return err
			}
			identity, err := fileIdentity(root, name)
			if err != nil {
				return err
			}
			state.Directories = append(state.Directories, mergeDirectory{Path: name, Identity: identity})
			state.PendingDirectory = ""
			return saveDirectoryMerge(journal, state)
		}
		if info, err := root.Lstat(name); err == nil {
			original, err := staged.Lstat(name)
			if err == nil && os.SameFile(info, original) {
				return nil
			}
			return fmt.Errorf("file_io: restore destination changed at %q", name)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return root.Link(path.Join(filepath.ToSlash(rel), name), name)
	})
	if err != nil {
		return err
	}
	for i := len(state.Directories) - 1; i >= 0; i-- {
		name := state.Directories[i].Path
		if !fs.ValidPath(name) || name == "." {
			return fmt.Errorf("file_io: invalid directory in restore journal")
		}
		if err = CheckDestinationEntry(root, name, true, nil); err != nil {
			return err
		}
		info, err := staged.Lstat(name)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		if err = restoreDirectoryMetadata(root, name, state.Directories[i].Identity, info, header); err != nil {
			return err
		}
	}
	return os.Remove(journal)
}

func restoreDirectoryMetadata(root *os.Root, name, expected string, info fs.FileInfo, header *tar.Header) error {
	directory, err := root.OpenRoot(name)
	if err != nil {
		return err
	}
	defer directory.Close()
	identity, err := fileIdentity(directory, ".")
	if err != nil {
		return err
	}
	if identity != expected {
		return fmt.Errorf("file_io: restore directory changed at %q", name)
	}
	if err = SetOwner(directory, ".", header.Uid, header.Gid, false); err != nil {
		return err
	}
	if err = directory.Chmod(".", info.Mode()); err != nil {
		return err
	}
	return directory.Chtimes(".", info.ModTime(), info.ModTime())
}
