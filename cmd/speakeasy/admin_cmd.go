package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"speakeasy/internal/admin"
	"speakeasy/internal/config"
)

// Shared so successive non-TTY prompts read consecutive lines rather than each
// consuming all of stdin into its own buffer.
var stdinReader = bufio.NewReader(os.Stdin)

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Manage the admin panel",
	}
	cmd.AddCommand(newAdminSetPasswordCmd())
	return cmd
}

func newAdminSetPasswordCmd() *cobra.Command {
	var passwordFile string
	cmd := &cobra.Command{
		Use:   "set-password",
		Short: "Set the admin panel password (writes a bcrypt hash)",
		Long: `Prompts for a password (no echo) and writes a bcrypt hash to the admin
password file. The daemon reads this file at startup to enable the /admin web
panel. Restart the daemon after changing the password.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			override := passwordFile
			if override == "" {
				override = cfg.Server.AdminPasswordFile
			}
			target := admin.HashPath(cfg.Server.DataDir, override)

			pw, err := promptPassword("New admin password: ")
			if err != nil {
				return err
			}
			confirm, err := promptPassword("Confirm password: ")
			if err != nil {
				return err
			}
			if pw != confirm {
				return errors.New("passwords do not match")
			}
			if len(pw) < 8 {
				return errors.New("password must be at least 8 characters")
			}

			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return fmt.Errorf("create dir: %w", err)
			}
			if err := admin.WriteHashFile(target, pw); err != nil {
				return err
			}
			// The CLI is typically invoked via sudo, so the new file is
			// owned by root. Hand it to whoever owns the parent directory
			// (usually the speakeasy daemon user) so the daemon can read it.
			if err := chownToParent(target); err != nil {
				return fmt.Errorf("chown %s: %w", target, err)
			}
			fmt.Printf("Wrote hash to %s (chmod 0600)\n", target)
			fmt.Println("Restart the daemon to pick up the new password.")
			return nil
		},
	}
	cmd.Flags().StringVar(&passwordFile, "file", "",
		"override path for the hash file (defaults to server.admin_password_file)")
	return cmd
}

// chownToParent sets path's owner/group to match its parent directory.
// On non-unix platforms Stat.Sys() doesn't yield a *syscall.Stat_t and the
// chown is silently skipped — speakeasy targets linux but we keep the build
// portable.
func chownToParent(path string) error {
	st, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return err
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return os.Chown(path, int(sys.Uid), int(sys.Gid))
}

// promptPassword prints prompt and reads a line without echoing. Falls back to
// plain stdin if stdin is not a TTY (rarely useful but keeps scripts workable).
func promptPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		pw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return string(pw), nil
	}
	// Non-TTY fallback: one line per call, reusing the shared reader.
	line, err := stdinReader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password: %w", err)
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", errors.New("empty password")
	}
	return line, nil
}
