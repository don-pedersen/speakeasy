package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"speakeasy/internal/config"
)

// Serve starts the public listener(s) and blocks until ctx is cancelled, at
// which point it shuts servers down gracefully and returns.
//
// Three modes:
//
//	autocert: HTTPS on https_port, Let's Encrypt via http_port (ACME + redirect)
//	files:    HTTPS on https_port with supplied cert/key, redirect on http_port
//	http:     plain HTTP on http_port (dev or behind a TLS-terminating proxy)
func Serve(ctx context.Context, cfg *config.Config, handler http.Handler) error {
	switch cfg.TLS.Mode {
	case config.TLSModeAutocert:
		return serveAutocert(ctx, cfg, handler)
	case config.TLSModeFiles:
		return serveFiles(ctx, cfg, handler)
	case config.TLSModeHTTP:
		return serveHTTP(ctx, cfg, handler)
	default:
		return fmt.Errorf("unknown tls mode %q", cfg.TLS.Mode)
	}
}

func serveAutocert(ctx context.Context, cfg *config.Config, handler http.Handler) error {
	mgr := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(cfg.Server.Domain),
		Cache:      autocert.DirCache(cfg.TLS.CacheDir),
	}
	httpsSrv := newHTTPSServer(cfg, handler, mgr.TLSConfig())
	// autocert.Manager.HTTPHandler wraps its ACME handler in front of whatever
	// we pass for non-challenge traffic. Passing nil gives an HTTP->HTTPS
	// redirect; with trust_proxy we still want the redirect when we're
	// serving port 80 directly (tunnel users typically point the tunnel at
	// the HTTPS port, so this only runs on bare-metal setups).
	httpSrv := newHTTPServer(cfg, mgr.HTTPHandler(nil))

	log.Printf("gateway: autocert on :%d (+ACME/redirect on :%d) for %s",
		cfg.Server.HTTPSPort, cfg.Server.HTTPPort, cfg.Server.Domain)
	return runBoth(ctx, httpsSrv, httpSrv, true)
}

func serveFiles(ctx context.Context, cfg *config.Config, handler http.Handler) error {
	cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
	if err != nil {
		return fmt.Errorf("load keypair: %w", err)
	}
	httpsSrv := newHTTPSServer(cfg, handler, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	var httpSrv *http.Server
	if !cfg.Server.TrustProxy {
		httpSrv = newHTTPServer(cfg, redirectHandler(cfg))
	}
	log.Printf("gateway: tls files on :%d for %s", cfg.Server.HTTPSPort, cfg.Server.Domain)
	return runBoth(ctx, httpsSrv, httpSrv, true)
}

func serveHTTP(ctx context.Context, cfg *config.Config, handler http.Handler) error {
	if !cfg.Server.TrustProxy {
		log.Printf("gateway: WARNING — tls.mode = %q and trust_proxy = false; cookies will be served without the Secure flag (dev only)",
			config.TLSModeHTTP)
	}
	httpSrv := newHTTPServer(cfg, handler)
	log.Printf("gateway: plain HTTP on :%d for %s (trust_proxy=%v)",
		cfg.Server.HTTPPort, cfg.Server.Domain, cfg.Server.TrustProxy)
	return runBoth(ctx, nil, httpSrv, false)
}

// --- helpers ---

func newHTTPSServer(cfg *config.Config, h http.Handler, tlsCfg *tls.Config) *http.Server {
	return &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Server.HTTPSPort),
		Handler:           h,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

func newHTTPServer(cfg *config.Config, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Server.HTTPPort),
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

func redirectHandler(cfg *config.Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := "https://" + cfg.Server.Domain
		if cfg.Server.HTTPSPort != 443 {
			target += ":" + strconv.Itoa(cfg.Server.HTTPSPort)
		}
		target += r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}

// runBoth manages the lifecycle of up to two http.Servers. httpsSrv may be nil
// (plain-HTTP mode); httpSrv may be nil (HTTPS-only when trust_proxy = true).
func runBoth(ctx context.Context, httpsSrv, httpSrv *http.Server, hasTLS bool) error {
	errCh := make(chan error, 2)

	if httpsSrv != nil {
		go func() {
			var err error
			if hasTLS {
				err = httpsSrv.ListenAndServeTLS("", "")
			} else {
				err = httpsSrv.ListenAndServe()
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("https: %w", err)
			}
		}()
	}
	if httpSrv != nil {
		go func() {
			if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("http: %w", err)
			}
		}()
	}

	select {
	case <-ctx.Done():
	case err := <-errCh:
		// Listener failed early; attempt orderly shutdown of the other and return.
		shutdown(httpsSrv, httpSrv)
		return err
	}

	shutdown(httpsSrv, httpSrv)
	return nil
}

func shutdown(servers ...*http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		if s == nil {
			continue
		}
		_ = s.Shutdown(ctx)
	}
}
