package decrypt_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("decrypt") }
func PrintUsage()   { fmt.Print(Usage()) }
