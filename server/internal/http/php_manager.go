package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// phpManagerIndex GET /php/manager — EasyApache-style PHP version/extension
// picker. The agent-backed plan computation is not ported yet, so this
// handler best-effort fetches `discovery.php` and `php.available` from the
// agent and shows a friendly notice that review/apply are not available
// yet. Mirrors PhpManagerController::index (php.manager, packages.manage).
func (s *Server) phpManagerIndex(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "PHP Manager",
		"Success":    "",
		"Error":      "",
		"Discovery":  nil,
		"Available":  nil,
		"AgentError": nil,
		"Stub":       true,
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	if raw, err := s.agent.Call(r.Context(), "discovery.php", nil, 60*time.Second); err == nil {
		var d any
		_ = json.Unmarshal(raw, &d)
		data["Discovery"] = d
	} else {
		data["AgentError"] = err.Error()
	}
	if raw, err := s.agent.Call(r.Context(), "php.available", nil, 60*time.Second); err == nil {
		var a any
		_ = json.Unmarshal(raw, &a)
		data["Available"] = a
	}
	s.render(w, "php_manager", data)
}

// phpManagerReview POST /php/manager/review — plans the install/update/
// remove changes for a submitted PHP profile. Stub until the agent
// `php.profile_plan` call is wired in.
func (s *Server) phpManagerReview(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "PHP Manager review is not yet ported in the Go panel.")
	http.Redirect(w, r, "/php/manager", http.StatusSeeOther)
}

// phpManagerApply POST /php/manager/apply — applies a PHP profile as a
// background job. Stub until `php.profile_apply` is wired in.
func (s *Server) phpManagerApply(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "php.profile_apply", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Failure, Details: map[string]any{"stub": true},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("error", "PHP Manager apply is not yet ported in the Go panel.")
	http.Redirect(w, r, "/php/manager", http.StatusSeeOther)
}

// phpManagerExport GET /php/manager/export — downloads the saved PHP
// profile as a JSON file. If no profile is saved (settings.php_profile
// empty), returns an empty profile with format/version markers so the
// export file is always valid. Mirrors PhpManagerController::export
// (php.manager.export, packages.manage).
func (s *Server) phpManagerExport(w http.ResponseWriter, r *http.Request) {
	raw, err := s.settings.String(r.Context(), "php_profile", "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var body []byte
	if raw == "" {
		body = []byte("{\"format\":\"whacp-php-profile\",\"version\":1,\"versions\":[],\"extensions\":[]}\n")
	} else {
		// Re-emit the stored JSON so it is always pretty-printed.
		var parsed any
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			http.Error(w, "saved profile is not valid JSON", http.StatusInternalServerError)
			return
		}
		out, err := json.MarshalIndent(parsed, "", "  ")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		body = append(out, '\n')
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\"whacp-php-profile.json\"")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// phpManagerImport POST /php/manager/import — uploads a whacp-php-profile
// JSON file and forwards it to review. Stub: validates the upload shape
// but returns a not-yet-ported flash because review/apply are not wired.
func (s *Server) phpManagerImport(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseMultipartForm(64 * 1024); err != nil {
		sess.Flash("error", "Could not read the uploaded file.")
		http.Redirect(w, r, "/php/manager", http.StatusSeeOther)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	up, fh, err := r.FormFile("profile")
	if err != nil {
		sess.Flash("error", "Choose a PHP profile file (whacp-php-profile.json).")
		http.Redirect(w, r, "/php/manager", http.StatusSeeOther)
		return
	}
	defer up.Close()
	if fh.Size > 65536 {
		sess.Flash("error", "Profile file too large (max 64KB).")
		http.Redirect(w, r, "/php/manager", http.StatusSeeOther)
		return
	}
	buf := make([]byte, fh.Size)
	if _, err := up.Read(buf); err != nil {
		sess.Flash("error", "Could not read the profile file.")
		http.Redirect(w, r, "/php/manager", http.StatusSeeOther)
		return
	}
	var parsed struct {
		Format     string   `json:"format"`
		Version    int      `json:"version"`
		Versions   []string `json:"versions"`
		Extensions []string `json:"extensions"`
	}
	if err := json.Unmarshal(buf, &parsed); err != nil || parsed.Format != "whacp-php-profile" {
		sess.Flash("error", "This is not a WhaCP PHP profile file.")
		http.Redirect(w, r, "/php/manager", http.StatusSeeOther)
		return
	}
	if len(parsed.Versions) == 0 {
		sess.Flash("error", "The profile has no PHP version.")
		http.Redirect(w, r, "/php/manager", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "php.profile_import", TargetType: "server",
		TargetID: itoa(s.currentServerID(r)), Result: audit.Success,
		Details: map[string]any{"versions": parsed.Versions},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("error", "PHP Manager import/review/apply are not fully ported in the Go panel; the file shape was accepted but no plan was computed.")
	http.Redirect(w, r, "/php/manager", http.StatusSeeOther)
}
