package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAndTOML(t *testing.T) {
	d := Defaults()
	if d.Archive.Compression != "xz" || d.Archive.Level != 6 || d.Cold.Tier != "Bulk" {
		t.Fatal("unsafe defaults")
	}
	file := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(file, []byte("[archive]\ncompression='gzip'\ncompression_level=9\n[memory]\nmax='512MiB'\n[remotes.other]\nkind='b2'\nbucket='other'\n"), 0600)
	c, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Archive.Compression != "gzip" || c.Memory.Max != "512MiB" || c.RemotesByName()["other"].Kind != "b2" {
		t.Fatal("TOML ignored")
	}
}
func TestExplicitEnvPreservesProcessAndFile(t *testing.T) {
	t.Setenv("GLESHA_TEST_EXISTING", "process")
	t.Setenv("GLESHA_TEST_NEW", "")
	os.Unsetenv("GLESHA_TEST_NEW")
	file := filepath.Join(t.TempDir(), "credentials.txt")
	contents := []byte("GLESHA_TEST_EXISTING=file\nGLESHA_TEST_NEW='file value'\n")
	os.WriteFile(file, contents, 0600)
	if err := Env(file); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("GLESHA_TEST_EXISTING") != "process" || os.Getenv("GLESHA_TEST_NEW") != "file value" {
		t.Fatal("environment precedence wrong")
	}
	after, _ := os.ReadFile(file)
	if string(after) != string(contents) {
		t.Fatal("credential file modified")
	}
}
