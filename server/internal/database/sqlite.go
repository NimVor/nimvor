// Package database opens the WhaCP SQLite database and applies migrations,
// preserving the exact behaviour of the PHP panel's Connection and Migrator so
// an existing installation upgrades in place.
package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, single static binary
)

// Open opens (or creates) the SQLite database at path with the same PRAGMAs the
// PHP panel sets: foreign keys on, a 5s busy timeout, WAL journalling and
// NORMAL synchronous. A file database is created 0640 in a 0750 directory.
func Open(path string) (*sql.DB, error) {
	memory := path == ":memory:"
	if !memory {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("database directory: %w", err)
		}
	}
	// modernc accepts PRAGMAs as connection parameters so every pooled
	// connection gets them, not just the first.
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	if !memory {
		dsn += "&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite is a single writer; keep a small, serialised pool to avoid
	// "database is locked" under concurrency (the busy_timeout still applies).
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if !memory {
		_ = os.Chmod(path, 0o640)
	}
	return db, nil
}
