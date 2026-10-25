package http

// PhpController port (panel/app/Http/Controllers/PhpController.php, 198 LOC,
// 8 routes): manage PHP versions — add the third-party repository, install a
// version, enable/disable, per-version extensions / php.ini / PHP-FPM
// settings, and the JSON modules list used by the per-site runtime form.
//
// The GET /php listing is rendered by discoveryPHP (discovery_handlers.go,
// already ported); only the POST mutators and the per-version show/modules
// endpoints live here.

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

var (
	phpVersionRe   = regexp.MustCompile(`^[5-9]\.[0-9]{1,2}$`)
	phpExtNameRe   = regexp.MustCompile(`^[a-z0-9_]{2,20}$`)
	phpIniKeyRe    = regexp.MustCompile(`^[a-z0-9_.]{2,64}$`)
	phpValidExtOps = map[string]bool{"install": true, "remove": true, "toggle": true}
)

// phpRepository POST /php/repository — enable the third-party PHP repo
// (Ondřej Surý PPA on Ubuntu, Remi on RHEL). Requires "confirm=yes".
func (s *Server) phpRepository(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	_ = r.ParseForm()
	if r.PostFormValue("confirm") != "yes" {
		phpFlashErr(w, r, "Confirm that the repository should be added.", "/php")
		return
	}
	res, err := s.agent.Call(r.Context(), "php.repository.enable", nil, 120*time.Second)
	if err != nil {
		phpFlashErr(w, r, agentMessage(err), "/php")
		return
	}
	var env map[string]any
	_ = json.Unmarshal(res, &env)
	name := "PHP repository"
	if n, ok := env["repository"].(string); ok && n != "" {
		name = n
	} else if data, ok := env["data"].(map[string]any); ok {
		if n, ok := data["repository"].(string); ok && n != "" {
			name = n
		}
	}
	jobID, _ := env["job_id"].(string)
	if jobID == "" {
		if data, ok := env["data"].(map[string]any); ok {
			jobID, _ = data["job_id"].(string)
		}
	}
	if jobID != "" {
		uid := user.ID
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), jobID, "Add "+name, "php", "repository", &uid)
	}
	phpAudit(r, user, "php.repository.enable", "php", name, audit.Success, nil)
	sess := sessionFrom(r.Context())
	if jobID != "" {
		sess.Flash("job", jobID)
	}
	sess.Flash("success", "Adding "+name+". Its PHP versions appear in the list once the package index is refreshed.")
	http.Redirect(w, r, "/php", http.StatusSeeOther)
}

// phpInstall POST /php/install — install a PHP version.
func (s *Server) phpInstall(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	_ = r.ParseForm()
	version := r.PostFormValue("version")
	if !phpVersionRe.MatchString(version) {
		phpFlashErr(w, r, "Choose a PHP version.", "/php")
		return
	}
	res, err := s.agent.Call(r.Context(), "php.install", map[string]any{"version": version}, 120*time.Second)
	if err != nil {
		phpFlashErr(w, r, agentMessage(err), "/php")
		return
	}
	var env map[string]any
	_ = json.Unmarshal(res, &env)
	jobID, _ := env["job_id"].(string)
	var pkgs []string
	if p, ok := env["packages"].([]any); ok {
		for _, x := range p {
			if s, ok := x.(string); ok {
				pkgs = append(pkgs, s)
			}
		}
	}
	if jobID == "" {
		if data, ok := env["data"].(map[string]any); ok {
			jobID, _ = data["job_id"].(string)
		}
	}
	if jobID != "" {
		uid := user.ID
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), jobID, "Install PHP "+version, "php", version, &uid)
	}
	phpAudit(r, user, "php.install", "php", version, audit.Success, map[string]any{"packages": pkgs})
	sess := sessionFrom(r.Context())
	if jobID != "" {
		sess.Flash("job", jobID)
	}
	sess.Flash("success", "Installing PHP "+version+". You can follow the progress below.")
	http.Redirect(w, r, "/php", http.StatusSeeOther)
}

// phpToggle POST /php/{version}/toggle — enable/disable a PHP version.
func (s *Server) phpToggle(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	version := r.PathValue("version")
	if !phpVersionRe.MatchString(version) {
		phpFlashErr(w, r, "Unknown PHP version.", "/php")
		return
	}
	_ = r.ParseForm()
	on := r.PostFormValue("enabled") == "1"
	if _, err := s.agent.Call(r.Context(), "php.set_enabled", map[string]any{"version": version, "enabled": on}, 60*time.Second); err != nil {
		phpFlashErr(w, r, agentMessage(err), "/php")
		return
	}
	action := "php.disable"
	msg := "PHP " + version + " stopped and disabled."
	if on {
		action = "php.enable"
		msg = "PHP " + version + " started and enabled."
	}
	phpAudit(r, user, action, "php", version, audit.Success, nil)
	sess := sessionFrom(r.Context())
	sess.Flash("success", msg)
	http.Redirect(w, r, "/php", http.StatusSeeOther)
}

// phpShow GET /php/{version} — extensions + ini + fpm settings for one version.
func (s *Server) phpShow(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	version := r.PathValue("version")
	if !phpVersionRe.MatchString(version) {
		http.Redirect(w, r, "/php", http.StatusSeeOther)
		return
	}
	tab := r.URL.Query().Get("tab")
	if tab != "extensions" && tab != "ini" && tab != "fpm" {
		tab = "extensions"
	}
	data := map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sessionFrom(r.Context())),
		"Title":      "PHP " + version,
		"Version":    version,
		"Tab":        tab,
		"Extensions": []any{},
		"Ini":        nil,
		"Fpm":        nil,
		"AgentError": "",
	}
	fetch := func(action string, dest *any) {
		raw, err := s.agent.Call(r.Context(), action, map[string]any{"version": version}, 60*time.Second)
		if err != nil {
			data["AgentError"] = agentMessage(err)
			return
		}
		var v any
		if json.Unmarshal(raw, &v) == nil {
			*dest = v
		}
	}
	var exts any
	fetch("php.extensions", &exts)
	data["Extensions"] = exts
	var ini any
	fetch("php.ini_get", &ini)
	data["Ini"] = ini
	var fpm any
	fetch("php.fpm_get", &fpm)
	data["Fpm"] = fpm
	s.render(w, "php_show", data)
}

// phpExtension POST /php/{version}/extensions/{op} — install/remove/toggle.
func (s *Server) phpExtension(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	version := r.PathValue("version")
	op := r.PathValue("op")
	_ = r.ParseForm()
	name := r.PostFormValue("name")
	to := "/php/" + version + "?tab=extensions"
	if !phpVersionRe.MatchString(version) || !phpExtNameRe.MatchString(name) || !phpValidExtOps[op] {
		phpFlashErr(w, r, "Unknown PHP version or extension.", "/php")
		return
	}
	if op == "toggle" {
		on := r.PostFormValue("enabled") == "1"
		if _, err := s.agent.Call(r.Context(), "php.extension_set", map[string]any{"version": version, "name": name, "enabled": on}, 60*time.Second); err != nil {
			phpFlashErr(w, r, agentMessage(err), to)
			return
		}
		action := "php.extension_disable"
		msg := name + " switched off for PHP " + version + "; PHP-FPM was reloaded."
		if on {
			action = "php.extension_enable"
			msg = name + " switched on for PHP " + version + "; PHP-FPM was reloaded."
		}
		phpAudit(r, user, action, "php", version, audit.Success, map[string]any{"extension": name})
		sess := sessionFrom(r.Context())
		sess.Flash("success", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	action := "php.extension_install"
	label := "Install " + name + " (PHP " + version + ")"
	if op == "remove" {
		action = "php.extension_remove"
		label = "Remove " + name + " (PHP " + version + ")"
	}
	res, err := s.agent.Call(r.Context(), action, map[string]any{"version": version, "name": name}, 120*time.Second)
	if err != nil {
		phpAudit(r, user, "php.extension_"+op, "php", version, audit.Failure, map[string]any{"extension": name, "error": err.Error()})
		phpFlashErr(w, r, agentMessage(err), to)
		return
	}
	var env map[string]any
	_ = json.Unmarshal(res, &env)
	jobID, _ := env["job_id"].(string)
	pkg, _ := env["package"].(string)
	if jobID == "" {
		if data, ok := env["data"].(map[string]any); ok {
			jobID, _ = data["job_id"].(string)
			if pkg == "" {
				pkg, _ = data["package"].(string)
			}
		}
	}
	if jobID != "" {
		uid := user.ID
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), jobID, label, "php", version, &uid)
	}
	phpAudit(r, user, "php.extension_"+op, "php", version, audit.Success, map[string]any{"extension": name, "package": pkg})
	sess := sessionFrom(r.Context())
	if jobID != "" {
		sess.Flash("job", jobID)
	}
	sess.Flash("success", label+" started.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// phpIni POST /php/{version}/ini — save php.ini settings.
func (s *Server) phpIni(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	version := r.PathValue("version")
	to := "/php/" + version + "?tab=ini"
	if !phpVersionRe.MatchString(version) {
		phpFlashErr(w, r, "Unknown PHP version.", "/php")
		return
	}
	_ = r.ParseForm()
	var content string
	if r.PostFormValue("mode") == "text" {
		content = r.PostFormValue("content")
	} else {
		lines := []string{}
		for key, vals := range r.PostForm {
			if !strings.HasPrefix(key, "ini[") || !strings.HasSuffix(key, "]") {
				continue
			}
			k := key[4 : len(key)-1]
			v := strings.TrimSpace(vals[0])
			if v != "" && phpIniKeyRe.MatchString(k) {
				lines = append(lines, k+" = "+v)
			}
		}
		// PostForm map iteration is unordered; the agent writes the file
		// as-is, but the sort order is cosmetic. Keep it simple.
		content = strings.Join(lines, "\n")
		// Also support the flat input name "ini[key]" — Go's PostForm
		// already iterates them; done above.
		if raw := r.PostFormValue("ini_text"); raw != "" {
			content = raw
		}
	}
	if _, err := s.agent.Call(r.Context(), "php.ini_set", map[string]any{"version": version, "content": content}, 60*time.Second); err != nil {
		phpAudit(r, user, "php.ini", "php", version, audit.Failure, map[string]any{"error": err.Error()})
		phpFlashErr(w, r, agentMessage(err), to)
		return
	}
	phpAudit(r, user, "php.ini", "php", version, audit.Success, nil)
	sess := sessionFrom(r.Context())
	sess.Flash("success", "php.ini settings of PHP "+version+" saved; PHP-FPM was reloaded.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// phpFpm POST /php/{version}/fpm — save PHP-FPM pool settings.
func (s *Server) phpFpm(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	version := r.PathValue("version")
	to := "/php/" + version + "?tab=fpm"
	if !phpVersionRe.MatchString(version) {
		phpFlashErr(w, r, "Unknown PHP version.", "/php")
		return
	}
	_ = r.ParseForm()
	settings := map[string]any{"version": version, "log_level": r.PostFormValue("log_level")}
	for _, k := range []string{"emergency_restart_threshold", "emergency_restart_interval_seconds", "process_control_timeout_seconds", "process_max"} {
		raw := strings.TrimSpace(r.PostFormValue(k))
		n := 0
		if raw != "" {
			if v, err := strconv.Atoi(raw); err == nil {
				n = v
			}
		}
		settings[k] = n
	}
	if _, err := s.agent.Call(r.Context(), "php.fpm_set", settings, 60*time.Second); err != nil {
		phpAudit(r, user, "php.fpm", "php", version, audit.Failure, map[string]any{"error": err.Error()})
		phpFlashErr(w, r, agentMessage(err), to)
		return
	}
	phpAudit(r, user, "php.fpm", "php", version, audit.Success, nil)
	sess := sessionFrom(r.Context())
	sess.Flash("success", "PHP-FPM settings of PHP "+version+" saved; PHP-FPM was reloaded.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// phpModules GET /php/{version}/modules — JSON list of loaded modules (used
// by the per-site runtime form).
func (s *Server) phpModules(w http.ResponseWriter, r *http.Request) {
	version := r.PathValue("version")
	if !phpVersionRe.MatchString(version) {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false})
		return
	}
	res, err := s.agent.Call(r.Context(), "php.modules", map[string]any{"version": version}, 30*time.Second)
	if err != nil {
		code := http.StatusBadGateway
		var ae *agent.Error
		if errors.As(err, &ae) && ae.Status != 0 {
			code = ae.Status
		}
		writeJSON(w, code, map[string]any{"success": false, "error": agentErrMap(err)})
		return
	}
	var modules any
	_ = json.Unmarshal(res, &modules)
	// Unwrap {success,data} if present.
	if m, ok := modules.(map[string]any); ok {
		if s, ok := m["success"].(bool); ok && s {
			if d, ok := m["modules"]; ok {
				modules = d
			}
		} else if data, ok := m["data"]; ok {
			modules = data
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "modules": modules})
}

// ---- helpers ----

func phpAudit(r *http.Request, user *identity.User, action, targetType, targetID, result string, details map[string]any) {
	var uid *int64
	if user != nil {
		id := user.ID
		uid = &id
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Result:     result,
		Details:    details,
		UserID:     uid,
		Username:   usernameOr(user),
	})
}

func phpFlashErr(w http.ResponseWriter, r *http.Request, msg, to string) {
	sess := sessionFrom(r.Context())
	sess.Flash("error", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}
