package version_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("version") }
func PrintUsage()   { fmt.Print(Usage()) }
