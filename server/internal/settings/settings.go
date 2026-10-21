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
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return def, nil
	}
}
