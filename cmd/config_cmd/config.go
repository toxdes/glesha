package config_cmd

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"glesha/backend/aws"
	"glesha/config"
	"glesha/file_io"
	L "glesha/logger"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type ConfigCmdEnv struct {
	ConfigPath  string
	Subcommand  string
	Path        string
	Value       string
	Interactive bool
}

type ConfigChange struct {
	Path     string
	OldValue any
	NewValue any
	Changed  bool
}

type StringValidator func(string) error
type Uint64Validator func(uint64) error

var configCmdEnv *ConfigCmdEnv

func Execute(ctx context.Context, args []string) error {
	err := parseFlags(args)
	if err != nil {
		return err
	}

	cfg := config.Get()

	if configCmdEnv.Interactive {
		return handleInteractive(cfg)
	}

	switch configCmdEnv.Subcommand {
	case "get":
		return handleGet(cfg)
	case "set":
		return handleSet(cfg)
	default:
		PrintUsage()
		return nil
	}
}

func parseFlags(args []string) error {
	defaultLogLevel := L.GetLogLevel().String()
	defaultColorMode := L.GetColorMode().String()
	defaultConfigPath, err := config.GetDefaultConfigPath()
	if err != nil {
		return fmt.Errorf("could not get default config path: %w", err)
	}

	configCmd := flag.NewFlagSet("config", flag.ExitOnError)
	configPath := configCmd.String("file", defaultConfigPath, fmt.Sprintf("path to config.json, defaults to: %s", defaultConfigPath))
	logLevel := configCmd.String("log-level", defaultLogLevel, "Set log level: debug info warn error panic")
	colorMode := configCmd.String("color", defaultColorMode, "Set color mode: auto always never")
	interactive := configCmd.Bool("interactive", false, "Interactive mode: prompt for each config value")

	configCmd.StringVar(configPath, "F", defaultConfigPath, "alias to -file")
	configCmd.StringVar(logLevel, "L", defaultLogLevel, "Set log level: debug info warn error panic")
	configCmd.BoolVar(interactive, "i", false, "alias to -interactive")

	configCmd.Usage = func() {
		PrintUsage()
	}

	err = configCmd.Parse(args)
	if err != nil {
		return fmt.Errorf("could not parse args for 'config' command")
	}

	err = L.SetColorModeFromString(*colorMode)
	if err != nil {
		return fmt.Errorf("could not set color mode to %s: %w", *colorMode, err)
	}
	if *colorMode != defaultColorMode {
		L.Info(fmt.Sprintf("Setting color mode to: %s", strings.ToUpper(*colorMode)))
	}

	err = L.SetLevelFromString(*logLevel)
	if err != nil {
		return err
	}
	if *logLevel != defaultLogLevel {
		L.Info(fmt.Sprintf("Setting log level to: %s", strings.ToUpper(*logLevel)))
	}

	nArgs := len(configCmd.Args())
	if nArgs < 1 && !*interactive {
		return fmt.Errorf("subcommand not provided. For more information check 'glesha help config'")
	}

	subcommand := ""
	if nArgs >= 1 {
		subcommand = configCmd.Arg(0)
	}
	if !*interactive && subcommand != "get" && subcommand != "set" {
		return fmt.Errorf("invalid subcommand: %s. Use 'get' or 'set'", subcommand)
	}

	if strings.HasPrefix(*configPath, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot expand ~ for configPath: %w", err)
		}
		expandedConfigPath := filepath.Join(homeDir, (*configPath)[2:])
		configPath = &expandedConfigPath
	}

	if *configPath != "" {
		readable, err := file_io.IsReadable(*configPath)
		if err != nil || !readable {
			return fmt.Errorf("config is not readable: %s", *configPath)
		}
	}

	configPathAbs, err := filepath.Abs(*configPath)
	if err != nil {
		return err
	}

	err = config.Parse(configPathAbs)
	if err != nil {
		return err
	}

	if *interactive {
		configCmdEnv = &ConfigCmdEnv{
			ConfigPath:  configPathAbs,
			Subcommand:  subcommand,
			Interactive: true,
		}
		return nil
	}

	switch subcommand {
	case "get":
		if nArgs < 2 {
			return fmt.Errorf("path not provided for 'get' command")
		}
		if nArgs > 2 {
			return fmt.Errorf("too many arguments for 'get' command")
		}
		path := configCmd.Arg(1)
		configCmdEnv = &ConfigCmdEnv{
			ConfigPath:  configPathAbs,
			Subcommand:  subcommand,
			Path:        path,
			Interactive: false,
		}
	case "set":
		if nArgs < 3 {
			return fmt.Errorf("path and value not provided for 'set' command")
		}
		if nArgs > 3 {
			return fmt.Errorf("too many arguments for 'set' command")
		}
		path := configCmd.Arg(1)
		value := configCmd.Arg(2)
		configCmdEnv = &ConfigCmdEnv{
			ConfigPath:  configPathAbs,
			Subcommand:  subcommand,
			Path:        path,
			Value:       value,
			Interactive: false,
		}
	}

	return nil
}

func handleGet(cfg *config.Config) error {
	value, err := config.GetValueByPath(cfg, configCmdEnv.Path)
	if err != nil {
		return err
	}

	key := strings.TrimPrefix(strings.TrimPrefix(configCmdEnv.Path, "$"), ".")
	L.Printf("%s: %v\n", key, value)
	return nil
}

func handleSet(cfg *config.Config) error {
	err := config.SetValueByPath(cfg, configCmdEnv.Path, configCmdEnv.Value)
	if err != nil {
		return err
	}

	err = config.Save()
	if err != nil {
		return fmt.Errorf("could not save config: %w", err)
	}

	key := strings.TrimPrefix(strings.TrimPrefix(configCmdEnv.Path, "$"), ".")
	L.Printf("%s: set to %s\n", key, configCmdEnv.Value)
	return nil
}

func handleInteractive(cfg *config.Config) error {
	reader := bufio.NewReader(os.Stdin)
	changes := []ConfigChange{}
	validator := aws.AwsValidator{}

	L.Println("\nInteractive Config Editor")
	L.Printf("Config file: %s\n\n", configCmdEnv.ConfigPath)

	archiveFormatVal, changed := promptArchiveFormat(reader, cfg.ArchiveFormat)
	changes = append(changes, ConfigChange{Path: "$.archive_format", OldValue: cfg.ArchiveFormat, NewValue: archiveFormatVal, Changed: changed})

	providerVal, changed := promptProvider(reader, cfg.Provider)
	changes = append(changes, ConfigChange{Path: "$.provider", OldValue: cfg.Provider, NewValue: providerVal, Changed: changed})

	autogeneratedVal, changed := promptBool(reader, "autogenerated", cfg.Autogenerated)
	changes = append(changes, ConfigChange{Path: "$.autogenerated", OldValue: cfg.Autogenerated, NewValue: autogeneratedVal, Changed: changed})

	if cfg.Aws == nil {
		cfg.Aws = &config.Aws{}
	}

	accessKeyVal, changed := promptPassword(reader, "aws.access_key", cfg.Aws.AccessKey)
	changes = append(changes, ConfigChange{Path: "$.aws.access_key", OldValue: maskValue(cfg.Aws.AccessKey), NewValue: maskValue(accessKeyVal), Changed: changed})

	secretKeyVal, changed := promptPassword(reader, "aws.secret_key", cfg.Aws.SecretKey)
	changes = append(changes, ConfigChange{Path: "$.aws.secret_key", OldValue: maskValue(cfg.Aws.SecretKey), NewValue: maskValue(secretKeyVal), Changed: changed})

	accountIdVal, changed := promptUint64WithValidator(reader, "aws.account_id", cfg.Aws.AccountId, validator.ValidateAccountId)
	changes = append(changes, ConfigChange{Path: "$.aws.account_id", OldValue: cfg.Aws.AccountId, NewValue: accountIdVal, Changed: changed})

	regionVal, changed := promptStringWithValidator(reader, "aws.region", cfg.Aws.Region, validator.ValidateRegion)
	changes = append(changes, ConfigChange{Path: "$.aws.region", OldValue: cfg.Aws.Region, NewValue: regionVal, Changed: changed})

	bucketNameVal, changed := promptStringWithValidator(reader, "aws.bucket_name", cfg.Aws.BucketName, validator.ValidateBucketName)
	changes = append(changes, ConfigChange{Path: "$.aws.bucket_name", OldValue: cfg.Aws.BucketName, NewValue: bucketNameVal, Changed: changed})

	storageClassVal, changed := promptStringWithValidator(reader, "aws.storage_class", cfg.Aws.StorageClass, validator.ValidateStorageClass)
	changes = append(changes, ConfigChange{Path: "$.aws.storage_class", OldValue: cfg.Aws.StorageClass, NewValue: storageClassVal, Changed: changed})

	L.Println("\nProposed Changes:")

	hasChanges := false
	for _, change := range changes {
		if change.Changed {
			hasChanges = true
			L.Printf("%s:\n", strings.TrimPrefix(strings.TrimPrefix(change.Path, "$"), "."))
			L.Printf("  Old: %v\n", change.OldValue)
			L.Printf("  New: %v\n\n", change.NewValue)
		}
	}

	if !hasChanges {
		L.Println("No changes to make.")
		return nil
	}

	L.Printf("Config file: %s\n", configCmdEnv.ConfigPath)
	confirmed := promptConfirmation(reader, "\nDo you want to save these changes? (yes/no): ")
	if !confirmed {
		L.Println("Changes discarded.")
		return nil
	}

	cfg.ArchiveFormat = archiveFormatVal
	cfg.Provider = providerVal
	cfg.Autogenerated = autogeneratedVal
	cfg.Aws.AccessKey = accessKeyVal
	cfg.Aws.SecretKey = secretKeyVal
	cfg.Aws.AccountId = accountIdVal
	cfg.Aws.Region = regionVal
	cfg.Aws.BucketName = bucketNameVal
	cfg.Aws.StorageClass = storageClassVal

	err := config.Save()
	if err != nil {
		return fmt.Errorf("could not save config: %w", err)
	}

	L.Println("Config saved successfully.")
	return nil
}

func promptString(reader *bufio.Reader, key string, currentValue string) (string, bool) {
	L.Printf("%s [%s]: ", key, currentValue)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return currentValue, false
	}
	return input, input != currentValue
}

func promptPassword(reader *bufio.Reader, key string, currentValue string) (string, bool) {
	L.Printf("%s [%s]: ", key, maskValue(currentValue))
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return currentValue, false
	}
	return input, input != currentValue
}

func promptUint64(reader *bufio.Reader, key string, currentValue uint64) (uint64, bool) {
	L.Printf("%s [%d]: ", key, currentValue)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return currentValue, false
	}
	val, err := strconv.ParseUint(input, 10, 64)
	if err != nil {
		L.Printf("Invalid uint64 value, using current value.\n")
		return currentValue, false
	}
	return val, val != currentValue
}

func promptBool(reader *bufio.Reader, key string, currentValue bool) (bool, bool) {
	L.Printf("%s [%t]: ", key, currentValue)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return currentValue, false
	}
	val, err := strconv.ParseBool(input)
	if err != nil {
		L.Printf("Invalid boolean value, using current value.\n")
		return currentValue, false
	}
	return val, val != currentValue
}

func promptArchiveFormat(reader *bufio.Reader, currentValue config.ArchiveFormat) (config.ArchiveFormat, bool) {
	L.Printf("archive_format [%s] (targz, zip): ", currentValue)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return currentValue, false
	}
	val, err := config.ParseArchiveFormat(input)
	if err != nil {
		L.Printf("Invalid archive format, using current value.\n")
		return currentValue, false
	}
	return val, val != currentValue
}

func promptProvider(reader *bufio.Reader, currentValue config.Provider) (config.Provider, bool) {
	L.Printf("provider [%s] (aws): ", currentValue)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return currentValue, false
	}
	val, err := config.ParseProvider(input)
	if err != nil {
		L.Printf("Invalid provider, using current value.\n")
		return currentValue, false
	}
	return val, val != currentValue
}

func promptConfirmation(reader *bufio.Reader, prompt string) bool {
	L.Print(prompt)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "yes" || input == "y"
}

func promptStringWithValidator(reader *bufio.Reader, key string, currentValue string, validator StringValidator) (string, bool) {
	L.Printf("%s [%s]: ", key, currentValue)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return currentValue, false
	}
	if validator != nil {
		if err := validator(input); err != nil {
			L.Printf("%s\n", err)
			return currentValue, false
		}
	}
	return input, input != currentValue
}

func promptUint64WithValidator(reader *bufio.Reader, key string, currentValue uint64, validator Uint64Validator) (uint64, bool) {
	L.Printf("%s [%d]: ", key, currentValue)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return currentValue, false
	}
	val, err := strconv.ParseUint(input, 10, 64)
	if err != nil {
		L.Printf("Invalid uint64 value, using current value.\n")
		return currentValue, false
	}
	if validator != nil {
		if err := validator(val); err != nil {
			L.Printf("%s\n", err)
			return currentValue, false
		}
	}
	return val, val != currentValue
}

func maskValue(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****"
	}
	return value[:2] + "****" + value[len(value)-2:]
}
