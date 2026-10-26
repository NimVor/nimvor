package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// NewAPIToken is the payload to create a personal API token.
type NewAPIToken struct {
	Name        string
	Ability     string // "read" or "write"
	ExpiresDays int    // 0 = never; the form defaults to 90
}

// APITokenError is a user-facing validation failure.
type APITokenError string

func (e APITokenError) Error() string { return string(e) }

// MaxAPITokens is the per-user cap (ApiTokenService::create).
const MaxAPITokens = 20

// CreateAPIToken inserts a new personal API token, returning the plaintext
// token (shown once) and its row id. Mirrors ApiTokenService::create:
// "whacp_" + 40 hex characters, only sha256 of the whole string is stored.
func (r *Repository) CreateAPIToken(ctx context.Context, userID int64, in NewAPIToken) (string, int64, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 60 {
		return "", 0, APITokenError("Name the token (e.g. the tool that uses it), at most 60 characters.")
	}
	if in.Ability != "read" && in.Ability != "write" {
		return "", 0, APITokenError("Choose read-only or read and write.")
	}
	if in.ExpiresDays < 0 || in.ExpiresDays > 3650 {
		return "", 0, APITokenError("Choose 0 (never) to 3650 days.")
	}
	var have int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE user_id = ?`, userID).Scan(&have); err != nil {
		return "", 0, err
	}
	if have >= MaxAPITokens {
		return "", 0, APITokenError(fmt.Sprintf("You have %d tokens; revoke one first.", MaxAPITokens))
	}
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		return "", 0, err
	}
	token := "whacp_" + hex.EncodeToString(secret)
	sum := sha256.Sum256([]byte(token))
	now := time.Now().UTC()
	var expires any
	if in.ExpiresDays > 0 {
		expires = now.Add(time.Duration(in.ExpiresDays) * 24 * time.Hour).Format(time.RFC3339)
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO api_tokens (user_id, name, token_hash, prefix, ability, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, name, hex.EncodeToString(sum[:]), token[:12], in.Ability, expires, now.Format(time.RFC3339))
	if err != nil {
		return "", 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return "", 0, err
	}
	return token, id, nil
}

// RevokeAPIToken deletes a single personal API token, restricted by owner.
// Mirrors ApiTokenService::revoke. Returns rows-affected.
func (r *Repository) RevokeAPIToken(ctx context.Context, userID, tokenID int64) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, tokenID, userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
