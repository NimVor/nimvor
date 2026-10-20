package http

import (
	"net/http"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// dnsIndex lists the DNS zones the user may see, tenant-scoped (website-aware).
// Gated by dns.manage, which the dns plan feature withholds when disabled.
// Mirrors DnsController::index (route "dns", dns.manage).
func (s *Server) dnsIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	zones, err := hosting.NewDnsRepository(s.db).Zones(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("list dns zones", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "dns", map[string]any{
		"User":  user,
		"CSRF":  session.CSRFToken(sessionFrom(r.Context())),
		"Zones": zones,
	})
}
