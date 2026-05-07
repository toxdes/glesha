package config_cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"glesha/backend/aws"
	"glesha/config"
	"glesha/file_io"
	L "glesha/logger"
)

type ConfigChange struct {
	Path     string
	OldValue any
	NewValue any
	Changed  bool
}

type StringValidator func(string) error
type Uint64Validator func(uint64) error

type ConfigCmdEnv struct {
	ConfigPath string
	Raw        bool
}

var configCmdEnv *ConfigCmdEnv

func NewConfigCmd() *cobra.Command {
	configCmdEnv = &ConfigCmdEnv{}

	defaultConfigPath, err := config.GetDefaultConfigPath()
	if err != nil {
		L.Panic(fmt.Errorf("could not get default config path: %w", err))
	}

	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage glesha configuration",
		Long:  Usage(),
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			expanded, err := file_io.ExpandTilde(configCmdEnv.ConfigPath)
			if err != nil {
				return fmt.Errorf("cannot expand ~ for configPath: %w", err)
			}
			configCmdEnv.ConfigPath = expanded

			if len(args) == 0 {
				return nil
			}

		if configCmdEnv.ConfigPath != "" {
			readable, err := file_io.IsReadable(configCmdEnv.ConfigPath)
			if err != nil {
				return fmt.Errorf("config: config %q not readable: %w", configCmdEnv.ConfigPath, err)
			}
			if !readable {
				return fmt.Errorf("config: config %q is not readable", configCmdEnv.ConfigPath)
			}
		}

		configPathAbs, err := filepath.Abs(configCmdEnv.ConfigPath)
		if err != nil {
			return fmt.Errorf("config: failed to get absolute path for %q: %w", configCmdEnv.ConfigPath, err)
		}
			configCmdEnv.ConfigPath = configPathAbs
			return config.Parse(configPathAbs)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	configCmd.PersistentFlags().StringVarP(&configCmdEnv.ConfigPath, "file", "F", defaultConfigPath,
		fmt.Sprintf("path to config.json, defaults to: %s", defaultConfigPath))
	configCmd.PersistentFlags().BoolVarP(&configCmdEnv.Raw, "raw", "r", false,
		"Print raw values without redaction")

	configCmd.AddCommand(newGetCmd())
	configCmd.AddCommand(newSetCmd())
	configCmd.AddCommand(newDumpCmd())
	configCmd.AddCommand(newInteractiveCmd())

	return configCmd
}

func newGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <path>",
		Short: "Get a configuration value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return handleGet(config.Get(), args[0], configCmdEnv.Raw)
		},
	}
}

func newSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <path> <value>",
		Short: "Set a configuration value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return handleSet(config.Get(), args[0], args[1])
		},
	}
}

func newDumpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dump",
		Short: "Print entire configuration as JSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return handleDump(config.Get(), configCmdEnv.Raw)
		},
	}
}

func newInteractiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "interactive",
		Short: "Interactive mode: prompt for each config value",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return handleInteractive(config.Get(), configCmdEnv.ConfigPath)
		},
	}
}

func handleGet(cfg *config.Config, path string, raw bool) error {
	value, err := config.GetValueByPath(cfg, path)
	if err != nil {
		return err
	}

	if !raw {
		segments, _ := config.ParsePath(path)
		level := config.GetFieldRedactLevel(segments)
		value = level.Apply(value)
	}

	key := strings.TrimPrefix(strings.TrimPrefix(path, "$"), ".")
	L.Printf("%s: %v\n", key, value)
	return nil
}

func handleSet(cfg *config.Config, path string, value string) error {
	err := config.SetValueByPath(cfg, path, value)
	if err != nil {
		return err
	}

	err = config.Save()
	if err != nil {
		return fmt.Errorf("could not save config: %w", err)
	}

	key := strings.TrimPrefix(strings.TrimPrefix(path, "$"), ".")
	L.Printf("%s: set to %s\n", key, value)
	return nil
}

func handleDump(cfg *config.Config, raw bool) error {
	var jsonStr string
	var err error
	if raw {
		jsonStr, err = cfg.ToJson()
	} else {
		jsonStr, err = config.ToRedactedJSON(cfg)
	}
	if err != nil {
		return fmt.Errorf("could not marshal config: %w", err)
	}
	L.Println(jsonStr)
	return nil
}

func handleInteractive(cfg *config.Config, configPath string) error {
	reader := bufio.NewReader(os.Stdin)
	changes := []ConfigChange{}
	validator := aws.AwsValidator{}

	L.Println("\nInteractive Config Editor")
	L.Printf("Config file: %s\n\n", configPath)

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

	L.Printf("Config file: %s\n", configPath)
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
	L.Printf("archive_format [%s] (targz): ", currentValue)
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
