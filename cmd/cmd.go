package cmd

import (
	"context"
	"glesha/cmd/add_cmd"
	"glesha/cmd/help_cmd"
	"glesha/cmd/run_cmd"
	"glesha/cmd/tui_cmd"
	"glesha/cmd/version_cmd"
	"os"
)

func Execute(ctx context.Context, args []string) error {
	if len(os.Args) < 2 {
		PrintUsage()
		return nil
	}

	values := map[string]string{
		"binary_name":  os.Args[0],
		"command_name": os.Args[1],
	}

	ctx = context.WithValue(ctx, "values", values)

	switch os.Args[1] {
	case "add":
		return add_cmd.Execute(ctx, args[2:])
	case "run":
		return run_cmd.Execute(ctx, args[2:])
	case "tui":
		return tui_cmd.Execute(ctx, args[2:])
	case "help":
		return help_cmd.Execute(ctx, args[2:])
	case "version", "--version", "-v":
		return version_cmd.Execute(ctx, args[2:])
	default:
		PrintUsage()
		return nil
	}
}
