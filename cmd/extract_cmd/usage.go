package extract_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("extract") }
func PrintUsage()   { fmt.Print(Usage()) }
