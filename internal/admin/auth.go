package admin

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/crypto/bcrypt"
)

const (
	adminUser  = "admin"
	bcryptCost = 12
)

// BasicAuth wraps a handler with HTTP Basic Auth against a bcrypt hash.
// Username is hardcoded to "admin"; password is compared constant-time against
// the hash.
type BasicAuth struct {
	hash []byte
}

// NewBasicAuth accepts a bcrypt hash string as stored in the password file.
func NewBasicAuth(hash string) (*BasicAuth, error) {
	hash = string(bytes.TrimSpace([]byte(hash)))
	if hash == "" {
		return nil, errors.New("admin password hash is empty")
	}
	// Smoke test the hash format: bcrypt.Cost returns the cost parameter
	// encoded in the hash, and errors if the input isn't a valid hash.
	if _, err := bcrypt.Cost([]byte(hash)); err != nil {
		return nil, fmt.Errorf("admin password hash is not a valid bcrypt hash: %w", err)
	}
	return &BasicAuth{hash: []byte(hash)}, nil
}

// HashPath returns the resolved admin hash file path. If override is set
// (typically from server.admin_password_file or a CLI --file flag) it wins;
// otherwise the file lives next to the rest of the daemon's state in dataDir.
// Both the CLI writer and the daemon reader call this so their defaults
// can't drift out of sync.
func HashPath(dataDir, override string) string {
	if override != "" {
		return override
	}
	return filepath.Join(dataDir, "admin.hash")
}

// LoadHashFile reads a bcrypt hash from a file, returning its contents.
// Trailing whitespace is trimmed.
func LoadHashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(data)), nil
}

// WriteHashFile hashes the plaintext password with bcrypt and writes it to
// path with mode 0600.
func WriteHashFile(path, password string) error {
	if password == "" {
		return errors.New("password must not be empty")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return fmt.Errorf("bcrypt: %w", err)
	}
	return os.WriteFile(path, append(hash, '\n'), 0o600)
}

// Wrap returns a handler that requires Basic Auth on all requests before
// delegating to next.
func (a *BasicAuth) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok {
			a.challenge(w)
			return
		}
		// Compare the username constant-time too — not strictly necessary
		// since it's fixed, but keeps the code uniform.
		if subtle.ConstantTimeCompare([]byte(user), []byte(adminUser)) != 1 {
			a.challenge(w)
			return
		}
		if err := bcrypt.CompareHashAndPassword(a.hash, []byte(pass)); err != nil {
			a.challenge(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *BasicAuth) challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="speakeasy admin", charset="UTF-8"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}
