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
	if err != nil {
		flag.Usage()
		os.Exit(1)
	}
	err = config.Parse(cmd.Get().ConfigPath)

	if err != nil {
		L.Error(fmt.Sprintf("%s\n", err.Error()))
		os.Exit(1)
	}

}

func main() {
	fmt.Println("Flags: OK")
	if cmd.Get().Verbose {
		L.SetLevel("debug")
	}
	L.Debug(fmt.Sprintf("\tconfig: %s, input: %s", cmd.Get().ConfigPath, cmd.Get().InputPath))
	fmt.Println("Config File: OK")

	L.Debug(fmt.Sprintf("\tconfig::AWSAccessKey %s", config.Get().AWSAccessKey))
	L.Debug(fmt.Sprintf("\tconfig::AWSBucketName %s", config.Get().AWSBucketName))
	L.Debug(fmt.Sprintf("\tconfig::AWSRegion %s", config.Get().AWSRegion))
}
