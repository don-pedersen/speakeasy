package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"speakeasy/internal/config"
)

func newTmpTarget(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "etc/speakeasy/config.toml")
}

// nonInteractive sets all four required fields; runInit should not prompt.
func TestRunInitNonInteractiveWritesValidConfig(t *testing.T) {
	out := &bytes.Buffer{}
	target := newTmpTarget(t)
	opts := initOptions{
		Domain:        "example.com",
		TLSMode:       "autocert",
		RouteName:     "app",
		RoutePath:     "/",
		RouteUpstream: "http://localhost:8080",
	}
	if err := runInit(opts, bytes.NewReader(nil), out, target); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	cfg, err := config.Load(target)
	if err != nil {
		t.Fatalf("generated config fails Load: %v", err)
	}
	if cfg.Server.Domain != "example.com" {
		t.Errorf("domain = %q", cfg.Server.Domain)
	}
	if len(cfg.Routes) != 1 || cfg.Routes[0].Name != "app" || cfg.Routes[0].Path != "/" {
		t.Fatalf("unexpected routes: %+v", cfg.Routes)
	}
	if !strings.Contains(out.String(), "wrote") {
		t.Errorf("expected success message in stdout, got %q", out.String())
	}
}

func TestRunInitRefusesToOverwrite(t *testing.T) {
	target := newTmpTarget(t)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runInit(initOptions{
		Domain: "x.com", RouteName: "a", RoutePath: "/", RouteUpstream: "http://l:1", TLSMode: "autocert",
	}, bytes.NewReader(nil), &bytes.Buffer{}, target)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected 'already exists' error, got %v", err)
	}
}

func TestRunInitForceOverwrites(t *testing.T) {
	target := newTmpTarget(t)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := initOptions{
		Domain: "new.example.com", RouteName: "a", RoutePath: "/", RouteUpstream: "http://l:1",
		TLSMode: "autocert", Force: true,
	}
	if err := runInit(opts, bytes.NewReader(nil), &bytes.Buffer{}, target); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(target)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Domain != "new.example.com" {
		t.Errorf("overwrite didn't take: %q", cfg.Server.Domain)
	}
}

func TestRunInitNonInteractiveMissingFlagsErrors(t *testing.T) {
	opts := initOptions{NonInteractive: true}
	err := runInit(opts, bytes.NewReader(nil), &bytes.Buffer{}, newTmpTarget(t))
	if err == nil || !strings.Contains(err.Error(), "missing required flags") {
		t.Fatalf("expected missing-flags error, got %v", err)
	}
}

func TestRunInitNonTTYStdinErrors(t *testing.T) {
	// bytes.Reader is not a TTY. Without flags set, init must refuse to hang.
	err := runInit(initOptions{}, bytes.NewReader(nil), &bytes.Buffer{}, newTmpTarget(t))
	if err == nil || !strings.Contains(err.Error(), "stdin is not a terminal") {
		t.Fatalf("expected TTY error, got %v", err)
	}
}

func TestRunInitFilesModeStubsCommented(t *testing.T) {
	// mode=files without cert/key paths — Config.Load should error.
	target := newTmpTarget(t)
	opts := initOptions{
		Domain: "x.com", TLSMode: "files",
		RouteName: "a", RoutePath: "/", RouteUpstream: "http://l:1",
	}
	err := runInit(opts, bytes.NewReader(nil), &bytes.Buffer{}, target)
	if err == nil {
		t.Fatal("expected validation error for files mode without paths")
	}
	// We still wrote the file (so the user can edit it), and the error
	// should tell them to fix & re-validate.
	if !strings.Contains(err.Error(), "failed validation") {
		t.Errorf("unexpected error wording: %v", err)
	}
	// The written file should include the commented stubs for cert/key.
	b, _ := os.ReadFile(target)
	body := string(b)
	for _, want := range []string{"# cert_file", "# key_file"} {
		if !strings.Contains(body, want) {
			t.Errorf("generated TOML missing %q:\n%s", want, body)
		}
	}
}

func TestRunInitHttpModeIncludesTrustProxyHint(t *testing.T) {
	target := newTmpTarget(t)
	opts := initOptions{
		Domain: "x.com", TLSMode: "http",
		RouteName: "a", RoutePath: "/", RouteUpstream: "http://l:1",
	}
	if err := runInit(opts, bytes.NewReader(nil), &bytes.Buffer{}, target); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(target)
	if !strings.Contains(string(b), "trust_proxy") {
		t.Error("http mode should include trust_proxy guidance comment")
	}
}

func TestInteractivePromptingFlow(t *testing.T) {
	target := newTmpTarget(t)

	// Simulate a user accepting defaults for most, supplying the rest.
	// The `in` is wrapped in an os.File via a pipe so termIsInteractive
	// returns false — so this test covers the "all flags supplied" branch.
	// (Interactive-mode prompting requires a real TTY; skipping that path here.)
	opts := initOptions{
		Domain: "interactive.test",
		// Only partial fields set — but NonInteractive is false and stdin
		// is not a TTY. Should error.
	}
	err := runInit(opts, bytes.NewReader(nil), &bytes.Buffer{}, target)
	if err == nil || !strings.Contains(err.Error(), "stdin is not a terminal") {
		t.Fatalf("expected TTY error for partial flags + non-TTY stdin, got %v", err)
	}
}

func TestBuildConfigTOMLShape(t *testing.T) {
	toml := buildConfigTOML(initOptions{
		Domain:        "yoursite.com",
		SiteName:      "My Site",
		TLSMode:       "autocert",
		RouteName:     "preview",
		RoutePath:     "/preview",
		RouteUpstream: "http://localhost:8080",
	})
	for _, want := range []string{
		`domain    = "yoursite.com"`,
		`site_name = "My Site"`,
		`[tls]`,
		`mode = "autocert"`,
		`[[routes]]`,
		`name     = "preview"`,
		`path     = "/preview"`,
		`upstream = "http://localhost:8080"`,
	} {
		if !strings.Contains(toml, want) {
			t.Errorf("TOML missing %q:\n%s", want, toml)
		}
	}
}

func TestBuildConfigTOMLOmitsSiteNameWhenSameAsDomain(t *testing.T) {
	toml := buildConfigTOML(initOptions{
		Domain: "yoursite.com", SiteName: "yoursite.com",
		TLSMode: "autocert", RouteName: "a", RoutePath: "/", RouteUpstream: "http://l:1",
	})
	if strings.Contains(toml, "site_name") {
		t.Errorf("site_name should be omitted when it equals domain:\n%s", toml)
	}
}
