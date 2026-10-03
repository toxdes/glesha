package import_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("import") }
func PrintUsage()   { fmt.Print(Usage()) }
