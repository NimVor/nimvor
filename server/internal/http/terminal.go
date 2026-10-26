package http

// This file ports the web terminal area (TerminalController). The agent
// runs the shells and serves the WebSocket at /terminal/ws (Nginx proxies
// it to the agent's TCP listener, so the browser talks to the agent
// directly — keystrokes never pass through the panel). The panel decides
// who may open which shell and hands the browser a single-use ticket
// bound to its address; a website account's shell is then signed in with
// the account's Linux password, which goes from the browser to the agent
// only, and a root shell (administrators, when the agent allows it) needs
// the panel password — and authenticator code — confirmed again. Signing
// out ends the sign-in's terminals.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/auth"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

const (
	// TerminalUnlockSeconds is how long a password confirmation unlocks
	// the root terminal (PHP TerminalService::UNLOCK_SECONDS).
	TerminalUnlockSeconds = 900
)

var (
	terminalIDRe    = regexp.MustCompile(`^[a-f0-9]{16}$`)
	terminalOwnerRe = regexp.MustCompile(`^[a-f0-9]{32}$`)
	// PHP preg_replace('/[^\pL\pN @._()#:-]/u', '', label)
	terminalLabelStripRe = regexp.MustCompile(`[^\pL\pN @._()#:-]`)
)

// terminalError is the PHP UserFacingException of the terminal area: a
// message with an HTTP status; a 428 asks the UI to show the password
// confirmation form.
type terminalError struct {
	Status  int
	Message string
}

func (e *terminalError) Error() string { return e.Message }

// terminalJSONError is the controller's jsonError: a UserFacing message
// with its status (clipped to 400-599, unlock flagged on 428), any other
// failure (the agent) as a 502 with its message.
func terminalJSONError(w http.ResponseWriter, err error) {
	status, msg := 502, "The WhaCP agent could not be reached."
	var te *terminalError
	if errors.As(err, &te) {
		status, msg = te.Status, te.Message
	} else {
		var ae *agent.Error
		if errors.As(err, &ae) && ae.Message != "" {
			msg = ae.Message
		}
	}
	if status < 400 || status > 599 {
		status = 400
	}
	unlock := status == http.StatusPreconditionRequired
	writeJSON(w, status, map[string]any{"success": false, "error": map[string]any{"message": msg, "unlock": unlock}})
}

// terminalStatus is the agent's terminal.status.
type terminalStatus struct {
	Enabled     bool             `json:"enabled"`
	RootAllowed bool             `json:"root_allowed"`
	Simulation  bool             `json:"simulation"`
	Sessions    []map[string]any `json:"sessions"`
	MaxSessions int              `json:"max_sessions"`
	MaxPerAcct  int              `json:"max_per_account"`
}

func (s *Server) terminalStatus(ctx context.Context) (*terminalStatus, error) {
	res, err := s.agent.Call(ctx, "terminal.status", nil)
	if err != nil {
		return nil, err
	}
	var st terminalStatus
	if err := json.Unmarshal(res, &st); err != nil {
		return nil, err
	}
	if st.Sessions == nil {
		st.Sessions = []map[string]any{}
	}
	return &st, nil
}

// shellMode is an account's effective shell mode ("" when the account is
// unknown) — the controller's mode lookup for the site page.
func (s *Server) shellMode(ctx context.Context, systemUser string) (string, error) {
	res, err := s.agent.Call(ctx, "shell.status", nil)
	if err != nil {
		return "", err
	}
	var st struct {
		Accounts []struct {
			User      string `json:"user"`
			Effective string `json:"effective"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(res, &st); err != nil {
		return "", err
	}
	for _, a := range st.Accounts {
		if a.User == systemUser {
			return a.Effective, nil
		}
	}
	return "", nil
}

// styleNonce is the per-response CSP style nonce (18 random bytes,
// base64url without padding — PHP's random_bytes(18) variant).
func styleNonce() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// terminalPage renders a terminal page with the per-response CSP (the
// panel's, plus a nonce for the style elements xterm.js creates; scripts
// stay 'self' only). Set before rendering so the security-headers
// middleware leaves the header alone.
func (s *Server) terminalPage(w http.ResponseWriter, name string, data map[string]any) {
	nonce := styleNonce()
	data["Nonce"] = nonce
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'nonce-"+nonce+"'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'")
	s.render(w, name, data)
}

// terminalOwner is this sign-in's owner token (created on first use); the
// agent kills a sign-in's sessions by it on logout.
func terminalOwner(sess *session.Session) string {
	o := sess.Get("terminal_owner")
	if !terminalOwnerRe.MatchString(o) {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		o = hex.EncodeToString(b)
		sess.Set("terminal_owner", o)
	}
	return o
}

// terminalUnlocked reports whether the panel password was confirmed
// within the unlock window.
func terminalUnlocked(sess *session.Session) bool {
	until, _ := strconv.ParseInt(sess.Get("terminal_unlocked_until"), 10, 64)
	return until > time.Now().Unix()
}

// terminalIdleMinutes is the terminal_idle_minutes setting (1..240).
func (s *Server) terminalIdleMinutes(ctx context.Context) int {
	idle, _ := s.settings.Int(ctx, "terminal_idle_minutes", 15)
	return max(1, min(240, idle))
}

// terminalSize is the controller's size(): cols 10..500 (80), rows 5..200
// (24), from the JSON body fetchJson sends.
func terminalSize(r *http.Request) (int, int) {
	var in struct {
		Cols *int `json:"cols"`
		Rows *int `json:"rows"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	cols, rows := 80, 24
	if in.Cols != nil {
		cols = *in.Cols
	}
	if in.Rows != nil {
		rows = *in.Rows
	}
	if cols < 10 {
		cols = 10
	}
	if cols > 500 {
		cols = 500
	}
	if rows < 5 {
		rows = 5
	}
	if rows > 200 {
		rows = 200
	}
	return cols, rows
}

// terminalSite renders the website's terminal page (TerminalController::site,
// sites.terminal, files.manage).
func (s *Server) terminalSite(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	if !ok {
		return
	}
	sess := sessionFrom(r.Context())
	status, _ := s.terminalStatus(r.Context())
	mode, shellErr := s.shellMode(r.Context(), site.SystemUser)
	agentMsg := ""
	var ae *agent.Error
	if errors.As(shellErr, &ae) {
		agentMsg = ae.Message
	} else if shellErr != nil {
		s.log.Error("shell status", "error", shellErr)
	}
	needsCode, _ := s.twoFactor.Enabled(r.Context(), user.ID)
	canServer, _ := s.authorizer().Can(r.Context(), user, "server.manage")
	canSecurity, _ := s.authorizer().Can(r.Context(), user, "security.manage")
	hasShell, _ := s.authorizer().RouteAllowed(r.Context(), user, "security.shell")
	s.terminalPage(w, "site_terminal", map[string]any{
		"User":              user,
		"CSRF":              session.CSRFToken(sess),
		"Site":              site,
		"Enabled":           status != nil && status.Enabled,
		"Mode":              mode,
		"AgentError":        agentMsg,
		"Unlocked":          terminalUnlocked(sess),
		"NeedsCode":         needsCode,
		"OpenURL":           fmt.Sprintf("/sites/%d/terminal/open", site.ID),
		"Idle":              s.terminalIdleMinutes(r.Context()),
		"Account":           site.SystemUser,
		"CanServerManage":   canServer,
		"CanSecurityManage": canSecurity,
		"HasShellRoute":     hasShell,
	})
}

// terminalUnlock re-confirms the panel password (and authenticator code)
// for the root-shell gate (TerminalController::unlock, JSON in/out).
func (s *Server) terminalUnlock(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	code := strings.TrimSpace(in.Code)
	// The attempt counter lives in the database (the login throttle), not in the
	// session: the session is a sealed cookie the client holds, so a counter
	// kept in it can be bypassed by replaying the old cookie after every wrong
	// guess. "terminal:<user>" keeps it apart from the sign-in counters.
	throttle := auth.NewThrottle(s.db)
	tkey := "terminal:" + user.Username
	if retry, err := throttle.RetryAfter(r.Context(), tkey, clientIP(r)); err == nil && retry > 0 {
		mins := int(math.Ceil(float64(retry) / 60))
		terminalJSONError(w, &terminalError{Status: http.StatusTooManyRequests,
			Message: fmt.Sprintf("Too many wrong attempts. Try again in %d minute(s).", mins)})
		return
	}
	var hash string
	if err := s.db.QueryRowContext(r.Context(), "SELECT password_hash FROM users WHERE id = ?", user.ID).Scan(&hash); err != nil {
		s.log.Error("terminal unlock", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	needsCode, _ := s.twoFactor.Enabled(r.Context(), user.ID)
	okay := in.Password != "" && hash != "" && identity.VerifyPassword(hash, in.Password)
	if okay && needsCode {
		_, vErr := s.twoFactor.Verify(r.Context(), user.ID, code)
		okay = code != "" && vErr == nil
	}
	if !okay {
		_ = throttle.RecordFailure(r.Context(), tkey, clientIP(r))
		s.audit.Record(r.Context(), audit.Entry{
			UserID: &user.ID, Username: user.Username, Action: "terminal.unlock",
			TargetType: "user", TargetID: strconv.FormatInt(user.ID, 10), Result: audit.Failure,
			IP: clientIP(r), UserAgent: r.UserAgent(),
		})
		msg := "The password is wrong."
		if needsCode {
			msg = "The password or the authenticator code is wrong."
		}
		terminalJSONError(w, &terminalError{Status: http.StatusForbidden, Message: msg})
		return
	}
	_ = throttle.RecordSuccess(r.Context(), tkey, clientIP(r))
	sess.Set("terminal_unlocked_until", strconv.FormatInt(time.Now().Unix()+TerminalUnlockSeconds, 10))
	s.audit.Record(r.Context(), audit.Entry{
		UserID: &user.ID, Username: user.Username, Action: "terminal.unlock",
		TargetType: "user", TargetID: strconv.FormatInt(user.ID, 10), Result: audit.Success,
		IP: clientIP(r), UserAgent: r.UserAgent(),
	})
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"minutes": TerminalUnlockSeconds / 60}})
}

// terminalOpen is the controller's private open(): it calls the agent for
// a ticket bound to the caller's address and audits it. A root shell (the
// target's root flag) needs the confirmed panel password; a website
// account authenticates to the agent with its own Linux password.
func (s *Server) terminalOpen(w http.ResponseWriter, r *http.Request, user *identity.User, target map[string]any, what string) {
	if root, _ := target["root"].(bool); root && !terminalUnlocked(sessionFrom(r.Context())) {
		terminalJSONError(w, &terminalError{Status: http.StatusPreconditionRequired, Message: "Confirm your password to open the terminal."})
		return
	}
	cols, rows := terminalSize(r)
	ip := clientIP(r)
	if net.ParseIP(ip) == nil {
		ip = ""
	}
	label := terminalLabelStripRe.ReplaceAllString(user.Username+" (#"+strconv.FormatInt(user.ID, 10)+")", "")
	if len(label) > 100 {
		label = label[:100]
	}
	maxm, _ := s.settings.Int(r.Context(), "terminal_max_minutes", 480)
	params := map[string]any{
		"cols":         cols,
		"rows":         rows,
		"client_ip":    ip,
		"label":        label,
		"owner":        terminalOwner(sessionFrom(r.Context())),
		"idle_minutes": s.terminalIdleMinutes(r.Context()),
		"max_minutes":  max(15, min(1440, maxm)),
	}
	for k, v := range target {
		params[k] = v
	}
	res, err := s.agent.Call(r.Context(), "terminal.open", params)
	if err != nil {
		terminalJSONError(w, err)
		return
	}
	var out struct {
		Ticket     string `json:"ticket"`
		SessionID  string `json:"session_id"`
		Mode       string `json:"mode"`
		User       string `json:"user"`
		PwRequired bool   `json:"password_required"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		terminalJSONError(w, &terminalError{Status: 502, Message: "The WhaCP agent returned an unexpected response."})
		return
	}
	s.audit.Record(r.Context(), audit.Entry{
		UserID: &user.ID, Username: user.Username, Action: "terminal.open",
		TargetType: "terminal", TargetID: out.SessionID, Result: audit.Success,
		IP: clientIP(r), UserAgent: r.UserAgent(),
		Details: map[string]any{"target": what, "account": out.User, "mode": out.Mode, "ip": clientIP(r)},
	})
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"ticket": out.Ticket, "session_id": out.SessionID, "mode": out.Mode,
		"user": out.User, "password_required": out.PwRequired,
	}})
}

// terminalOpenSite starts a website account's shell (TerminalController::openSite,
// JSON in/out).
func (s *Server) terminalOpenSite(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": map[string]string{"message": "This website does not exist or you do not have access to it."}})
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).Find(r.Context(), s.currentServerID(r), id, user)
	if errors.Is(err, hosting.ErrWebsiteNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": map[string]string{"message": "This website does not exist or you do not have access to it."}})
		return
	}
	if err != nil {
		s.log.Error("find website", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if site.SystemUser == "" {
		terminalJSONError(w, &terminalError{Status: http.StatusBadRequest, Message: "This website has no system account."})
		return
	}
	if site.AccountSuspended {
		terminalJSONError(w, &terminalError{Status: http.StatusForbidden, Message: "The hosting account is suspended."})
		return
	}
	s.terminalOpen(w, r, user, map[string]any{"user": site.SystemUser}, "website "+site.Domain)
}

// terminalOpenRoot starts a root shell (TerminalController::openRoot,
// JSON in/out).
func (s *Server) terminalOpenRoot(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	if user.RoleName != "admin" {
		terminalJSONError(w, &terminalError{Status: http.StatusForbidden, Message: "Only administrators can open a root shell."})
		return
	}
	s.terminalOpen(w, r, user, map[string]any{"root": true}, "root")
}

// terminalIndex lists the open sessions and the root shell
// (TerminalController::index, all-scope only; the route is already
// security.manage-gated).
func (s *Server) terminalIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		renderStatus(w, s, "error", map[string]any{"Title": "Access denied", "Message": "Only administrators see every terminal."}, http.StatusForbidden)
		return
	}
	sess := sessionFrom(r.Context())
	status, err := s.terminalStatus(r.Context())
	agentMsg := ""
	var ae *agent.Error
	if errors.As(err, &ae) {
		agentMsg = ae.Message
	} else if err != nil {
		s.log.Error("terminal status", "error", err)
		agentMsg = "The WhaCP agent could not be reached."
	}
	needsCode, _ := s.twoFactor.Enabled(r.Context(), user.ID)
	okMsg, _ := sess.PullFlash("success")
	errMsg, _ := sess.PullFlash("error")
	sessions := []map[string]string{}
	if status != nil {
		modes := map[string]string{"jailed": "Jailed", "normal": "Normal", "root": "Root", "simulation": "Simulation"}
		for _, x := range status.Sessions {
			sessions = append(sessions, terminalSessionView(x, modes))
		}
	}
	s.terminalPage(w, "terminal_index", map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"Status":     status,
		"Sessions":   sessions,
		"AgentError": agentMsg,
		"Success":    okMsg,
		"Error":      errMsg,
		"IsAdmin":    user.RoleName == "admin",
		"Unlocked":   terminalUnlocked(sess),
		"NeedsCode":  needsCode,
		"OpenURL":    "/terminal/root/open",
	})
}

// terminalSessionView is one open-session row with its display values
// (the PHP view's date/bytes formatting, which the template can't do).
func terminalSessionView(x map[string]any, modes map[string]string) map[string]string {
	str := func(k string) string {
		v, _ := x[k].(string)
		return v
	}
	num := func(k string) int64 {
		switch v := x[k].(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		}
		return 0
	}
	mode := str("mode")
	if label, ok := modes[mode]; ok {
		mode = label
	}
	parse := func(iso, layout string) string {
		ts, err := time.Parse(time.RFC3339, iso)
		if err != nil {
			return ""
		}
		return ts.Format(layout)
	}
	return map[string]string{
		"ID":        str("id"),
		"User":      str("user"),
		"Mode":      mode,
		"Label":     str("label"),
		"ClientIP":  str("client_ip"),
		"Started":   parse(str("started"), "2006-01-02 15:04"),
		"LastInput": parse(str("last_input"), "15:04:05"),
		"In":        formatBytes(num("bytes_in")),
		"Out":       formatBytes(num("bytes_out")),
	}
}

// terminalClose ends a session (TerminalController::close, form POST).
func (s *Server) terminalClose(w http.ResponseWriter, r *http.Request) {
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
	id := r.PathValue("id")
	if !terminalIDRe.MatchString(id) {
		sess.Flash("error", "Unknown session.")
		http.Redirect(w, r, "/terminal", http.StatusSeeOther)
		return
	}
	res, err := s.agent.Call(r.Context(), "terminal.kill", map[string]any{"id": id})
	if err != nil {
		var ae *agent.Error
		msg := "The WhaCP agent could not be reached."
		if errors.As(err, &ae) && ae.Message != "" {
			msg = ae.Message
		}
		s.log.Error("terminal kill", "id", id, "error", err)
		sess.Flash("error", msg)
		http.Redirect(w, r, "/terminal", http.StatusSeeOther)
		return
	}
	var out struct {
		Closed int `json:"closed"`
	}
	_ = json.Unmarshal(res, &out)
	s.audit.Record(r.Context(), audit.Entry{
		UserID: &user.ID, Username: user.Username, Action: "terminal.close",
		TargetType: "terminal", TargetID: id, Result: audit.Success,
		IP: clientIP(r), UserAgent: r.UserAgent(),
	})
	if out.Closed > 0 {
		sess.Flash("success", "Terminal session closed.")
	} else {
		sess.Flash("error", "That session had already ended.")
	}
	http.Redirect(w, r, "/terminal", http.StatusSeeOther)
}

// terminalCloseOwn ends this sign-in's terminals (best effort — the agent
// ends idle sessions on its own). Mirrors TerminalService::closeOwn.
func (s *Server) terminalCloseOwn(ctx context.Context, sess *session.Session) {
	owner := sess.Get("terminal_owner")
	if !terminalOwnerRe.MatchString(owner) {
		return
	}
	_, _ = s.agent.Call(ctx, "terminal.kill", map[string]any{"owner": owner}, 10*time.Second)
}
