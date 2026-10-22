package http

// This file ports the shell-access page (ShellController, Security → Shell
// access): the website accounts' disabled/jailed/normal shell modes (the
// agent applies them while the account's SSH/SFTP login is on), the change
// forms (one account or every account) and, for the unconfirmed-security-
// change banner, the confirm/undo routes (SecurityController::confirm/undo,
// the agent's firewall/SSH guard).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

var shellUserRe = regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,31}$`)

// shellError is the shell area's UserFacingException (flash + redirect).
type shellError struct{ Message string }

func (e *shellError) Error() string { return e.Message }

// shellViewRow is one website-account row of the page.
type shellViewRow struct {
	User           string
	Owner          string
	Websites       string
	SSHLogin       bool
	Keys           int
	PasswordLogin  bool
	Mode           string
	Effective      string
	EffectiveLabel string
	EffectiveClass string
}

// shellStatusData is the agent's shell.status plus the websites'
// domains/owner per account (ShellService::status).
type shellStatusData struct {
	Accounts    []shellAccount
	JailProblem string
}

type shellAccount struct {
	User          string
	Folder        string
	Mode          string
	Effective     string
	SSHLogin      bool
	Keys          int
	PasswordLogin bool
	LoginShell    string
	Domains       []string
	Owner         string
}

func (s *Server) shellStatusData(ctx context.Context, serverID int64) (*shellStatusData, error) {
	res, err := s.agent.Call(ctx, "shell.status", nil)
	if err != nil {
		return nil, err
	}
	var st struct {
		Accounts []struct {
			User          string `json:"user"`
			Folder        string `json:"folder"`
			Mode          string `json:"mode"`
			Effective     string `json:"effective"`
			SSHLogin      bool   `json:"ssh_login"`
			Keys          int    `json:"keys"`
			PasswordLogin bool   `json:"password_login"`
			LoginShell    string `json:"login_shell"`
		} `json:"accounts"`
		JailProblem string `json:"jail_problem"`
	}
	if err := json.Unmarshal(res, &st); err != nil {
		return nil, err
	}
	type site struct {
		domains []string
		owner   string
	}
	sites := map[string]*site{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT w.system_user, w.domain, u.username
		FROM websites w LEFT JOIN users u ON u.id = w.owner_id
		WHERE w.server_id = ? ORDER BY w.domain`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var su, dom string
		var owner sql.NullString
		if err := rows.Scan(&su, &dom, &owner); err != nil {
			return nil, err
		}
		si := sites[su]
		if si == nil {
			si = &site{}
			sites[su] = si
		}
		si.domains = append(si.domains, dom)
		if owner.Valid {
			si.owner = owner.String
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := &shellStatusData{JailProblem: st.JailProblem, Accounts: []shellAccount{}}
	for _, a := range st.Accounts {
		acc := shellAccount{User: a.User, Folder: a.Folder, Mode: a.Mode, Effective: a.Effective,
			SSHLogin: a.SSHLogin, Keys: a.Keys, PasswordLogin: a.PasswordLogin, LoginShell: a.LoginShell}
		if si := sites[a.User]; si != nil {
			acc.Domains = si.domains
			acc.Owner = si.owner
		}
		out.Accounts = append(out.Accounts, acc)
	}
	return out, nil
}

// shellRows renders the accounts (the PHP view's labels, domains and
// login summary — the template can't do string formatting).
func shellRows(st *shellStatusData) []shellViewRow {
	labels := map[string][2]string{
		"disabled": {"Disabled", "is-off"},
		"jailed":   {"Jailed", "is-ok"},
		"normal":   {"Normal", "is-warn"},
	}
	rows := make([]shellViewRow, 0, len(st.Accounts))
	for _, a := range st.Accounts {
		row := shellViewRow{
			User: a.User, Owner: a.Owner,
			Websites:      a.Folder,
			SSHLogin:      a.SSHLogin, Keys: a.Keys, PasswordLogin: a.PasswordLogin,
			Mode:          a.Mode, Effective: a.Effective,
			EffectiveLabel: a.Effective, EffectiveClass: "",
		}
		if l, ok := labels[a.Effective]; ok {
			row.EffectiveLabel, row.EffectiveClass = l[0], l[1]
		}
		if len(a.Domains) > 0 {
			row.Websites = strings.Join(a.Domains, ", ")
		}
		rows = append(rows, row)
	}
	return rows
}

// securityPending is the unconfirmed security change (nil when none) —
// SecurityService::pending.
func (s *Server) securityPending(ctx context.Context) map[string]any {
	res, err := s.agent.Call(ctx, "security.pending", nil)
	if err != nil {
		return nil
	}
	var out struct {
		Pending map[string]any `json:"pending"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil
	}
	return out.Pending
}

// shellAccessIndex renders the shell-access page (ShellController::index,
// security.view, all-scope only).
func (s *Server) shellAccessIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		renderStatus(w, s, "error", map[string]any{"Title": "Access denied", "Message": "Only administrators manage shell access."}, http.StatusForbidden)
		return
	}
	sess := sessionFrom(r.Context())
	status, err := s.shellStatusData(r.Context(), s.currentServerID(r))
	agentMsg := ""
	var ae *agent.Error
	if errors.As(err, &ae) {
		agentMsg = ae.Message
	} else if err != nil {
		s.log.Error("shell status", "error", err)
		agentMsg = "The WhaCP agent could not be reached."
	}
	ok, _ := s.authorizer().Can(r.Context(), user, "security.manage")
	okMsg, _ := sess.PullFlash("success")
	errMsg, _ := sess.PullFlash("error")
	s.render(w, "security_shell", map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"Status":     status,
		"Rows":       func() []shellViewRow {
			if status == nil {
				return nil
			}
			return shellRows(status)
		}(),
		"AgentError": agentMsg,
		"Pending":    s.securityPending(r.Context()),
		"CanManage":  ok && user.RoleName == "admin",
		"Success":    okMsg,
		"Error":      errMsg,
	})
}

// shellSetResult is one account of the set reply (agent + skipped).
type shellSetResult struct {
	User      string
	Mode      string
	Effective string
	Skipped   bool
}

// shellAccessUpdate sets shell access (ShellController::update, form POST).
func (s *Server) shellAccessUpdate(w http.ResponseWriter, r *http.Request) {
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
	to := "/security/shell"
	mode := r.FormValue("mode")
	all := r.FormValue("scope") == "all"
	var users []string
	if all {
		st, err := s.shellStatusData(r.Context(), s.currentServerID(r))
		if err != nil {
			var ae *agent.Error
			msg := "The WhaCP agent could not be reached."
			if errors.As(err, &ae) && ae.Message != "" {
				msg = ae.Message
			}
			sess.Flash("error", msg)
			http.Redirect(w, r, to, http.StatusSeeOther)
			return
		}
		for _, a := range st.Accounts {
			users = append(users, a.User)
		}
	} else {
		users = r.Form["users[]"]
	}
	if all && r.FormValue("confirm") != "all" {
		sess.Flash("error", "Confirm the change for every account.")
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	done, err := s.setShellAccess(r, user, users, mode)
	if err != nil {
		var se *shellError
		if errors.As(err, &se) {
			sess.Flash("error", se.Message)
		} else {
			var ae *agent.Error
			msg := "The WhaCP agent could not be reached."
			if errors.As(err, &ae) && ae.Message != "" {
				msg = ae.Message
			}
			s.log.Error("shell set", "error", err)
			sess.Flash("error", msg)
		}
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	var skipped []string
	waiting, changed := 0, 0
	for _, a := range done {
		if a.Skipped {
			skipped = append(skipped, a.User)
			continue
		}
		changed++
		if a.Effective != mode {
			waiting++
		}
	}
	msg := fmt.Sprintf("Shell access set to %s for %d account(s).", mode, changed)
	if waiting > 0 && mode != "disabled" {
		msg += fmt.Sprintf(" %d of them have no SSH/SFTP login yet: it takes effect once their login is turned on (FTP / SFTP page).", waiting)
	}
	if len(skipped) > 0 {
		msg += " Skipped (their plan does not include SSH): " + strings.Join(skipped, ", ") + "."
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// setShellAccess is ShellService::set: the validated mode change (agent
// shell.set, 120 s), the plan-SSH skip and the audit.
func (s *Server) setShellAccess(r *http.Request, user *identity.User, users []string, mode string) ([]shellSetResult, error) {
	ctx := r.Context()
	serverID := s.currentServerID(r)
	if user.RoleName != "admin" {
		return nil, &shellError{Message: "Only administrators can change shell access."}
	}
	if mode != "disabled" && mode != "jailed" && mode != "normal" {
		return nil, &shellError{Message: "Choose disabled, jailed or normal."}
	}
	seen := map[string]bool{}
	clean := []string{}
	for _, u := range users {
		if shellUserRe.MatchString(u) && !seen[u] {
			seen[u] = true
			clean = append(clean, u)
		}
	}
	if len(clean) == 0 {
		return nil, &shellError{Message: "Select at least one account."}
	}
	// A shell only for accounts whose plan includes SSH.
	var skipped []string
	if mode != "disabled" {
		keep := []string{}
		for _, u := range clean {
			skip, err := s.accountSSHDisabled(ctx, serverID, u)
			if err != nil {
				return nil, err
			}
			if skip {
				skipped = append(skipped, u)
				continue
			}
			keep = append(keep, u)
		}
		if len(keep) == 0 {
			return nil, &shellError{Message: "The hosting plan of " + strings.Join(skipped, ", ") + " does not include SSH (Feature Manager)."}
		}
		clean = keep
	}
	res, err := s.agent.Call(ctx, "shell.set", map[string]any{"users": clean, "mode": mode}, 120*time.Second)
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{
		UserID: &user.ID, Username: user.Username, Action: "shell.set",
		TargetType: "server", TargetID: strconv.FormatInt(serverID, 10), Result: audit.Success,
		IP: clientIP(r), UserAgent: r.UserAgent(),
		Details: map[string]any{"mode": mode, "users": clean},
	})
	var out struct {
		Accounts []struct {
			User      string `json:"user"`
			Mode      string `json:"mode"`
			Effective string `json:"effective"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	done := make([]shellSetResult, 0, len(out.Accounts)+len(skipped))
	for _, a := range out.Accounts {
		done = append(done, shellSetResult{User: a.User, Mode: a.Mode, Effective: a.Effective})
	}
	for _, u := range skipped {
		done = append(done, shellSetResult{User: u, Mode: "disabled", Effective: "disabled", Skipped: true})
	}
	return done, nil
}

// accountSSHDisabled reports whether the account's plan lacks SSH
// (ShellService::set's skip check, via the site's owner).
func (s *Server) accountSSHDisabled(ctx context.Context, serverID int64, systemUser string) (bool, error) {
	var ownerID sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT owner_id FROM websites WHERE server_id = ? AND system_user = ? AND owner_id IS NOT NULL LIMIT 1`,
		serverID, systemUser).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !ownerID.Valid {
		return false, nil
	}
	account, err := s.users.Find(ctx, ownerID.Int64)
	if errors.Is(err, identity.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	disabled, err := s.features.DisabledFor(ctx, account)
	if err != nil {
		return false, err
	}
	for _, f := range disabled {
		if f == "ssh" {
			return true, nil
		}
	}
	return false, nil
}

// securityConfirm / securityUndo keep or roll back the unconfirmed security
// change (SecurityController::confirm/undo, security.manage). The return_to
// whitelist holds the ported security pages (PHP's list points at the not
// yet ported /security overview and friends).
func (s *Server) securityConfirm(w http.ResponseWriter, r *http.Request) { s.securityGuardAction(w, r, "confirm") }
func (s *Server) securityUndo(w http.ResponseWriter, r *http.Request)   { s.securityGuardAction(w, r, "undo") }

func (s *Server) securityGuardAction(w http.ResponseWriter, r *http.Request, which string) {
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
	id := r.FormValue("id")
	to := r.FormValue("return_to")
	if to != "/security/shell" {
		to = "/security/shell"
	}
	action := "security." + which
	msg := "Change confirmed and kept."
	if which == "undo" {
		msg = "Change undone."
	}
	if _, err := s.agent.Call(r.Context(), action, map[string]any{"id": id}); err != nil {
		var ae *agent.Error
		m := "The WhaCP agent could not be reached."
		if errors.As(err, &ae) && ae.Message != "" {
			m = ae.Message
		}
		s.log.Error(action, "id", id, "error", err)
		sess.Flash("error", m)
	} else {
		s.audit.Record(r.Context(), audit.Entry{
			UserID: &user.ID, Username: user.Username, Action: action,
			TargetType: "server", TargetID: "security", Result: audit.Success,
			IP: clientIP(r), UserAgent: r.UserAgent(), Details: map[string]any{"pending": id},
		})
		sess.Flash("success", msg)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
