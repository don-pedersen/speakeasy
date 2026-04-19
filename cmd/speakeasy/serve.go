package main

import (
	"fmt"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"speakeasy/internal/config"
	"speakeasy/internal/daemon"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return daemon.Run(ctx, daemon.Options{Config: cfg, Version: version})
		},
	}
}
