package http

import (
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

var phpVersionRE = regexp.MustCompile(`^[5-9]\.[0-9]{1,2}$`)
var phpExtRE = regexp.MustCompile(`^[a-z][a-z0-9_]{1,30}$`)

// phpProfileFromRequest reads and validates the versions/extensions lists
// from a PHP Manager form. Mirrors PhpManagerController::fromRequest.
func phpProfileFromRequest(r *http.Request) (versions, exts []string) {
	for _, v := range append(append([]string{}, r.Form["versions[]"]...), r.Form["versions"]...) {
		v = trimWS(v)
		if phpVersionRE.MatchString(v) {
			versions = append(versions, v)
		}
	}
	for _, e := range append(append([]string{}, r.Form["extensions[]"]...), r.Form["extensions"]...) {
		e = trimWS(e)
		if phpExtRE.MatchString(e) {
			exts = append(exts, e)
		}
	}
	return versions, exts
}

func trimWS(s string) string {
	// small helper; we already import strings via several files but keep
	// imports minimal here.
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// phpManagerIndex GET /php/manager — EasyApache-style PHP version/extension
// picker. Best-effort fetches `discovery.php` and `php.available` from the
// agent and renders the selection form. Mirrors PhpManagerController::index
// (php.manager, packages.manage).
func (s *Server) phpManagerIndex(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	data, err := s.phpManagerView(r.Context())
	if err != nil {
		data = map[string]any{"AgentError": err.Error()}
	}
	data["CSRF"] = session.CSRFToken(sess)
	data["User"] = user
	data["Title"] = "PHP Manager"
	data["Success"], data["Error"] = "", ""
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	s.render(w, "php_manager", data)
}

// phpManagerReview POST /php/manager/review — asks the agent to plan the
// install/update/remove set for a submitted PHP profile and renders a
// review page. Mirrors PhpManagerController::review (php.manager,
// packages.manage).
func (s *Server) phpManagerReview(w http.ResponseWriter, r *http.Request) {
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
	versions, exts := phpProfileFromRequest(r)
	if len(versions) == 0 {
		sess.Flash("error", "Choose at least one PHP version.")
		http.Redirect(w, r, "/php/manager", http.StatusSeeOther)
		return
	}
	profile := map[string]any{"versions": versions, "extensions": exts}
	raw, err := s.agent.Call(r.Context(), "php.profile_plan", profile, 120*time.Second)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/php/manager", http.StatusSeeOther)
		return
	}
	var plan any
	_ = json.Unmarshal(raw, &plan)
	s.renderPhpReview(w, session.CSRFToken(sess), user, versions, exts, plan)
}

// phpManagerApply POST /php/manager/apply — applies a PHP profile as a
// background job (php.profile_apply), tracks the job, saves the profile
// (with job_id/applied_at) as settings.php_profile, and redirects back to
// /php/manager with a job flash. Mirrors PhpManagerController::apply.
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
	versions, exts := phpProfileFromRequest(r)
	if len(versions) == 0 {
		sess.Flash("error", "Choose at least one PHP version.")
		http.Redirect(w, r, "/php/manager", http.StatusSeeOther)
		return
	}
	profile := map[string]any{
		"versions":         versions,
		"extensions":       exts,
		"confirm_removals": r.FormValue("confirm_removals") == "1",
	}
	raw, err := s.agent.Call(r.Context(), "php.profile_apply", profile, 120*time.Second)
	uid := user.ID
	if err != nil {
		s.audit.Record(r.Context(), audit.Entry{
			Action: "php.profile_apply", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
			Result:  audit.Failure,
			Details: map[string]any{"versions": versions, "error": err.Error()},
			UserID:  &uid, Username: user.Username,
		})
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/php/manager", http.StatusSeeOther)
		return
	}
	var res struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(raw, &res)
	if res.JobID != "" {
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), res.JobID, "Apply PHP profile", "server", itoa(s.currentServerID(r)), &uid)
		sess.Flash("job", res.JobID)
	}
	// Remember the profile (with job_id/applied_at) so subsequent visits
	// show the saved selection. A settings.Set failure is non-fatal.
	saved := map[string]any{
		"versions":   versions,
		"extensions": exts,
		"job_id":     res.JobID,
		"applied_at": time.Now().UTC().Format("2006-01-02 15:04"),
	}
	if savedJSON, err := json.Marshal(saved); err == nil {
		_ = s.settings.Set(r.Context(), "php_profile", string(savedJSON), user.ID)
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "php.profile_apply", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result:  audit.Success,
		Details: map[string]any{"versions": versions, "extensions": exts, "job_id": res.JobID},
		UserID:  &uid, Username: user.Username,
	})
	sess.Flash("success", "Applying the PHP profile… Follow the progress in the job log.")
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
// JSON file, validates it, and sends it through the same review pipeline
// (php.profile_plan) so the administrator can confirm before applying.
// Mirrors PhpManagerController::import (php.manager, packages.manage).
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
	versions := []string{}
	exts := []string{}
	for _, v := range parsed.Versions {
		v = trimWS(v)
		if phpVersionRE.MatchString(v) {
			versions = append(versions, v)
		}
	}
	for _, e := range parsed.Extensions {
		e = trimWS(e)
		if phpExtRE.MatchString(e) {
			exts = append(exts, e)
		}
	}
	if len(versions) == 0 {
		sess.Flash("error", "The profile has no PHP version.")
		http.Redirect(w, r, "/php/manager", http.StatusSeeOther)
		return
	}
	profile := map[string]any{"versions": versions, "extensions": exts}
	raw, err := s.agent.Call(r.Context(), "php.profile_plan", profile, 120*time.Second)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/php/manager", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "php.profile_import", TargetType: "server",
		TargetID: itoa(s.currentServerID(r)), Result: audit.Success,
		Details: map[string]any{"versions": versions},
		UserID:  &uid, Username: user.Username,
	})
	var plan any
	_ = json.Unmarshal(raw, &plan)
	s.renderPhpReview(w, session.CSRFToken(sess), user, versions, exts, plan)
}
