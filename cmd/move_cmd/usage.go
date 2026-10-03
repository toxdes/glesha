package move_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("move") }
func PrintUsage()   { fmt.Print(Usage()) }
