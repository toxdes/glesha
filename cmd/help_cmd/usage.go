package help_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("help") }
func PrintUsage()   { fmt.Print(Usage()) }
