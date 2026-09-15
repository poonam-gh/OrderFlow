package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"orderflow/app"
	"orderflow/dependencies"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run the HTTP API server",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		appCtx, err := app.NewAppContext(ctx, configPath)
		if err != nil {
			return fmt.Errorf("init app context: %w", err)
		}
		defer appCtx.Close()

		deps := dependencies.NewServerDependencies(appCtx)
		return deps.Run(ctx, fmt.Sprintf(":%d", appCtx.Config.Server.Port))
	},
}

func init() {
	rootCmd.AddCommand(serverCmd)
}
