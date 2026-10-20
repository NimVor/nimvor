package http

import (
	"net/http"

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
