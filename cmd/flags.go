package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Provider string

func (p Provider) String() string {
	switch p {
	case ProviderAws:
		return "aws"
	default:
		return "Unknown"
	}
}

const (
	ProviderAws Provider = "aws"
)

type Cmd struct {
	InputPath  string
	OutputPath string
	ConfigPath string
	Verbose    bool
	Version    bool
	AssumeYes  bool
	Providers  []Provider
}

var cmd Cmd

const flagUsageStr string = `Usage:
	glesha -input INPUT -config CONFIG_PATH -output OUTPUT
Description:
	Archives the given file or directory into a .tar.gz file, stores encrypted metadata in SQLite and uploads to AWS Glacier Deep Archive.
Options:
  -backend [COMMA_SEPARATED_BACKENDS]
    which cloud storage provider to use. Defaults to none. Currently supports: aws
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
  -assume-yes
    Assume yes to all yes/no prompts
Examples:
  1. Just archive, don't upload
	  glesha -input ./dir_to_upload -c ~/.config/glesha/config.json
  2. Archive and upload to AWS
    glesha -input ./dir_to_upload -c ~/.config/glesha/config.json -backend aws
`

func parseProviders(providersStr string) ([]Provider, error) {
	var providers []Provider
	if providersStr == "" {
		return providers, nil
	}
	for s := range strings.SplitSeq(providersStr, ",") {
		p := Provider(strings.ToLower(s))
		switch p {
		case ProviderAws:
			{
				providers = append(providers, p)
				break
			}
		default:
			return nil, fmt.Errorf("Invalid provider: %s", s)
		}
	}
	return providers, nil
}

func Configure() error {

	var inputPath string
	var outputPath string
	var configPath string
	var verbose bool
	var version bool
	var assumeYes bool
	var provider string
	flag.Usage = func() {
		fmt.Fprintf(
			os.Stderr, "%s",
			flagUsageStr)
	}
	flag.StringVar(&inputPath, "input", "", "Path to file or directory to archive (required)")
	flag.StringVar(&outputPath, "output", ".", "Path to directory where archive should be generated")
	flag.StringVar(&configPath, "config", "", "Path to config.json file (required)")
	flag.StringVar(&provider, "provider", "", "Which provider to use for uploading")
	flag.BoolVar(&verbose, "verbose", false, "Print more information")
	flag.BoolVar(&version, "version", false, "Print version")
	flag.BoolVar(&assumeYes, "assume-yes", false, "Assume yes to all yes/no prompts")
	flag.Parse()

	parsedProviders, err := parseProviders(provider)

	if err != nil {
		return err
	}

	if inputPath == "" {
		return fmt.Errorf("required arg inputPath is not provided")
	}

	if outputPath == "" {
		return fmt.Errorf("required arg outputPath is not provided")
	}

	if configPath == "" {
		return fmt.Errorf("required arg configPath is not provided")
	}

	if strings.HasPrefix(inputPath, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("Cannot expand ~ for inputPath: %w", err)
		}
		inputPath = filepath.Join(homeDir, inputPath[2:])
	}

	if strings.HasPrefix(outputPath, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("Cannot expand ~ for inputPath: %w", err)
		}
		inputPath = filepath.Join(homeDir, outputPath[2:])
	}

	if strings.HasPrefix(configPath, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("Cannot expand ~ for inputPath: %w", err)
		}
		inputPath = filepath.Join(homeDir, configPath[2:])
	}

	cmd = Cmd{InputPath: inputPath, OutputPath: outputPath, ConfigPath: configPath, Verbose: verbose, Version: version, AssumeYes: assumeYes, Providers: parsedProviders}

	return nil
}

func Get() *Cmd {
	return &cmd
}
