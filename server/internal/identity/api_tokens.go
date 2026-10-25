package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// NewAPIToken is the payload to create a personal API token.
type NewAPIToken struct {
	Name    string
	Ability string // "read" or "write"
}

// CreateAPIToken inserts a new personal API token, returning the plaintext
// token (shown once) and its row id. Mirrors ApiTokenService::create.
// The token format is "whcp_" + 8 prefix chars + "_" + 40 hex secret chars;
// only sha256(secret) is stored.
func (r *Repository) CreateAPIToken(ctx context.Context, userID int64, in NewAPIToken) (string, int64, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return "", 0, fmt.Errorf("name is required")
	}
	ability := in.Ability
	if ability != "write" {
		ability = "read"
	}
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		return "", 0, err
	}
	token := hex.EncodeToString(secret) // 40 hex chars
	prefix := token[:8]
	full := "whcp_" + prefix + "_" + token
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO api_tokens (user_id, name, token_hash, prefix, ability, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, NULL, ?)`,
		userID, name, hash, prefix, ability, now)
	if err != nil {
		return "", 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return "", 0, err
	}
	return full, id, nil
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
