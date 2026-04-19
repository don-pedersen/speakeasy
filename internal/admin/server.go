package admin

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"

	"speakeasy/internal/config"
	"speakeasy/internal/store"
	"speakeasy/internal/token"
)

// Deps are the runtime dependencies the admin API needs.
type Deps struct {
	Store   *store.Store
	Signer  *token.Signer
	Routes  []config.Route
	Domain  string // server.domain, used to build invite URLs
	Version string
}

// NewMux returns an http.Handler wired to the admin endpoints. Auth is the
// caller's responsibility (filesystem perms on the unix socket, basic auth on
// the HTTPS listener).
//
// The "GET /admin" route serves the embedded HTML UI so the same mux works
// both for the CLI (over the unix socket, where the UI is ignored) and the
// HTTPS admin panel.
func NewMux(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin", d.serveUI)
	mux.HandleFunc("GET /admin/", d.serveUI) // trailing-slash variant
	mux.HandleFunc("GET /admin/api/health", d.health)
	mux.HandleFunc("GET /admin/api/routes", d.listRoutes)
	mux.HandleFunc("GET /admin/api/tokens", d.listTokens)
	mux.HandleFunc("POST /admin/api/tokens", d.createToken)
	mux.HandleFunc("POST /admin/api/tokens/{jti}/revoke", d.revokeToken)
	mux.HandleFunc("POST /admin/api/tokens/{jti}/unrevoke", d.unrevokeToken)
	return mux
}

func (d Deps) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{Status: "ok", Version: d.Version})
}

func (d Deps) listRoutes(w http.ResponseWriter, r *http.Request) {
	out := make([]RouteInfo, 0, len(d.Routes))
	for _, rt := range d.Routes {
		out = append(out, RouteInfo{Name: rt.Name, Path: rt.Path, Upstream: rt.Upstream})
	}
	writeJSON(w, http.StatusOK, ListRoutesResponse{Routes: out})
}

func (d Deps) listTokens(w http.ResponseWriter, r *http.Request) {
	rows, err := d.Store.ListTokens(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	now := time.Now()
	out := make([]TokenInfo, 0, len(rows))
	for _, t := range rows {
		out = append(out, TokenInfo{
			JTI: t.JTI, Label: t.Label, Route: t.Route, Msg: t.Msg,
			CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt,
			RevokedAt: t.RevokedAt, LastUsedAt: t.LastUsedAt,
			Active: t.Active(now),
		})
	}
	writeJSON(w, http.StatusOK, ListTokensResponse{Tokens: out})
}

func (d Deps) createToken(w http.ResponseWriter, r *http.Request) {
	var req CreateTokenRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	req.Route = strings.TrimSpace(req.Route)

	if req.Label == "" {
		writeError(w, http.StatusBadRequest, errors.New("label is required"))
		return
	}
	if req.Route == "" {
		writeError(w, http.StatusBadRequest, errors.New("route is required"))
		return
	}
	if !d.routeExists(req.Route) {
		writeError(w, http.StatusBadRequest,
			fmt.Errorf("unknown route %q (valid routes: %s)", req.Route, strings.Join(d.routeNames(), ", ")))
		return
	}

	exp, err := token.ParseExpires(req.Expires, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	signed, jti, err := d.Signer.Mint(req.Label, req.Route, req.Msg, exp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	tk, err := d.Store.CreateToken(r.Context(), store.CreateTokenInput{
		JTI:       jti,
		Label:     req.Label,
		Route:     req.Route,
		Msg:       req.Msg,
		ExpiresAt: exp,
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	inviteURL := d.inviteURL(signed)
	writeJSON(w, http.StatusCreated, CreateTokenResponse{
		JTI:       tk.JTI,
		Token:     signed,
		InviteURL: inviteURL,
		Label:     tk.Label,
		Route:     tk.Route,
		ExpiresAt: tk.ExpiresAt,
		CreatedAt: tk.CreatedAt,
		QRCode:    qrDataURL(inviteURL),
	})
}

// qrDataURL returns a base64 data URL of a PNG QR code for the given content.
// Errors are swallowed — a missing QR is a UI degradation, not a failure.
func qrDataURL(content string) string {
	if content == "" {
		return ""
	}
	png, err := qrcode.Encode(content, qrcode.Medium, 256)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}

func (d Deps) revokeToken(w http.ResponseWriter, r *http.Request) {
	jti := r.PathValue("jti")
	if err := d.Store.RevokeToken(r.Context(), jti); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) unrevokeToken(w http.ResponseWriter, r *http.Request) {
	jti := r.PathValue("jti")
	if err := d.Store.UnrevokeToken(r.Context(), jti); err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			writeError(w, http.StatusConflict, err)
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, err)
		default:
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) routeExists(name string) bool {
	for _, r := range d.Routes {
		if r.Name == name {
			return true
		}
	}
	return false
}

func (d Deps) routeNames() []string {
	out := make([]string, 0, len(d.Routes))
	for _, r := range d.Routes {
		out = append(out, r.Name)
	}
	return out
}

func (d Deps) inviteURL(jwt string) string {
	if d.Domain == "" {
		return ""
	}
	u := url.URL{
		Scheme:   "https",
		Host:     d.Domain,
		Path:     "/invite",
		RawQuery: "token=" + url.QueryEscape(jwt),
	}
	return u.String()
}

// --- helpers ---

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("empty request body")
		}
		return fmt.Errorf("bad json: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, ErrorResponse{Error: err.Error()})
}
