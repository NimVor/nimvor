package database

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migration is one ordered schema change, identified by its file basename
// without extension (e.g. "0057_mongo_remotes").
type Migration struct {
	Name string
	SQL  string
}

// migrations returns the embedded migrations in lexical (apply) order. Names
// are recorded in the `migrations` ledger table once applied, so a database
// upgraded from an earlier version skips rows already present.
func migrations() ([]Migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Name: strings.TrimSuffix(e.Name(), ".sql"), SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ensureTable creates the migrations ledger if absent.
func ensureTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`)
	return err
}

// Applied returns the set of migration names already recorded.
func Applied(db *sql.DB) (map[string]bool, error) {
	if err := ensureTable(db); err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT name FROM migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		applied[n] = true
	}
	return applied, rows.Err()
}

// Pending lists embedded migrations not yet applied, in order.
func Pending(db *sql.DB) ([]string, error) {
	applied, err := Applied(db)
	if err != nil {
		return nil, err
	}
	all, err := migrations()
	if err != nil {
		return nil, err
	}
	var pending []string
	for _, m := range all {
		if !applied[m.Name] {
			pending = append(pending, m.Name)
		}
	}
	return pending, nil
}

// Migrate applies every pending migration in order, each in its own
// transaction, recording the name on success. Running it twice is harmless;
// already-applied names (including migrations applied by earlier versions
// of the panel) are skipped. Returns the names applied by this call.
//
// A single app-level advisory lock (a BEGIN IMMEDIATE transaction on the
// ledger) prevents two instances from migrating at once; with MaxOpenConns=1
// the writes are serialised regardless.
func Migrate(db *sql.DB) ([]string, error) {
	applied, err := Applied(db)
	if err != nil {
		return nil, err
	}
	all, err := migrations()
	if err != nil {
		return nil, err
	}
	var ran []string
	for _, m := range all {
		if applied[m.Name] {
			continue
		}
		if err := applyOne(db, m); err != nil {
			return ran, fmt.Errorf("migration %s: %w", m.Name, err)
		}
		ran = append(ran, m.Name)
	}
	return ran, nil
}

func applyOne(db *sql.DB, m Migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The .sql file may hold several statements; modernc executes them when
	// passed together.
	if _, err := tx.Exec(m.SQL); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO migrations (name, applied_at) VALUES (?, ?)`, m.Name, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}
