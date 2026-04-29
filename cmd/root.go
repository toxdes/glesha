package cmd

import (
	"context"

	"github.com/spf13/cobra"

	"glesha/cmd/add_cmd"
	"glesha/cmd/config_cmd"
	"glesha/cmd/run_cmd"
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
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd: true,
	},
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
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	rootCmd.PersistentFlags().StringP("log-level", "L", L.GetLogLevel().String(),
		"Set log level: debug info warn error panic")
	rootCmd.PersistentFlags().String("color", L.GetColorMode().String(),
		"Set color mode: auto always never")

	rootCmd.AddCommand(add_cmd.NewAddCmd())
	rootCmd.AddCommand(run_cmd.NewRunCmd())
	rootCmd.AddCommand(config_cmd.NewConfigCmd())

	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			L.Printf("%s version v%s, build %s\n", cmd.Root().Name(), Version, CommitHash)
			return nil
		},
	})
}

func Execute(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}
