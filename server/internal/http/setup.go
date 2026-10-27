package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// setupRunPermissions are what running the setup needs: it acts on all of these
// (SetupController::RUN_PERMISSIONS).
var setupRunPermissions = []string{"server.manage", "packages.manage", "php.manage", "services.manage", "security.manage",
	"mail.manage", "dns.manage", "ftp.manage", "plans.manage", "backups.manage", "settings.manage"}

func (s *Server) maySetup(r *http.Request) bool {
	for _, p := range setupRunPermissions {
		if !s.can(r, p) {
			return false
		}
	}
	return true
}

func (s *Server) serverSetup(r *http.Request, user *identity.User) *hosting.ServerSetup {
	svc := hosting.NewServerSetup(s.db, s.currentServerID(r), s.agent, s.settings, hosting.NewPlanRepository(s.db), s.features,
		hosting.NewBackupScheduler(s.db, s.currentServerID(r), s.backupsAudit(r, user)), s.websiteAuditHook(r, user))
	svc.ActorID = user.ID
	return svc
}

type checkItem struct {
	ID, Label, Detail, Route string
	Done, CanOpen            bool
}

type setupStepView struct{ ID, Label, Status, Detail string }

// setupIndex GET /setup — the setup wizard: choose a server profile and run its
// steps from the browser, see what is done and what is left. Mirrors
// SetupController::index.
func (s *Server) setupIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess := sessionFrom(ctx)
	user := userFrom(ctx)
	if seen, _ := s.settings.Bool(ctx, "setup_wizard_seen", false); !seen {
		_ = s.settings.Set(ctx, "setup_wizard_seen", true, user.ID)
	}
	data := map[string]any{
		"CSRF": session.CSRFToken(sess), "User": user, "Title": "Setup wizard",
		"Success": pullFlashKey(sess, "success"), "Error": pullFlashKey(sess, "error"), "AgentError": "",
	}
	var components []any
	memoryMB := 0
	if raw, err := s.agent.Call(ctx, "components.status", nil, 60*time.Second); err != nil {
		data["AgentError"] = agentMessage(err)
	} else {
		_ = json.Unmarshal(raw, &components)
		if raw2, err := s.agent.Call(ctx, "system.metrics", nil, 30*time.Second); err == nil {
			memoryMB = int(jnum(agentObject(raw2), "memory", "total_bytes")) / (1024 * 1024)
		}
	}
	svc := s.serverSetup(r, user)
	last := svc.Last(ctx)
	recommended := "web"
	switch {
	case memoryMB == 0:
	case memoryMB >= 3800:
		recommended = "full"
	case memoryMB < 1800:
		recommended = "minimal"
	}
	profile, php, thirdParty, hostname := recommended, hosting.SetupPHPDefault, true, ""
	if last != nil {
		profile, php, thirdParty, hostname = last.Profile, last.Options.PHP, last.Options.ThirdPartyRepos, last.Options.Hostname
	}
	if hostname == "" {
		if h, _ := os.Hostname(); strings.Contains(h, ".") {
			hostname = strings.ToLower(h)
		}
	}
	type profileView struct {
		ID, Name, Description, For string
		Recommended, Checked       bool
		Steps                      []setupStepView
	}
	desc := map[string][2]string{
		"full":    {"Web server, PHP, MariaDB, email (Postfix, Dovecot, OpenDKIM, Rspamd), DNS (authoritative), FTP, Redis, Memcached, ClamAV, firewall with protections, Fail2ban, hardening, SMTP restrictions, automatic security updates.", "4 GB of memory or more"},
		"web":     {"Web server, PHP, MariaDB, FTP, Redis, Memcached, firewall with protections, Fail2ban, hardening, automatic security updates. No email, DNS or malware scanner.", "2 GB or more"},
		"minimal": {"Web server, PHP, MariaDB, Let's Encrypt, firewall, Fail2ban, hardening, automatic security updates.", "less than 2 GB"},
	}
	var profiles []profileView
	for _, p := range hosting.SetupProfileOrder {
		v := profileView{ID: p, Name: hosting.SetupProfileNames[p], Description: desc[p][0], For: desc[p][1], Recommended: p == recommended, Checked: p == profile}
		for _, id := range hosting.StepsFor(p) {
			st := setupStepView{ID: id, Label: hosting.SetupStepLabel(id)}
			if last != nil && last.Profile == p {
				st.Status, st.Detail = last.Steps[id].Status, last.Steps[id].Detail
			}
			st.Status = strings.TrimSpace(st.Status)
			v.Steps = append(v.Steps, st)
		}
		profiles = append(profiles, v)
	}
	type phpChoice struct {
		V       string
		Checked bool
		ID      string
	}
	var choices []phpChoice
	for _, v := range hosting.SetupPHPChoices {
		choices = append(choices, phpChoice{v, hasString(php, v), strings.ReplaceAll(v, ".", "")})
	}
	data["Profiles"], data["PHPChoices"], data["Hostname"], data["ThirdParty"] = profiles, choices, hostname, thirdParty
	data["MemoryGB"] = fmt.Sprintf("%.1f", float64(memoryMB)/1024)
	data["HasMemory"] = memoryMB > 0
	data["MayRun"] = s.maySetup(r)
	data["Done"], _ = s.settings.Bool(ctx, "setup_wizard_done", false)
	data["Checklist"], data["Left"] = s.setupChecklist(r, user, last, components, profile)
	data["HasLast"] = last != nil
	if last != nil {
		fin := ""
		if last.FinishedAt == nil {
			fin = " (not finished)"
		}
		started := strings.Replace(last.StartedAt, "T", " ", 1)
		if len(started) > 16 {
			started = started[:16]
		}
		data["LastSummary"] = hosting.SetupProfileNames[last.Profile] + ", " + started + " UTC" + fin
	}
	s.render(w, "setup_index", data)
}

// setupChecklist is what is recommended after the setup, done or not.
func (s *Server) setupChecklist(r *http.Request, user *identity.User, last *hosting.SetupState, components []any, profile string) ([]checkItem, int) {
	ctx := r.Context()
	count := func(q string, args ...any) int {
		var n int
		_ = s.db.QueryRowContext(ctx, q, args...).Scan(&n)
		return n
	}
	var failed []string
	if last != nil {
		for id, st := range last.Steps {
			if st.Status == "fail" {
				failed = append(failed, id)
			}
		}
	}
	detail := "Choose a profile and run the setup below."
	if last != nil {
		detail = "The last setup finished without failures."
		if len(failed) > 0 {
			detail = fmt.Sprintf("%d step(s) failed in the last setup: run it again once fixed.", len(failed))
		}
	}
	items := []checkItem{{ID: "setup", Label: "Set up the server", Detail: detail, Route: "/setup", Done: last != nil && last.FinishedAt != nil && len(failed) == 0}}
	if components != nil {
		expected := hosting.SetupProfiles[profile]
		if saved, _ := s.settings.String(ctx, "components_expected", ""); saved != "" && saved != "none" {
			expected = strings.Split(saved, ",")
		}
		var notReady []string
		for _, c := range components {
			m := jmap(c)
			if hasString(expected, jstr(m, "id")) && !jbool(m, "ready") {
				notReady = append(notReady, jstr(m, "name"))
			}
		}
		d := "Everything this server should have is installed, set up and running."
		if len(notReady) > 0 {
			d = "Needs attention: " + strings.Join(notReady, ", ") + "."
		}
		items = append(items, checkItem{ID: "components", Label: "Every component ready", Detail: d, Route: "/components", Done: len(notReady) == 0})
	}
	tf, _ := s.twoFactor.Enabled(ctx, user.ID)
	key, _ := s.wa.Has(user.ID)
	items = append(items,
		checkItem{ID: "two_factor", Label: "Two-factor sign-in for your account", Detail: "A code from an app or a security key besides your password.", Route: "/profile", Done: tf || key},
		checkItem{ID: "remote_backups", Label: "Backups kept off this server", Detail: "Add a remote storage (S3, SFTP, FTP or Google Drive) and use it in the backup schedules.", Route: "/backups",
			Done: count(`SELECT COUNT(*) FROM backup_storages WHERE active = 1`) > 0},
		checkItem{ID: "backup_key", Label: "Backup key downloaded", Detail: "Encrypted backups cannot be restored on a new server without it.", Route: "/backups",
			Done: count(`SELECT COUNT(*) FROM audit_logs WHERE action = 'backup.key_export' AND result = 'success'`) > 0},
		checkItem{ID: "account", Label: "First hosting account", Detail: "Create an account with a plan and its website.", Route: "/accounts/new",
			Done: count(`SELECT COUNT(*) FROM websites WHERE server_id = ?`, s.currentServerID(r)) > 0})
	left := 0
	for i := range items {
		if !items[i].Done {
			left++
		}
		if items[i].ID != "setup" && !items[i].Done {
			items[i].CanOpen = s.canOpenPath(r, items[i].Route)
		}
	}
	return items, left
}

// canOpenPath reports whether the user may use a GET path (route permission and plan feature).
func (s *Server) canOpenPath(r *http.Request, path string) bool {
	perm := map[string]string{"/components": "server.view", "/backups": "backups.view", "/accounts/new": "users.manage", "/profile": ""}[path]
	if perm != "" && !s.can(r, perm) {
		return false
	}
	name := map[string]string{"/components": "components", "/backups": "backups", "/accounts/new": "accounts.create"}[path]
	if name == "" {
		return true
	}
	ok, err := s.authorizer().RouteAllowed(r.Context(), userFrom(r.Context()), name)
	return err == nil && ok
}

// setupStep POST /setup/step — runs one wizard step (JSON). A step that starts
// a job returns "running" with the job id; the page follows the job and calls
// the step again with it. Mirrors SetupController::step.
func (s *Server) setupStep(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if !session.ValidateCSRF(sess, formToken(r)) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": map[string]any{"message": "Page expired. Reload the page and try again."}})
		return
	}
	if !s.maySetup(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "error": map[string]any{"message": "Running the setup needs an administrator: it installs packages and changes the firewall, email, DNS, plans and backups."}})
		return
	}
	var in struct {
		Profile         string   `json:"profile"`
		Hostname        string   `json:"hostname"`
		PHP             []string `json:"php"`
		ThirdPartyRepos *bool    `json:"third_party_repos"`
		Step            string   `json:"step"`
		AfterJob        string   `json:"after_job"`
		Round           int      `json:"round"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": map[string]any{"message": "The request could not be read."}})
		return
	}
	fail := func(code int, msg string) {
		writeJSON(w, code, map[string]any{"success": false, "error": map[string]any{"message": msg}})
	}
	for _, v := range in.PHP {
		if !hasString(hosting.SetupPHPChoices, v) {
			fail(http.StatusUnprocessableEntity, "Choose PHP versions from the list.")
			return
		}
	}
	third := true
	if in.ThirdPartyRepos != nil {
		third = *in.ThirdPartyRepos
	}
	if in.Round < 0 {
		in.Round = 0
	}
	res, err := s.serverSetup(r, user).RunStep(r.Context(), in.Profile, hosting.SetupOptions{Hostname: in.Hostname, PHP: in.PHP, ThirdPartyRepos: third}, in.Step, in.AfterJob, in.Round)
	if err != nil {
		var ve *hosting.ValidationError
		var ue *hosting.UserError
		switch {
		case errors.As(err, &ve):
			fail(http.StatusUnprocessableEntity, firstValidationError(ve))
		case errors.As(err, &ue):
			fail(http.StatusConflict, ue.What)
		default:
			fail(http.StatusServiceUnavailable, agentMessage(err))
		}
		return
	}
	if res.Status == "running" && in.AfterJob == "" {
		uid := user.ID
		s.audit.Record(r.Context(), audit.Entry{Action: "server.setup_step", TargetType: "server", TargetID: itoa(s.currentServerID(r)), Result: audit.Success,
			Details: map[string]any{"profile": in.Profile, "step": res.ID, "job_id": res.JobID}, UserID: &uid, Username: user.Username})
	}
	step := map[string]any{"id": res.ID, "label": res.Label, "status": res.Status, "detail": res.Detail}
	if res.JobID != "" {
		step["job_id"] = res.JobID
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "step": step})
}

// setupDismiss POST /setup/dismiss — closes the wizard so the dashboard
// no longer nags; fully implemented (just settings + audit). Mirrors
// SetupController::dismiss (setup.dismiss, server.manage).
func (s *Server) setupDismiss(w http.ResponseWriter, r *http.Request) {
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
	if err := s.settings.Set(r.Context(), "setup_wizard_seen", true, user.ID); err != nil {
		s.log.Error("set setup_wizard_seen", "error", err)
	}
	if err := s.settings.Set(r.Context(), "setup_wizard_done", true, user.ID); err != nil {
		s.log.Error("set setup_wizard_done", "error", err)
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "server.setup_dismiss", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Success, UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "The setup wizard is closed. Reopen it any time under Server → Setup wizard.")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
