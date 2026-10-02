package catalog_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("catalog") }
func PrintUsage()   { fmt.Print(Usage()) }
