package cmd

import (
	"flag"
	"fmt"
)

type Cmd struct {
	InputPath  string
	ConfigPath string
}

var cmd Cmd

func Configure() error {

	var inputPath string
	var configPath string

	flag.StringVar(&inputPath, "dir", "", "Path to file or directory to archive (required)")
	flag.StringVar(&configPath, "config", "", "Path to config.json file (required)")
	flag.Parse()

	if inputPath == "" {
		return fmt.Errorf("required arg inputPath is not provided")
	}

	if configPath == "" {
		return fmt.Errorf("required arg configPath is not provided")
	}
	cmd = Cmd{InputPath: inputPath, ConfigPath: configPath}
	return nil
}

func Get() Cmd {
	return cmd
}
