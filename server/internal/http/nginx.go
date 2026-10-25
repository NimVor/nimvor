package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// nginxIndex renders GET /nginx for administrators: install/running status,
// configuration-test state, and the server-wide tuning form. Mirrors
// NginxController::index (nginx, server.view).
func (s *Server) nginxIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.renderError(w, http.StatusForbidden, "Access denied", "Nginx is managed by server administrators.")
		return
	}
	sess := sessionFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Nginx",
		"Status":     nil,
		"Tuning":     nginxDefaultTuning(),
		"AgentError": nil,
		"Success":    "",
		"Error":      "",
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	if raw, err := s.agent.Call(r.Context(), "nginx.status", nil, 30*time.Second); err == nil {
		var st any
		_ = json.Unmarshal(raw, &st)
		data["Status"] = st
	} else {
		data["AgentError"] = err.Error()
	}
	// Load tuning values from settings (defaults match NginxService::tuning).
	t := map[string]any{}
	if v, err := s.settings.Int(r.Context(), "nginx_client_max_body_mb", 128); err == nil {
		t["ClientMaxBodyMB"] = v
	} else {
		t["ClientMaxBodyMB"] = 128
	}
	if v, err := s.settings.Int(r.Context(), "nginx_keepalive", 65); err == nil {
		t["KeepaliveTimeout"] = v
	} else {
		t["KeepaliveTimeout"] = 65
	}
	if v, err := s.settings.Int(r.Context(), "nginx_body_timeout", 60); err == nil {
		t["ClientBodyTimeout"] = v
	} else {
		t["ClientBodyTimeout"] = 60
	}
	if v, err := s.settings.Int(r.Context(), "nginx_send_timeout", 60); err == nil {
		t["SendTimeout"] = v
	} else {
		t["SendTimeout"] = 60
	}
	if v, err := s.settings.Bool(r.Context(), "nginx_gzip", false); err == nil {
		t["Gzip"] = v
	}
	if v, err := s.settings.Bool(r.Context(), "nginx_hide_version", false); err == nil {
		t["HideVersion"] = v
	}
	data["Tuning"] = t
	s.render(w, "server_nginx", data)
}

func nginxDefaultTuning() map[string]any {
	return map[string]any{
		"ClientMaxBodyMB":  128,
		"KeepaliveTimeout": 65,
		"ClientBodyTimeout": 60,
		"SendTimeout":      60,
		"Gzip":             false,
		"HideVersion":      false,
	}
}

// nginxTest POST /nginx/test — runs nginx -t on the server. Mirrors
// NginxController::test (nginx.test, server.view).
func (s *Server) nginxTest(w http.ResponseWriter, r *http.Request) {
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
		http.Redirect(w, r, "/nginx", http.StatusSeeOther)
		return
	}
	raw, err := s.agent.Call(r.Context(), "nginx.test", nil, 60*time.Second)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/nginx", http.StatusSeeOther)
		return
	}
	ok := false
	output := ""
	if m, _ := raw.(map[string]any); m != nil {
		ok, _ = m["ok"].(bool)
		if o, _ := m["output"].(string); o != "" {
			output = o
		}
	}
	if ok {
		sess.Flash("success", "The Nginx configuration is valid.")
	} else {
		sess.Flash("error", "The configuration is invalid: "+output)
	}
	http.Redirect(w, r, "/nginx", http.StatusSeeOther)
}

// nginxReload POST /nginx/reload — tells nginx to reload (systemctl reload).
// Mirrors NginxController::reload (nginx.reload, services.manage).
func (s *Server) nginxReload(w http.ResponseWriter, r *http.Request) {
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
		http.Redirect(w, r, "/nginx", http.StatusSeeOther)
		return
	}
	uid := user.ID
	if _, err := s.agent.Call(r.Context(), "nginx.reload", nil, 60*time.Second); err != nil {
		s.audit.Record(r.Context(), audit.Entry{
			Action: "nginx.reload", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
			Result: audit.Failure, Details: map[string]any{"error": err.Error()},
			UserID: &uid, Username: user.Username,
		})
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/nginx", http.StatusSeeOther)
		return
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "nginx.reload", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Success, UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Nginx reloaded.")
	http.Redirect(w, r, "/nginx", http.StatusSeeOther)
}

// nginxTuning POST /nginx/tuning — saves server-wide Nginx tuning and calls
// nginx.tuning_apply so the agent writes the drop-in and reloads. Mirrors
// NginxController::tuning (nginx.tuning, server.manage).
func (s *Server) nginxTuning(w http.ResponseWriter, r *http.Request) {
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
		http.Redirect(w, r, "/nginx", http.StatusSeeOther)
		return
	}
	atoiOr := func(v string, def int) int {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return def
		}
		return n
	}
	vals := map[string]any{
		"nginx_client_max_body_mb": atoiOr(r.FormValue("client_max_body_mb"), 128),
		"nginx_keepalive":          atoiOr(r.FormValue("keepalive_timeout"), 65),
		"nginx_body_timeout":       atoiOr(r.FormValue("client_body_timeout"), 60),
		"nginx_send_timeout":       atoiOr(r.FormValue("send_timeout"), 60),
		"nginx_gzip":               r.FormValue("gzip") == "1",
		"nginx_hide_version":       r.FormValue("hide_version") == "1",
	}
	for k, v := range vals {
		if err := s.settings.Set(r.Context(), k, v, user.ID); err != nil {
			sess.Flash("error", "Could not save tuning: "+err.Error())
			http.Redirect(w, r, "/nginx", http.StatusSeeOther)
			return
		}
	}
	uid := user.ID
	if _, err := s.agent.Call(r.Context(), "nginx.tuning_apply", vals, 120*time.Second); err != nil {
		s.audit.Record(r.Context(), audit.Entry{
			Action: "nginx.tuning", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
			Result: audit.Failure, Details: map[string]any{"error": err.Error(), "values": vals},
			UserID: &uid, Username: user.Username,
		})
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/nginx", http.StatusSeeOther)
		return
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "nginx.tuning", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Success, Details: vals, UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Nginx tuning applied.")
	http.Redirect(w, r, "/nginx", http.StatusSeeOther)
}
