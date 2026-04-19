package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"speakeasy/internal/admin"
	"speakeasy/internal/config"
)

func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Mint, list, revoke, and unrevoke access tokens",
	}
	cmd.AddCommand(newTokenMintCmd())
	cmd.AddCommand(newTokenListCmd())
	cmd.AddCommand(newTokenRevokeCmd())
	cmd.AddCommand(newTokenUnrevokeCmd())
	return cmd
}

func newTokenMintCmd() *cobra.Command {
	var (
		label   string
		route   string
		expires string
		msg     string
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:   "mint",
		Short: "Mint a new access token",
		Example: `  speakeasy token mint --label alice --route client-preview --expires 30d
  speakeasy token mint --label bob   --route demo            --expires never --msg "hey bob"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := adminClient()
			if err != nil {
				return err
			}
			res, err := c.CreateToken(cmd.Context(), admin.CreateTokenRequest{
				Label: label, Route: route, Expires: expires, Msg: msg,
			})
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(os.Stdout).Encode(res)
			}
			fmt.Printf("Token minted\n")
			fmt.Printf("  Label:   %s\n", res.Label)
			fmt.Printf("  Route:   %s\n", res.Route)
			if res.ExpiresAt != nil {
				fmt.Printf("  Expires: %s\n", res.ExpiresAt.Format(time.RFC3339))
			} else {
				fmt.Printf("  Expires: never\n")
			}
			fmt.Printf("  JTI:     %s\n", res.JTI)
			fmt.Printf("\nShare link:\n  %s\n", res.InviteURL)
			return nil
		},
	}
	cmd.Flags().StringVarP(&label, "label", "l", "", "token label (required)")
	cmd.Flags().StringVarP(&route, "route", "r", "", "named route the token is bound to (required)")
	cmd.Flags().StringVarP(&expires, "expires", "e", "", "duration (30d, 2h) or date (2026-05-01) or 'never'")
	cmd.Flags().StringVarP(&msg, "msg", "m", "", "welcome message shown on the invite page")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit full JSON response")
	_ = cmd.MarkFlagRequired("label")
	_ = cmd.MarkFlagRequired("route")
	return cmd
}

func newTokenListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List minted tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := adminClient()
			if err != nil {
				return err
			}
			tokens, err := c.ListTokens(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(os.Stdout).Encode(tokens)
			}
			if len(tokens) == 0 {
				fmt.Println("(no tokens)")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "LABEL\tROUTE\tSTATUS\tEXPIRES\tLAST USED\tJTI")
			for _, t := range tokens {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					t.Label, t.Route, statusWord(t),
					fmtTimePtr(t.ExpiresAt, "never"),
					fmtTimePtr(t.LastUsedAt, "—"),
					t.JTI,
				)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit full JSON response")
	return cmd
}

func newTokenRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <label|jti>",
		Short: "Revoke a token (by label or jti)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := adminClient()
			if err != nil {
				return err
			}
			jti, err := c.ResolveJTI(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := c.RevokeToken(cmd.Context(), jti); err != nil {
				return err
			}
			fmt.Printf("Revoked %s\n", args[0])
			return nil
		},
	}
}

func newTokenUnrevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unrevoke <jti>",
		Short: "Restore a previously revoked token (by jti)",
		Long:  "Unrevoke takes a jti, not a label: labels can point to multiple revoked tokens over time.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := adminClient()
			if err != nil {
				return err
			}
			if err := c.UnrevokeToken(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Printf("Unrevoked %s\n", args[0])
			return nil
		},
	}
}

// adminClient loads the config to find the socket path, then builds a client.
func adminClient() (*admin.Client, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return admin.NewSocketClient(cfg.Server.SocketPath), nil
}

func statusWord(t admin.TokenInfo) string {
	switch {
	case t.RevokedAt != nil:
		return "revoked"
	case t.ExpiresAt != nil && t.ExpiresAt.Before(time.Now()):
		return "expired"
	default:
		return "active"
	}
}

func fmtTimePtr(t *time.Time, zero string) string {
	if t == nil {
		return zero
	}
	return t.UTC().Format("2006-01-02 15:04 MST")
}

