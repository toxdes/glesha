package retry_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("retry") }
func PrintUsage()   { fmt.Print(Usage()) }
