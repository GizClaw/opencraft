// Package userdb owns the user-level SQLite database
// (~/.opencraft/user.db): one connection configured for WAL, shared
// by every user-scoped store (usage, automations). Opening is a
// startup responsibility of the desktop shell, so user-level pages
// work before any workspace or runtime is assembled.
package userdb

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the "sqlite" driver.
)

// DB is the shared user database handle.
type DB struct {
	db *sql.DB
}

// Open opens (creating if necessary) the user database at path and
// applies the shared pragmas: WAL journaling, a busy timeout for
// cross-connection writers, and a single connection so callers never
// contend with each other inside the process.
func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("userdb: create directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("userdb: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("userdb: %s: %w", pragma, err)
		}
	}
	return &DB{db: db}, nil
}

// SQLDB returns the underlying *sql.DB for store constructors.
func (d *DB) SQLDB() *sql.DB {
	return d.db
}

// Close closes the shared handle. Callers must stop every store that
// uses the connection first.
func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}
