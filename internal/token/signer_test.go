package token

import (
	"crypto/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 64)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestMintAndVerifyRoundTrip(t *testing.T) {
	k := newKey(t)
	s, err := New(k)
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(24 * time.Hour)
	tok, jti, err := s.Mint("alice", "client-preview", "hi", &exp)
	if err != nil {
		t.Fatal(err)
	}
	if tok == "" || jti == "" {
		t.Fatal("empty token or jti")
	}

	c, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if c.Label != "alice" || c.Route != "client-preview" || c.Msg != "hi" {
		t.Errorf("claims mismatch: %+v", c)
	}
	if c.ID != jti {
		t.Errorf("jti mismatch: %s vs %s", c.ID, jti)
	}
	if c.Version != schemaVersion {
		t.Errorf("version: %d", c.Version)
	}
}

func TestMintRejectsEmptyArgs(t *testing.T) {
	s, _ := New(newKey(t))
	if _, _, err := s.Mint("", "r", "", nil); err == nil {
		t.Error("expected error for empty label")
	}
	if _, _, err := s.Mint("a", "", "", nil); err == nil {
		t.Error("expected error for empty route")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	s, _ := New(newKey(t))
	exp := time.Now().Add(-time.Hour)
	tok, _, _ := s.Mint("alice", "r", "", &exp)
	_, err := s.Verify(tok)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("want expired error, got %v", err)
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	s1, _ := New(newKey(t))
	tok, _, _ := s1.Mint("a", "r", "", nil)

	s2, _ := New(newKey(t))
	if _, err := s2.Verify(tok); err == nil {
		t.Fatal("expected verification error with wrong key")
	}
}

func TestVerifyRejectsTampered(t *testing.T) {
	s, _ := New(newKey(t))
	tok, _, _ := s.Mint("a", "r", "", nil)
	// Flip a character in the payload section.
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("malformed token: %s", tok)
	}
	parts[1] = parts[1][:len(parts[1])-1] + "A"
	tampered := strings.Join(parts, ".")
	if _, err := s.Verify(tampered); err == nil {
		t.Fatal("expected verify to fail on tampered token")
	}
}

func TestNonExpiringToken(t *testing.T) {
	s, _ := New(newKey(t))
	tok, _, _ := s.Mint("a", "r", "", nil)
	c, err := s.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.ExpiresAt != nil {
		t.Errorf("expected no expiry, got %v", c.ExpiresAt)
	}
}

func TestNewRejectsShortKey(t *testing.T) {
	if _, err := New(make([]byte, 16)); err == nil {
		t.Fatal("expected rejection of short key")
	}
}

func TestLoadOrGenerateKeyCreates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "signing.key")
	k1, err := LoadOrGenerateKey(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(k1) < minKeyLen {
		t.Fatalf("short key: %d", len(k1))
	}
	k2, err := LoadOrGenerateKey(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(k1) != string(k2) {
		t.Fatal("key changed between loads")
	}
}
