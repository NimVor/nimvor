package http

// This file ports the on-demand security checks (SecurityChecksController):
// file integrity (dpkg/rpm verification + the WhaCP baseline), the TLS
// configuration audit and the OS vulnerability advisories. Each page opens
// instantly with a Run button; running calls the agent synchronously and
// re-renders the page with the findings (no redirect). The checks are
// read-only; only recording the file-integrity baseline changes state.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

type integrityFinding struct {
	Path     string `json:"path"`
	Class    string `json:"class"`
	Issue    string `json:"issue"`
	Severity string `json:"severity"`
	Detail   string `json:"detail"`
}

// integrityResult mirrors the agent's security.integrity reply.
type integrityResult struct {
	HasBaseline bool               `json:"has_baseline"`
	Findings    []integrityFinding `json:"findings"`
}

type tlsFinding struct {
	Issue    string `json:"issue"`
	Severity string `json:"severity"`
	Detail   string `json:"detail"`
}

type tlsEndpointResult struct {
	Endpoint    string       `json:"endpoint"`
	Kind        string       `json:"kind"`
	MinVersion  string       `json:"min_version"`
	BestVersion string       `json:"best_version"`
	Findings    []tlsFinding `json:"findings"`
}

// tlsAuditResult mirrors the agent's tls.audit reply.
type tlsAuditResult struct {
	Results []tlsEndpointResult `json:"results"`
}

type advisoryItem struct {
	Package   string   `json:"package"`
	Installed string   `json:"installed"`
	Fixed     string   `json:"fixed"`
	Advisory  string   `json:"advisory"`
	Severity  string   `json:"severity"`
	CVEs      []string `json:"cves"`
	Kind      string   `json:"kind"`
}

// advisoriesResult mirrors the agent's packages.advisories reply.
type advisoriesResult struct {
	Advisories []advisoryItem `json:"advisories"`
	Security   int            `json:"security"`
	Normal     int            `json:"normal"`
	Unknown    int            `json:"unknown"`
}

// checksRender is SecurityChecksController::page: the three pages share the
// chrome (no pending banner — the checks are read-only), the flashes and
// canManage (the integrity page's baseline button).
func (s *Server) checksRender(w http.ResponseWriter, r *http.Request, tab string, data map[string]any) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	ok, _ := s.authorizer().Can(r.Context(), user, "security.manage")
	data["User"] = user
	data["CSRF"] = session.CSRFToken(sess)
	data["CanManage"] = ok
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	s.render(w, "security_"+tab, data)
}

// agentRunError extracts the message shown in the "could not run" bar.
func (s *Server) agentRunError(err error) string {
	var ae *agent.Error
	if errors.As(err, &ae) && ae.Message != "" {
		return ae.Message
	}
	s.log.Error("security check", "error", err)
	return "The WhaCP agent could not be reached."
}

// integrity (GET) — SecurityChecksController::integrity: opens instantly,
// no agent call.
func (s *Server) integrityPage(w http.ResponseWriter, r *http.Request) {
	s.checksRender(w, r, "integrity", map[string]any{
		"Ran": false, "Result": nil, "RunError": "",
	})
}

// runIntegrity (POST /security/integrity/run) — the synchronous check; the
// result re-renders the page, an agent error re-renders it with the message.
func (s *Server) runIntegrity(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	runError := ""
	var res *integrityResult
	if raw, err := s.agent.Call(ctx, "security.integrity", nil, 180*time.Second); err != nil {
		runError = s.agentRunError(err)
	} else if err := json.Unmarshal(raw, &res); err != nil {
		s.log.Error("security integrity", "error", err)
		runError = "The WhaCP agent answered with an unreadable result."
	} else {
		user := userFrom(ctx)
		s.audit.Record(ctx, audit.Entry{
			UserID:     &user.ID,
			Username:   user.Username,
			Action:     "security.integrity",
			TargetType: "server",
			TargetID:   "integrity",
			Result:     audit.Success,
			IP:         clientIP(r),
			UserAgent:  r.UserAgent(),
			Details:    map[string]any{"findings": len(res.Findings)},
		})
	}
	s.checksRender(w, r, "integrity", map[string]any{
		"Ran": true, "Result": res, "RunError": runError,
	})
}

// setIntegrityBaseline (POST /security/integrity/baseline) — the one
// state-changing check: record the current WhaCP-owned files as known-good.
func (s *Server) setIntegrityBaseline(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	to := "/security/integrity"
	ctx := r.Context()
	res, err := s.agent.Call(ctx, "security.integrity_baseline", nil, 60*time.Second)
	if err != nil {
		sess.Flash("error", "The baseline could not be recorded: "+s.agentRunError(err))
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	var out struct {
		Baselined int `json:"baselined"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		s.log.Error("security integrity baseline", "error", err)
	}
	user := userFrom(ctx)
	s.audit.Record(ctx, audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "security.integrity_baseline",
		TargetType: "server",
		TargetID:   "integrity",
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    map[string]any{"files": out.Baselined},
	})
	sess.Flash("success", fmt.Sprintf("Integrity baseline recorded for %d WhaCP-owned file(s). Run the check after an update to refresh it.", out.Baselined))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// tlsEndpoints is SecurityChecksController::tlsEndpoints: every enabled
// HTTPS website (dialled on loopback with its name for SNI) and the mail
// server's three TLS ports when it is set up.
func (s *Server) tlsEndpoints(ctx context.Context, serverID int64) []map[string]any {
	eps := []map[string]any{}
	rows, err := s.db.QueryContext(ctx,
		`SELECT domain FROM websites WHERE server_id = ? AND enabled = 1 AND https = 1 ORDER BY domain LIMIT 200`, serverID)
	if err != nil {
		s.log.Error("tls endpoints", "error", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var d string
			if rows.Scan(&d) == nil {
				eps = append(eps, map[string]any{"port": 443, "server_name": d, "kind": "website"})
			}
		}
	}
	if raw, err := s.agent.Call(ctx, "mail.status", nil); err == nil {
		var st struct {
			Configured bool   `json:"configured"`
			Hostname   string `json:"hostname"`
		}
		if json.Unmarshal(raw, &st) == nil && st.Configured && st.Hostname != "" {
			eps = append(eps,
				map[string]any{"port": 465, "server_name": st.Hostname, "kind": "mail-smtps"},
				map[string]any{"port": 587, "server_name": st.Hostname, "kind": "mail-submission", "starttls": "smtp"},
				map[string]any{"port": 993, "server_name": st.Hostname, "kind": "mail-imaps"})
		}
	}
	return eps
}

// tls (GET) — SecurityChecksController::tls: the endpoint count up front.
func (s *Server) tlsPage(w http.ResponseWriter, r *http.Request) {
	eps := s.tlsEndpoints(r.Context(), s.currentServerID(r))
	s.checksRender(w, r, "tls", map[string]any{
		"Ran": false, "Result": nil, "RunError": "", "Endpoints": len(eps),
	})
}

// runTls (POST /security/tls/run) — with no endpoints there is nothing to
// audit (no agent call); otherwise tls.audit re-renders the page.
func (s *Server) runTls(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	eps := s.tlsEndpoints(ctx, s.currentServerID(r))
	if len(eps) == 0 {
		s.checksRender(w, r, "tls", map[string]any{
			"Ran": true, "Result": nil, "RunError": "", "Endpoints": 0,
		})
		return
	}
	runError := ""
	var res *tlsAuditResult
	if raw, err := s.agent.Call(ctx, "tls.audit", map[string]any{"endpoints": eps}, 180*time.Second); err != nil {
		runError = s.agentRunError(err)
	} else if err := json.Unmarshal(raw, &res); err != nil {
		s.log.Error("tls audit", "error", err)
		runError = "The WhaCP agent answered with an unreadable result."
	} else {
		user := userFrom(ctx)
		s.audit.Record(ctx, audit.Entry{
			UserID:     &user.ID,
			Username:   user.Username,
			Action:     "tls.audit",
			TargetType: "server",
			TargetID:   "tls",
			Result:     audit.Success,
			IP:         clientIP(r),
			UserAgent:  r.UserAgent(),
			Details:    map[string]any{"endpoints": len(eps)},
		})
	}
	s.checksRender(w, r, "tls", map[string]any{
		"Ran": true, "Result": res, "RunError": runError, "Endpoints": len(eps),
	})
}

// advisories (GET) — SecurityChecksController::advisories: opens instantly.
func (s *Server) advisoriesPage(w http.ResponseWriter, r *http.Request) {
	s.checksRender(w, r, "advisories", map[string]any{
		"Ran": false, "Result": nil, "RunError": "",
	})
}

// runAdvisories (POST /security/advisories/run) — packages.advisories;
// like PHP, it is not audited (the check is a plain read of the package DB).
func (s *Server) runAdvisories(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	runError := ""
	var res *advisoriesResult
	if raw, err := s.agent.Call(ctx, "packages.advisories", nil, 120*time.Second); err != nil {
		runError = s.agentRunError(err)
	} else if err := json.Unmarshal(raw, &res); err != nil {
		s.log.Error("packages advisories", "error", err)
		runError = "The WhaCP agent answered with an unreadable result."
	}
	s.checksRender(w, r, "advisories", map[string]any{
		"Ran": true, "Result": res, "RunError": runError,
	})
}
