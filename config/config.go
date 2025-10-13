package config

import (
	"encoding/json"
	"fmt"
	"glesha/file_io"
	L "glesha/logger"
	"os"
	"path/filepath"
	"slices"
)

type Aws struct {
	AccessKey    string `json:"access_key"`
	SecretKey    string `json:"secret_key"`
	AccountID    uint64 `json:"account_id"`
	Region       string `json:"region"`
	BucketName   string `json:"bucket_name"`
	StorageClass string `json:"storage_class"`
}

type Config struct {
	ArchiveFormat ArchiveFormat `json:"archive_format"`
	Provider      Provider      `json:"provider"`
	Aws           *Aws          `json:"aws,omitempty"`
}

var config Config
var configPath string

func Parse(configPathArg string) error {
	file, err := os.Open(configPathArg)
	if err != nil {
		return fmt.Errorf("config: could not open open config file for reading")
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		return fmt.Errorf("config: malformed config %s: %w", configPathArg, err)
	}
	err = validate(&config)
	if err != nil {
		return fmt.Errorf("config: could not validate config: %w", err)
	}

	configPath, err = filepath.Abs(configPath)
	if err != nil {
		return err
	}
	return nil
}

func Get() *Config {
	return &config
}

func GetDefaultConfigDir() (string, error) {
	configDir, configDirError := os.UserConfigDir()
	homeDir, homeDirError := os.UserHomeDir()
	if configDirError != nil && homeDirError != nil {
		return "", fmt.Errorf("config: cannot find config dir: Config: %w, Home: %w", configDirError, homeDirError)
	}
	var dir string
	if configDirError == nil {
		dir = configDir
	} else {
		dir = homeDir
	}
	dir, err := filepath.Abs(filepath.Join(dir, "glesha"))
	if err != nil {
		return "", err
	}
	L.Debug(fmt.Sprintf("Using config directory: %s", dir))
	err = os.MkdirAll(dir, os.ModePerm)
	if err != nil {
		return "", err
	}
	return dir, nil
}

func GetDefaultConfigPath() (string, error) {
	configDir, err := GetDefaultConfigDir()
	if err != nil {
		return "", err
	}
	configFilePath := filepath.Join(configDir, "config.json")
	if !file_io.Exists(configFilePath) {
		_, err = file_io.WriteToFile(configFilePath, []byte(DumpDefaultConfig()), file_io.WRITE_OVERWRITE)
	}
	if err != nil {
		return "", err
	}
	return configFilePath, err
}

func GetConfigPath() string {
	return configPath
}

func (c *Config) ToJson() (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func DumpDefaultConfig() string {
	defaultConfig := Config{
		ArchiveFormat: AF_TARGZ,
		Provider:      PROVIDER_AWS,
		Aws: &Aws{
			AccessKey:    "aws-access-key",
			SecretKey:    "aws-secret-key",
			AccountID:    0,
			Region:       "aws-region-name",
			BucketName:   "aws-s3-bucket-name",
			StorageClass: "aws-s3-storage-class",
		},
	}
	configStr, err := defaultConfig.ToJson()
	if err != nil {
		return ""
	}
	return configStr
}

func validate(c *Config) error {
	if !slices.Contains([]ArchiveFormat{AF_TARGZ}, c.ArchiveFormat) {
		return fmt.Errorf("unknown archive format")
	}
	if !slices.Contains([]Provider{PROVIDER_AWS}, c.Provider) {
		return fmt.Errorf("unknown provider")
	}
	// NOTE: aws specific keys are validated in aws_validator.go
	return nil
}
