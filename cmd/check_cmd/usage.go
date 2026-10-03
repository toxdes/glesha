package check_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("check") }
func PrintUsage()   { fmt.Print(Usage()) }
