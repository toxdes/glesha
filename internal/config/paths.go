package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultStatePath is independent of the config file location.
func DefaultStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return statePath(runtime.GOOS, home, os.Getenv), nil
}

func statePath(platform, home string, env func(string) string) string {
	var base string
	switch platform {
	case "darwin":
		base = filepath.Join(home, "Library", "Application Support")
	case "windows":
		base = env("LOCALAPPDATA")
		if !filepath.IsAbs(base) {
			base = filepath.Join(home, "AppData", "Local")
		}
	default:
		base = env("XDG_DATA_HOME")
		if !filepath.IsAbs(base) {
			base = filepath.Join(home, ".local", "share")
		}
	}
	return filepath.Join(base, "glesha", "state")
}

func LegacyStatePath() (string, error) {
	base, err := os.UserConfigDir()
	return filepath.Join(base, "glesha", "state-v2"), err
}
