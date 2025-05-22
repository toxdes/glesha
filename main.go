package main

import (
	"context"
	"flag"
	"fmt"
	"glesha/archiver"
	"glesha/cmd"
	"glesha/config"
	L "glesha/logger"
	"os"
	"os/signal"
	"syscall"
)

func init() {
	L.SetLevel("error")
	err := cmd.Configure()
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

func handleKillSignal(signalChannel chan os.Signal, arc archiver.Archiver) {
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)
	signalChannelContext, cancel := context.WithCancel(context.Background())
	go func() {
		<-signalChannel
		cancel()
	}()
	select {
	case <-signalChannelContext.Done():
		L.Debug("Received kill signal, saving work")
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
	L.Debug(fmt.Sprintf("\tconfig::AWSAccessKey %s", configs.AWSAccessKey))
	L.Debug(fmt.Sprintf("\tconfig::AWSBucketName %s", configs.AWSBucketName))
	L.Debug(fmt.Sprintf("\tconfig::AWSRegion %s", configs.AWSRegion))
	var tgzArchive archiver.Archiver
	archiveStatusChannel := make(chan archiver.ArchiveStatus)
	tgzArchive, err := archiver.NewTarGzArchiver(args.InputPath, args.OutputPath, archiveStatusChannel)
	if err != nil {
		L.Panic(err)
	}
	go func() {
		err = tgzArchive.Plan()
		if err != nil {
			L.Panic(err)
		}
	}()

	go handleKillSignal(signalChannel, tgzArchive)
	L.Debug("After handleKilSignal")
	for archiveStatus := range tgzArchive.GetStatusChannel() {
		L.Debug(fmt.Sprintf("archiveStatus signal: %s", archiver.StatusString(archiveStatus)))
		if archiveStatus == archiver.STATUS_PLANNED {
			fmt.Println("Plan Archive: OK")
			filesInfo, err := tgzArchive.GetInfo()
			if err != nil {
				L.Panic(err)
			}
			fmt.Printf("Total: %d files (%s)\n", filesInfo.ReadableFileCount, L.HumanReadableBytes(filesInfo.SizeInBytes))
			go func() {
				err = tgzArchive.Start()
				if err != nil {
					L.Panic(err)
				}
			}()
		}
		if archiveStatus == archiver.STATUS_PAUSED {
			fmt.Println("Pause Archive: OK")
			os.Exit(0)
		}
		if archiveStatus == archiver.STATUS_COMPLETED {
			fmt.Println("Create Archive: OK")
		}
	}
}
