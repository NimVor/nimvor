package http

import (
	"net/http"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/notify"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// notificationsIndex lists the notifications the user may see, newest first.
// Mirrors NotificationsController::index (route "notifications",
// notifications.view).
func (s *Server) notificationsIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	items, err := notify.NewRepository(s.db).List(r.Context(), user, 50)
	if err != nil {
		s.log.Error("list notifications", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "notifications", map[string]any{
		"User":          user,
		"CSRF":          session.CSRFToken(sessionFrom(r.Context())),
		"Notifications": items,
	})
}

// notificationsRead marks all the user's visible notifications read (CSRF-
// protected). Mirrors NotificationsController::read with no ids.
func (s *Server) notificationsRead(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	if _, err := notify.NewRepository(s.db).MarkAllRead(r.Context(), userFrom(r.Context())); err != nil {
		s.log.Error("mark notifications read", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/notifications", http.StatusSeeOther)
}

// notificationsSummary returns the JSON payload the topbar bell polls: unread
// count plus the newest 8 unread items (severity/title/body truncated/ago/link).
// Mirrors NotificationsController::summary (GET /notifications/summary).
func (s *Server) notificationsSummary(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	repo := notify.NewRepository(s.db)
	unread, err := repo.UnreadCount(r.Context(), user)
	if err != nil {
		s.log.Error("unread count", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "internal"})
		return
	}
	latest, err := repo.ListUnread(r.Context(), user, 5)
	if err != nil {
		s.log.Error("latest notifications", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "internal"})
		return
	}
	items, err := repo.ListUnread(r.Context(), user, 8)
	if err != nil {
		s.log.Error("notifications list", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "internal"})
		return
	}
	latestOut := make([]map[string]any, 0, len(latest))
	for _, n := range latest {
		latestOut = append(latestOut, map[string]any{
			"id": n.ID, "severity": n.Severity, "title": n.Title, "created_at": n.CreatedAt,
		})
	}
	itemsOut := make([]map[string]any, 0, len(items))
	for _, n := range items {
		body := n.Body
		if len([]rune(body)) > 160 {
			body = string([]rune(body)[:159]) + "…"
		}
		link := n.Link
		if !strings.HasPrefix(link, "/") || strings.HasPrefix(link, "//") {
			link = ""
		}
		where := ""
		if n.Domain.Valid {
			where = n.Domain.String
		}
		itemsOut = append(itemsOut, map[string]any{
			"id":       n.ID,
			"severity": n.Severity,
			"title":    n.Title,
			"body":     body,
			"ago":      notificationAgo(n.CreatedAt),
			"read":     n.IsRead,
			"link":     link,
			"where":    where,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"unread":  unread,
		"latest":  latestOut,
		"items":   itemsOut,
	})
}

// notificationsEmail toggles the current user's email-notification opt-in.
// Mirrors NotificationsController::email (POST /notifications/email).
func (s *Server) notificationsEmail(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	user := userFrom(r.Context())
	on := r.FormValue("notify_email") == "1"
	if err := notify.NewRepository(s.db).SetEmailOpt(r.Context(), user.ID, on); err != nil {
		s.log.Error("set email opt-in", "error", err)
		sess.Flash("error", "Could not save the preference.")
		http.Redirect(w, r, "/notifications", http.StatusSeeOther)
		return
	}
	if on {
		sess.Flash("success", "You will be emailed about failures you can see. Email must be set up under Settings.")
	} else {
		sess.Flash("success", "You will no longer be emailed about failures.")
	}
	http.Redirect(w, r, "/notifications", http.StatusSeeOther)
}

// notificationAgo is a tiny English relative-time formatter for ISO-8601 created_at
// timestamps ("3 minutes ago", "2 hours ago"), matching the PHP time_ago()
// helper used by the summary payload. Precise granularity is not required for
// the UI; we stay in whole units.
func notificationAgo(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		n := int(d / time.Minute)
		if n == 1 {
			return "1 minute ago"
		}
		return itoa(n) + " minutes ago"
	case d < 24*time.Hour:
		n := int(d / time.Hour)
		if n == 1 {
			return "1 hour ago"
		}
		return itoa(n) + " hours ago"
	case d < 30*24*time.Hour:
		n := int(d / (24 * time.Hour))
		if n == 1 {
			return "1 day ago"
		}
		return itoa(n) + " days ago"
	default:
		n := int(d / (30 * 24 * time.Hour))
		if n <= 1 {
			return "1 month ago"
		}
		return itoa(n) + " months ago"
	}
}
