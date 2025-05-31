package main

import (
	"context"
	"flag"
	"fmt"
	"glesha/archive"
	"glesha/backend"
	"glesha/cmd"
	"glesha/config"
	L "glesha/logger"
	"glesha/upload"
	"os"
	"os/signal"
	"syscall"
)

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
	err = config.Parse(args.ConfigPath)

	if err != nil {
		L.Panic(err)
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
		fmt.Print("Received kill signal, saving work\n")
		err := arc.HandleKillSignal()
		if err != nil {
			L.Panic(err)
		}
		os.Exit(0)
	}
}

func main() {
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
			L.Panic(fmt.Errorf("Cannot get backend for provider %s : %w", p.String(), err))
			continue
		}
		uploader := upload.NewUploader(archiver.GetArchiveFilePath(), b)
		fmt.Printf("Upload (%s): Planning...\n", p.String())
		err = uploader.Plan()
		if err != nil {
			L.Panic(err)
		}
		fmt.Println("Plan Upload: OK")
	}
}
