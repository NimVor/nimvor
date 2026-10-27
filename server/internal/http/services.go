package http

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// serviceUnitRx restricts unit names to systemd patterns.
// Mirrors PHP: /^[A-Za-z0-9_][A-Za-z0-9@._:-]{0,199}\.service$/
var serviceUnitRx = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9@._:-]{0,199}\.service$`)

// servicesControl starts/stops/restarts/reloads/enables/disables a systemd
// unit via the agent. Supports the dashboard/service widget via JSON Accept
// header and an explicit ?return_to for pages that host their own service
// controls (e.g. /mail/admin/server). Mirrors ServicesController::control
// (POST /services/control, services.manage).
func (s *Server) servicesControl(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	unit := r.FormValue("unit")
	op := r.FormValue("op")
	returnTo := r.FormValue("return_to")
	if returnTo != "/mail/admin/server" {
		returnTo = "/services"
	}
	jsonReq := r.Header.Get("X-Requested-With") == "XMLHttpRequest" ||
		strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json")
	if !serviceUnitRx.MatchString(unit) {
		servicesControlReply(w, r, jsonReq, http.StatusUnprocessableEntity, returnTo, "Invalid service operation.")
		return
	}
	allowed := map[string]bool{
		"start": true, "stop": true, "restart": true,
		"reload": true, "enable": true, "disable": true,
	}
	if !allowed[op] {
		servicesControlReply(w, r, jsonReq, http.StatusUnprocessableEntity, returnTo, "Invalid service operation.")
		return
	}
	user := userFrom(r.Context())
	uid := user.ID
	if _, err := s.agent.Call(r.Context(), "services.control", map[string]any{"unit": unit, "op": op}, 60*time.Second); err != nil {
		s.audit.Record(r.Context(), audit.Entry{
			Action: "service." + op, TargetType: "service", TargetID: unit,
			Result: audit.Failure, Details: map[string]any{"error": err.Error()},
			UserID: &uid, Username: user.Username,
		})
		servicesControlReply(w, r, jsonReq, http.StatusBadGateway, returnTo, err.Error())
		return
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "service." + op, TargetType: "service", TargetID: unit,
		Result: audit.Success, UserID: &uid, Username: user.Username,
	})
	msg := strings.ToUpper(op[:1]) + op[1:] + " of " + unit + " completed."
	if jsonReq {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": msg})
		return
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, returnTo, http.StatusSeeOther)
}

// servicesRestartWhacp restarts a WhaCP-owned service ("panel" or "agent") via
// the agent. Server admins only. Mirrors ServicesController::restartWhacp
// (POST /services/whacp/{key}/restart, services.manage).
func (s *Server) servicesRestartWhacp(w http.ResponseWriter, r *http.Request) {
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
		sess.Flash("error", "Only server administrators can restart WhaCP.")
		http.Redirect(w, r, "/services", http.StatusSeeOther)
		return
	}
	key := r.PathValue("key")
	uid := user.ID
	resRaw, err := s.agent.Call(r.Context(), "whacp.restart", map[string]any{"key": key}, 60*time.Second)
	if err != nil {
		s.audit.Record(r.Context(), audit.Entry{
			Action: "whacp.restart", TargetType: "service", TargetID: key,
			Result: audit.Failure, Details: map[string]any{"error": err.Error()},
			UserID: &uid, Username: user.Username,
		})
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/services", http.StatusSeeOther)
		return
	}
	unit := key
	scheduled := false
	if m := agentObject(resRaw); m != nil {
		if u, ok := m["unit"].(string); ok && u != "" {
			unit = u
		}
		scheduled, _ = m["scheduled"].(bool)
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "whacp.restart", TargetType: "service", TargetID: unit,
		Result: audit.Success, UserID: &uid, Username: user.Username,
	})
	var msg string
	if scheduled {
		if key == "panel" {
			msg = "The panel restarts in a few seconds; reload this page then."
		} else {
			msg = "The agent restarts in a few seconds; running tasks finish first."
		}
	} else {
		msg = "Restarted " + unit + "."
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, "/services", http.StatusSeeOther)
}

func servicesControlReply(w http.ResponseWriter, r *http.Request, jsonReq bool, code int, returnTo, msg string) {
	if jsonReq {
		writeJSON(w, code, map[string]any{
			"success": false,
			"error":   map[string]any{"message": msg},
		})
		return
	}
	sessionFrom(r.Context()).Flash("error", msg)
	http.Redirect(w, r, returnTo, http.StatusSeeOther)
}
