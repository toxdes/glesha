package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	AWSAccessKey  string `json:"aws_access_key"`
	AWSSecretKey  string `json:"aws_secret_key"`
	AWSRegion     string `json:"aws_region"`
	AWSBucketName string `json:"aws_bucket_name"`
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
		return fmt.Errorf("malformed config: %s", configPath)
	}
	return nil
}

func Get() Config {
	return config
}
