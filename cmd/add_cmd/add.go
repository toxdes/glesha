package add_cmd

import (
	"context"
	"flag"
	"fmt"
	"glesha/config"
	"glesha/database"
	"glesha/file_io"
	L "glesha/logger"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type AddCmdEnv struct {
	InputPath     string
	OutputPath    string
	ConfigPath    string
	ConfigDir     string
	ContentHash   string
	Verbose       bool
	AssumeYes     bool
	Provider      config.Provider
	ArchiveFormat config.ArchiveFormat
	DB            *database.DB
	IgnoredDirs   map[string]bool
	FilesInfo     *file_io.FilesInfo
}

var addCmdEnv *AddCmdEnv

var ErrNoExistingTask error

func Execute(ctx context.Context, args []string) error {
	// parse cli args
	err := parseFlags(args)
	if err != nil {
		return err
	}

	// initialize db connection
	dbPath, err := database.GetDBFilePath()
	if err != nil {
		return err
	}
	db, err := database.NewDB(dbPath, ctx)
	if err != nil {
		return err
	}
	addCmdEnv.DB = db
	err = addCmdEnv.DB.Init()
	if err != nil {
		return err
	}
	defer addCmdEnv.DB.Close()

	// compute content hash
	filesInfo, err := file_io.ComputeFilesInfo(ctx, addCmdEnv.InputPath, addCmdEnv.IgnoredDirs)
	if err != nil {
		return err
	}
	addCmdEnv.FilesInfo = filesInfo
	return queueTask(ctx)
}

func queueTask(ctx context.Context) error {
	task, err := addCmdEnv.DB.FindSimilarTask(
		ctx,
		addCmdEnv.InputPath,
		addCmdEnv.Provider,
		addCmdEnv.FilesInfo,
		addCmdEnv.ArchiveFormat,
	)
	if err != nil && err != database.ErrNoExistingTask {
		return err
	}
	var taskId int64
	if task != nil {
		taskId = task.ID
	}

	if err == database.ErrNoExistingTask {
		taskId, err = addCmdEnv.DB.CreateTask(ctx,
			addCmdEnv.InputPath,
			addCmdEnv.OutputPath,
			addCmdEnv.ConfigPath,
			addCmdEnv.ArchiveFormat,
			addCmdEnv.Provider,
			time.Now(),
			time.Now(),
			addCmdEnv.FilesInfo,
		)
		if err != nil {
			return err
		}
		L.Printf("Task created with id: %d\n", taskId)
	} else {
		L.Printf("Similar Task already exist with id: %d\n", taskId)
	}
	L.Printf("Use 'glesha run <id>' to run the task.\n")
	L.Printf("For more information, see 'glesha help add'.\n")
	return err
}

func parseFlags(args []string) error {

	globalWorkDir, err := file_io.GetGlobalWorkDir()
	if err != nil {
		return err
	}
	defaultOutputPath := globalWorkDir

	addCmd := flag.NewFlagSet("add", flag.ExitOnError)
	outputPath := addCmd.String("output", defaultOutputPath, "Path to file or directory to archive (required)")
	configPath := addCmd.String("config", "", "Path to file or directory to archive (required)")
	provider := addCmd.String("provider", "", "Which provider to use for uploading")
	archiveFormat := addCmd.String("archive-format", "", "Which archive format to use for archiving")
	logLevel := addCmd.String("log-level", L.GetLogLevel().String(), "Set log level: debug info warn error panic")

	var assumeYes bool

	addCmd.StringVar(outputPath, "o", defaultOutputPath, "alias to -output")
	addCmd.StringVar(configPath, "c", "", "alias to -config")
	addCmd.StringVar(provider, "p", "", "alias to -provider")
	addCmd.StringVar(archiveFormat, "a", "", "alias to -archive-format")
	addCmd.StringVar(logLevel, "L", L.GetLogLevel().String(), "Set log level: debug info warn error panic")
	addCmd.BoolVar(&assumeYes, "assume-yes", false, "Assume yes to all yes/no prompts")
	addCmd.Usage = func() {
		PrintUsage()
	}

	err = addCmd.Parse(args)
	nArgs := len(addCmd.Args())
	if nArgs < 1 {
		return fmt.Errorf("PATH not provided. For more information check 'glesha help add'")
	}

	if nArgs > 1 {
		return fmt.Errorf("Too many arguments. For more information check 'glesha help add'")
	}

	inputPathArg := addCmd.Arg(0)

	if logLevel != nil {
		err = L.SetLevelFromString(*logLevel)
		if err != nil {
			return err
		}
	}

	if len(inputPathArg) == 0 {
		return fmt.Errorf("PATH is not provided")
	}
	inputPath := &inputPathArg
	inputPathAbs, err := filepath.Abs(inputPathArg)
	if err != nil {
		return err
	}

	if outputPath == nil || len(*outputPath) == 0 {
		return fmt.Errorf("output path is not available")
	}

	outputPathAbs, err := filepath.Abs(*outputPath)
	if err != nil {
		return err
	}
	outputPath = &outputPathAbs

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

	if configPath != nil && *configPath != "" {
		if !file_io.IsReadable(*configPath) {
			return fmt.Errorf("Config is not readable: %s", *configPath)
		}
	} else {
		defaultConfigPath, err := config.GetDefaultConfigPath()
		if err != nil {
			return err
		}
		configPath = &defaultConfigPath
	}
	configPathAbs, err := filepath.Abs(*configPath)
	if err != nil {
		return err
	}
	err = config.Parse(configPathAbs)

	if err != nil {
		return err
	}

	configs := config.Get()

	// override config with cli flags
	if len(*archiveFormat) > 0 {
		switch config.ArchiveFormat(*archiveFormat) {
		case config.AF_TARGZ:
			L.Debug(fmt.Sprintf("Overriding archive format: %v -> %v", configs.ArchiveFormat, config.AF_TARGZ))
			configs.ArchiveFormat = config.AF_TARGZ
		default:
			return fmt.Errorf("invalid archive format: %s", *archiveFormat)
		}
	}

	if provider != nil && len(*provider) > 0 {
		parsedProvider, err := config.ParseProvider(*provider)
		if err != nil {
			return err
		}
		L.Debug(fmt.Sprintf("Overriding provider: %v -> %v", configs.Provider, parsedProvider))
		configs.Provider = parsedProvider

	}

	configDir, err := config.GetDefaultConfigDir()

	if err != nil {
		return err
	}

	addCmdEnv = &AddCmdEnv{
		InputPath:     inputPathAbs,
		OutputPath:    outputPathAbs,
		ConfigPath:    configPathAbs,
		ConfigDir:     configDir,
		AssumeYes:     assumeYes,
		ArchiveFormat: configs.ArchiveFormat,
		Provider:      configs.Provider,
		ContentHash:   "",
		DB:            nil,
	}
	return nil
}
