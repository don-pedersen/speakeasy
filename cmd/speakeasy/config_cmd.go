package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"speakeasy/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration commands",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Load and validate the config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			fmt.Printf("OK: %s (%d route(s))\n", configPath, len(cfg.Routes))
			return nil
		},
	})
	return cmd
}
