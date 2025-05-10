package main

import (
	"flag"
	"fmt"
	"glesha/cmd"
	"glesha/config"
	L "glesha/logger"
	"os"
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
	fmt.Println("Flags: OK")
	args := cmd.Get()

	L.Debug(fmt.Sprintf("\tconfig: %s, input: %s", args.ConfigPath, args.InputPath))
	fmt.Println("Config File: OK")
	configs := config.Get()
	L.Debug(fmt.Sprintf("\tconfig::AWSAccessKey %s", configs.AWSAccessKey))
	L.Debug(fmt.Sprintf("\tconfig::AWSBucketName %s", configs.AWSBucketName))
	L.Debug(fmt.Sprintf("\tconfig::AWSRegion %s", configs.AWSRegion))
}
