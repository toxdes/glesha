package state_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("state") }
func PrintUsage()   { fmt.Print(Usage()) }
