package storage_class_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("storage-class") }
func PrintUsage()   { fmt.Print(Usage()) }
