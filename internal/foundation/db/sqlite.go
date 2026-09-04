// Package db owns SQLite connection scaffolding shared by user-level
// stores: one connection configured for WAL and a busy timeout.
// It deliberately owns no tables; orchestration/migrations defines and
// executes every schema on the handle.
package db

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

// OpenOptions configures one SQLite connection.
type OpenOptions struct {
	// ForeignKeys enables PRAGMA foreign_keys=ON. User-scoped
	// databases enable it; legacy workspace schemas keep it off until
	// their cleanup migration removes dangling references.
	ForeignKeys bool
}

// Open opens (creating if necessary) the database at path with the
// default options (foreign keys enabled).
func Open(path string) (*DB, error) {
	return OpenWithOptions(path, OpenOptions{ForeignKeys: true})
}

// OpenWithOptions opens a database and applies shared pragmas: WAL
// journaling, a busy timeout, and a single connection so callers never
// contend with each other inside the process.
func OpenWithOptions(path string, opts OpenOptions) (*DB, error) {
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
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
	}
	if opts.ForeignKeys {
		pragmas = append(pragmas, "PRAGMA foreign_keys=ON")
	}
	for _, pragma := range pragmas {
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
