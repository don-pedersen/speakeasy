// Package daemon wires together the gateway's long-running process: the store,
// the signing key, the public gateway listener, and the admin unix socket.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"speakeasy/internal/admin"
	"speakeasy/internal/config"
	"speakeasy/internal/gateway"
	"speakeasy/internal/store"
	"speakeasy/internal/token"
)

// Options configures a daemon run. Version is reported by the /admin/api/health
// endpoint.
type Options struct {
	Config  *config.Config
	Version string
}

// Run blocks until ctx is cancelled, then shuts the daemon down gracefully.
func Run(ctx context.Context, opts Options) error {
	cfg := opts.Config
	if cfg == nil {
		return errors.New("daemon.Run: config is required")
	}

	if err := os.MkdirAll(cfg.Server.DataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir %s: %w", cfg.Server.DataDir, err)
	}

	st, err := store.Open(filepath.Join(cfg.Server.DataDir, "speakeasy.db"))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = st.Close() }()

	keyPath := filepath.Join(cfg.Server.DataDir, "signing.key")
	key, err := token.LoadOrGenerateKey(keyPath)
	if err != nil {
		return fmt.Errorf("signing key: %w", err)
	}
	signer, err := token.New(key)
	if err != nil {
		return err
	}

	adminMux := admin.NewMux(admin.Deps{
		Store:   st,
		Signer:  signer,
		Routes:  cfg.Routes,
		Domain:  cfg.Server.Domain,
		Version: opts.Version,
	})

	sockCloser, err := serveUnixSocket(cfg.Server.SocketPath, adminMux)
	if err != nil {
		return err
	}
	defer sockCloser()
	log.Printf("speakeasy %s listening on admin socket %s", opts.Version, cfg.Server.SocketPath)

	// Public gateway handler.
	gw, err := gateway.New(cfg, st, signer, opts.Version)
	if err != nil {
		return fmt.Errorf("gateway: %w", err)
	}

	// Optionally enable the /admin web panel — requires a password hash file.
	if auth, err := loadAdminAuth(cfg); err != nil {
		return fmt.Errorf("admin panel: %w", err)
	} else if auth != nil {
		gw.SetAdminHandler(auth.Wrap(adminMux))
		log.Printf("admin panel enabled at https://%s/admin", cfg.Server.Domain)
	} else {
		log.Printf("admin panel DISABLED: run `speakeasy admin set-password` to enable")
	}

	// Background janitor: expire sessions every minute.
	janitorDone := make(chan struct{})
	go sessionJanitor(ctx, st, janitorDone)

	// Public listener — blocks until ctx is cancelled or a listener errors.
	serveErr := gateway.Serve(ctx, cfg, gw)

	<-janitorDone
	log.Printf("shutdown complete")
	return serveErr
}

// serveUnixSocket binds to path, sets perms, and serves handler in a goroutine.
// The returned closer shuts the HTTP server down and removes the socket file.
func serveUnixSocket(path string, handler http.Handler) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	// Remove any stale socket from a previous unclean shutdown.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o660); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod socket: %w", err)
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("admin socket server error: %v", err)
		}
	}()

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = os.Remove(path)
	}, nil
}

// loadAdminAuth returns a non-nil *admin.BasicAuth when the configured
// password file exists and contains a valid bcrypt hash. A missing file is
// treated as "admin panel disabled" (returns nil, nil). An unreadable or
// malformed file is an error.
func loadAdminAuth(cfg *config.Config) (*admin.BasicAuth, error) {
	path := cfg.Server.AdminPasswordFile
	if path == "" {
		// Default: next to the DB.
		path = filepath.Join(cfg.Server.DataDir, "admin.hash")
	}
	hash, err := admin.LoadHashFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if hash == "" {
		return nil, nil
	}
	return admin.NewBasicAuth(hash)
}

func sessionJanitor(ctx context.Context, st *store.Store, done chan<- struct{}) {
	defer close(done)
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			n, err := st.DeleteExpiredSessions(ctx)
			if err != nil {
				log.Printf("janitor: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("janitor: purged %d expired session(s)", n)
			}
		}
	}
}
