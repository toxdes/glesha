package help_cmd

import (
	"context"
	"fmt"
	"glesha/cmd/add_cmd"
	"glesha/cmd/run_cmd"
)

func Execute(ctx context.Context, args []string) error {
	if len(args) != 1 {
		PrintUsage()
		return nil
	}

	switch args[0] {
	case "add":
		add_cmd.PrintUsage()
	case "run":
		run_cmd.PrintUsage()
	case "help":
		PrintUsage()
	case "config":
		ConfigPrintUsage()
	default:
		return fmt.Errorf("No such command: %s", args[0])
	}
	return nil
}
