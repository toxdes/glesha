package main

import (
	"context"
	"flag"
	"fmt"
	"glesha/archive"
	"glesha/backend"
	"glesha/cmd"
	"glesha/config"
	"glesha/database"
	"glesha/file_io"
	L "glesha/logger"
	"glesha/upload"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

type GleshaEnv struct {
	cmd           *cmd.Cmd
	config        *config.Config
	db            *database.DB
	globalWorkDir string
	configPath    string
}

var gleshaEnv *GleshaEnv

func getGleshaWorkDir() (string, error) {
	homeDir, homeDirError := os.UserHomeDir()
	configDir, configDirError := os.UserConfigDir()
	if homeDirError != nil && configDirError != nil {
		return "", fmt.Errorf("Couldn't find a place for work directory")
	}
	if len(configDir) > 0 {
		return filepath.Abs(filepath.Join(configDir, "glesha"))
	}
	return filepath.Abs(filepath.Join(homeDir, "glesha"))
}

func init() {
	L.SetLevel("error")
	err := cmd.Configure()
	if err != nil {
		L.Panic(err)
	}
	args := cmd.Get()
	if args.Verbose {
		L.SetLevel("debug")
	}
	if args.Version {
		cmd.PrintVersion()
		os.Exit(0)
	}
	if err != nil {
		flag.Usage()
		L.Panic(err)
	}
	var gleshaConfigPath string
	workDir, err := getGleshaWorkDir()
	if err != nil {
		L.Panic(err)
	}
	if len(args.ConfigPath) > 0 {
		gleshaConfigPath = args.ConfigPath
	} else {
		L.Debug("Config wasn't provided, will use default config")
		configPath := config.GetDefaultConfigPath(workDir)
		if err != nil {
			L.Panic(err)
		}
		configExistsAtPath := file_io.Exists(configPath)
		if !configExistsAtPath {
			L.Info("Config doesn't exist at default path, creating one. Please edit afterwards.")
			configStr := config.DumpDefaultConfig()
			L.Debug(configStr)
			_, err = file_io.WriteToFile(configPath, []byte(configStr), file_io.WRITE_OVERWRITE)
			if err != nil {
				L.Panic(err)
			}
			L.Info(fmt.Sprintf("Config created at: %s", configPath))
		}
		gleshaConfigPath = configPath
	}
	fmt.Printf("Using config at: %s\n", gleshaConfigPath)

	err = config.Parse(gleshaConfigPath)

	if err != nil {
		L.Panic(err)
	}
	dbCtx, _ := context.WithCancel(context.Background())
	if err != nil {
		L.Panic(err)
	}
	if err != nil {
		L.Panic(err)
	}
	err = os.MkdirAll(workDir, os.ModePerm)
	if err != nil {
		L.Panic(err)
	}
	dbPath := filepath.Join(workDir, "glesha-db.db")
	db, err := database.NewDB(dbPath, dbCtx)
	if err != nil {
		L.Panic(err)
	}
	err = db.Init()
	if err != nil {
		L.Panic(err)
	}
	gleshaEnv = &GleshaEnv{
		cmd:           args,
		config:        config.Get(),
		db:            db,
		globalWorkDir: workDir,
		configPath:    gleshaConfigPath,
	}
}

func handleKillSignal(signalChannel chan os.Signal, arc archive.Archiver) {
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)
	signalChannelContext, cancel := context.WithCancel(context.Background())
	go func() {
		<-signalChannel
		cancel()
	}()
	select {
	case <-signalChannelContext.Done():
		fmt.Print("\nReceived kill signal, terminating gracefully...\n")
		err := arc.HandleKillSignal()
		if err != nil {
			L.Panic(err)
		}
	}
}

func main() {
	if gleshaEnv == nil {
		L.Panic("Couldn't setup glesha. Exiting")
	}
	fmt.Println("Flags: OK")
	args := cmd.Get()
	signalChannel := make(chan os.Signal, 1)
	defer close(signalChannel)
	L.Debug(fmt.Sprintf("\tconfig: %s, input: %s, output: %s", args.ConfigPath, args.InputPath, args.OutputPath))
	fmt.Println("Config File: OK")
	configs := config.Get()
	var archiver archive.Archiver
	var err error
	archiveStatusChannel := make(chan archive.ArchiveStatus)
	archiver, err = archive.GetArchiver(configs.ArchiveType, args.InputPath, args.OutputPath, archiveStatusChannel)
	if err != nil {
		L.Panic(err)
	}
	go func() {
		err = archiver.Plan()
		if err != nil {
			L.Panic(err)
		}
	}()

	go handleKillSignal(signalChannel, archiver)
	for archiveStatus := range archiver.GetStatusChannel() {
		L.Debug(fmt.Sprintf("archiveStatus signal: %s", archiveStatus.String()))
		if archiveStatus == archive.STATUS_PLANNED {
			fmt.Println("Plan Archive: OK")
			filesInfo, err := archiver.GetInfo()
			if err != nil {
				L.Panic(err)
			}
			fmt.Printf("Total: %d files (%s)\n", filesInfo.ReadableFileCount, L.HumanReadableBytes(filesInfo.SizeInBytes))
			go func() {
				err = archiver.Start()
				if err != nil {
					L.Panic(err)
				}
			}()
		}
		if archiveStatus == archive.STATUS_PAUSED {
			fmt.Println("Pause Archive: OK")
			os.Exit(0)
		}
		if archiveStatus == archive.STATUS_ABORTED {
			fmt.Println("Archive: ABORTED")
			os.Exit(0)
		}
		if archiveStatus == archive.STATUS_COMPLETED {
			fmt.Println("Create Archive: OK")
		}
	}

	if len(args.Providers) == 0 {
		fmt.Println("Upload Archive: SKIP")
		return
	}
	for _, p := range args.Providers {
		b, err := backend.GetBackendForProvider(p)
		if err != nil {
			L.Panic(fmt.Errorf("Cannot get backend for provider %s : %w\nMake sure your configuration is correct at: %s", p.String(), err, gleshaEnv.configPath))
			continue
		}
		uploader := upload.NewUploader(archiver.GetArchiveFilePath(), b)
		fmt.Printf("Upload (%s): Planning...\n", p.String())
		err = uploader.Plan()
		if err != nil {
			L.Panic(err)
		}
		fmt.Println("Plan Upload: OK")
		err = uploader.Start()
		if err != nil {
			L.Panic(err)
		}
	}
}
