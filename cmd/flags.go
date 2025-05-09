package cmd

import (
	"flag"
	"fmt"
	"os"
)

type Cmd struct {
	InputPath  string
	ConfigPath string
	Verbose    bool
}

var cmd Cmd

func Configure() error {

	var inputPath string
	var configPath string
	var verbose bool
	flag.Usage = func() {
		fmt.Fprintf(
			os.Stderr,
			`Usage:
				glesha -input INPUT -config CONFIG_PATH
			Description:
				Archives the given file or directory into a .tar.gz file, stores encrypted metadata in SQLite, and uploads to AWS Glacier Deep Archive.
			Options:
				--input
					Path to file or directory to archive (required)
				--config
					Path to config.json file (required)
				--verbose
					Print more information
			`)
	}
	flag.StringVar(&inputPath, "input", "", "Path to file or directory to archive (required)")
	flag.StringVar(&configPath, "config", "", "Path to config.json file (required)")
	flag.BoolVar(&verbose, "verbose", false, "Print more information")
	flag.Parse()

	if inputPath == "" {
		return fmt.Errorf("required arg inputPath is not provided")
	}

	if configPath == "" {
		return fmt.Errorf("required arg configPath is not provided")
	}
	cmd = Cmd{InputPath: inputPath, ConfigPath: configPath, Verbose: verbose}
	return nil
}

func Get() Cmd {
	return cmd
}
