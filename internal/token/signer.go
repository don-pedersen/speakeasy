// Package token mints and verifies Speakeasy access tokens (JWT HS256).
//
// Session tokens are NOT JWTs — they are opaque IDs backed by a sessions row.
// See internal/store for sessions.
package token

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	issuer        = "speakeasy"
	schemaVersion = 1
	minKeyLen     = 32
	defaultKeyLen = 64
)

// Claims is the Speakeasy access-token payload. The JTI is the stable token
// identity and matches the tokens.jti row in storage.
type Claims struct {
	jwt.RegisteredClaims
	Label   string `json:"label,omitempty"`
	Route   string `json:"route"`
	Msg     string `json:"msg,omitempty"`
	Version int    `json:"v,omitempty"`
}

// Signer mints and verifies access tokens using a symmetric key.
type Signer struct {
	key []byte
}

func New(key []byte) (*Signer, error) {
	if len(key) < minKeyLen {
		return nil, fmt.Errorf("signing key must be at least %d bytes", minKeyLen)
	}
	return &Signer{key: key}, nil
}

// Mint signs a new access token. The returned jti is newly generated and should
// be persisted to the tokens table alongside the same label/route/expiry.
func (s *Signer) Mint(label, route, msg string, expires *time.Time) (tokenStr string, jti string, err error) {
	if label == "" || route == "" {
		return "", "", errors.New("Mint: label and route are required")
	}
	jti = newJTI()
	now := time.Now().UTC()

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   issuer,
			Subject:  label,
			ID:       jti,
			IssuedAt: jwt.NewNumericDate(now),
		},
		Label:   label,
		Route:   route,
		Msg:     msg,
		Version: schemaVersion,
	}
	if expires != nil {
		claims.ExpiresAt = jwt.NewNumericDate(expires.UTC())
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(s.key)
	if err != nil {
		return "", "", err
	}
	return signed, jti, nil
}

// Verify parses, validates, and returns the claims. The expiry is checked if
// present; tokens with no exp never expire. Revocation is NOT checked here —
// the caller must look up the JTI against the store.
func (s *Signer) Verify(tokenStr string) (*Claims, error) {
	c := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenStr, c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.key, nil
	},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(issuer),
	)
	if err != nil {
		return nil, err
	}
	if !tok.Valid {
		return nil, errors.New("token invalid")
	}
	if c.Route == "" {
		return nil, errors.New("token missing route claim")
	}
	if c.ID == "" {
		return nil, errors.New("token missing jti")
	}
	return c, nil
}

// newJTI returns a base64url-encoded 128-bit random identifier.
func newJTI() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("crypto/rand: %w", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
