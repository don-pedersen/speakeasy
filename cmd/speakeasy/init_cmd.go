package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"speakeasy/internal/config"
)

func newInitCmd() *cobra.Command {
	opts := initOptions{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate a starter config.toml",
		Long: `Write a minimal working config.toml. Interactive by default; if --domain,
--route-name, --route-path, and --route-upstream are all set, runs
non-interactively — suitable for scripts and unattended install.

The generated config covers domain, a single TLS mode (default: autocert),
and one route. Edit afterwards for more routes, trust_proxy, session TTL, etc.`,
		Example: `  # Interactive
  sudo speakeasy init

  # Scripted / unattended
  sudo speakeasy init \
    --domain example.com \
    --route-name app --route-path / --route-upstream http://localhost:8080`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(opts, os.Stdin, os.Stdout, configPath)
		},
	}
	cmd.Flags().StringVar(&opts.Domain, "domain", "", "public hostname (e.g. example.com)")
	cmd.Flags().StringVar(&opts.SiteName, "site-name", "", "display name on invite landing (default: domain)")
	cmd.Flags().StringVar(&opts.TLSMode, "tls", "autocert", "tls mode: autocert, files, or http")
	cmd.Flags().StringVar(&opts.RouteName, "route-name", "", "first route name")
	cmd.Flags().StringVar(&opts.RoutePath, "route-path", "", "route path (must start with /; cannot shadow reserved paths)")
	cmd.Flags().StringVar(&opts.RouteUpstream, "route-upstream", "", "upstream URL (e.g. http://localhost:8080)")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "overwrite an existing config")
	cmd.Flags().BoolVar(&opts.NonInteractive, "non-interactive", false, "never prompt; error if any required value is missing")
	return cmd
}

type initOptions struct {
	Domain         string
	SiteName       string
	TLSMode        string
	RouteName      string
	RoutePath      string
	RouteUpstream  string
	Force          bool
	NonInteractive bool
}

func runInit(opts initOptions, in io.Reader, out io.Writer, target string) error {
	if _, err := os.Stat(target); err == nil && !opts.Force {
		return fmt.Errorf("%s already exists — pass --force to overwrite", target)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	requiredMissing := opts.Domain == "" || opts.RouteName == "" ||
		opts.RoutePath == "" || opts.RouteUpstream == ""

	var prompter promptFn
	switch {
	case !requiredMissing:
		prompter = noopPrompt
	case opts.NonInteractive:
		return errors.New("missing required flags (--domain, --route-name, --route-path, --route-upstream) and --non-interactive set")
	case !termIsInteractive(in):
		return errors.New("missing required flags and stdin is not a terminal — pass flags or run interactively")
	default:
		r := bufio.NewReader(in)
		prompter = func(label, def string) string {
			if def != "" {
				fmt.Fprintf(out, "%s [%s]: ", label, def)
			} else {
				fmt.Fprintf(out, "%s: ", label)
			}
			line, _ := r.ReadString('\n')
			line = strings.TrimSpace(line)
			if line == "" {
				return def
			}
			return line
		}
	}

	if opts.Domain == "" {
		opts.Domain = prompter("Public hostname", "")
	}
	if opts.Domain == "" {
		return errors.New("domain is required")
	}
	if opts.SiteName == "" {
		opts.SiteName = prompter("Display name (shown on invite page)", opts.Domain)
	}
	if opts.TLSMode == "" {
		opts.TLSMode = prompter("TLS mode (autocert, files, or http)", "autocert")
	}
	if opts.RouteName == "" {
		opts.RouteName = prompter("First route name", "app")
	}
	if opts.RoutePath == "" {
		opts.RoutePath = prompter("Route path", "/")
	}
	if opts.RouteUpstream == "" {
		opts.RouteUpstream = prompter("Upstream URL", "http://localhost:8080")
	}

	body := buildConfigTOML(opts)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(target), err)
	}
	// 0640: owner rw, group r (service user usually in the file's group)
	if err := os.WriteFile(target, []byte(body), 0o640); err != nil {
		return err
	}

	// Sanity-check what we just wrote — emit a helpful error if validation
	// fails (e.g. TLS mode = files without cert/key paths filled in).
	if _, err := config.Load(target); err != nil {
		return fmt.Errorf("wrote %s but the result failed validation:\n  %w\nEdit the file to fix and re-run `speakeasy config validate`.", target, err)
	}

	fmt.Fprintf(out, "\n✓ wrote %s\n\n", target)
	fmt.Fprintf(out, "Next:\n")
	fmt.Fprintf(out, "  sudo systemctl enable --now speakeasy\n")
	fmt.Fprintf(out, "  speakeasy --config %s token mint --label alice --route %s --expires 7d\n",
		target, opts.RouteName)
	return nil
}

// buildConfigTOML emits a minimal config. Options the user didn't ask about
// (trust_proxy, session_ttl, data_dir, etc.) fall back to built-in defaults,
// which are the sane choice for almost every deployment.
func buildConfigTOML(opts initOptions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Generated by `speakeasy init` at %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "# Full reference: packaging/config.toml.example in the repo.\n\n")

	fmt.Fprintf(&b, "[server]\n")
	fmt.Fprintf(&b, "domain    = %q\n", opts.Domain)
	if opts.SiteName != "" && opts.SiteName != opts.Domain {
		fmt.Fprintf(&b, "site_name = %q\n", opts.SiteName)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "[tls]\n")
	fmt.Fprintf(&b, "mode = %q\n", opts.TLSMode)
	if opts.TLSMode == "files" {
		fmt.Fprintf(&b, "# cert_file = \"/etc/ssl/certs/yoursite.pem\"\n")
		fmt.Fprintf(&b, "# key_file  = \"/etc/ssl/private/yoursite.key\"\n")
	}
	if opts.TLSMode == "http" {
		fmt.Fprintf(&b, "# For http mode behind a TLS-terminating proxy (Caddy, nginx, Cloudflare),\n")
		fmt.Fprintf(&b, "# also set server.trust_proxy = true so the Secure cookie flag stays on.\n")
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "[[routes]]\n")
	fmt.Fprintf(&b, "name     = %q\n", opts.RouteName)
	fmt.Fprintf(&b, "path     = %q\n", opts.RoutePath)
	fmt.Fprintf(&b, "upstream = %q\n", opts.RouteUpstream)

	return b.String()
}

type promptFn func(label, def string) string

func noopPrompt(label, def string) string { return def }

// termIsInteractive reports whether r is an *os.File backed by a TTY.
// Anything else (pipe, file) returns false so non-interactive callers fail
// cleanly instead of hanging.
func termIsInteractive(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
