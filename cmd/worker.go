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

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Run the background worker pool",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		appCtx, err := app.NewAppContext(ctx, configPath)
		if err != nil {
			return fmt.Errorf("init app context: %w", err)
		}
		defer appCtx.Close()

		deps := dependencies.NewWorkerDependencies(appCtx)
		return deps.Run(ctx)
	},
}

func init() {
	rootCmd.AddCommand(workerCmd)
}
