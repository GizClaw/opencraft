// Package db owns SQLite connection scaffolding shared by user-level
// stores: one connection configured for WAL and a busy timeout.
// It deliberately owns no tables; internal/foundation/compat defines and
// executes every schema on the handle.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/GizClaw/flowcraft/core/telemetry"

	_ "modernc.org/sqlite" // registers the "sqlite" driver.
)

// DB is the shared user database handle.
type DB struct {
	db *sql.DB
}

// OpenOptions configures one SQLite connection.
type OpenOptions struct {
	// ForeignKeys enables PRAGMA foreign_keys=ON. User-scoped
	// databases enable it from the start; workspace session databases
	// open without it so cleanup migrations can drop legacy parent
	// tables, and internal/foundation/compat re-enables enforcement
	// once the schema is clean (see SetForeignKeys).
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
		// The busy timeout comes first, so it covers the pragma after
		// it — the one that can collide with a handle being closed.
		// Opening the WAL (which is what switching to WAL mode does,
		// and what a fresh connection to a WAL database does as well)
		// takes the database's exclusive lock, and a connection being
		// closed holds exactly that lock for the length of its
		// sqlite3WalClose: it unlinks the -wal and -shm files under
		// it. This process reaches that state routinely, because a
		// store handle outlives its last caller by however long the
		// detached search-backfill walk of the previous open takes to
		// unwind, and a retiring Host's release does it in another
		// goroutine. Without the timeout in front, the next open fails
		// on the spot with "database is locked" — measured as a coin
		// toss on an application page reading its conversations right
		// after the runtime was torn down.
		"PRAGMA busy_timeout=5000",
		"PRAGMA journal_mode=WAL",
		// WAL + NORMAL is the combination SQLite documents as safe:
		// a commit still writes the WAL before it is acknowledged, so
		// a crash cannot corrupt the database — the worst case is
		// losing the last transactions that had not reached a
		// checkpoint when the machine lost power. It drops the second
		// fsync per commit that FULL pays (WAL frame + directory), and
		// the turn-end archive+memory transaction is exactly the write
		// path that pays it on every turn.
		"PRAGMA synchronous=NORMAL",
	}
	if opts.ForeignKeys {
		pragmas = append(pragmas, "PRAGMA foreign_keys=ON")
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			telemetry.WarnErr(context.Background(),
				"userdb: close database after pragma failure", db.Close())
			return nil, fmt.Errorf("userdb: %s: %w", pragma, err)
		}
	}
	return &DB{db: db}, nil
}

// SQLDB returns the underlying *sql.DB for store constructors.
func (d *DB) SQLDB() *sql.DB {
	return d.db
}

// SetForeignKeys toggles PRAGMA foreign_keys on this connection.
// Workspace handles open without enforcement so legacy cleanup
// migrations can drop parent tables; callers switch enforcement back
// on after the schema no longer references dropped tables.
func (d *DB) SetForeignKeys(on bool) error {
	value := "OFF"
	if on {
		value = "ON"
	}
	if _, err := d.db.Exec("PRAGMA foreign_keys=" + value); err != nil {
		return fmt.Errorf("userdb: PRAGMA foreign_keys=%s: %w", value, err)
	}
	return nil
}

// Close closes the shared handle. Callers must stop every store that
// uses the connection first.
func (d *DB) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}
