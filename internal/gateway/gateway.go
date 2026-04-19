// Package gateway is the public HTTP(S) surface: route matching, session
// enforcement, and reverse-proxy passthrough to upstream apps.
package gateway

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"speakeasy/internal/config"
	"speakeasy/internal/store"
	"speakeasy/internal/token"
)

const (
	cookieName = "speakeasy_session"

	// reserved public paths (kept in sync with config.reservedPaths)
	pathInvite = "/invite"
	pathEnter  = "/enter"
	pathDenied = "/denied"
	pathHealth = "/health"
	pathAdmin  = "/admin"
)

// Gateway is the main HTTP handler.
type Gateway struct {
	cfg          *config.Config
	store        *store.Store
	signer       *token.Signer
	routes       []*routeHandler
	sessionTTL   time.Duration
	cookieSecure bool
	trustProxy   bool
	version      string
	adminHandler http.Handler // nil = admin panel disabled
}

// SetAdminHandler mounts an admin handler at /admin and /admin/*. If nil,
// those paths fall through to the denied response.
func (g *Gateway) SetAdminHandler(h http.Handler) { g.adminHandler = h }

// New constructs a Gateway from a loaded config + open store/signer.
func New(cfg *config.Config, st *store.Store, s *token.Signer, version string) (*Gateway, error) {
	if cfg == nil || st == nil || s == nil {
		return nil, errors.New("gateway.New: cfg, store, and signer are required")
	}
	g := &Gateway{
		cfg:          cfg,
		store:        st,
		signer:       s,
		sessionTTL:   time.Duration(cfg.Server.SessionTTL) * time.Second,
		cookieSecure: cfg.TLS.Mode != config.TLSModeHTTP || cfg.Server.TrustProxy,
		trustProxy:   cfg.Server.TrustProxy,
		version:      version,
	}
	for _, r := range cfg.Routes {
		rh, err := newRouteHandler(r)
		if err != nil {
			return nil, fmt.Errorf("route %q: %w", r.Name, err)
		}
		g.routes = append(g.routes, rh)
	}
	return g, nil
}

// ServeHTTP dispatches:
//  1. Public endpoints (/health, /invite, /enter, /denied)
//  2. Gated routes (session-verified, proxied)
//  3. Denied anything else
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case pathHealth:
		g.handleHealth(w, r)
		return
	case pathInvite:
		g.handleInvite(w, r)
		return
	case pathEnter:
		g.handleEnter(w, r)
		return
	case pathDenied:
		g.renderDenied(w, http.StatusUnauthorized)
		return
	}

	// Admin panel, if enabled.
	if g.adminHandler != nil && (r.URL.Path == pathAdmin || strings.HasPrefix(r.URL.Path, pathAdmin+"/")) {
		g.adminHandler.ServeHTTP(w, r)
		return
	}

	rh := g.matchRoute(r.URL.Path)
	if rh == nil {
		g.renderDenied(w, http.StatusUnauthorized)
		return
	}

	sess, ok := g.checkSession(w, r, rh.route.Name)
	if !ok {
		g.renderDenied(w, http.StatusUnauthorized)
		return
	}
	_ = sess // reserved for future per-request logging / last_seen updates

	rh.ServeHTTP(w, r)
}

// matchRoute returns the route whose path is a prefix of reqPath. For
// unambiguous matching each route.Path owns itself and its sub-tree — i.e.,
// "/preview" matches /preview, /preview/, and /preview/foo but NOT /previewx.
func (g *Gateway) matchRoute(reqPath string) *routeHandler {
	var best *routeHandler
	for _, r := range g.routes {
		if reqPath == r.route.Path || strings.HasPrefix(reqPath, r.route.Path+"/") {
			// Longest-prefix wins (though route paths today can't nest —
			// validation forbids duplicates and reserved-path shadowing).
			if best == nil || len(r.route.Path) > len(best.route.Path) {
				best = r
			}
		}
	}
	return best
}

// routeByName looks up a configured route by its name field.
func (g *Gateway) routeByName(name string) *routeHandler {
	for _, r := range g.routes {
		if r.route.Name == name {
			return r
		}
	}
	return nil
}

// clientIP returns the client's apparent source IP for logging. When
// trust_proxy is enabled, the leftmost X-Forwarded-For entry wins.
func (g *Gateway) clientIP(r *http.Request) string {
	if g.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if comma := strings.IndexByte(xff, ','); comma >= 0 {
				return strings.TrimSpace(xff[:comma])
			}
			return strings.TrimSpace(xff)
		}
	}
	if colon := strings.LastIndexByte(r.RemoteAddr, ':'); colon >= 0 {
		return r.RemoteAddr[:colon]
	}
	return r.RemoteAddr
}

func (g *Gateway) logf(r *http.Request, format string, args ...any) {
	log.Printf("%s %s %s → "+format,
		append([]any{g.clientIP(r), r.Method, r.URL.Path}, args...)...)
}
