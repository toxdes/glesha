package upload_cmd

import (
	"fmt"

	"glesha/cmd/app_cmd"
)

func Usage() string { return app_cmd.CommandUsage("upload") }
func PrintUsage()   { fmt.Print(Usage()) }
