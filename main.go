package main

import (
	"context"
	"flag"
	"fmt"
	"glesha/archive"
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
		L.Error(err)
		flag.Usage()
		os.Exit(1)
	}
	err = config.Parse(args.ConfigPath)

	if err != nil {
		L.Error(err)
		os.Exit(1)
	}

}

func main() {
	signalChannel := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)
	fmt.Println("Flags: OK")
	args := cmd.Get()

	L.Debug(fmt.Sprintf("\tconfig: %s, input: %s, output: %s", args.ConfigPath, args.InputPath, args.OutputPath))
	fmt.Println("Config File: OK")
	configs := config.Get()
	L.Debug(fmt.Sprintf("\tconfig::AWSAccessKey %s", configs.AWSAccessKey))
	L.Debug(fmt.Sprintf("\tconfig::AWSBucketName %s", configs.AWSBucketName))
	L.Debug(fmt.Sprintf("\tconfig::AWSRegion %s", configs.AWSRegion))
	var tgzArchive archive.Archive
	tgzArchive, err := archive.NewTarGzArchive(args.InputPath, args.OutputPath)
	if err != nil {
		L.Error(err)
		os.Exit(1)
	}
	err = tgzArchive.Plan()
	if err != nil {
		L.Error(err)
		os.Exit(1)
	}
	fmt.Println("Plan Archive: OK")
	filesInfo, err := tgzArchive.GetInfo()
	if err != nil {
		L.Error(err)
		os.Exit(1)
	}
	L.Debug(fmt.Sprintf("Readable Files: %d Total Files: %d, Total Size: %d B",
		filesInfo.ReadableFileCount,
		filesInfo.TotalFileCount,
		filesInfo.SizeInBytes))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		err = tgzArchive.Start()
		if err != nil {
			L.Error(err)
		}
		done <- struct{}{}
	}()

	go func() {
		<-signalChannel
		cancel()
	}()
	select {
	case <-done:
		fmt.Println("Done!")
	case <-ctx.Done():
		L.Debug("Received kill signal, saving work")
		tgzArchive.Pause()
		L.Debug("Paused, exiting")
	}
}
