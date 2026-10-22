// Package notify reads the panel's notifications with per-recipient
// visibility, ported from the read path of Services\NotificationService.
package notify

import (
	"context"
	"database/sql"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// Notification is one row for the list, with the related website domain and
// whether the current viewer has read it.
type Notification struct {
	ID        int64
	Kind      string
	Severity  string
	Title     string
	Body      string
	Link      string
	CreatedAt string
	Domain    sql.NullString
	IsRead    bool
}

// Repository reads notifications.
type Repository struct{ db *sql.DB }

// NewRepository wraps a database handle.
func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

// visibility returns the SQL predicate (on alias n) selecting what the user may
// see, with its positional args. Mirrors NotificationService::visibility: staff
// see everything; an own-scope user sees notifications for a website they may
// see, or ones owned within their scope. Derived only from the principal.
func visibility(u *identity.User) (string, []any) {
	if hosting.IsAll(u) {
		return "1 = 1", nil
	}
	sites, sp := hosting.Condition(u, "w.owner_id")
	owners, op := hosting.Condition(u, "n.owner_id")
	cond := "((n.website_id IS NOT NULL AND n.website_id IN (SELECT w.id FROM websites w WHERE " + sites + "))" +
		" OR (n.website_id IS NULL AND n.owner_id IS NOT NULL AND " + owners + "))"
	return cond, append(sp, op...)
}

// List returns the newest notifications the user may see, with read state for
// that user. Mirrors NotificationService::list (without the optional filters).
func (r *Repository) List(ctx context.Context, u *identity.User, limit int) ([]Notification, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	cond, vargs := visibility(u)
	var uid int64
	if u != nil {
		uid = u.ID
	}
	// Arg order follows placeholder order in the SQL: the read-join uid first,
	// then the visibility predicate's args, then the limit.
	args := append([]any{uid}, vargs...)
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, `
		SELECT n.id, n.kind, n.severity, n.title, n.body, n.link, n.created_at,
		       w.domain, (r.read_at IS NOT NULL) AS is_read
		FROM notifications n
		LEFT JOIN websites w ON w.id = n.website_id
		LEFT JOIN notification_reads r ON r.notification_id = n.id AND r.user_id = ?
		WHERE `+cond+` ORDER BY n.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.Kind, &n.Severity, &n.Title, &n.Body, &n.Link, &n.CreatedAt, &n.Domain, &n.IsRead); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkAllRead marks every notification the user may see as read for that user.
// It only touches the user's own notification_reads rows and is scoped by the
// same visibility rule, so a user can never mark another tenant's items.
// Returns the number newly marked. Mirrors NotificationService::markRead(nil).
func (r *Repository) MarkAllRead(ctx context.Context, u *identity.User) (int64, error) {
	cond, vargs := visibility(u)
	var uid int64
	if u != nil {
		uid = u.ID
	}
	// Placeholder order: SELECT's uid and now first, then the visibility args.
	args := append([]any{uid, time.Now().UTC().Format(time.RFC3339)}, vargs...)
	res, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO notification_reads (notification_id, user_id, read_at)
		SELECT n.id, ?, ? FROM notifications n WHERE `+cond, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Store inserts a notification with its dedupe key (the write half of
// NotificationService::notify — the panel-side insert; the email for
// warning/error rows is a follow-up, as is the PHP Redactor). owner_id stays
// null exactly like the panel's av/hardening calls (the owner is resolved by
// the website on read). Returns the new row id, or 0 when the dedupe key
// already exists.
func (r *Repository) Store(ctx context.Context, websiteID sql.NullInt64, kind, severity, title, body, link, dedupeKey string) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO notifications (server_id, website_id, kind, severity, title, body, link, dedupe_key, created_at)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)`,
		websiteID, kind, severity, title, body, link, dedupeKey, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, nil
	}
	return res.LastInsertId()
}
