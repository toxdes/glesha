package cmd

import (
	"flag"
	"fmt"
	"os"
)

type Cmd struct {
	InputPath  string
	OutputPath string
	ConfigPath string
	Verbose    bool
	Version    bool
}

var cmd Cmd

const flagUsageStr string = `Usage:
	glesha -input INPUT -config CONFIG_PATH -output OUTPUT
Description:
	Archives the given file or directory into a .tar.gz file, stores encrypted metadata in SQLite and uploads to AWS Glacier Deep Archive.
Options:
	-input
		Path to file or directory to archive (required)
	-output
		Path to directory where archive should be generated (required)
	-config
		Path to config.json file (required)
	-verbose
		Print more debug information
	-version
		Prints version
Examples:
	glesha -input ./dir_to_upload -c ~/.config/glesha/config.json 
`

func Configure() error {

	var inputPath string
	var outputPath string
	var configPath string
	var verbose bool
	var version bool
	flag.Usage = func() {
		fmt.Fprintf(
			os.Stderr, "%s",
			flagUsageStr)
	}
	flag.StringVar(&inputPath, "input", "", "Path to file or directory to archive (required)")
	flag.StringVar(&outputPath, "output", ".", "Path to directory where archive should be generated")
	flag.StringVar(&configPath, "config", "", "Path to config.json file (required)")
	flag.BoolVar(&verbose, "verbose", false, "Print more information")
	flag.BoolVar(&version, "version", false, "Print version")
	flag.Parse()

	cmd = Cmd{InputPath: inputPath, OutputPath: outputPath, ConfigPath: configPath, Verbose: verbose, Version: version}

	if inputPath == "" {
		return fmt.Errorf("required arg inputPath is not provided")
	}

	if outputPath == "" {
		return fmt.Errorf("required arg outputPath is not provided")
	}

	if configPath == "" {
		return fmt.Errorf("required arg configPath is not provided")
	}
	return nil
}

func Get() Cmd {
	return cmd
}
