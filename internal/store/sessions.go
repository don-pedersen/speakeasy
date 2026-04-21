package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Session is a server-side record backing a browser cookie.
// The cookie value is the Session.ID.
type Session struct {
	ID         string
	TokenID    int64
	Route      string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

// CreateSession issues a new session bound to the given active token. The
// session ID is generated from crypto/rand and returned inside Session.
func (s *Store) CreateSession(ctx context.Context, tokenID int64, route string, ttl time.Duration) (*Session, error) {
	if route == "" {
		return nil, errors.New("CreateSession: route is required")
	}
	now := s.now()
	sess := &Session{
		ID:         newID(),
		TokenID:    tokenID,
		Route:      route,
		CreatedAt:  now,
		ExpiresAt:  now.Add(ttl),
		LastSeenAt: now,
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, token_id, route, created_at, expires_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.TokenID, sess.Route,
		timeArg(sess.CreatedAt), timeArg(sess.ExpiresAt), timeArg(sess.LastSeenAt),
	)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

const sessionCols = `id, token_id, route, created_at, expires_at, last_seen_at`

func scanSession(row interface {
	Scan(...any) error
}) (*Session, error) {
	var (
		s        Session
		created  string
		expires  string
		lastSeen string
	)
	if err := row.Scan(&s.ID, &s.TokenID, &s.Route, &created, &expires, &lastSeen); err != nil {
		return nil, err
	}
	var err error
	if s.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	if s.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires); err != nil {
		return nil, fmt.Errorf("parse expires_at: %w", err)
	}
	if s.LastSeenAt, err = time.Parse(time.RFC3339Nano, lastSeen); err != nil {
		return nil, fmt.Errorf("parse last_seen_at: %w", err)
	}
	s.CreatedAt = s.CreatedAt.UTC()
	s.ExpiresAt = s.ExpiresAt.UTC()
	s.LastSeenAt = s.LastSeenAt.UTC()
	return &s, nil
}

func (s *Store) GetSession(ctx context.Context, id string) (*Session, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+sessionCols+` FROM sessions WHERE id = ?`, id)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sess, err
}

// TouchSessionLastSeen updates last_seen_at on a session.
func (s *Store) TouchSessionLastSeen(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = ? WHERE id = ?`,
		timeArg(s.now()), id)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// DeleteExpiredSessions removes sessions whose expires_at is <= now.
// Intended to run on a background ticker; returns the number removed.
func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ?`, timeArg(s.now()))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
