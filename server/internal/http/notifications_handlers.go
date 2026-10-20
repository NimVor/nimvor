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
