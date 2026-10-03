package browse_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("browse") }
func PrintUsage()   { fmt.Print(Usage()) }
