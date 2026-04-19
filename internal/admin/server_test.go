package admin

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"speakeasy/internal/config"
	"speakeasy/internal/store"
	"speakeasy/internal/token"
)

func newTestDeps(t *testing.T) Deps {
	t.Helper()
	p := filepath.Join(t.TempDir(), "speakeasy.db")
	s, err := store.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	key := make([]byte, 64)
	_, _ = rand.Read(key)
	signer, err := token.New(key)
	if err != nil {
		t.Fatal(err)
	}
	return Deps{
		Store:  s,
		Signer: signer,
		Routes: []config.Route{
			{Name: "preview", Path: "/preview", Upstream: "http://localhost:8081"},
			{Name: "demo", Path: "/demo", Upstream: "http://localhost:8082"},
		},
		Domain:  "example.com",
		Version: "test",
	}
}

func newTestClient(t *testing.T) (*httptest.Server, Deps) {
	d := newTestDeps(t)
	ts := httptest.NewServer(NewMux(d))
	t.Cleanup(ts.Close)
	return ts, d
}

func mustJSON(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()
	var req *http.Request
	var err error
	if body != nil {
		buf, _ := json.Marshal(body)
		req, err = http.NewRequest(method, url, strings.NewReader(string(buf)))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHealth(t *testing.T) {
	ts, _ := newTestClient(t)
	resp := mustJSON(t, "GET", ts.URL+"/admin/api/health", nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestCreateTokenFlow(t *testing.T) {
	ts, d := newTestClient(t)
	resp := mustJSON(t, "POST", ts.URL+"/admin/api/tokens", CreateTokenRequest{
		Label: "alice", Route: "preview", Expires: "30d", Msg: "hi",
	})
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var got CreateTokenResponse
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.JTI == "" || got.Token == "" {
		t.Fatalf("empty jti/token: %+v", got)
	}
	if got.Label != "alice" || got.Route != "preview" {
		t.Fatalf("mismatch: %+v", got)
	}
	if !strings.HasPrefix(got.InviteURL, "https://example.com/invite?token=") {
		t.Fatalf("bad invite url: %q", got.InviteURL)
	}
	if _, err := d.Signer.Verify(got.Token); err != nil {
		t.Fatalf("minted token fails verify: %v", err)
	}
}

func TestCreateTokenUnknownRoute(t *testing.T) {
	ts, _ := newTestClient(t)
	resp := mustJSON(t, "POST", ts.URL+"/admin/api/tokens", CreateTokenRequest{
		Label: "alice", Route: "nope", Expires: "30d",
	})
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var e ErrorResponse
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if !strings.Contains(e.Error, "unknown route") {
		t.Fatalf("bad error: %q", e.Error)
	}
}

func TestCreateTokenConflict(t *testing.T) {
	ts, _ := newTestClient(t)
	body := CreateTokenRequest{Label: "alice", Route: "preview"}
	_ = mustJSON(t, "POST", ts.URL+"/admin/api/tokens", body).Body.Close()
	resp := mustJSON(t, "POST", ts.URL+"/admin/api/tokens", body)
	defer resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestListTokens(t *testing.T) {
	ts, _ := newTestClient(t)
	for _, lbl := range []string{"a", "b"} {
		_ = mustJSON(t, "POST", ts.URL+"/admin/api/tokens", CreateTokenRequest{
			Label: lbl, Route: "preview",
		}).Body.Close()
	}
	resp := mustJSON(t, "GET", ts.URL+"/admin/api/tokens", nil)
	defer resp.Body.Close()
	var got ListTokensResponse
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Tokens) != 2 {
		t.Fatalf("count=%d", len(got.Tokens))
	}
	for _, tk := range got.Tokens {
		if !tk.Active {
			t.Errorf("expected active: %+v", tk)
		}
	}
}

func TestRevokeAndUnrevoke(t *testing.T) {
	ts, _ := newTestClient(t)
	resp := mustJSON(t, "POST", ts.URL+"/admin/api/tokens", CreateTokenRequest{
		Label: "alice", Route: "preview",
	})
	var created CreateTokenResponse
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	r := mustJSON(t, "POST", ts.URL+"/admin/api/tokens/"+created.JTI+"/revoke", nil)
	if r.StatusCode != 204 {
		t.Fatalf("revoke status=%d", r.StatusCode)
	}
	r.Body.Close()

	r = mustJSON(t, "POST", ts.URL+"/admin/api/tokens/"+created.JTI+"/unrevoke", nil)
	if r.StatusCode != 204 {
		t.Fatalf("unrevoke status=%d", r.StatusCode)
	}
	r.Body.Close()

	// Revoke a bogus jti → 404
	r = mustJSON(t, "POST", ts.URL+"/admin/api/tokens/no-such/revoke", nil)
	if r.StatusCode != 404 {
		t.Fatalf("bogus revoke status=%d", r.StatusCode)
	}
	r.Body.Close()
}

func TestListRoutes(t *testing.T) {
	ts, _ := newTestClient(t)
	resp := mustJSON(t, "GET", ts.URL+"/admin/api/routes", nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var got ListRoutesResponse
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Routes) != 2 {
		t.Fatalf("count=%d", len(got.Routes))
	}
}

func TestAdminUIServed(t *testing.T) {
	ts, _ := newTestClient(t)
	resp := mustJSON(t, "GET", ts.URL+"/admin", nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type=%q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Speakeasy admin") {
		t.Errorf("admin UI body missing title: %q", string(body)[:min(120, len(body))])
	}
}

func TestMintResponseIncludesQR(t *testing.T) {
	ts, _ := newTestClient(t)
	resp := mustJSON(t, "POST", ts.URL+"/admin/api/tokens", CreateTokenRequest{
		Label: "alice", Route: "preview",
	})
	defer resp.Body.Close()
	var got CreateTokenResponse
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if !strings.HasPrefix(got.QRCode, "data:image/png;base64,") {
		t.Fatalf("qr_code missing/wrong: %.64q", got.QRCode)
	}
}

func TestClientResolveJTI(t *testing.T) {
	ts, _ := newTestClient(t)
	// Create alice.
	r := mustJSON(t, "POST", ts.URL+"/admin/api/tokens", CreateTokenRequest{Label: "alice", Route: "preview"})
	var created CreateTokenResponse
	_ = json.NewDecoder(r.Body).Decode(&created)
	r.Body.Close()

	// Client pointed at the test server (non-socket).
	c := &Client{http: http.DefaultClient, base: ts.URL}

	jti, err := c.ResolveJTI(context.Background(), "alice")
	if err != nil || jti != created.JTI {
		t.Fatalf("resolve by label: jti=%q err=%v want=%q", jti, err, created.JTI)
	}
	jti, err = c.ResolveJTI(context.Background(), created.JTI)
	if err != nil || jti != created.JTI {
		t.Fatalf("resolve by jti: jti=%q err=%v", jti, err)
	}
	if _, err := c.ResolveJTI(context.Background(), "ghost"); err == nil {
		t.Fatal("expected error for unknown label")
	}
}
