// Package settings reads panel settings from the shared `settings` table.
// Both panels store values JSON-encoded (SettingRepository.php), so the
// accessors here decode them back. This is the Go port of SettingRepository
// plus the accessors SettingsService::get uses for the keys this migration
// needs; the full definition table (SettingsService::definitions) lands with
// the settings-page batch.
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Repository wraps a database handle for reading settings.
type Repository struct{ db *sql.DB }

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

// raw returns the stored (JSON-encoded) value for key and whether the row
// exists.
func (r *Repository) raw(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRecord) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// String returns the setting as a string, or def when the row is absent. A
// stored JSON string comes back unquoted; any other stored scalar comes back
// as its raw text (matching PHP's (string) cast for the keys read here).
func (r *Repository) String(ctx context.Context, key, def string) (string, error) {
	raw, ok, err := r.raw(ctx, key)
	if err != nil {
		return "", err
	}
	if !ok {
		return def, nil
	}
	if strings.HasPrefix(raw, `"`) {
		var s string
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return def, nil
		}
		return s, nil
	}
	return raw, nil
}

// Prefix returns every setting whose key starts with prefix, decoded the way
// String decodes a JSON string. Used by the areas that keep a small keyed
// collection in settings (the MongoDB per-database notes). The settings table
// is small, so the prefix is matched in Go and the LIKE wildcards in a key
// ("_", "%") can never widen the match.
func (r *Repository) Prefix(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if strings.HasPrefix(raw, `"`) {
			var str string
			if err := json.Unmarshal([]byte(raw), &str); err != nil {
				continue
			}
			out[key] = str
			continue
		}
		out[key] = raw
	}
	return out, rows.Err()
}

// Int returns the setting as an integer, or def when the row is absent or not
// a whole number (PHP: (int) cast of the JSON scalar).
func (r *Repository) Int(ctx context.Context, key string, def int) (int, error) {
	raw, ok, err := r.raw(ctx, key)
	if err != nil {
		return def, err
	}
	if !ok {
		return def, nil
	}
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return def, nil
	}
	return int(v), nil
}

// Bool returns the setting as a boolean, or def when the row is absent or not
// a JSON boolean (PHP: (bool) cast).
func (r *Repository) Bool(ctx context.Context, key string, def bool) (bool, error) {
	raw, ok, err := r.raw(ctx, key)
	if err != nil {
		return def, err
	}
	if !ok {
		return def, nil
	}
	switch strings.TrimSpace(raw) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return def, nil
	}
}

// Set persists a setting JSON-encoded (PHP SettingRepository::set), for a
// value matching one of the accessors above (bool, int, string).
func (r *Repository) Set(ctx context.Context, key string, v any, actor int64) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	by := sql.NullInt64{Int64: actor, Valid: actor > 0}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at, updated_by) VALUES (?, ?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at, updated_by = excluded.updated_by`,
		key, string(b), time.Now().UTC().Format(time.RFC3339), by)
	return err
}
