package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type ArchiveType string

const (
	TarGz ArchiveType = "targz"
	Zip   ArchiveType = "zip"
)

func (archiveType *ArchiveType) String() string {
	switch *archiveType {
	case TarGz:
		return ".tar.gz"
	case Zip:
		return ".zip"
	default:
		return "Unknown"
	}
}

func (archiveType *ArchiveType) UnmarshalJSON(data []byte) error {
	var maybeType string
	err := json.Unmarshal(data, &maybeType)
	if err != nil {
		return err
	}
	t := ArchiveType(maybeType)
	switch t {
	case TarGz, Zip:
		{
			*archiveType = t
			return nil
		}
	default:
		return fmt.Errorf("unknown archive_type: %s. supported archive types: %s", maybeType, TarGz)
	}
}

type Aws struct {
	AccessKey  string `json:"access_key"`
	SecretKey  string `json:"secret_key"`
	Region     string `json:"region"`
	BucketName string `json:"bucket_name"`
}

type Config struct {
	ArchiveType ArchiveType `json:"archive_type"`
	Aws         *Aws        `json:"aws,omitempty"`
}

var config Config

func Parse(configPath string) error {
	file, err := os.Open(configPath)
	if err != nil {
		return fmt.Errorf("couldn't open open config file for reading")
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		return fmt.Errorf("malformed config %s: %w", configPath, err)
	}
	return nil
}

func Get() *Config {
	return &config
}

func GetDefaultConfigPath(gleshaWorkDir string) string {
	return filepath.Join(gleshaWorkDir, "config.json")
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
		ArchiveType: TarGz,
		Aws: &Aws{
			AccessKey:  "aws-access-key",
			SecretKey:  "aws-secret-key",
			Region:     "aws-region-name",
			BucketName: "aws-s3-bucket-name",
		},
	}
	configStr, err := defaultConfig.ToJson()
	if err != nil {
		return ""
	}
	return configStr
}
