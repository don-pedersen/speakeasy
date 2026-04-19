package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server Server  `toml:"server"`
	TLS    TLS     `toml:"tls"`
	Routes []Route `toml:"routes"`
}

type Server struct {
	Domain            string `toml:"domain"`
	SiteName          string `toml:"site_name"` // shown on invite landing; defaults to Domain
	HTTPPort          int    `toml:"http_port"`
	HTTPSPort         int    `toml:"https_port"`
	AdminPasswordFile string `toml:"admin_password_file"`
	DataDir           string `toml:"data_dir"`
	SocketPath        string `toml:"socket_path"`
	SessionTTL        int    `toml:"session_ttl"`
	// TrustProxy is true when a fronting reverse proxy (Cloudflare Tunnel,
	// nginx, etc.) terminates TLS and forwards plain HTTP. When set, the
	// gateway:
	//   - sets the Secure cookie flag even in tls.mode = "http"
	//   - reads client IP from X-Forwarded-For for logging
	//   - suppresses HTTP->HTTPS redirects (the proxy owns that)
	TrustProxy bool `toml:"trust_proxy"`
}

type TLS struct {
	Mode     string `toml:"mode"`
	CacheDir string `toml:"cache_dir"`
	CertFile string `toml:"cert_file"`
	KeyFile  string `toml:"key_file"`
}

type Route struct {
	Name        string `toml:"name"`
	Path        string `toml:"path"`
	Upstream    string `toml:"upstream"`
	StripPrefix bool   `toml:"strip_prefix"`
}

const (
	TLSModeAutocert = "autocert"
	TLSModeFiles    = "files"
	// TLSModeHTTP is dev-only: serves plain HTTP on http_port, no HTTPS.
	// Session cookies are issued without the Secure flag in this mode.
	TLSModeHTTP = "http"
)

// reservedPaths are public endpoints the gateway serves directly; routes must
// not shadow them.
var reservedPaths = []string{"/invite", "/enter", "/denied", "/health", "/admin"}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg := &Config{}
	meta, err := toml.Decode(string(data), cfg)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		return nil, fmt.Errorf("unknown config keys: %s", strings.Join(keys, ", "))
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Server.HTTPPort == 0 {
		c.Server.HTTPPort = 80
	}
	if c.Server.HTTPSPort == 0 {
		c.Server.HTTPSPort = 443
	}
	if c.Server.DataDir == "" {
		c.Server.DataDir = "/var/lib/speakeasy"
	}
	if c.Server.SocketPath == "" {
		c.Server.SocketPath = "/run/speakeasy/speakeasy.sock"
	}
	if c.Server.SessionTTL == 0 {
		c.Server.SessionTTL = 86400
	}
	if c.Server.SiteName == "" {
		c.Server.SiteName = c.Server.Domain
	}
	if c.TLS.Mode == "" {
		c.TLS.Mode = TLSModeAutocert
	}
	if c.TLS.CacheDir == "" {
		c.TLS.CacheDir = filepath.Join(c.Server.DataDir, "certs")
	}
}

func (c *Config) validate() error {
	if c.Server.Domain == "" {
		return fmt.Errorf("server.domain is required")
	}
	switch c.TLS.Mode {
	case TLSModeAutocert, TLSModeHTTP:
	case TLSModeFiles:
		if c.TLS.CertFile == "" || c.TLS.KeyFile == "" {
			return fmt.Errorf(`tls.cert_file and tls.key_file are required when mode = "files"`)
		}
	default:
		return fmt.Errorf(`tls.mode must be %q, %q, or %q`, TLSModeAutocert, TLSModeFiles, TLSModeHTTP)
	}

	names := map[string]bool{}
	paths := map[string]bool{}
	for i, r := range c.Routes {
		if r.Name == "" {
			return fmt.Errorf("route[%d]: name is required", i)
		}
		if names[r.Name] {
			return fmt.Errorf("route[%d]: duplicate name %q", i, r.Name)
		}
		names[r.Name] = true

		if r.Path == "" || !strings.HasPrefix(r.Path, "/") {
			return fmt.Errorf("route %q: path must start with /", r.Name)
		}
		if r.Path != "/" && strings.HasSuffix(r.Path, "/") {
			return fmt.Errorf("route %q: path must not end with / (got %q)", r.Name, r.Path)
		}
		for _, res := range reservedPaths {
			if r.Path == res || strings.HasPrefix(r.Path, res+"/") {
				return fmt.Errorf("route %q: path %q shadows reserved endpoint %q", r.Name, r.Path, res)
			}
		}
		if paths[r.Path] {
			return fmt.Errorf("route %q: duplicate path %q", r.Name, r.Path)
		}
		paths[r.Path] = true

		if r.Upstream == "" {
			return fmt.Errorf("route %q: upstream is required", r.Name)
		}
		if !strings.HasPrefix(r.Upstream, "http://") && !strings.HasPrefix(r.Upstream, "https://") {
			return fmt.Errorf("route %q: upstream must start with http:// or https://", r.Name)
		}
	}
	return nil
}
