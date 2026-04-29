package cmd

import (
	"context"

	"github.com/spf13/cobra"

	"glesha/cmd/add_cmd"
	"glesha/cmd/config_cmd"
	"glesha/cmd/help_cmd"
	"glesha/cmd/run_cmd"
	"glesha/cmd/version_cmd"
	L "glesha/logger"
)

// NOTE: populated at build time with -ldflags (-X)
var Version string

// NOTE: populated at build time with -ldflags (-X)
var CommitHash string

var rootCmd = &cobra.Command{
	Use:           "glesha",
	Short:         "Cross-platform archive and upload utility",
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		logLevel, _ := cmd.Flags().GetString("log-level")
		if err := L.SetLevelFromString(logLevel); err != nil {
			return err
		}
		colorMode, _ := cmd.Flags().GetString("color")
		if err := L.SetColorModeFromString(colorMode); err != nil {
			return err
		}
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringP("log-level", "L", L.GetLogLevel().String(),
		"Set log level: debug info warn error panic")
	rootCmd.PersistentFlags().String("color", L.GetColorMode().String(),
		"Set color mode: auto always never")

	// bridge adapters — call existing Execute() unchanged
	// DisableFlagParsing so cobra doesn't interfere with internal flag.Parse()

	addCmd := &cobra.Command{
		Use:                "add",
		Short:              "Creates a glesha archive and upload task",
		Long:               add_cmd.Usage(),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return add_cmd.Execute(cmd.Context(), args)
		},
	}
	rootCmd.AddCommand(addCmd)

	runCmd := &cobra.Command{
		Use:                "run",
		Short:              "Runs a glesha task",
		Long:               run_cmd.Usage(),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run_cmd.Execute(cmd.Context(), args)
		},
	}
	rootCmd.AddCommand(runCmd)

	rootCmd.AddCommand(config_cmd.NewConfigCmd())

	helpCmd := &cobra.Command{
		Use:                "help",
		Short:              "Help about a subcommand",
		Long:               help_cmd.Usage(),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return help_cmd.Execute(cmd.Context(), args)
		},
	}
	rootCmd.AddCommand(helpCmd)

	versionCmd := &cobra.Command{
		Use:                "version",
		Short:              "Print version information",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.WithValue(cmd.Context(), "values", map[string]string{
				"binary_name": cmd.Root().Name(),
			})
			return version_cmd.Execute(ctx, args)
		},
	}
	rootCmd.AddCommand(versionCmd)
}

func Execute(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}
