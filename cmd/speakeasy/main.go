package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	configPath string

	// version is overridden at build time via -ldflags "-X main.version=...".
	version = "dev"
)

func main() {
	root := &cobra.Command{
		Use:          "speakeasy",
		Short:        "Self-hosted JWT gateway",
		Version:      version,
		SilenceUsage: true,
	}
	root.PersistentFlags().StringVarP(&configPath, "config", "c",
		"/etc/speakeasy/config.toml", "path to config file")

	root.AddCommand(newServeCmd())
	root.AddCommand(newConfigCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newTokenCmd())
	root.AddCommand(newAdminCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
