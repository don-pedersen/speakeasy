package token

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LoadOrGenerateKey reads a signing key from path. If the file does not exist,
// it generates defaultKeyLen random bytes, writes them with mode 0600, and
// returns them. The parent directory is created with mode 0700 if missing.
func LoadOrGenerateKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(data) < minKeyLen {
			return nil, fmt.Errorf("%s: key is %d bytes, need at least %d", path, len(data), minKeyLen)
		}
		return data, nil
	case errors.Is(err, os.ErrNotExist):
		// fall through to generate
	default:
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	key := make([]byte, defaultKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("crypto/rand: %w", err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return key, nil
}
