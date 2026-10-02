package history_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("history") }
func PrintUsage()   { fmt.Print(Usage()) }
