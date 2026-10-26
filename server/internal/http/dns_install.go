package http

import (
	"net/http"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// dnsInstall POST /dns/install — queues a `packages.install bind9` job.
// Mirrors DnsController::install (dns.install, packages.manage).
func (s *Server) dnsInstall(w http.ResponseWriter, r *http.Request) {
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
	if !hosting.IsAll(user) {
		http.Redirect(w, r, "/dns", http.StatusSeeOther)
		return
	}
	raw, err := s.agent.Call(r.Context(), "packages.install", map[string]any{"name": "bind9"}, 120*time.Second)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/dns", http.StatusSeeOther)
		return
	}
	jobID := ""
	if m := agentObject(raw); m != nil {
		if j, ok := m["job_id"].(string); ok {
			jobID = j
		}
	}
	uid := user.ID
	if jobID != "" {
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), jobID, "Install BIND", "server", "dns", &uid)
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "packages.install", TargetType: "server", TargetID: "dns",
		Result: audit.Success, Details: map[string]any{"name": "bind9"},
		UserID: &uid, Username: user.Username,
	})
	if jobID != "" {
		sess.Flash("job", jobID)
	}
	sess.Flash("success", "Installing BIND. Then set it up for WhaCP below.")
	http.Redirect(w, r, "/dns", http.StatusSeeOther)
}

// dnsSetup POST /dns/setup — runs `dns.configure` on the agent to drop
// WhaCP's BIND configuration. The optional `confirm` flag permits overwriting
// an existing package-managed config. Mirrors DnsController::setup
// (dns.setup, server.manage).
func (s *Server) dnsSetup(w http.ResponseWriter, r *http.Request) {
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
	if !hosting.IsAll(user) {
		http.Redirect(w, r, "/dns", http.StatusSeeOther)
		return
	}
	confirm := r.FormValue("confirm") == "1"
	raw, err := s.agent.Call(r.Context(), "dns.configure", map[string]any{
		"confirm_edit_package_config": confirm,
	}, 90*time.Second)
	if err != nil {
		uid := user.ID
		s.audit.Record(r.Context(), audit.Entry{
			Action: "dns.configure", TargetType: "server", TargetID: "dns",
			Result: audit.Failure, Details: map[string]any{"error": err.Error()},
			UserID: &uid, Username: user.Username,
		})
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/dns", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "dns.configure", TargetType: "server", TargetID: "dns",
		Result: audit.Success, UserID: &uid, Username: user.Username,
		Details: map[string]any{"confirm_edit_package_config": confirm, "raw": raw},
	})
	sess.Flash("success", "The DNS server is ready.")
	http.Redirect(w, r, "/dns", http.StatusSeeOther)
}
