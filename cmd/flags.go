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

const flagUsageStr string = `
USAGE
    glesha -input INPUT -config CONFIG_PATH -output OUTPUT

DESCRIPTION
    Archives the given file or directory into a .tar.gz file, stores encrypted metadata
    in SQLite and uploads to AWS Glacier Deep Archive.

OPTIONS
    --backend [COMMA_SEPARATED_BACKENDS]                                            
        which cloud storage provider to use. Defaults to none. Currently supports: aws
    --input, -i                                                                     
        Path to file or directory to archive (required)                               
    --output, -o                                                                    
        Path to directory where archive should be generated (required)                
    --config, -c                                                                    
        Path to config.json file                                                      
    --verbose                                                                       
        Print more debug information                                                  
    --version                                                                       
        Prints version                                                                
    --assume-yes                                                                    
        Assume yes to all yes/no prompts

EXAMPLES
    1. Just archive, don't upload
        glesha -i ./dir_to_upload -c ~/.config/glesha/config.json

    2. Archive and upload to AWS
        glesha -i ./dir_to_upload -c ~/.config/glesha/config.json -backend aws
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

	inputPath := flag.String("input", "", "Path to file or directory to archive (required)")
	outputPath := flag.String("output", ".", "Path to file or directory to archive (required)")
	configPath := flag.String("config", "", "Path to file or directory to archive (required)")
	provider := flag.String("provider", "", "Which provider to use for uploading")

	var verbose bool
	var version bool
	var assumeYes bool

	flag.Usage = func() {
		fmt.Fprintf(
			os.Stderr, "%s",
			flagUsageStr)
	}
	flag.StringVar(inputPath, "i", "", "alias to -input")
	flag.StringVar(outputPath, "o", ".", "alias to -output")
	flag.StringVar(configPath, "c", "", "alias to -config")
	flag.StringVar(provider, "p", "", "alias to -provider")
	flag.BoolVar(&verbose, "verbose", false, "Print more information")
	flag.BoolVar(&version, "version", false, "Print version")
	flag.BoolVar(&assumeYes, "assume-yes", false, "Assume yes to all yes/no prompts")
	flag.Parse()

	parsedProviders, err := parseProviders(*provider)

	if !version {
		if err != nil {
			return err
		}

		if *inputPath == "" {
			return fmt.Errorf("required arg inputPath is not provided")
		}

		if *outputPath == "" {
			return fmt.Errorf("required arg outputPath is not provided")
		}

		if strings.HasPrefix(*inputPath, "~/") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("Cannot expand ~ for inputPath: %w", err)
			}
			expandedInputPath := filepath.Join(homeDir, (*inputPath)[2:])
			inputPath = &expandedInputPath
		}

		if strings.HasPrefix(*outputPath, "~/") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("Cannot expand ~ for inputPath: %w", err)
			}
			expandedOutputPath := filepath.Join(homeDir, (*outputPath)[2:])
			outputPath = &expandedOutputPath
		}

		if strings.HasPrefix(*configPath, "~/") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("Cannot expand ~ for inputPath: %w", err)
			}
			expandedConfigPath := filepath.Join(homeDir, (*configPath)[2:])
			configPath = &expandedConfigPath
		}
	}

	cmd = Cmd{
		InputPath:  *inputPath,
		OutputPath: *outputPath,
		ConfigPath: *configPath,
		Verbose:    verbose,
		Version:    version,
		AssumeYes:  assumeYes,
		Providers:  parsedProviders,
	}

	return nil
}

func Get() *Cmd {
	return &cmd
}
