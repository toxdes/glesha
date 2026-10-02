package archive_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("archive") }
func PrintUsage()   { fmt.Print(Usage()) }
