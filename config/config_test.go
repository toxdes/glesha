package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParse(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-config")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	t.Run("FileDoesNotExist", func(t *testing.T) {
		err := Parse(filepath.Join(tempDir, "non-existent.json"))
		assert.Error(t, err)
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		configPath := filepath.Join(tempDir, "invalid.json")
		file, err := os.Create(configPath)
		assert.NoError(t, err)
		file.WriteString("invalid json")
		file.Close()

		err = Parse(configPath)
		assert.Error(t, err)
	})

	t.Run("InvalidData", func(t *testing.T) {
		configPath := filepath.Join(tempDir, "invalid-data.json")
		file, err := os.Create(configPath)
		assert.NoError(t, err)
		file.WriteString(`{"archive_format": "invalid"}`)
		file.Close()

		err = Parse(configPath)
		assert.Error(t, err)
	})

	t.Run("ValidConfig", func(t *testing.T) {
		configPath := filepath.Join(tempDir, "valid.json")
		file, err := os.Create(configPath)
		assert.NoError(t, err)
		file.WriteString(`{
			"archive_format": "targz",
			"provider": "aws",
			"aws": {
				"access_key": "key",
				"secret_key": "secret",
				"account_id": 123456789012,
				"region": "us-east-1",
				"bucket_name": "bucket",
				"storage_class": "STANDARD"
			}
		}`)
		file.Close()

		err = Parse(configPath)
		assert.NoError(t, err)

		cfg := Get()
		assert.Equal(t, AF_TARGZ, cfg.ArchiveFormat)
		assert.Equal(t, PROVIDER_AWS, cfg.Provider)
		assert.NotNil(t, cfg.Aws)
		assert.Equal(t, "key", cfg.Aws.AccessKey)
	})
}

func TestGetDefaultConfigDir(t *testing.T) {
	tempHome, err := os.MkdirTemp("", "test-home")
	assert.NoError(t, err)
	defer os.RemoveAll(tempHome)

	t.Setenv("HOME", tempHome)

	configDir, err := GetDefaultConfigDir()
	assert.NoError(t, err)

	expectedDir := filepath.Join(tempHome, ".config", "glesha")
	assert.Equal(t, expectedDir, configDir)
	info, err := os.Stat(configDir)
	assert.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestGetDefaultConfigPath(t *testing.T) {
	tempHome, err := os.MkdirTemp("", "test-home")
	assert.NoError(t, err)
	defer os.RemoveAll(tempHome)

	t.Setenv("HOME", tempHome)

	configPath, err := GetDefaultConfigPath()
	assert.NoError(t, err)

	expectedPath := filepath.Join(tempHome, ".config", "glesha", "config.json")
	assert.Equal(t, expectedPath, configPath)
	info, err := os.Stat(configPath)
	assert.NoError(t, err)
	assert.False(t, info.IsDir())
}
