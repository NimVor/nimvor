package http

import (
	"net/http"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// auditIndex lists audit-log entries (newest first), with optional filters.
// Staff-only (audit.view); no tenant scoping. Mirrors AuditLogController::index
// (route "audit", audit.view).
func (s *Server) auditIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	f := audit.Filters{
		Action: r.URL.Query().Get("action"),
		Result: r.URL.Query().Get("result"),
		User:   r.URL.Query().Get("user"),
		Q:      r.URL.Query().Get("q"),
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	res, err := s.audit.Search(r.Context(), f, page, 50)
	if err != nil {
		s.log.Error("audit search", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "audit", map[string]any{
		"User":    user,
		"CSRF":    session.CSRFToken(sessionFrom(r.Context())),
		"Rows":    res.Rows,
		"Total":   res.Total,
		"Filters": f,
	})
}
