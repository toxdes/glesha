package list_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("list") }
func PrintUsage()   { fmt.Print(Usage()) }
