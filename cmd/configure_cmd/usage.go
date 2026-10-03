package configure_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("configure") }
func PrintUsage()   { fmt.Print(Usage()) }
