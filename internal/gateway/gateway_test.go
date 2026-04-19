package gateway

import (
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"speakeasy/internal/config"
	"speakeasy/internal/store"
	"speakeasy/internal/token"
)

type testEnv struct {
	gw       *Gateway
	server   *httptest.Server
	client   *http.Client
	store    *store.Store
	signer   *token.Signer
	upstream *httptest.Server
	upstream2 *httptest.Server
}

// newTestEnv spins up two fake upstreams and a Gateway in front of them,
// wrapped in an httptest.Server. The returned http.Client does not follow
// redirects, so tests can inspect the 302 from /invite.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("upstream-1:" + r.URL.Path))
	}))
	t.Cleanup(upstream.Close)

	upstream2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("upstream-2:" + r.URL.Path + " fwd=" + r.Header.Get("X-Forwarded-Proto")))
	}))
	t.Cleanup(upstream2.Close)

	cfg := &config.Config{
		Server: config.Server{
			Domain:     "example.test",
			SiteName:   "Secret Club",
			SessionTTL: 3600,
		},
		TLS: config.TLS{Mode: config.TLSModeHTTP},
		Routes: []config.Route{
			{Name: "preview", Path: "/preview", Upstream: upstream.URL},
			{Name: "demo", Path: "/demo", Upstream: upstream2.URL, StripPrefix: true},
		},
	}

	p := filepath.Join(t.TempDir(), "speakeasy.db")
	st, err := store.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	key := make([]byte, 64)
	_, _ = rand.Read(key)
	signer, err := token.New(key)
	if err != nil {
		t.Fatal(err)
	}

	gw, err := New(cfg, st, signer, "test")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(gw)
	t.Cleanup(ts.Close)

	jar, _ := newCookieJar()
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return &testEnv{
		gw: gw, server: ts, client: client,
		store: st, signer: signer,
		upstream: upstream, upstream2: upstream2,
	}
}

type memJar struct {
	m map[string][]*http.Cookie
}

func newCookieJar() (*memJar, error) {
	return &memJar{m: map[string][]*http.Cookie{}}, nil
}
func (j *memJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.m[u.Host] = append(j.m[u.Host], cookies...)
}
func (j *memJar) Cookies(u *url.URL) []*http.Cookie { return j.m[u.Host] }

// mintFor creates a token in the store and returns the signed JWT.
func (e *testEnv) mintFor(t *testing.T, label, route string) string {
	t.Helper()
	jwt, jti, err := e.signer.Mint(label, route, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateToken(t.Context(), store.CreateTokenInput{
		JTI: jti, Label: label, Route: route,
	}); err != nil {
		t.Fatal(err)
	}
	return jwt
}

func TestHealthIsPublic(t *testing.T) {
	e := newTestEnv(t)
	resp, err := e.client.Get(e.server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestDeniedPageDirectly(t *testing.T) {
	e := newTestEnv(t)
	resp, err := e.client.Get(e.server.URL + "/denied")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestGatedPathWithoutSessionIsDenied(t *testing.T) {
	e := newTestEnv(t)
	resp, err := e.client.Get(e.server.URL + "/preview/stuff")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if strings.Contains(strReadAll(t, resp.Body), "upstream") {
		t.Error("should not reach upstream without a session")
	}
}

func TestUnknownPathIsDenied(t *testing.T) {
	e := newTestEnv(t)
	resp, err := e.client.Get(e.server.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestInviteLandingFlow(t *testing.T) {
	e := newTestEnv(t)
	jwt := e.mintFor(t, "alice", "preview")

	// /invite returns 200 HTML landing with cookie set.
	resp, err := e.client.Get(e.server.URL + "/invite?token=" + jwt)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := strReadAll(t, resp.Body)
	for _, want := range []string{"Secret Club", `href="/preview"`, "Enter now"} {
		if !strings.Contains(body, want) {
			t.Errorf("landing missing %q\n---\n%s", want, body)
		}
	}
	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("session cookie not set")
	}
	if !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie hardening missing: %+v", sessionCookie)
	}

	// With the cookie, hit /preview — should proxy through.
	resp2, err := e.client.Get(e.server.URL + "/preview/hello")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("preview: status=%d", resp2.StatusCode)
	}
	if got := strReadAll(t, resp2.Body); got != "upstream-1:/preview/hello" {
		t.Fatalf("proxy body mismatch: %q", got)
	}
}

func TestEnterImmediateRedirect(t *testing.T) {
	e := newTestEnv(t)
	jwt := e.mintFor(t, "alice", "preview")

	resp, err := e.client.Get(e.server.URL + "/enter?token=" + jwt)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 302 {
		t.Fatalf("status=%d, want 302", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/preview" {
		t.Fatalf("Location=%q", got)
	}
	found := false
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			found = true
		}
	}
	if !found {
		t.Error("enter did not set cookie")
	}
}

func TestInviteLandingShowsWelcomeMessage(t *testing.T) {
	e := newTestEnv(t)
	jwt, jti, err := e.signer.Mint("alice", "preview", "hey alice — check this out", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateToken(t.Context(), store.CreateTokenInput{
		JTI: jti, Label: "alice", Route: "preview", Msg: "hey alice — check this out",
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := e.client.Get(e.server.URL + "/invite?token=" + jwt)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if !strings.Contains(strReadAll(t, resp.Body), "hey alice") {
		t.Error("landing did not include msg")
	}
}

func TestInviteErrorShowsHelpfulDetail(t *testing.T) {
	e := newTestEnv(t)
	exp := time.Now().Add(-time.Hour)
	jwt, jti, _ := e.signer.Mint("ghost", "preview", "", &exp)
	_, _ = e.store.CreateToken(t.Context(), store.CreateTokenInput{
		JTI: jti, Label: "ghost", Route: "preview", ExpiresAt: &exp,
	})

	resp, err := e.client.Get(e.server.URL + "/invite?token=" + jwt)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body := strReadAll(t, resp.Body)
	if !strings.Contains(body, "expired") {
		t.Errorf("error body missing 'expired': %q", body)
	}
	// Error page is part of the invite family, so it DOES show site name.
	if !strings.Contains(body, "Secret Club") {
		t.Errorf("invite error page should include site name")
	}
}

func TestAdminHandlerDispatched(t *testing.T) {
	e := newTestEnv(t)
	e.gw.SetAdminHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("admin:" + r.URL.Path))
	}))

	for _, path := range []string{"/admin", "/admin/api/tokens", "/admin/anything"} {
		resp, err := e.client.Get(e.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body := strReadAll(t, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.HasPrefix(body, "admin:") {
			t.Errorf("path=%s status=%d body=%q", path, resp.StatusCode, body)
		}
	}
}

func TestAdminHandlerDisabledByDefault(t *testing.T) {
	e := newTestEnv(t)
	// No SetAdminHandler call. /admin should fall through to denied.
	resp, err := e.client.Get(e.server.URL + "/admin")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestDeniedLeaksNoSiteInfo(t *testing.T) {
	e := newTestEnv(t)
	resp, err := e.client.Get(e.server.URL + "/denied")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body := strReadAll(t, resp.Body)
	// Must not mention the site name or any route path.
	for _, leaks := range []string{"Secret Club", "example.test", "/preview", "/demo", "preview", "demo"} {
		if strings.Contains(body, leaks) {
			t.Errorf("/denied leaked %q:\n%s", leaks, body)
		}
	}
}

func TestSessionScopedToOneRoute(t *testing.T) {
	e := newTestEnv(t)
	jwt := e.mintFor(t, "alice", "preview")

	if resp, err := e.client.Get(e.server.URL + "/invite?token=" + jwt); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}

	// Hitting /demo with a /preview-scoped session must be denied.
	resp2, err := e.client.Get(e.server.URL + "/demo/anything")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 401 {
		t.Fatalf("cross-route should deny, got %d", resp2.StatusCode)
	}
}

func TestStripPrefixProxies(t *testing.T) {
	e := newTestEnv(t)
	jwt := e.mintFor(t, "bob", "demo")

	if resp, err := e.client.Get(e.server.URL + "/invite?token=" + jwt); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}

	resp2, err := e.client.Get(e.server.URL + "/demo/foo")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	body := strReadAll(t, resp2.Body)
	// strip_prefix should turn /demo/foo into /foo at the upstream.
	if !strings.HasPrefix(body, "upstream-2:/foo") {
		t.Fatalf("body=%q", body)
	}
	// And X-Forwarded-Proto should be populated.
	if !strings.Contains(body, "fwd=https") {
		t.Fatalf("expected forwarded-proto in body: %q", body)
	}
}

func TestRevokedTokenAtInviteRejected(t *testing.T) {
	e := newTestEnv(t)
	jwt, jti, err := e.signer.Mint("ghost", "preview", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := e.store.CreateToken(t.Context(), store.CreateTokenInput{
		JTI: jti, Label: "ghost", Route: "preview",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.RevokeToken(t.Context(), tk.JTI); err != nil {
		t.Fatal(err)
	}

	resp, err := e.client.Get(e.server.URL + "/invite?token=" + jwt)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	e := newTestEnv(t)
	exp := time.Now().Add(-time.Hour)
	jwt, jti, err := e.signer.Mint("expired", "preview", "", &exp)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.store.CreateToken(t.Context(), store.CreateTokenInput{
		JTI: jti, Label: "expired", Route: "preview", ExpiresAt: &exp,
	})

	resp, err := e.client.Get(e.server.URL + "/invite?token=" + jwt)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body := strReadAll(t, resp.Body)
	if !strings.Contains(body, "expired") {
		t.Errorf("expected 'expired' in body, got %q", body)
	}
}

func TestInviteWithoutTokenIs400(t *testing.T) {
	e := newTestEnv(t)
	resp, err := e.client.Get(e.server.URL + "/invite")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestSessionCookieStrippedFromUpstream(t *testing.T) {
	// Build a dedicated upstream that echoes incoming Cookie header.
	inspect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("cookies=" + r.Header.Get("Cookie")))
	}))
	defer inspect.Close()

	cfg := &config.Config{
		Server: config.Server{Domain: "t", SessionTTL: 3600},
		TLS:    config.TLS{Mode: config.TLSModeHTTP},
		Routes: []config.Route{{Name: "r", Path: "/r", Upstream: inspect.URL}},
	}
	st, _ := store.Open(filepath.Join(t.TempDir(), "x.db"))
	defer st.Close()
	key := make([]byte, 64)
	_, _ = rand.Read(key)
	s, _ := token.New(key)
	gw, err := New(cfg, st, s, "t")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(gw)
	defer ts.Close()

	jar, _ := newCookieJar()
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

	jwt, jti, _ := s.Mint("u", "r", "", nil)
	_, _ = st.CreateToken(t.Context(), store.CreateTokenInput{JTI: jti, Label: "u", Route: "r"})

	if resp, err := client.Get(ts.URL + "/invite?token=" + jwt); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}

	// Add an extra cookie manually; speakeasy_session should be stripped, this one passed through.
	u, _ := url.Parse(ts.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "other", Value: "kept"}})

	resp2, err := client.Get(ts.URL + "/r/x")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	body := strReadAll(t, resp2.Body)
	if strings.Contains(body, cookieName) {
		t.Errorf("session cookie leaked to upstream: %q", body)
	}
	if !strings.Contains(body, "other=kept") {
		t.Errorf("other cookies should pass through; got %q", body)
	}
}

func strReadAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
