package run_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("run") }
func PrintUsage()   { fmt.Print(Usage()) }
