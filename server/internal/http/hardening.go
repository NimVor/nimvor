package http

// This file ports the server hardening pages (HardeningController):
// Compiler Access, SMTP Restrictions, Outgoing Connections, Shell Fork Bomb
// Protection and Traceroute. Every page is all-scope (403 otherwise); every
// change is role-admin; the agent applies and the change is audited
// (server / the server id).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// hProgram is one restricted binary row (compiler / traceroute).
type hProgram struct {
	Path  string `json:"path"`
	Owner string `json:"owner"`
	Mode  string `json:"mode"`
}

// hardeningStatus mirrors the agent's hardening.status reply.
type hardeningStatus struct {
	Compilers struct {
		Restricted bool       `json:"restricted"`
		Programs   []hProgram `json:"programs"`
		Group      string     `json:"group"`
		Members    []string   `json:"members"`
	} `json:"compilers"`
	SMTP struct {
		Enabled   bool     `json:"enabled"`
		Active    bool     `json:"active"`
		Available bool     `json:"available"`
		Allowed   []string `json:"allowed"`
		Ports     []int    `json:"ports"`
	} `json:"smtp"`
	Egress struct {
		Available       bool     `json:"available"`
		Active          bool     `json:"active"`
		MetadataBlocked bool     `json:"metadata_blocked"`
		BlockPrivate    bool     `json:"block_private"`
		TCPPorts        []int    `json:"tcp_ports"`
		UDPPorts        []int    `json:"udp_ports"`
		Accounts        int      `json:"accounts"`
		Metadata        []string `json:"metadata"`
	} `json:"egress"`
	Forkbomb struct {
		Enabled bool   `json:"enabled"`
		File    string `json:"file"`
		Soft    int    `json:"soft"`
		Hard    int    `json:"hard"`
	} `json:"forkbomb"`
	Traceroute struct {
		Enabled  bool       `json:"enabled"`
		Programs []hProgram `json:"programs"`
	} `json:"traceroute"`
}

// hardeningPage renders one hardening page (HardeningController::page):
// the all-scope gate, hardening.status, the pending banner.
func (s *Server) hardeningPage(w http.ResponseWriter, r *http.Request, page string) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		renderStatus(w, s, "error", map[string]any{
			"Title": "Access denied", "Message": "Only administrators manage server hardening.",
		}, http.StatusForbidden)
		return
	}
	sess := sessionFrom(r.Context())
	ctx := r.Context()
	var st *hardeningStatus
	agentMsg := ""
	var ae *agent.Error
	if res, err := s.agent.Call(ctx, "hardening.status", nil); err != nil {
		if errors.As(err, &ae) && ae.Message != "" {
			agentMsg = ae.Message
		} else {
			s.log.Error("hardening status", "error", err)
			agentMsg = "The WhaCP agent could not be reached."
		}
	} else if err := json.Unmarshal(res, &st); err != nil {
		s.log.Error("hardening status", "error", err)
		agentMsg = "The WhaCP agent answered with an unreadable state."
	}
	ok, _ := s.authorizer().Can(ctx, user, "security.manage")
	data := map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"Pending":    s.securityPending(ctx),
		"AgentError": agentMsg,
		"Status":     st,
		// The view's $canManage: the permission and the role.
		"CanManage": ok && user.RoleName == "admin",
		"ReturnTo":  "/security/" + page,
		"Accounts":  []string{},
	}
	if page == "compilers" {
		data["Accounts"] = s.hardeningAccounts(ctx, s.currentServerID(r))
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	s.render(w, "security_hardening_"+page, data)
}

// hardeningCompilers / SaveCompilers — Compiler Access.
func (s *Server) hardeningCompilers(w http.ResponseWriter, r *http.Request) {
	s.hardeningPage(w, r, "compilers")
}

// hardeningAccounts is HardeningService::accounts: the website accounts on
// this server.
func (s *Server) hardeningAccounts(ctx context.Context, serverID int64) []string {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT system_user FROM websites WHERE server_id = ? AND system_user <> '' ORDER BY system_user`, serverID)
	if err != nil {
		s.log.Error("hardening accounts", "error", err)
		return []string{}
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var u string
		if rows.Scan(&u) == nil {
			out = append(out, u)
		}
	}
	return out
}

func (s *Server) hardeningSaveCompilers(w http.ResponseWriter, r *http.Request) {
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
	to := "/security/compilers"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
	if user.RoleName != "admin" {
		fail("Only administrators change server hardening.")
		return
	}
	restricted := r.FormValue("restricted") == "1"
	// The allowed accounts (form order, deduplicated) — the form's
	// users[] checkboxes.
	seen := map[string]bool{}
	users := []string{}
	for _, u := range r.Form["users[]"] {
		if !seen[u] {
			seen[u] = true
			users = append(users, u)
		}
	}
	known := s.hardeningAccounts(r.Context(), s.currentServerID(r))
	knownSet := map[string]bool{}
	for _, a := range known {
		knownSet[a] = true
	}
	unknown := []string{}
	for _, u := range users {
		if !knownSet[u] {
			unknown = append(unknown, u)
		}
	}
	if len(unknown) > 0 {
		fail("Not a website account on this server: " + strings.Join(unknown, ", ") + ".")
		return
	}
	send := users
	if !restricted {
		send = []string{}
	}
	if _, err := s.agent.Call(r.Context(), "hardening.compilers",
		map[string]any{"restricted": restricted, "users": send}, 120*time.Second); err != nil {
		agentErrorRedirect(w, r, "hardening compilers", err, to)
		return
	}
	s.hardeningAudit(r, "hardening.compilers", map[string]any{"restricted": restricted, "users": users})
	msg := "Compilers can be run by every account again."
	if restricted {
		msg = "Compilers are restricted. Allowed: " + strings.Join(users, ", ") + "."
		if len(users) == 0 {
			msg = "Compilers are restricted to root."
		}
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// hardeningSmtp / SaveSmtp — SMTP Restrictions.
func (s *Server) hardeningSmtp(w http.ResponseWriter, r *http.Request) {
	s.hardeningPage(w, r, "smtp")
}

func (s *Server) hardeningSaveSmtp(w http.ResponseWriter, r *http.Request) {
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
	to := "/security/smtp"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
	if user.RoleName != "admin" {
		fail("Only administrators change server hardening.")
		return
	}
	enabled := r.FormValue("enabled") == "1"
	if _, err := s.agent.Call(r.Context(), "hardening.smtp", map[string]any{"enabled": enabled}, 120*time.Second); err != nil {
		agentErrorRedirect(w, r, "hardening smtp", err, to)
		return
	}
	s.hardeningAudit(r, "hardening.smtp", map[string]any{"enabled": enabled})
	if enabled {
		sess.Flash("success", "SMTP restrictions are on.")
	} else {
		sess.Flash("success", "SMTP restrictions are off.")
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// hPorts is HardeningService::ports: the comma- or space-separated port
// list (1..65535, deduplicated, sorted, at most 64).
func hPorts(list, what string) ([]int, error) {
	seen := map[int]bool{}
	out := []int{}
	for _, p := range strings.FieldsFunc(strings.TrimSpace(list),
		func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ','
		}) {
		if len(p) > 5 || !isDigits(p) {
			return nil, fmt.Errorf("%s ports: %q is not a port number (1–65535).", what, p)
		}
		n, _ := strconv.Atoi(p)
		if n < 1 || n > 65535 {
			return nil, fmt.Errorf("%s ports: %q is not a port number (1–65535).", what, p)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) > 64 {
		return nil, fmt.Errorf("%s ports: at most 64.", what)
	}
	sort.Ints(out)
	return out, nil
}

// hardeningEgress / SaveEgress — Outgoing Connections.
func (s *Server) hardeningEgress(w http.ResponseWriter, r *http.Request) {
	s.hardeningPage(w, r, "egress")
}

func (s *Server) hardeningSaveEgress(w http.ResponseWriter, r *http.Request) {
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
	to := "/security/egress"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
	if user.RoleName != "admin" {
		fail("Only administrators change server hardening.")
		return
	}
	tcp, err := hPorts(r.FormValue("tcp_ports"), "TCP")
	if err != nil {
		fail(err.Error())
		return
	}
	udp, err := hPorts(r.FormValue("udp_ports"), "UDP")
	if err != nil {
		fail(err.Error())
		return
	}
	params := map[string]any{
		"metadata_blocked": r.FormValue("metadata_blocked") == "1",
		"block_private":    r.FormValue("block_private") == "1",
		"tcp_ports":        tcp,
		"udp_ports":        udp,
	}
	if _, err := s.agent.Call(r.Context(), "hardening.egress", params, 120*time.Second); err != nil {
		agentErrorRedirect(w, r, "hardening egress", err, to)
		return
	}
	s.hardeningAudit(r, "hardening.egress", params)
	sess.Flash("success", "Outgoing connection limits for website accounts saved.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// hardeningForkbomb / SaveForkbomb — Shell Fork Bomb Protection.
func (s *Server) hardeningForkbomb(w http.ResponseWriter, r *http.Request) {
	s.hardeningPage(w, r, "forkbomb")
}

func (s *Server) hardeningSaveForkbomb(w http.ResponseWriter, r *http.Request) {
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
	to := "/security/forkbomb"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
	if user.RoleName != "admin" {
		fail("Only administrators change server hardening.")
		return
	}
	enabled := r.FormValue("enabled") == "1"
	if _, err := s.agent.Call(r.Context(), "hardening.forkbomb", map[string]any{"enabled": enabled}, 60*time.Second); err != nil {
		agentErrorRedirect(w, r, "hardening forkbomb", err, to)
		return
	}
	s.hardeningAudit(r, "hardening.forkbomb", map[string]any{"enabled": enabled})
	if enabled {
		sess.Flash("success", "Fork bomb protection is on. It applies to new logins.")
	} else {
		sess.Flash("success", "Fork bomb protection is off.")
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// hardeningTraceroute / SaveTraceroute — Traceroute Enable/Disable.
func (s *Server) hardeningTraceroute(w http.ResponseWriter, r *http.Request) {
	s.hardeningPage(w, r, "traceroute")
}

func (s *Server) hardeningSaveTraceroute(w http.ResponseWriter, r *http.Request) {
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
	to := "/security/traceroute"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
	if user.RoleName != "admin" {
		fail("Only administrators change server hardening.")
		return
	}
	enabled := r.FormValue("enabled") == "1"
	if _, err := s.agent.Call(r.Context(), "hardening.traceroute", map[string]any{"enabled": enabled}, 60*time.Second); err != nil {
		agentErrorRedirect(w, r, "hardening traceroute", err, to)
		return
	}
	s.hardeningAudit(r, "hardening.traceroute", map[string]any{"enabled": enabled})
	if enabled {
		sess.Flash("success", "Traceroute is enabled for every user.")
	} else {
		sess.Flash("success", "Traceroute is disabled for users other than root.")
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// hardeningAudit is HardeningService::record (server / the server id).
func (s *Server) hardeningAudit(r *http.Request, action string, details map[string]any) {
	user := userFrom(r.Context())
	s.audit.Record(r.Context(), audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     action,
		TargetType: "server",
		TargetID:   strconv.FormatInt(s.currentServerID(r), 10),
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    details,
	})
}
