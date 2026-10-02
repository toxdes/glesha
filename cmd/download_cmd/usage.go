package download_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("download") }
func PrintUsage()   { fmt.Print(Usage()) }
