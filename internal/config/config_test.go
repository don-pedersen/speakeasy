package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTmp(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadAppliesDefaults(t *testing.T) {
	p := writeTmp(t, `
[server]
domain = "example.com"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.HTTPPort != 80 || cfg.Server.HTTPSPort != 443 {
		t.Errorf("port defaults not applied: %+v", cfg.Server)
	}
	if cfg.Server.SocketPath == "" || cfg.Server.DataDir == "" {
		t.Errorf("path defaults not applied: %+v", cfg.Server)
	}
	if cfg.TLS.Mode != TLSModeAutocert {
		t.Errorf("tls mode default = %q, want %q", cfg.TLS.Mode, TLSModeAutocert)
	}
	if !strings.HasPrefix(cfg.TLS.CacheDir, cfg.Server.DataDir) {
		t.Errorf("cache dir default = %q, want under %q", cfg.TLS.CacheDir, cfg.Server.DataDir)
	}
}

func TestLoadRejectsMissingDomain(t *testing.T) {
	p := writeTmp(t, ``)
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "domain") {
		t.Fatalf("expected domain error, got %v", err)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	p := writeTmp(t, `
[server]
domain = "example.com"
mystery = "nope"
`)
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown key error, got %v", err)
	}
}

func TestLoadFilesModeRequiresCerts(t *testing.T) {
	p := writeTmp(t, `
[server]
domain = "example.com"

[tls]
mode = "files"
`)
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "cert_file") {
		t.Fatalf("expected cert_file error, got %v", err)
	}
}

func TestLoadRouteValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing name",
			body: `
[server]
domain = "x.com"
[[routes]]
path = "/p"
upstream = "http://localhost:1"
`,
			want: "name is required",
		},
		{
			name: "duplicate path",
			body: `
[server]
domain = "x.com"
[[routes]]
name = "a"
path = "/p"
upstream = "http://localhost:1"
[[routes]]
name = "b"
path = "/p"
upstream = "http://localhost:2"
`,
			want: "duplicate path",
		},
		{
			name: "path missing leading slash",
			body: `
[server]
domain = "x.com"
[[routes]]
name = "a"
path = "p"
upstream = "http://localhost:1"
`,
			want: "must start with /",
		},
		{
			name: "upstream missing scheme",
			body: `
[server]
domain = "x.com"
[[routes]]
name = "a"
path = "/p"
upstream = "localhost:1"
`,
			want: "http:// or https://",
		},
		{
			name: "route shadows reserved /invite",
			body: `
[server]
domain = "x.com"
[[routes]]
name = "a"
path = "/invite"
upstream = "http://localhost:1"
`,
			want: "reserved endpoint",
		},
		{
			name: "route nested under reserved /health",
			body: `
[server]
domain = "x.com"
[[routes]]
name = "a"
path = "/health/foo"
upstream = "http://localhost:1"
`,
			want: "reserved endpoint",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTmp(t, tc.body)
			_, err := Load(p)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestLoadAcceptsValidConfig(t *testing.T) {
	p := writeTmp(t, `
[server]
domain = "example.com"
http_port = 8080
https_port = 8443
data_dir = "/tmp/speakeasy"
socket_path = "/tmp/speakeasy.sock"

[tls]
mode = "files"
cert_file = "/etc/ssl/cert.pem"
key_file = "/etc/ssl/key.pem"

[[routes]]
name = "preview"
path = "/preview"
upstream = "http://localhost:8081"
strip_prefix = true
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Routes) != 1 || cfg.Routes[0].Name != "preview" {
		t.Fatalf("unexpected routes: %+v", cfg.Routes)
	}
}
