package store

import (
	"database/sql"
	"fmt"
)

// migrations is an append-only list. Each entry is one schema version; the
// statements run inside a transaction, and PRAGMA user_version is bumped on
// success. Never reorder or mutate a released migration — add a new one.
var migrations = [][]string{
	// v1: tokens + sessions.
	{
		`CREATE TABLE tokens (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			jti          TEXT    NOT NULL UNIQUE,
			label        TEXT    NOT NULL,
			route        TEXT    NOT NULL,
			msg          TEXT    NOT NULL DEFAULT '',
			created_at   TEXT    NOT NULL,
			expires_at   TEXT,
			revoked_at   TEXT,
			last_used_at TEXT
		)`,
		// One active token per label; revoked tokens retain history without blocking re-issuance.
		`CREATE UNIQUE INDEX tokens_label_active
			ON tokens(label) WHERE revoked_at IS NULL`,
		`CREATE INDEX tokens_route ON tokens(route)`,

		`CREATE TABLE sessions (
			id           TEXT    PRIMARY KEY,
			token_id     INTEGER NOT NULL REFERENCES tokens(id) ON DELETE CASCADE,
			route        TEXT    NOT NULL,
			created_at   TEXT    NOT NULL,
			expires_at   TEXT    NOT NULL,
			last_seen_at TEXT    NOT NULL
		)`,
		`CREATE INDEX sessions_expires ON sessions(expires_at)`,
		`CREATE INDEX sessions_token   ON sessions(token_id)`,
	},
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf("database version %d is newer than this binary (%d)", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		for _, stmt := range migrations[i] {
			if _, err := tx.Exec(stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("migration v%d: %w", i+1, err)
			}
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("bump user_version to %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
