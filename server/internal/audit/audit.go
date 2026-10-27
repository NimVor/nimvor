// Package audit records privileged and destructive actions to the audit_logs
// table, with sensitive values redacted from the details. It is the Go port of
// Services\AuditLogger + Support\Redactor; the table schema and row shape are
// shared with the PHP panel, so entries from both components interleave in one
// log. An audit write that fails is logged, never silently dropped.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"regexp"
	"time"
)

// Result values, matching AuditLogger's constants.
const (
	Success = "success"
	Failure = "failure"
	Denied  = "denied"
)

// Entry is one audit record. Actor/IP/UserAgent describe who and from where;
// Details is redacted before storage.
type Entry struct {
	UserID     *int64
	Username   string
	Action     string
	TargetType string
	TargetID   string
	Result     string
	IP         string
	UserAgent  string
	Details    map[string]any
}

// Logger writes audit rows.
type Logger struct {
	db  *sql.DB
	log *slog.Logger
}

// New builds an audit Logger.
func New(db *sql.DB, log *slog.Logger) *Logger { return &Logger{db: db, log: log} }

// Record inserts an audit row (redacting Details) and always emits a structured
// log line. A DB failure is logged, not propagated — losing the request over a
// log write would be worse — but it is never silent. Mirrors AuditLogger::record.
func (l *Logger) Record(ctx context.Context, e Entry) {
	if e.Result == "" {
		e.Result = Success
	}
	// The scheduler and the command line act as a system user with no users row.
	if e.UserID != nil && *e.UserID <= 0 {
		e.UserID = nil
	}
	if len(e.Username) > 190 {
		e.Username = e.Username[:190]
	}
	if len(e.TargetID) > 255 {
		e.TargetID = e.TargetID[:255]
	}
	details := "{}"
	if b, err := json.Marshal(Redact(e.Details)); err == nil {
		details = string(b)
	}
	_, err := l.db.ExecContext(ctx,
		`INSERT INTO audit_logs (user_id, username, action, target_type, target_id, result, ip, user_agent, details, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.UserID, e.Username, e.Action, e.TargetType, e.TargetID, e.Result, e.IP, e.UserAgent, details,
		time.Now().UTC().Format(time.RFC3339))
	if err != nil && l.log != nil {
		l.log.Error("audit log write failed", "action", e.Action, "error", err)
	}
	if l.log != nil {
		l.log.Info("audit", "action", e.Action, "target", e.TargetType+":"+e.TargetID,
			"result", e.Result, "user", e.Username, "ip", e.IP)
	}
}

// sensitiveKey matches Redactor::SENSITIVE_KEY.
var sensitiveKey = regexp.MustCompile(`(?i)pass(word|wd)?|secret|token|api[_-]?key|authorization|cookie|private[_-]?key|credential`)

// Placeholder is Redactor::PLACEHOLDER.
const Placeholder = "[REDACTED]"

// Redact returns a copy of details with sensitive keys replaced. Nested maps
// are redacted recursively. Mirrors Support\Redactor::redact for the shapes
// the panel passes (maps and scalars).
func Redact(details map[string]any) map[string]any {
	if details == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(details))
	for k, v := range details {
		if sensitiveKey.MatchString(k) {
			out[k] = Placeholder
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			out[k] = Redact(nested)
			continue
		}
		out[k] = v
	}
	return out
}
