package status_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("status") }
func PrintUsage()   { fmt.Print(Usage()) }
