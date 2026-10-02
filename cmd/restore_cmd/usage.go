package restore_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("restore") }
func PrintUsage()   { fmt.Print(Usage()) }
