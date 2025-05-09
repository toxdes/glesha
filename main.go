package main

import (
	"flag"
	"fmt"
	"glesha/cmd"
	"glesha/config"
	"os"
)

func init() {
	err := cmd.Configure()
	if err != nil {
		flag.Usage()
		os.Exit(1)
	}
	err = config.Parse(cmd.Get().ConfigPath)

	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err.Error())
		os.Exit(1)
	}

}

func main() {
	fmt.Println("Flags: OK")
	fmt.Printf("\tconfig: %s, input: %s\n", cmd.Get().ConfigPath, cmd.Get().InputPath)
	fmt.Println("Config File: OK")
	fmt.Printf("\tconfig::AWSAccessKey %s\n", config.Get().AWSAccessKey)
	fmt.Printf("\tconfig::AWSBucketName %s\n", config.Get().AWSBucketName)
	fmt.Printf("\tconfig::AWSRegion %s\n", config.Get().AWSRegion)
}
