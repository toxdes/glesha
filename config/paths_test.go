package config

import (
	"path/filepath"
	"testing"
)

func TestStatePath(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	for _, tc := range []struct {
		name, platform, value, want string
	}{
		{"linux default", "linux", "", filepath.Join(home, ".local", "share", "glesha", "state")},
		{"linux override", "linux", data, filepath.Join(data, "glesha", "state")},
		{"ignore relative XDG", "linux", "relative", filepath.Join(home, ".local", "share", "glesha", "state")},
		{"macOS", "darwin", data, filepath.Join(home, "Library", "Application Support", "glesha", "state")},
		{"windows", "windows", data, filepath.Join(data, "glesha", "state")},
		{"windows fallback", "windows", "", filepath.Join(home, "AppData", "Local", "glesha", "state")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := statePath(tc.platform, home, func(string) string { return tc.value }); got != tc.want {
				t.Fatalf("got %s; want %s", got, tc.want)
			}
		})
	}
}
