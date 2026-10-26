package auth

// Package auth: API Bearer-token authenticator for /api/v1 (ApiAuthenticate.php).
//
// A personal API token (profile page) authenticates REST calls exactly as
// its owning user does: the token's ability ('read' for GET/HEAD, 'write' for
// mutating methods) gates writes, and the user's permissions/ownership scope
// apply unchanged. Responses are JSON only — {success: true, data} or
// {success: false, error: {code, message, ...}}. The rate limiter
// (120 req/min/token) is stubbed; key lookups are already bounded by the
// unique token_hash index.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// apiTokenRe is the token format ApiTokenService::authenticate accepts.
var apiTokenRe = regexp.MustCompile(`^whacp_[a-f0-9]{40}$`)

// APIRatePerMinute is the per-token request budget (ApiTokenService::RATE_PER_MINUTE).
const APIRatePerMinute = 120

// Hit counts one request against the token's current one-minute window and
// reports whether it is still within the budget (ApiTokenService::hit).
func (a *APIAuthenticator) Hit(ctx context.Context, tokenID int64) (bool, error) {
	window := time.Now().Unix() / 60
	if _, err := a.db.ExecContext(ctx,
		`INSERT INTO api_rate (token_id, window, hits) VALUES (?, ?, 1)
		 ON CONFLICT (token_id, window) DO UPDATE SET hits = hits + 1`, tokenID, window); err != nil {
		return false, err
	}
	var hits int
	if err := a.db.QueryRowContext(ctx, `SELECT hits FROM api_rate WHERE token_id = ? AND window = ?`, tokenID, window).Scan(&hits); err != nil {
		return false, err
	}
	if window%50 == 0 { // occasional cleanup instead of a timer
		_, _ = a.db.ExecContext(ctx, `DELETE FROM api_rate WHERE window < ?`, window-5)
	}
	return hits <= APIRatePerMinute, nil
}

type ctxKey int

const (
	ctxAPIUser ctxKey = iota + 20
	ctxAPIToken
)

// APITokenRow is the api_tokens row that authorised a request. Ability is
// "read" (GET/HEAD only) or "write" (all methods).
type APITokenRow struct {
	ID        int64
	UserID    int64
	Name      string
	Prefix    string
	Ability   string
	ExpiresAt sql.NullString
}

// APIAuthenticator looks up tokens from the Authorization header.
type APIAuthenticator struct {
	users *identity.Repository
	db    *sql.DB
}

// NewAPIAuthenticator builds the authenticator.
func NewAPIAuthenticator(db *sql.DB, users *identity.Repository) *APIAuthenticator {
	return &APIAuthenticator{db: db, users: users}
}

// Authenticate parses the Bearer token, loads the user + token, and returns
// them. On failure it returns a StatusError suitable for writeJSON.
func (a *APIAuthenticator) Authenticate(r *http.Request) (*identity.User, *APITokenRow, *StatusError) {
	h := r.Header.Get("Authorization")
	if h == "" || !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return nil, nil, &StatusError{Code: http.StatusUnauthorized, ErrCode: "unauthenticated", Message: "Missing Bearer token. Use Authorization: Bearer whacp_…."}
	}
	token := strings.TrimSpace(h[7:])
	if token == "" {
		return nil, nil, &StatusError{Code: http.StatusUnauthorized, ErrCode: "unauthenticated", Message: "Empty Bearer token."}
	}
	if !apiTokenRe.MatchString(token) {
		return nil, nil, &StatusError{Code: http.StatusUnauthorized, ErrCode: "invalid_token", Message: "Unknown or revoked token."}
	}
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	var (
		tok APITokenRow
	)
	err := a.db.QueryRowContext(r.Context(),
		`SELECT id, user_id, name, prefix, ability, expires_at FROM api_tokens WHERE token_hash = ?`, hash).
		Scan(&tok.ID, &tok.UserID, &tok.Name, &tok.Prefix, &tok.Ability, &tok.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, &StatusError{Code: http.StatusUnauthorized, ErrCode: "invalid_token", Message: "Unknown or revoked token."}
	}
	if err != nil {
		return nil, nil, &StatusError{Code: http.StatusInternalServerError, ErrCode: "internal", Message: "Token lookup failed."}
	}
	if tok.ExpiresAt.Valid {
		if t, err := time.Parse(time.RFC3339, tok.ExpiresAt.String); err == nil && t.Before(time.Now()) {
			return nil, nil, &StatusError{Code: http.StatusUnauthorized, ErrCode: "token_expired", Message: "Token expired."}
		}
	}
	u, err := a.users.Find(r.Context(), tok.UserID)
	if err != nil || u == nil || !identity.CanAct(u) {
		return nil, nil, &StatusError{Code: http.StatusUnauthorized, ErrCode: "user_inactive", Message: "Token's account is inactive or suspended."}
	}
	// Best-effort last_used update; never fail the request.
	ip := clientIP(r)
	_, _ = a.db.ExecContext(r.Context(),
		`UPDATE api_tokens SET last_used_at = ?, last_used_ip = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), ip, tok.ID)
	return u, &tok, nil
}

// WithAPIContext returns a request with the authenticated user and token set.
func WithAPIContext(r *http.Request, u *identity.User, t *APITokenRow) *http.Request {
	ctx := context.WithValue(r.Context(), ctxAPIUser, u)
	ctx = context.WithValue(ctx, ctxAPIToken, t)
	return r.WithContext(ctx)
}

// APIUserFrom returns the user authenticated by the API token (or nil).
func APIUserFrom(ctx context.Context) *identity.User {
	u, _ := ctx.Value(ctxAPIUser).(*identity.User)
	return u
}

// APITokenFrom returns the token that authorised the request (or nil).
func APITokenFrom(ctx context.Context) *APITokenRow {
	t, _ := ctx.Value(ctxAPIToken).(*APITokenRow)
	return t
}

// StatusError is a JSON-API error envelope.
type StatusError struct {
	Code    int
	ErrCode string
	Message string
	Details map[string]any
}

func (e *StatusError) Error() string { return e.Message }

// clientIP returns the remote address (no trusted-proxy handling — same as
// the web middleware for now).
func clientIP(r *http.Request) string {
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i]
	}
	return addr
}
