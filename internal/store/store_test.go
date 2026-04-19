package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	p := filepath.Join(t.TempDir(), "speakeasy.db")
	s, err := Open(p)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// fixedClock returns a Store now() that advances only when explicitly told.
func fixedClock(start time.Time) (func() time.Time, func(d time.Duration)) {
	now := start
	return func() time.Time { return now },
		func(d time.Duration) { now = now.Add(d) }
}

func TestMigrationIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "speakeasy.db")

	for i := 0; i < 3; i++ {
		s, err := Open(p)
		if err != nil {
			t.Fatalf("Open #%d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i, err)
		}
	}
}

func TestMigrationRejectsFutureVersion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "speakeasy.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 999`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	_, err = Open(p)
	if err == nil || !errorContains(err, "newer than this binary") {
		t.Fatalf("expected newer-version error, got %v", err)
	}
}

func TestCreateAndGetToken(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	exp := time.Now().Add(24 * time.Hour).UTC()

	tk, err := s.CreateToken(ctx, CreateTokenInput{
		JTI:       "jti-alice",
		Label:     "alice",
		Route:     "client-preview",
		Msg:       "hi alice",
		ExpiresAt: &exp,
	})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if tk.ID == 0 || tk.Label != "alice" || tk.Route != "client-preview" {
		t.Fatalf("unexpected: %+v", tk)
	}
	if !tk.Active(time.Now()) {
		t.Errorf("expected active")
	}

	got, err := s.GetTokenByJTI(ctx, "jti-alice")
	if err != nil {
		t.Fatalf("GetTokenByJTI: %v", err)
	}
	if got.Label != "alice" || got.Msg != "hi alice" {
		t.Errorf("mismatch: %+v", got)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp.Truncate(time.Nanosecond)) {
		t.Errorf("expires_at mismatch: %v vs %v", got.ExpiresAt, exp)
	}

	byLabel, err := s.GetActiveTokenByLabel(ctx, "alice")
	if err != nil || byLabel.ID != tk.ID {
		t.Errorf("GetActiveTokenByLabel: %+v err=%v", byLabel, err)
	}
}

func TestGetTokenNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetTokenByJTI(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestActiveLabelUniqueness(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateToken(ctx, CreateTokenInput{
		JTI: "jti-1", Label: "alice", Route: "r1",
	}); err != nil {
		t.Fatal(err)
	}

	// Second active "alice" must conflict.
	_, err := s.CreateToken(ctx, CreateTokenInput{
		JTI: "jti-2", Label: "alice", Route: "r1",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}

	// After revoking the first, re-minting must succeed.
	if err := s.RevokeToken(ctx, "jti-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateToken(ctx, CreateTokenInput{
		JTI: "jti-2", Label: "alice", Route: "r1",
	}); err != nil {
		t.Fatalf("re-mint after revoke: %v", err)
	}
}

func TestRevokeTokenRevokesSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk, err := s.CreateToken(ctx, CreateTokenInput{JTI: "j", Label: "bob", Route: "r"})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.CreateSession(ctx, tk.ID, tk.Route, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.RevokeToken(ctx, "j"); err != nil {
		t.Fatal(err)
	}

	_, err = s.GetSession(ctx, sess.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("session should be gone, got %v", err)
	}
}

func TestRevokeTokenNotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.RevokeToken(context.Background(), "no-such")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRevokeThenUnrevokeConflict(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// Create, revoke, create again with same label, then try to unrevoke the first.
	if _, err := s.CreateToken(ctx, CreateTokenInput{JTI: "j1", Label: "c", Route: "r"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeToken(ctx, "j1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateToken(ctx, CreateTokenInput{JTI: "j2", Label: "c", Route: "r"}); err != nil {
		t.Fatal(err)
	}

	err := s.UnrevokeToken(ctx, "j1")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestUnrevokeBringsTokenBack(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.CreateToken(ctx, CreateTokenInput{JTI: "j", Label: "d", Route: "r"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeToken(ctx, "j"); err != nil {
		t.Fatal(err)
	}
	if err := s.UnrevokeToken(ctx, "j"); err != nil {
		t.Fatal(err)
	}
	tk, err := s.GetTokenByJTI(ctx, "j")
	if err != nil || tk.RevokedAt != nil {
		t.Fatalf("expected active after unrevoke, got %+v err=%v", tk, err)
	}
}

func TestListTokensOrderedByCreatedDesc(t *testing.T) {
	s := newTestStore(t)
	now, advance := fixedClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	s.SetNow(now)
	ctx := context.Background()

	if _, err := s.CreateToken(ctx, CreateTokenInput{JTI: "1", Label: "a", Route: "r"}); err != nil {
		t.Fatal(err)
	}
	advance(time.Second)
	if _, err := s.CreateToken(ctx, CreateTokenInput{JTI: "2", Label: "b", Route: "r"}); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Label != "b" || list[1].Label != "a" {
		t.Fatalf("unexpected order: %+v", list)
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk, err := s.CreateToken(ctx, CreateTokenInput{JTI: "j", Label: "e", Route: "rr"})
	if err != nil {
		t.Fatal(err)
	}

	sess, err := s.CreateSession(ctx, tk.ID, tk.Route, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" || sess.Route != "rr" {
		t.Fatalf("bad session: %+v", sess)
	}

	got, err := s.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenID != tk.ID {
		t.Fatalf("token id mismatch")
	}

	before := got.LastSeenAt
	time.Sleep(5 * time.Millisecond)
	if err := s.TouchSessionLastSeen(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSession(ctx, sess.ID)
	if !got.LastSeenAt.After(before) {
		t.Errorf("LastSeenAt did not advance: before=%v after=%v", before, got.LastSeenAt)
	}

	if err := s.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.GetSession(ctx, sess.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	s := newTestStore(t)
	now, advance := fixedClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	s.SetNow(now)
	ctx := context.Background()

	tk, err := s.CreateToken(ctx, CreateTokenInput{JTI: "j", Label: "f", Route: "r"})
	if err != nil {
		t.Fatal(err)
	}
	short, err := s.CreateSession(ctx, tk.ID, tk.Route, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	long, err := s.CreateSession(ctx, tk.ID, tk.Route, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	advance(5 * time.Minute)
	n, err := s.DeleteExpiredSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("deleted=%d, want 1", n)
	}
	if _, err := s.GetSession(ctx, short.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("short should be gone")
	}
	if _, err := s.GetSession(ctx, long.ID); err != nil {
		t.Errorf("long should still exist, got %v", err)
	}
}

func TestTouchTokenLastUsed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk, err := s.CreateToken(ctx, CreateTokenInput{JTI: "j", Label: "g", Route: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.LastUsedAt != nil {
		t.Errorf("LastUsedAt should be nil at creation")
	}
	if err := s.TouchTokenLastUsed(ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetTokenByJTI(ctx, "j")
	if got.LastUsedAt == nil {
		t.Errorf("expected LastUsedAt set")
	}
}

func errorContains(err error, substr string) bool {
	return err != nil && (err.Error() == substr || containsSubstring(err.Error(), substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
