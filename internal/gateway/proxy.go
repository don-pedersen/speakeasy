package gateway

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"speakeasy/internal/config"
)

// routeHandler serves one configured route: reverse-proxies to route.Upstream
// and optionally strips route.Path from the forwarded request.
type routeHandler struct {
	route config.Route
	proxy *httputil.ReverseProxy
}

func newRouteHandler(r config.Route) (*routeHandler, error) {
	target, err := url.Parse(r.Upstream)
	if err != nil {
		return nil, fmt.Errorf("parse upstream %q: %w", r.Upstream, err)
	}
	p := httputil.NewSingleHostReverseProxy(target)

	// NewSingleHostReverseProxy sets req.URL.Scheme/Host and joins paths; we
	// layer on top: strip_prefix (optional) and forwarding headers.
	orig := p.Director
	p.Director = func(req *http.Request) {
		orig(req)
		if r.StripPrefix {
			trimmed := strings.TrimPrefix(req.URL.Path, r.Path)
			if !strings.HasPrefix(trimmed, "/") {
				trimmed = "/" + trimmed
			}
			req.URL.Path = trimmed
			req.URL.RawPath = ""
		}
		// Forward the original Host so upstreams that route on Host still work.
		// httputil already sets X-Forwarded-For; we add -Proto and -Host.
		if req.Header.Get("X-Forwarded-Proto") == "" {
			req.Header.Set("X-Forwarded-Proto", "https")
		}
		if req.Header.Get("X-Forwarded-Host") == "" {
			req.Header.Set("X-Forwarded-Host", req.Host)
		}
	}
	// Don't leak Speakeasy's session cookie to the upstream.
	p.ModifyResponse = nil // no-op placeholder for future response rewrites

	return &routeHandler{route: r, proxy: p}, nil
}

func (rh *routeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Strip our session cookie before forwarding so upstreams never see it.
	stripCookie(r, cookieName)
	rh.proxy.ServeHTTP(w, r)
}

// stripCookie removes one cookie by name from the request. Used to avoid
// leaking speakeasy_session to upstreams.
func stripCookie(r *http.Request, name string) {
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cookies {
		if c.Name == name {
			continue
		}
		r.AddCookie(c)
	}
}
