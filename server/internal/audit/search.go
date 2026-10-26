package audit

import (
	"context"
	"database/sql"
	"strings"
)

// Record is one audit_logs row for display.
type Record struct {
	ID         int64
	UserID     sql.NullInt64
	Username   string
	Action     string
	TargetType string
	TargetID   string
	Result     string
	IP         string
	Details    string
	CreatedAt  string
}

// Filters narrows a search. Empty fields are ignored. Mirrors the filters of
// AuditLogRepository::search.
type Filters struct {
	Action string // prefix match
	Result string // exact
	User   string // exact username
	Q      string // substring over target_id / details / ip
}

// Page is a page of audit records plus the total matching count.
type Page struct {
	Rows  []Record
	Total int
}

// likeEscape escapes LIKE wildcards so user input matches literally.
func likeEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	return strings.ReplaceAll(s, "_", `\_`)
}

// Search returns a page of audit records (newest first), applying the filters.
// Audit viewing is a staff-only permission (audit.view), so there is no tenant
// scoping — administrators see every entry. Mirrors AuditLogRepository::search.
func (l *Logger) Search(ctx context.Context, f Filters, page, perPage int) (Page, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 50
	}
	var where []string
	var args []any
	if f.Action != "" {
		where = append(where, `action LIKE ? ESCAPE '\'`)
		args = append(args, likeEscape(f.Action)+"%")
	}
	if f.Result != "" {
		where = append(where, "result = ?")
		args = append(args, f.Result)
	}
	if f.User != "" {
		where = append(where, "username = ?")
		args = append(args, f.User)
	}
	if f.Q != "" {
		where = append(where, `(target_id LIKE ? ESCAPE '\' OR details LIKE ? ESCAPE '\' OR ip LIKE ? ESCAPE '\')`)
		q := "%" + likeEscape(f.Q) + "%"
		args = append(args, q, q, q)
	}
	clause := ""
	if len(where) > 0 {
		clause = "WHERE " + strings.Join(where, " AND ")
	}

	var out Page
	if err := l.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_logs "+clause, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := l.db.QueryContext(ctx,
		"SELECT id, user_id, username, action, target_type, target_id, result, ip, details, created_at FROM audit_logs "+
			clause+" ORDER BY id DESC LIMIT ? OFFSET ?",
		append(args, perPage, (page-1)*perPage)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.UserID, &r.Username, &r.Action, &r.TargetType, &r.TargetID, &r.Result, &r.IP, &r.Details, &r.CreatedAt); err != nil {
			return out, err
		}
		out.Rows = append(out.Rows, r)
	}
	return out, rows.Err()
}
