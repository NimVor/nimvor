package greylist

import (
	"bufio"
	"context"
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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/spf"
)

// Server answers Postfix policy requests and control requests.
type Server struct {
	mu      sync.RWMutex
	cfg     *Config
	trusted []netip.Prefix // trusted hosts and trusted providers

	Store    *Store
	Resolver spf.Resolver
	Now      func() time.Time
	Log      *log.Logger

	spfMu    sync.Mutex
	spfCache map[string]spfEntry
}

type spfEntry struct {
	pass bool
	at   time.Time
}

// NewServer returns a server for a configuration.
func NewServer(cfg *Config) *Server {
	s := &Server{Store: NewStore(), Resolver: net.DefaultResolver, Now: time.Now, Log: log.New(os.Stderr, "", 0), spfCache: map[string]spfEntry{}}
	s.SetConfig(cfg)
	return s
}

// SetConfig replaces the configuration.
func (s *Server) SetConfig(cfg *Config) {
	cfg.Normalize()
	var trusted []netip.Prefix
	for _, t := range cfg.Trusted {
		if p, err := ParseRange(t.Range); err == nil {
			trusted = append(trusted, p)
		}
	}
	for _, p := range cfg.Providers {
		if p == nil || !p.Trust {
			continue
		}
		for _, r := range p.Ranges {
			if pr, err := ParseRange(r); err == nil {
				trusted = append(trusted, pr)
			}
		}
	}
	s.mu.Lock()
	s.cfg, s.trusted = cfg, trusted
	s.mu.Unlock()
	s.spfMu.Lock()
	s.spfCache = map[string]spfEntry{}
	s.spfMu.Unlock()
}

func (s *Server) isTrusted(ip netip.Addr) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.trusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) spfPass(ctx context.Context, ip netip.Addr, sender, helo string) bool {
	k := ip.String() + "\x00" + strings.ToLower(sender) + "\x00" + strings.ToLower(helo)
	now := s.Now()
	s.spfMu.Lock()
	if e, ok := s.spfCache[k]; ok && now.Sub(e.at) < time.Hour {
		s.spfMu.Unlock()
		return e.pass
	}
	s.spfMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, _ := spf.Check(ctx, s.Resolver, ip, sender, helo)
	pass := res == spf.Pass
	if res != spf.TempError {
		s.spfMu.Lock()
		if len(s.spfCache) > 20000 {
			s.spfCache = map[string]spfEntry{}
		}
		s.spfCache[k] = spfEntry{pass: pass, at: now}
		s.spfMu.Unlock()
	}
	return pass
}

// Decide answers one policy request (attribute map); the result is the
// Postfix action.
func (s *Server) Decide(ctx context.Context, req map[string]string) string {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if !cfg.Enabled || req["request"] != "smtpd_access_policy" || !strings.EqualFold(req["protocol_state"], "RCPT") || req["sasl_username"] != "" {
		return "DUNNO"
	}
	ip, err := netip.ParseAddr(req["client_address"])
	if err != nil {
		return "DUNNO"
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || s.isTrusted(ip) {
		return "DUNNO"
	}
	recipient := req["recipient"]
	if recipient == "" || len(recipient) > 320 || len(req["sender"]) > 320 {
		return "DUNNO"
	}
	if cfg.SPFBypass && s.spfPass(ctx, ip, req["sender"], req["helo_name"]) {
		return "DUNNO"
	}
	d := s.Store.Check(cfg, ip.String(), req["sender"], recipient, s.Now())
	if d.Defer {
		mins := int(d.Wait.Minutes() + 0.999)
		return fmt.Sprintf("DEFER_IF_PERMIT 4.7.1 Greylisted, please try again in %d minute(s)", max(mins, 1))
	}
	return "DUNNO"
}

// ServePolicy handles one Postfix connection (several requests in turn).
func (s *Server) ServePolicy(ctx context.Context, c net.Conn) {
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
		if _, err := io.WriteString(c, "action="+s.Decide(ctx, req)+"\n\n"); err != nil {
			return
		}
	}
}

// ControlRequest is what the agent sends on the control socket.
type ControlRequest struct {
	Op       string      `json:"op"` // report, purge, delete, delete_all
	Query    ReportQuery `json:"query"`
	Triplets []Triplet   `json:"triplets,omitempty"`
}

// ServeControl handles one control request.
func (s *Server) ServeControl(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second))
	var req ControlRequest
	if err := json.NewDecoder(io.LimitReader(c, 1<<20)).Decode(&req); err != nil {
		json.NewEncoder(c).Encode(map[string]any{"error": "bad request"})
		return
	}
	out := map[string]any{}
	switch req.Op {
	case "report":
		rows, total, counts := Report(s.Store.Snapshot(), req.Query, s.Now())
		out["rows"], out["total"], out["counts"] = rows, total, counts
	case "purge":
		out["removed"] = s.Store.Purge(s.Now())
	case "delete":
		out["removed"] = s.Store.Delete(req.Triplets, false)
	case "delete_all":
		out["removed"] = s.Store.Delete(nil, true)
	default:
		out["error"] = "unknown op"
	}
	json.NewEncoder(c).Encode(out)
}

// Control sends a request to the service's control socket.
func Control(ctx context.Context, socket string, req ControlRequest, out any) error {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second))
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return err
	}
	var raw json.RawMessage
	if err := json.NewDecoder(io.LimitReader(c, 64<<20)).Decode(&raw); err != nil {
		return err
	}
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return errors.New(e.Error)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// LoadConfig reads a configuration file.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	c.Normalize()
	return &c, nil
}

// Main runs the policy service: the configuration comes from the systemd
// credential "config" (or -config), the policy socket from socket
// activation (fd 3), state is kept in $STATE_DIRECTORY and the control
// socket is created in $RUNTIME_DIRECTORY with mode 0600.
func Main(args []string) int {
	lg := log.New(os.Stderr, "", 0)
	cfgPath := filepath.Join(os.Getenv("CREDENTIALS_DIRECTORY"), "config")
	stateDir, runDir := os.Getenv("STATE_DIRECTORY"), os.Getenv("RUNTIME_DIRECTORY")
	listen := ""
	for i := 0; i+1 < len(args); i += 2 {
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
		lg.Print("greylist-policy runs from its systemd unit (STATE_DIRECTORY and RUNTIME_DIRECTORY unset)")
		return 2
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		lg.Printf("configuration: %v", err)
		return 1
	}
	s := NewServer(cfg)
	s.Log = lg
	statePath := filepath.Join(stateDir, "triplets.json")
	if b, err := os.ReadFile(statePath); err == nil {
		if err := s.Store.Load(b, time.Now()); err != nil {
			lg.Printf("state ignored: %v", err)
		}
	}
	save := func(force bool) {
		b := s.Store.Marshal(force)
		if b == nil {
			return
		}
		tmp := statePath + ".tmp"
		if err := os.WriteFile(tmp, b, 0o600); err == nil {
			if err := os.Rename(tmp, statePath); err != nil {
				lg.Printf("save state: %v", err)
			}
		} else {
			lg.Printf("save state: %v", err)
		}
	}

	var policy net.Listener
	if listen != "" {
		os.Remove(listen)
		policy, err = net.Listen("unix", listen)
	} else if os.Getenv("LISTEN_FDS") == "1" && os.Getenv("LISTEN_PID") == fmt.Sprint(os.Getpid()) {
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
	// The configuration is a systemd credential, copied when the service
	// starts: the agent restarts the service to change it.
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
			go s.ServePolicy(ctx, c)
		}
	}()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	// Expired triplets stay visible in the report for a while (the
	// administrator can remove them); they are cleaned up hourly.
	lastPurge := time.Now()
	lg.Printf("greylisting policy service ready (enabled=%v)", cfg.Enabled)
	for {
		select {
		case <-ctx.Done():
			control.Close()
			policy.Close()
			save(true)
			return 0
		case <-tick.C:
			if time.Since(lastPurge) > time.Hour {
				s.Store.Purge(time.Now())
				lastPurge = time.Now()
			}
			save(false)
		}
	}
}
