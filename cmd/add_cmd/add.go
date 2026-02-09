package add_cmd

import (
	"context"
	"flag"
	"fmt"
	"glesha/config"
	"glesha/database"
	"glesha/database/repository"
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
	TaskRepo      repository.TaskRepository
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
	dbPath, err := database.GetDBFilePath(ctx)
	if err != nil {
		return err
	}
	db, err := database.NewDB(dbPath)
	if err != nil {
		return err
	}
	addCmdEnv.DB = db
	err = addCmdEnv.DB.Init(ctx)
	if err != nil {
		return err
	}
	defer addCmdEnv.DB.Close(ctx)

	// compute content hash
	L.Info(fmt.Sprintf("Checking if files are changed in %s", addCmdEnv.InputPath))
	filesInfo, err := file_io.ComputeFilesInfo(ctx, addCmdEnv.InputPath, addCmdEnv.IgnoredDirs)
	if err != nil {
		return err
	}
	addCmdEnv.FilesInfo = filesInfo
	addCmdEnv.TaskRepo = repository.NewTaskRepository(db)
	return queueTask(ctx, addCmdEnv.TaskRepo)
}

func queueTask(ctx context.Context, taskRepo repository.TaskRepository) error {
	task, err := taskRepo.FindSimilarTask(
		ctx,
		addCmdEnv.InputPath,
		addCmdEnv.Provider,
		addCmdEnv.FilesInfo,
		addCmdEnv.ArchiveFormat,
	)
	if err != nil && err != database.ErrDoesNotExist {
		return err
	}
	var taskId int64
	if task != nil {
		taskId = task.Id
	}

	if err == database.ErrDoesNotExist {
		taskId, err = addCmdEnv.TaskRepo.CreateTask(ctx,
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
	defaultLogLevel := L.GetLogLevel().String()
	defaultColorMode := L.GetColorMode().String()

	addCmd := flag.NewFlagSet("add", flag.ExitOnError)
	outputPath := addCmd.String("output", defaultOutputPath, "Path to file or directory to archive (required)")
	configPath := addCmd.String("config", "", "Path to file or directory to archive (required)")
	provider := addCmd.String("provider", "", "Which provider to use for uploading")
	archiveFormat := addCmd.String("archive-format", "", "Which archive format to use for archiving")
	logLevel := addCmd.String("log-level", defaultLogLevel, "Set log level: debug info warn error panic")
	colorMode := addCmd.String("color", defaultColorMode, "Set color mode: auto always never")

	var assumeYes bool
	addCmd.StringVar(outputPath, "o", defaultOutputPath, "alias to -output")
	addCmd.StringVar(configPath, "c", "", "alias to -config")
	addCmd.StringVar(provider, "p", "", "alias to -provider")
	addCmd.StringVar(archiveFormat, "a", "", "alias to -archive-format")
	addCmd.StringVar(logLevel, "L", defaultLogLevel, "Set log level: debug info warn error panic")
	addCmd.BoolVar(&assumeYes, "assume-yes", false, "Assume yes to all yes/no prompts")

	addCmd.Usage = func() {
		PrintUsage()
	}

	err = addCmd.Parse(args)

	if err != nil {
		return fmt.Errorf("could not parse args for 'add' command")
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

	nArgs := len(addCmd.Args())

	if nArgs < 1 {
		return fmt.Errorf("PATH not provided. For more information check 'glesha help add'")
	}

	if nArgs > 1 {
		return fmt.Errorf("too many arguments. For more information check 'glesha help add'")
	}

	inputPathArg := addCmd.Arg(0)

	if len(inputPathArg) == 0 {
		return fmt.Errorf("PATH is not valid")
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
			return fmt.Errorf("cannot expand ~ for inputPath: %w", err)
		}
		*inputPath = filepath.Join(homeDir, (*inputPath)[2:])
	}

	if strings.HasPrefix(*outputPath, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot expand ~ for inputPath: %w", err)
		}
		*outputPath = filepath.Join(homeDir, (*outputPath)[2:])
	}

	if strings.HasPrefix(*configPath, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot expand ~ for inputPath: %w", err)
		}
		expandedConfigPath := filepath.Join(homeDir, (*configPath)[2:])
		configPath = &expandedConfigPath
	}

	if configPath != nil && *configPath != "" {
		readable, err := file_io.IsReadable(*configPath)

		if err != nil || !readable {
			return fmt.Errorf("config is not readable: %s", *configPath)
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

	if configs.Autogenerated {
		L.Warn(config.GetAutoGenConfigWarning(configPathAbs))
	}

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
