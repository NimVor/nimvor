// Package mailrate is NimVor's outgoing mail limit for website accounts: a
// Postfix policy service asked for every recipient of mail submitted over
// SMTP from this machine. It finds the account that owns the client's
// socket in the kernel's socket table (/proc/self/net/tcp*), counts that
// account's recipients per clock hour and defers them past its limit.
//
// Website mail reaches it because PHP's mail() and the commands NimVor runs
// as an account use NimVor's sendmail (package sendmail), which submits over
// SMTP to 127.0.0.1, and website accounts may not use Postfix's own
// sendmail (authorized_submit_users). System accounts (below MinUID) are
// never limited.
package mailrate

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Config is the service's configuration (written by the agent).
type Config struct {
	Enabled bool `json:"enabled"`
	// PerHour is the default limit of recipients per account and hour
	// (0: no limit).
	PerHour int `json:"per_hour"`
	// Accounts are per-account limits by UID (0: no limit).
	Accounts map[string]int `json:"accounts"`
	// Names are the account names by UID (for reports).
	Names  map[string]string `json:"names"`
	MinUID int               `json:"min_uid"`
}

// MaxPerHour bounds a limit.
const MaxPerHour = 1000000

// Normalize fills defaults.
func (c *Config) Normalize() {
	if c.Accounts == nil {
		c.Accounts = map[string]int{}
	}
	if c.Names == nil {
		c.Names = map[string]string{}
	}
	if c.MinUID <= 0 {
		c.MinUID = 1000
	}
}

// Validate checks the limits.
func (c *Config) Validate() error {
	if c.PerHour < 0 || c.PerHour > MaxPerHour {
		return fmt.Errorf("the limit must be between 0 and %d", MaxPerHour)
	}
	for uid, n := range c.Accounts {
		if _, err := strconv.Atoi(uid); err != nil {
			return fmt.Errorf("invalid account %q", uid)
		}
		if n < 0 || n > MaxPerHour {
			return fmt.Errorf("the limit must be between 0 and %d", MaxPerHour)
		}
	}
	return nil
}

// Limit is an account's limit (0: none).
func (c *Config) Limit(uid int) int {
	if n, ok := c.Accounts[strconv.Itoa(uid)]; ok {
		return n
	}
	return c.PerHour
}

// Owner finds the UID owning a local TCP connection from client to
// server (both on this machine).
type Owner func(client, server netip.AddrPort) (int, bool)

// ProcOwner reads the socket tables below dir (normally /proc/self/net).
func ProcOwner(dir string) Owner {
	return func(client, server netip.AddrPort) (int, bool) {
		for _, f := range []string{"tcp", "tcp6"} {
			b, err := os.ReadFile(filepath.Join(dir, f))
			if err != nil {
				continue
			}
			if uid, ok := findOwner(string(b), client, server); ok {
				return uid, true
			}
		}
		return 0, false
	}
}

// findOwner scans a /proc/net/tcp table for the socket whose local end is
// client and remote end is server.
func findOwner(table string, client, server netip.AddrPort) (int, bool) {
	for _, line := range strings.Split(table, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 8 {
			continue
		}
		local, ok1 := parseProcAddr(f[1])
		remote, ok2 := parseProcAddr(f[2])
		if !ok1 || !ok2 || local != client {
			continue
		}
		if server.IsValid() && remote.Port() != server.Port() {
			continue
		}
		uid, err := strconv.Atoi(f[7])
		if err != nil {
			continue
		}
		return uid, true
	}
	return 0, false
}

// parseProcAddr reads "0100007F:0019" (IPv4) or the 32-digit IPv6 form;
// the address is in host order per 32-bit word.
func parseProcAddr(s string) (netip.AddrPort, bool) {
	hexAddr, hexPort, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, false
	}
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	raw, err := hex.DecodeString(hexAddr)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.AddrPort{}, false
	}
	for i := 0; i < len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	addr, _ := netip.AddrFromSlice(raw)
	return netip.AddrPortFrom(addr.Unmap(), uint16(port)), true
}

// Event records an account reaching its limit.
type Event struct {
	UID   int    `json:"uid"`
	Name  string `json:"name"`
	Time  int64  `json:"time"`
	Limit int    `json:"limit"`
}

// Server answers Postfix policy requests and control requests.
type Server struct {
	mu     sync.Mutex
	cfg    *Config
	hour   int64 // start of the counted hour (Unix seconds)
	counts map[int]int
	events []Event

	Owner Owner
	Now   func() time.Time
	Log   *log.Logger
}

// NewServer returns a server for a configuration.
func NewServer(cfg *Config, owner Owner) *Server {
	s := &Server{counts: map[int]int{}, Owner: owner, Now: time.Now, Log: log.New(io.Discard, "", 0)}
	s.SetConfig(cfg)
	return s
}

// SetConfig replaces the configuration.
func (s *Server) SetConfig(cfg *Config) {
	cfg.Normalize()
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

func (s *Server) rollLocked(now time.Time) {
	h := now.Truncate(time.Hour).Unix()
	if h != s.hour {
		s.hour, s.counts = h, map[int]int{}
	}
}

// Decide answers one policy request; the result is the Postfix action.
func (s *Server) Decide(req map[string]string) string {
	if req["request"] != "smtpd_access_policy" || !strings.EqualFold(req["protocol_state"], "RCPT") || req["sasl_username"] != "" {
		return "DUNNO"
	}
	ip, err := netip.ParseAddr(req["client_address"])
	if err != nil || !ip.Unmap().IsLoopback() {
		return "DUNNO"
	}
	port, err := strconv.ParseUint(req["client_port"], 10, 16)
	if err != nil {
		return "DUNNO"
	}
	var server netip.AddrPort
	if sp, err := strconv.ParseUint(req["server_port"], 10, 16); err == nil {
		if sa, err := netip.ParseAddr(req["server_address"]); err == nil {
			server = netip.AddrPortFrom(sa.Unmap(), uint16(sp))
		}
	}
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	if !cfg.Enabled {
		return "DUNNO"
	}
	uid, ok := s.Owner(netip.AddrPortFrom(ip.Unmap(), uint16(port)), server)
	if !ok {
		// The client is gone (or not a TCP socket of this machine's
		// network namespace): nothing to attribute.
		s.Log.Printf("mail from %s:%d: owner not found", ip, port)
		return "DUNNO"
	}
	if uid < cfg.MinUID {
		return "DUNNO"
	}
	limit := cfg.Limit(uid)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollLocked(s.Now())
	if limit > 0 && s.counts[uid] >= limit {
		if s.counts[uid] == limit {
			name := cfg.Names[strconv.Itoa(uid)]
			s.events = append(s.events, Event{UID: uid, Name: name, Time: s.Now().Unix(), Limit: limit})
			if len(s.events) > 500 {
				s.events = s.events[len(s.events)-500:]
			}
			s.Log.Printf("account %s (uid %d) reached its limit of %d recipients per hour", name, uid, limit)
		}
		s.counts[uid] = limit + 1 // counted once past the limit
		return fmt.Sprintf("DEFER_IF_PERMIT 4.7.1 This account has reached its limit of %d recipients per hour", limit)
	}
	s.counts[uid]++
	return "DUNNO"
}

// ServePolicy handles one Postfix connection (several requests in turn).
func (s *Server) ServePolicy(c net.Conn) {
	defer c.Close()
	r := bufio.NewReaderSize(c, 4096)
	for {
		c.SetDeadline(time.Now().Add(5 * time.Minute))
		req := map[string]string{}
		size := 0
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			size += len(line)
			if size > 64<<10 {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if k, v, ok := strings.Cut(line, "="); ok {
				req[k] = v
			}
		}
		if _, err := io.WriteString(c, "action="+s.Decide(req)+"\n\n"); err != nil {
			return
		}
	}
}

// AccountUsage is one account's count in the current hour.
type AccountUsage struct {
	UID     int    `json:"uid"`
	Name    string `json:"name"`
	Sent    int    `json:"sent"`
	Limit   int    `json:"limit"`
	Blocked bool   `json:"blocked"`
}

// Status is the current hour's counts and the recent limit events.
type Status struct {
	Hour     int64          `json:"hour"`
	Accounts []AccountUsage `json:"accounts"`
	Events   []Event        `json:"events"`
}

// Snapshot returns the status.
func (s *Server) Snapshot() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollLocked(s.Now())
	st := Status{Hour: s.hour, Accounts: []AccountUsage{}, Events: append([]Event{}, s.events...)}
	for uid, n := range s.counts {
		limit := s.cfg.Limit(uid)
		sent := n
		if limit > 0 && sent > limit {
			sent = limit
		}
		st.Accounts = append(st.Accounts, AccountUsage{UID: uid, Name: s.cfg.Names[strconv.Itoa(uid)], Sent: sent, Limit: limit, Blocked: limit > 0 && n > limit})
	}
	sort.Slice(st.Accounts, func(i, j int) bool { return st.Accounts[i].Sent > st.Accounts[j].Sent })
	return st
}

// Reset clears an account's count for the current hour (all when uid < 0).
func (s *Server) Reset(uid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if uid < 0 {
		s.counts = map[int]int{}
		return
	}
	delete(s.counts, uid)
}

type saved struct {
	Hour   int64          `json:"hour"`
	Counts map[string]int `json:"counts"`
	Events []Event        `json:"events"`
}

// Marshal returns the state to keep across restarts.
func (s *Server) Marshal() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := saved{Hour: s.hour, Counts: map[string]int{}, Events: s.events}
	for uid, n := range s.counts {
		v.Counts[strconv.Itoa(uid)] = n
	}
	b, _ := json.Marshal(v)
	return b
}

// Load restores saved state (the counts only if still the same hour).
func (s *Server) Load(b []byte) error {
	var v saved
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = v.Events
	if len(s.events) > 500 {
		s.events = s.events[len(s.events)-500:]
	}
	if v.Hour == s.Now().Truncate(time.Hour).Unix() {
		s.hour = v.Hour
		for k, n := range v.Counts {
			if uid, err := strconv.Atoi(k); err == nil && n > 0 {
				s.counts[uid] = n
			}
		}
	}
	return nil
}

// ControlRequest is what the agent sends on the control socket.
type ControlRequest struct {
	Op  string `json:"op"` // status, reset
	UID int    `json:"uid"`
}

// ServeControl handles one control request.
func (s *Server) ServeControl(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second))
	var req ControlRequest
	if err := json.NewDecoder(io.LimitReader(c, 1<<16)).Decode(&req); err != nil {
		json.NewEncoder(c).Encode(map[string]any{"error": "bad request"})
		return
	}
	switch req.Op {
	case "status":
		json.NewEncoder(c).Encode(s.Snapshot())
	case "reset":
		s.Reset(req.UID)
		json.NewEncoder(c).Encode(s.Snapshot())
	default:
		json.NewEncoder(c).Encode(map[string]any{"error": "unknown op"})
	}
}

// Control sends a request to the service's control socket.
func Control(ctx context.Context, socket string, req ControlRequest) (Status, error) {
	var st Status
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return st, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second))
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return st, err
	}
	var raw json.RawMessage
	if err := json.NewDecoder(io.LimitReader(c, 16<<20)).Decode(&raw); err != nil {
		return st, err
	}
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return st, errors.New(e.Error)
	}
	return st, json.Unmarshal(raw, &st)
}

// Main runs the policy service: the configuration comes from the systemd
// credential "config", the policy socket from socket activation (fd 3),
// state is kept in $STATE_DIRECTORY and the control socket (0600) is
// created in $RUNTIME_DIRECTORY.
func Main(args []string) int {
	lg := log.New(os.Stderr, "", 0)
	cfgPath := filepath.Join(os.Getenv("CREDENTIALS_DIRECTORY"), "config")
	stateDir, runDir := os.Getenv("STATE_DIRECTORY"), os.Getenv("RUNTIME_DIRECTORY")
	listen := ""
	for i := 0; i < len(args); i += 2 {
		if i+1 >= len(args) {
			lg.Printf("%s needs a value", args[i])
			return 2
		}
		switch args[i] {
		case "-config":
			cfgPath = args[i+1]
		case "-state":
			stateDir = args[i+1]
		case "-run":
			runDir = args[i+1]
		case "-listen":
			listen = args[i+1]
		default:
			lg.Printf("unknown option %s", args[i])
			return 2
		}
	}
	if stateDir == "" || runDir == "" {
		lg.Print("mailrate-policy runs from its systemd unit (STATE_DIRECTORY and RUNTIME_DIRECTORY unset)")
		return 2
	}
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		lg.Printf("configuration: %v", err)
		return 1
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		lg.Printf("configuration: %v", err)
		return 1
	}
	s := NewServer(&cfg, ProcOwner("/proc/self/net"))
	s.Log = lg
	statePath := filepath.Join(stateDir, "state.json")
	if b, err := os.ReadFile(statePath); err == nil {
		if err := s.Load(b); err != nil {
			lg.Printf("state ignored: %v", err)
		}
	}
	save := func() {
		tmp := statePath + ".tmp"
		if err := os.WriteFile(tmp, s.Marshal(), 0o600); err == nil {
			os.Rename(tmp, statePath)
		}
	}
	var policy net.Listener
	if listen != "" {
		os.Remove(listen)
		policy, err = net.Listen("unix", listen)
	} else if os.Getenv("LISTEN_FDS") == "1" && os.Getenv("LISTEN_PID") == strconv.Itoa(os.Getpid()) {
		f := os.NewFile(3, "policy")
		policy, err = net.FileListener(f)
		f.Close()
	} else {
		err = errors.New("no policy socket (socket activation expected)")
	}
	if err != nil {
		lg.Printf("policy socket: %v", err)
		return 1
	}
	controlPath := filepath.Join(runDir, "control")
	os.Remove(controlPath)
	old := syscall.Umask(0o177)
	control, err := net.Listen("unix", controlPath)
	syscall.Umask(old)
	if err != nil {
		lg.Printf("control socket: %v", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() {
		for {
			c, err := control.Accept()
			if err != nil {
				return
			}
			go s.ServeControl(c)
		}
	}()
	go func() {
		for {
			c, err := policy.Accept()
			if err != nil {
				return
			}
			go s.ServePolicy(c)
		}
	}()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	lg.Printf("mail sending limit service ready (enabled=%v, per hour=%d)", cfg.Enabled, cfg.PerHour)
	for {
		select {
		case <-ctx.Done():
			control.Close()
			policy.Close()
			save()
			return 0
		case <-tick.C:
			save()
		}
	}
}
