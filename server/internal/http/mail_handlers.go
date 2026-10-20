package http

import (
	"net/http"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// mailIndex lists the email domains the user may see, tenant-scoped
// (website-aware). Gated by mail.manage, which the email plan feature withholds
// when disabled. Mirrors MailController::index (route "mail", mail.manage).
func (s *Server) mailIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	domains, err := hosting.NewMailRepository(s.db).Domains(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("list mail domains", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "mail", map[string]any{
		"User":    user,
		"CSRF":    session.CSRFToken(sessionFrom(r.Context())),
		"Domains": domains,
	})
}
