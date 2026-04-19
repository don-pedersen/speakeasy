// Package store is the persistence layer: SQLite-backed tokens and sessions.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open opens (or creates) a SQLite database at path and applies migrations.
// Use ":memory:" for an in-process database (tests only — single connection).
func Open(path string) (*Store, error) {
	dsn := buildDSN(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// :memory: databases are per-connection — pin to a single conn so tests
	// don't see "table missing" between pooled connections.
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// SetNow overrides the time source. Tests only.
func (s *Store) SetNow(fn func() time.Time) {
	s.now = fn
}

func buildDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(on)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	return "file:" + path + "?" + q.Encode()
}

// newID returns a 32-byte random identifier encoded as unpadded base64url (43 chars).
func newID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("crypto/rand: %w", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// nullableTime scans a TEXT/NULL column into *time.Time.
func nullableTime(src sql.NullString) (*time.Time, error) {
	if !src.Valid {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, src.String)
	if err != nil {
		return nil, fmt.Errorf("parse time %q: %w", src.String, err)
	}
	t = t.UTC()
	return &t, nil
}

func nullableTimeArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func timeArg(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
