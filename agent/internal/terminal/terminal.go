// Package terminal is the web terminal: a shell in the browser, over a
// WebSocket that the panel's Nginx site proxies to a loopback listener of
// the agent (terminal_listen).
//
// The panel asks for a session with terminal.open, which checks the
// account (its shell access must be jailed or normal, and it must not be
// in a privileged group; root only when terminal_root is on in the
// agent's own configuration) and returns a single-use ticket valid for a
// minute. The browser sends the ticket as its first WebSocket message,
// with the account's Linux password (see auth.go), which the agent checks
// before any shell starts; the panel never sees it. The ticket is bound to
// the client address the panel saw and the WebSocket's Origin must match
// its Host.
//
// Each session is its own transient systemd unit, as the account, with
// the same sandbox as other commands run as the account, in its resource
// limits slice, on a pseudo terminal the account opens itself. The shell
// is the account's: the bubblewrap jail (jailed) or bash (normal). A
// session ends when the shell exits, the WebSocket closes, after an idle
// time without input or at its maximum duration, and can be ended from
// the panel. Starts and ends are logged (who, account, address, duration,
// bytes); keystrokes are not recorded, since they include passwords typed
// at prompts.
package terminal

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/ftpmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

const (
	// Path is the WebSocket endpoint.
	Path = "/terminal/ws"

	ticketTTL     = time.Minute
	authTimeout   = 10 * time.Second
	pingInterval  = 25 * time.Second
	maxSessions   = 20
	maxPerAccount = 3
	sessionTasks  = 512

	// ModeRoot is a root shell (terminal_root); ModeSimulation is the
	// development stand-in (simulate_commands).
	ModeRoot       = "root"
	ModeSimulation = "simulation"
)

// Spec is what a session runs.
type Spec struct {
	ID, User, Mode, Program string
	Args                    []string
	UID, GID                int
	Groups                  []uint32
	Home, Slice, TTY        string
	Max                     time.Duration
}

type ticket struct {
	spec Spec
	// needPassword: the account's Linux password must be given over the
	// WebSocket before the shell starts (every website account session).
	needPassword bool
	cols         int
	rows         int
	clientIP     string
	label        string
	owner        string
	idle         time.Duration
	expires      time.Time
}

// Session is a running terminal.
type Session struct {
	ID       string
	User     string
	Mode     string
	Label    string
	Owner    string
	ClientIP string
	Started  time.Time
	lastIn   atomic.Int64 // unix nanoseconds of the last input
	bytesIn  atomic.Int64
	bytesOut atomic.Int64
	idle     time.Duration
	max      time.Duration
	end      func(reason string)
}

// Manager holds tickets and sessions.
type Manager struct {
	Env *host.Env
	// Enabled is set when the WebSocket endpoint listens.
	Enabled bool
	// Spawn starts the shell on spec.TTY and returns a function that stops
	// it (tests replace it).
	Spawn func(ctx context.Context, s Spec) (stop func(), err error)
	Now   func() time.Time
	// CheckEvery is how often idle and maximum times are checked.
	CheckEvery time.Duration
	// Verify checks a Linux account's password (unix_chkpwd; tests
	// replace it).
	Verify func(ctx context.Context, user, password string) error

	guard throttle

	mu       sync.Mutex
	tickets  map[string]*ticket
	sessions map[string]*Session
}

// Shared is the manager of the running agent (set by Register).
var Shared *Manager

// NewManager builds a manager; commands run through systemd, or as the
// agent's own user in simulation mode.
func NewManager(e *host.Env) *Manager {
	m := &Manager{Env: e, Now: time.Now, tickets: map[string]*ticket{}, sessions: map[string]*Session{}, CheckEvery: 5 * time.Second}
	m.Spawn = m.systemdSpawn
	m.Verify = func(ctx context.Context, user, password string) error { return checkPassword(ctx, e, user, password) }
	if e.Cfg.SimulateCommands {
		m.Spawn = simulationSpawn
	}
	return m
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func hashTicket(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

var (
	labelRe = regexp.MustCompile(`^[\pL\pN @._()#:-]{0,100}$`)
	ownerRe = regexp.MustCompile(`^[a-f0-9]{16,64}$`)
	idRe    = regexp.MustCompile(`^[a-f0-9]{16}$`)
)

// OpenParams are terminal.open's parameters.
type OpenParams struct {
	User        string `json:"user"`
	Root        bool   `json:"root"`
	Cols        int    `json:"cols"`
	Rows        int    `json:"rows"`
	ClientIP    string `json:"client_ip"`
	Label       string `json:"label"`
	Owner       string `json:"owner"`
	IdleMinutes int    `json:"idle_minutes"`
	MaxMinutes  int    `json:"max_minutes"`
}

// supplementaryGroups lists the groups that name user as a member.
func supplementaryGroups(e *host.Env, user string) []uint32 {
	b, err := e.Root.ReadFile("/etc/group")
	if err != nil {
		return nil
	}
	var out []uint32
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) < 4 {
			continue
		}
		for _, m := range strings.Split(f[3], ",") {
			if m == user {
				if gid, err := strconv.ParseUint(f[2], 10, 32); err == nil {
					out = append(out, uint32(gid))
				}
			}
		}
	}
	return out
}

// spec checks who the session is for and builds what it runs.
func (m *Manager) spec(p OpenParams) (Spec, error) {
	e := m.Env
	s := Spec{ID: randomHex(8)}
	if e.Cfg.SimulateCommands {
		name := p.User
		if p.Root {
			name = "root"
		}
		return Spec{ID: s.ID, User: name, Mode: ModeSimulation, Program: "/bin/sh", Home: os.TempDir()}, nil
	}
	if p.Root {
		if !e.Cfg.TerminalRoot {
			return s, actions.Conflict("Root shells are turned off in the web terminal. Only the server's root user can turn them on (terminal_root in /etc/whacp/agent.json).")
		}
		s.User, s.Mode, s.Program, s.Args, s.Home = "root", ModeRoot, "/bin/bash", []string{"-l"}, "/root"
		return s, nil
	}
	cred, u, err := e.Credential(p.User)
	if err != nil {
		return s, actions.InvalidParams("user: not a website account")
	}
	if filepath.Dir(filepath.Clean(u.Home)) != filepath.Clean(e.Cfg.SitesRoot) {
		return s, actions.InvalidParams("user: not a website account")
	}
	mode, program := ftpmgr.ShellFor(e, p.User)
	switch mode {
	case ftpmgr.ShellJailed:
		if problem := ftpmgr.JailReady(e); problem != "" {
			return s, actions.Conflict(problem)
		}
	case ftpmgr.ShellNormal:
		s.Args = []string{"-l"}
	default:
		return s, actions.Conflict("Shell access is disabled for " + p.User + ". An administrator can allow it under Security → Shell access (jailed or normal), with the account's SSH/SFTP login on.")
	}
	if bad := PrivilegedGroups(e, p.User, u.GID); len(bad) > 0 {
		return s, actions.Conflict(p.User + " belongs to privileged groups (" + strings.Join(bad, ", ") + "); a website account must not. Remove it from them (gpasswd -d " + p.User + " <group>) before opening a terminal.")
	}
	s.User, s.Mode, s.Program = p.User, mode, program
	s.UID, s.GID, s.Home, s.Slice = int(cred.UID), int(cred.GID), u.Home, cred.Slice
	s.Groups = supplementaryGroups(e, p.User)
	return s, nil
}

// Open validates a request and returns a ticket.
func (m *Manager) Open(p OpenParams) (map[string]any, error) {
	if !m.Enabled {
		return nil, actions.Conflict("The web terminal is not enabled on this server. It needs the panel served by Nginx (install.sh --public-https), which sets terminal_listen in /etc/whacp/agent.json.")
	}
	if p.Root == (p.User != "") {
		return nil, actions.InvalidParams("give either user or root")
	}
	if p.Cols < 10 || p.Cols > 500 || p.Rows < 5 || p.Rows > 200 {
		p.Cols, p.Rows = 80, 24
	}
	if !labelRe.MatchString(p.Label) {
		return nil, actions.InvalidParams("label: letters, digits and @._()#:- only (100 at most)")
	}
	if p.Owner != "" && !ownerRe.MatchString(p.Owner) {
		return nil, actions.InvalidParams("owner: 16 to 64 hexadecimal characters")
	}
	if p.ClientIP != "" && net.ParseIP(p.ClientIP) == nil {
		return nil, actions.InvalidParams("client_ip: not an address")
	}
	if p.IdleMinutes <= 0 {
		p.IdleMinutes = 15
	}
	if p.MaxMinutes <= 0 {
		p.MaxMinutes = 480
	}
	if p.IdleMinutes > 240 || p.MaxMinutes > 1440 || p.IdleMinutes > p.MaxMinutes {
		return nil, actions.InvalidParams("idle_minutes: 1 to 240; max_minutes: 1 to 1440, not less than idle_minutes")
	}
	s, err := m.spec(p)
	if err != nil {
		return nil, err
	}
	s.Max = time.Duration(p.MaxMinutes) * time.Minute
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.capacity(s.User); err != nil {
		return nil, err
	}
	now := m.Now()
	for k, t := range m.tickets {
		if now.After(t.expires) {
			delete(m.tickets, k)
		}
	}
	if len(m.tickets) >= 100 {
		return nil, actions.Conflict("Too many terminals are being opened; try again in a minute.")
	}
	tok := randomHex(32)
	m.tickets[hashTicket(tok)] = &ticket{spec: s, needPassword: s.Mode != ModeRoot, cols: p.Cols, rows: p.Rows, clientIP: p.ClientIP, label: p.Label, owner: p.Owner,
		idle: time.Duration(p.IdleMinutes) * time.Minute, expires: now.Add(ticketTTL)}
	return map[string]any{"ticket": tok, "expires_in": int(ticketTTL.Seconds()), "session_id": s.ID, "mode": s.Mode, "user": s.User, "password_required": s.Mode != ModeRoot}, nil
}

// capacity refuses a session over the limits (m.mu held).
func (m *Manager) capacity(user string) error {
	if len(m.sessions) >= maxSessions {
		return actions.Conflict(fmt.Sprintf("The server already has %d open terminals; close one first.", maxSessions))
	}
	n := 0
	for _, s := range m.sessions {
		if s.User == user {
			n++
		}
	}
	if n >= maxPerAccount {
		return actions.Conflict(fmt.Sprintf("%s already has %d open terminals; close one first.", user, maxPerAccount))
	}
	return nil
}

// take consumes a ticket.
func (m *Manager) take(tok, clientIP string) (*ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := hashTicket(tok)
	t, ok := m.tickets[k]
	if !ok {
		return nil, errors.New("unknown or used ticket")
	}
	delete(m.tickets, k)
	if m.Now().After(t.expires) {
		return nil, errors.New("expired ticket")
	}
	if t.clientIP != "" && !sameIP(t.clientIP, clientIP) {
		return nil, fmt.Errorf("ticket for %s used from %s", t.clientIP, clientIP)
	}
	if err := m.capacity(t.spec.User); err != nil {
		return nil, err
	}
	return t, nil
}

func sameIP(a, b string) bool {
	x, y := net.ParseIP(a), net.ParseIP(b)
	return x != nil && y != nil && x.Equal(y)
}

// Info describes a session for the panel.
func (s *Session) Info() map[string]any {
	return map[string]any{"id": s.ID, "user": s.User, "mode": s.Mode, "label": s.Label, "client_ip": s.ClientIP,
		"started": s.Started.UTC().Format(time.RFC3339), "last_input": time.Unix(0, s.lastIn.Load()).UTC().Format(time.RFC3339),
		"bytes_in": s.bytesIn.Load(), "bytes_out": s.bytesOut.Load(), "idle_minutes": int(s.idle.Minutes()), "max_minutes": int(s.max.Minutes())}
}

// List returns the open sessions, oldest first.
func (m *Manager) List() []map[string]any {
	m.mu.Lock()
	all := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.mu.Unlock()
	sort.Slice(all, func(i, j int) bool { return all[i].Started.Before(all[j].Started) })
	out := make([]map[string]any, 0, len(all))
	for _, s := range all {
		out = append(out, s.Info())
	}
	return out
}

// Kill ends the session id, or every session of owner.
func (m *Manager) Kill(id, owner, reason string) int {
	m.mu.Lock()
	var hit []*Session
	for _, s := range m.sessions {
		if (id != "" && s.ID == id) || (owner != "" && s.Owner == owner) {
			hit = append(hit, s)
		}
	}
	m.mu.Unlock()
	for _, s := range hit {
		s.end(reason)
	}
	return len(hit)
}

// CloseAll ends every session.
func (m *Manager) CloseAll(reason string) {
	m.mu.Lock()
	all := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.mu.Unlock()
	for _, s := range all {
		s.end(reason)
	}
}

// clientIP is the browser's address: Nginx sets X-Forwarded-For to it.
func clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		parts := strings.Split(xf, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	return h
}

// originMatches requires a browser Origin whose host is the Host the
// browser connected to (no cross-site WebSocket).
func originMatches(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// Handler serves the WebSocket endpoint.
func (m *Manager) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != Path {
			http.NotFound(w, r)
			return
		}
		ip := clientIP(r)
		if !originMatches(r) {
			m.Env.Log.Warn("terminal: refused a WebSocket from another origin", "origin", r.Header.Get("Origin"), "host", r.Host, "client_ip", ip)
			http.Error(w, "Origin not allowed.", http.StatusForbidden)
			return
		}
		ws := upgrade(w, r)
		if ws == nil {
			return
		}
		_ = ws.c.SetReadDeadline(time.Now().Add(authTimeout))
		op, msg, err := ws.ReadMessage()
		var auth struct {
			Type     string `json:"type"`
			Ticket   string `json:"ticket"`
			Password string `json:"password"`
		}
		if err != nil || op != opText || json.Unmarshal(msg, &auth) != nil || auth.Type != "auth" || len(auth.Ticket) != 64 {
			ws.Close(4401, "authentication required")
			return
		}
		t, err := m.take(auth.Ticket, ip)
		if err != nil {
			m.Env.Log.Warn("terminal: refused a ticket", "client_ip", ip, "error", err.Error())
			m.sendJSON(ws, map[string]any{"type": "exit", "reason": "This terminal link is no longer valid. Open the terminal again."})
			ws.Close(4401, "invalid ticket")
			return
		}
		if t.needPassword && !m.authenticate(ws, t, ip, auth.Password) {
			return
		}
		auth.Password = ""
		_ = ws.c.SetReadDeadline(time.Time{})
		m.run(ws, t, ip)
	})
}

// authenticate checks the account's Linux password: up to maxAttempts
// per ticket, throttled per account and per client address. The password
// is never logged or echoed.
func (m *Manager) authenticate(ws *wsConn, t *ticket, ip, password string) bool {
	user := t.spec.User
	keys := []string{"user:" + user, "ip:" + ip}
	for attempt := 1; ; attempt++ {
		if wait := m.guard.blocked(m.Now(), keys...); wait > 0 {
			m.Env.Log.Warn("terminal: sign-in refused after repeated wrong passwords", "user", user, "client_ip", ip)
			m.sendJSON(ws, map[string]any{"type": "exit", "reason": fmt.Sprintf("Too many wrong passwords. Try again in %d minutes.", int(wait.Minutes())+1)})
			ws.Close(4429, "too many attempts")
			return false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := m.Verify(ctx, user, password)
		cancel()
		password = ""
		if err == nil {
			m.guard.clear(keys[0])
			m.Env.Log.Info("terminal: account password accepted", "user", user, "client_ip", ip)
			return true
		}
		if !errors.Is(err, errWrongPassword) {
			m.Env.Log.Error("terminal: the password could not be checked", "user", user, "error", err.Error())
			m.sendJSON(ws, map[string]any{"type": "exit", "reason": "The password could not be checked on this server."})
			ws.Close(1011, "no password check")
			return false
		}
		m.guard.record(m.Now(), keys...)
		m.Env.Log.Warn("terminal: wrong account password", "user", user, "client_ip", ip, "attempt", attempt)
		time.Sleep(failurePause)
		if attempt >= maxAttempts {
			m.sendJSON(ws, map[string]any{"type": "exit", "reason": "Wrong password. Open the terminal again to retry."})
			ws.Close(4401, "wrong password")
			return false
		}
		m.sendJSON(ws, map[string]any{"type": "auth_failed", "reason": "Wrong password for " + user + ".", "attempts_left": maxAttempts - attempt})
		_ = ws.c.SetReadDeadline(time.Now().Add(2 * time.Minute))
		op, msg, err := ws.ReadMessage()
		var in struct {
			Type     string `json:"type"`
			Password string `json:"password"`
		}
		if err != nil || op != opText || json.Unmarshal(msg, &in) != nil || in.Type != "password" {
			ws.Close(4401, "authentication required")
			return false
		}
		password = in.Password
	}
}

func (m *Manager) sendJSON(ws *wsConn, v any) error {
	b, _ := json.Marshal(v)
	return ws.write(opText, b)
}

// run connects a WebSocket to a new shell until either side ends.
func (m *Manager) run(ws *wsConn, t *ticket, ip string) {
	log := m.Env.Log
	master, tty, err := openPTY()
	if err != nil {
		log.Error("terminal: no pseudo terminal", "error", err.Error())
		m.sendJSON(ws, map[string]any{"type": "exit", "reason": "The server could not allocate a terminal."})
		ws.Close(1011, "no pty")
		return
	}
	_ = setSize(master, t.cols, t.rows)
	spec := t.spec
	spec.TTY = tty
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	stop, err := m.Spawn(ctx, spec)
	cancel()
	if err != nil {
		master.Close()
		log.Error("terminal: the shell did not start", "user", spec.User, "error", err.Error())
		m.sendJSON(ws, map[string]any{"type": "exit", "reason": "The shell did not start: " + err.Error()})
		ws.Close(1011, "spawn failed")
		return
	}
	s := &Session{ID: spec.ID, User: spec.User, Mode: spec.Mode, Label: t.label, Owner: t.owner, ClientIP: ip, Started: m.Now(), idle: t.idle, max: spec.Max}
	s.lastIn.Store(m.Now().UnixNano())
	done := make(chan struct{})
	var once sync.Once
	s.end = func(reason string) {
		once.Do(func() {
			m.mu.Lock()
			delete(m.sessions, s.ID)
			m.mu.Unlock()
			stop()
			master.Close()
			m.sendJSON(ws, map[string]any{"type": "exit", "reason": reason})
			ws.Close(1000, "")
			close(done)
			log.Info("terminal session ended", "session", s.ID, "user", s.User, "mode", s.Mode, "by", s.Label, "client_ip", s.ClientIP,
				"reason", reason, "duration_s", int(m.Now().Sub(s.Started).Seconds()), "bytes_in", s.bytesIn.Load(), "bytes_out", s.bytesOut.Load())
		})
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	log.Info("terminal session started", "session", s.ID, "user", s.User, "mode", s.Mode, "by", s.Label, "client_ip", ip, "tty", tty)
	m.sendJSON(ws, map[string]any{"type": "ready", "session_id": s.ID, "user": s.User, "mode": s.Mode,
		"idle_minutes": int(s.idle.Minutes()), "max_minutes": int(s.max.Minutes())})

	// Shell → browser.
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				s.bytesOut.Add(int64(n))
				if ws.write(opBinary, buf[:n]) != nil {
					s.end("The connection was lost.")
					return
				}
			}
			if err != nil {
				s.end("The shell exited.")
				return
			}
		}
	}()
	// Timeouts and keep-alive.
	go func() {
		tick := time.NewTicker(m.CheckEvery)
		defer tick.Stop()
		lastPing := m.Now()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				now := m.Now()
				if now.Sub(time.Unix(0, s.lastIn.Load())) >= s.idle {
					s.end(fmt.Sprintf("Closed after %d minutes without input.", int(s.idle.Minutes())))
					return
				}
				if now.Sub(s.Started) >= s.max {
					s.end(fmt.Sprintf("Closed: sessions last at most %d minutes.", int(s.max.Minutes())))
					return
				}
				if now.Sub(lastPing) >= pingInterval {
					lastPing = now
					_ = ws.write(opPing, nil)
				}
			}
		}
	}()
	// Browser → shell.
	for {
		op, msg, err := ws.ReadMessage()
		if err != nil {
			s.end("The browser closed the terminal.")
			return
		}
		switch op {
		case opBinary:
			s.lastIn.Store(m.Now().UnixNano())
			s.bytesIn.Add(int64(len(msg)))
			if _, err := master.Write(msg); err != nil {
				s.end("The shell exited.")
				return
			}
		case opText:
			var c struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(msg, &c) == nil && c.Type == "resize" && c.Cols >= 10 && c.Cols <= 500 && c.Rows >= 5 && c.Rows <= 200 {
				_ = setSize(master, c.Cols, c.Rows)
			}
		}
	}
}

// ttyWrapper runs as the account inside the unit: it opens the terminal
// itself (so neither PID 1 nor root opens it for the account), which makes
// it the session's controlling terminal, then becomes the shell.
const ttyWrapper = `t=$1; shift; exec "$@" <"$t" >"$t" 2>&1`

// UnitName is a session's unit.
func UnitName(id string) string { return "whacp-term-" + id + ".service" }

// SystemdArgs are the systemd-run arguments of a session.
func SystemdArgs(s Spec) []string {
	a := []string{"--quiet", "--collect", "--service-type=exec", "--unit=" + UnitName(s.ID),
		"--property=StandardInput=null", "--working-directory=" + s.Home,
		fmt.Sprintf("--property=RuntimeMaxSec=%d", int(s.Max.Seconds())+60),
		fmt.Sprintf("--property=TasksMax=%d", sessionTasks), "--property=KillMode=control-group", "--property=TimeoutStopSec=5",
		"--setenv=TERM=xterm-256color", "--setenv=LANG=C.UTF-8", "--setenv=HOME=" + s.Home, "--setenv=SHELL=/bin/bash"}
	if s.Mode == ModeRoot {
		a = append(a, "--setenv=USER=root", "--setenv=LOGNAME=root", "--setenv=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	} else {
		a = append(a, fmt.Sprintf("--uid=%d", s.UID), fmt.Sprintf("--gid=%d", s.GID),
			"--setenv=USER="+s.User, "--setenv=LOGNAME="+s.User, "--setenv=PATH=/usr/local/bin:/usr/bin:/bin")
		if len(s.Groups) > 0 {
			g := make([]string, len(s.Groups))
			for i, id := range s.Groups {
				g[i] = fmt.Sprint(id)
			}
			a = append(a, "--property=SupplementaryGroups="+strings.Join(g, " "))
		}
		for _, p := range execx.TransientHardening() {
			if !strings.HasPrefix(p, "KillMode=") && !strings.HasPrefix(p, "TimeoutStopSec=") {
				a = append(a, "--property="+p)
			}
		}
		if s.Slice != "" {
			a = append(a, "--slice="+s.Slice)
		}
	}
	tail := append([]string{"/bin/sh", "-c", ttyWrapper, "whacp-term", s.TTY, s.Program}, s.Args...)
	for i := range tail {
		// systemd expands $VAR in command lines; "$$" is a literal "$".
		tail[i] = strings.ReplaceAll(tail[i], "$", "$$")
	}
	return append(append(a, "--"), tail...)
}

// systemdSpawn starts a session's unit.
func (m *Manager) systemdSpawn(ctx context.Context, s Spec) (func(), error) {
	e := m.Env
	if s.Mode != ModeRoot {
		// The account opens its terminal; nobody else may.
		if err := os.Chown(s.TTY, s.UID, -1); err != nil {
			return nil, fmt.Errorf("terminal ownership: %w", err)
		}
		if err := os.Chmod(s.TTY, 0o600); err != nil {
			return nil, fmt.Errorf("terminal mode: %w", err)
		}
	}
	if out, err := e.Runner.Run(ctx, "systemd-run", SystemdArgs(s)...); err != nil {
		return nil, errors.New(host.Output(out))
	}
	unit := UnitName(s.ID)
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = e.Systemctl(ctx, "stop", unit)
	}, nil
}

// simulationSpawn (development only, simulate_commands) runs /bin/sh as
// the agent's own user on the terminal.
func simulationSpawn(_ context.Context, s Spec) (func(), error) {
	f, err := os.OpenFile(s.TTY, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cmd := exec.Command(s.Program, "-i")
	cmd.Dir = s.Home
	cmd.Env = []string{"TERM=xterm-256color", "HOME=" + s.Home, "PATH=/usr/local/bin:/usr/bin:/bin", "PS1=[simulation " + s.User + "]$ "}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = f, f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go cmd.Wait()
	return func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}, nil
}
