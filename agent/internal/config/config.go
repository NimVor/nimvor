// Package config loads the WhaCP agent configuration.
//
// Configuration is read from a JSON file (default /etc/whacp/agent.json).
// Every field has a safe default so the agent can start with an empty or
// missing file during development.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultPath is the default configuration file location.
const DefaultPath = "/etc/whacp/agent.json"

// Config holds all agent settings.
type Config struct {
	// SocketPath is the Unix socket the agent listens on.
	SocketPath string `json:"socket_path"`
	// SocketMode is the octal permission mode for the socket (e.g. "0660").
	SocketMode string `json:"socket_mode"`
	// SocketGroup is the group that owns the socket (the panel's group).
	SocketGroup string `json:"socket_group"`
	// TokenFile contains the shared bearer token used by the panel.
	TokenFile string `json:"token_file"`
	// AllowedUIDs restricts which local users may connect (in addition to
	// the token check). Empty means "root and the owner of SocketGroup".
	AllowedUIDs []int `json:"allowed_uids"`
	// RootDir prefixes every configuration path the agent inspects.
	// It is "/" in production and points at a fixture tree in tests.
	RootDir string `json:"root_dir"`
	// ProcDir is where procfs is mounted (metrics source).
	ProcDir string `json:"proc_dir"`
	// LogLevel is one of debug, info, warn, error.
	LogLevel string `json:"log_level"`
	// MetricsInterval is how often the background collector samples.
	MetricsInterval Duration `json:"metrics_interval"`
	// MetricsHistory is the number of samples kept in memory.
	MetricsHistory int `json:"metrics_history"`
	// ActionTimeout bounds the duration of a single action.
	ActionTimeout Duration `json:"action_timeout"`
	// AllowWriteActions gates every action that modifies the server.
	// It is false by default: the current release is read-only.
	AllowWriteActions bool `json:"allow_write_actions"`
	// SitesRoot is where WhaCP-managed websites live (<sites_root>/<domain>).
	SitesRoot string `json:"sites_root"`
	// StateDir holds configuration backups, job logs and keys.
	StateDir string `json:"state_dir"`
	// BackupDir is the local backup destination.
	BackupDir string `json:"backup_dir"`
	// WebUser is the account Nginx workers run as (default from the OS).
	WebUser string `json:"web_user"`
	// ListenTCP optionally exposes the API over TLS for a remote WhaCP
	// master (multi-server). Empty disables it.
	ListenTCP string `json:"listen_tcp"`
	// TLSCert / TLSKey are used for ListenTCP (generated when missing).
	TLSCert string `json:"tls_cert"`
	TLSKey  string `json:"tls_key"`
	// AllowedClients lists the addresses/networks allowed to use
	// ListenTCP (required when ListenTCP is set).
	AllowedClients []string `json:"allowed_clients"`
	// TerminalListen is the loopback address of the web terminal's
	// WebSocket endpoint, which the panel's Nginx site proxies to
	// (/terminal/ws). Empty disables the web terminal. Loopback only.
	TerminalListen string `json:"terminal_listen"`
	// TerminalRoot lets server administrators open a root shell in the
	// web terminal. Off by default; only root can turn it on (here), so a
	// compromised panel cannot.
	TerminalRoot bool `json:"terminal_root"`
	// MaxJobs bounds concurrently running background jobs.
	MaxJobs int `json:"max_jobs"`
	// SimulateCommands replaces every system tool with a simulation. For
	// development against a fixture tree only; refused when root_dir is "/".
	SimulateCommands bool `json:"simulate_commands"`
}

// Duration is a time.Duration that unmarshals from a Go duration string.
type Duration struct{ time.Duration }

// UnmarshalJSON parses strings such as "5s" or "2m".
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// MarshalJSON renders the duration as a string.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		SocketPath:      "/run/whacp/agent.sock",
		SocketMode:      "0660",
		SocketGroup:     "whacp",
		TokenFile:       "/etc/whacp/agent.token",
		RootDir:         "/",
		ProcDir:         "/proc",
		LogLevel:        "info",
		MetricsInterval: Duration{5 * time.Second},
		MetricsHistory:  180,
		ActionTimeout:   Duration{30 * time.Second},
		SitesRoot:       "/home/whacp",
		StateDir:        "/var/lib/whacp-agent",
		BackupDir:       "/var/backups/whacp",
		TLSCert:         "/etc/whacp/agent-tls.crt",
		TLSKey:          "/etc/whacp/agent-tls.key",
		MaxJobs:         2,
	}
}

// Load reads the file at path and overlays it on the defaults.
// A missing file is not an error; defaults are used.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, cfg.Validate()
		}
		return cfg, fmt.Errorf("read config: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, cfg.Validate()
}

// Validate checks that the configuration is usable and safe.
func (c Config) Validate() error {
	if !filepath.IsAbs(c.SocketPath) {
		return errors.New("socket_path must be absolute")
	}
	if !filepath.IsAbs(c.TokenFile) {
		return errors.New("token_file must be absolute")
	}
	if !filepath.IsAbs(c.RootDir) {
		return errors.New("root_dir must be absolute")
	}
	if !filepath.IsAbs(c.ProcDir) {
		return errors.New("proc_dir must be absolute")
	}
	if _, err := c.SocketFileMode(); err != nil {
		return err
	}
	for name, p := range map[string]string{"sites_root": c.SitesRoot, "state_dir": c.StateDir, "backup_dir": c.BackupDir} {
		if !filepath.IsAbs(p) || filepath.Clean(p) == "/" {
			return fmt.Errorf("%s must be an absolute path other than /", name)
		}
	}
	if c.SimulateCommands && filepath.Clean(c.RootDir) == "/" {
		return errors.New("simulate_commands requires a root_dir other than /")
	}
	if c.ListenTCP != "" {
		if _, _, err := net.SplitHostPort(c.ListenTCP); err != nil {
			return fmt.Errorf("listen_tcp must be host:port: %v", err)
		}
		if len(c.AllowedClients) == 0 {
			return errors.New("listen_tcp requires allowed_clients (the panel server's address)")
		}
		for _, a := range c.AllowedClients {
			if net.ParseIP(a) == nil {
				if _, _, err := net.ParseCIDR(a); err != nil {
					return fmt.Errorf("allowed_clients: %q is not an address or network", a)
				}
			}
		}
		if !filepath.IsAbs(c.TLSCert) || !filepath.IsAbs(c.TLSKey) {
			return errors.New("tls_cert and tls_key must be absolute")
		}
	}
	if c.TerminalListen != "" {
		host, _, err := net.SplitHostPort(c.TerminalListen)
		if err != nil {
			return fmt.Errorf("terminal_listen must be host:port: %v", err)
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return errors.New("terminal_listen must be a loopback address (127.0.0.1:port or [::1]:port)")
		}
	}
	if c.MaxJobs < 1 || c.MaxJobs > 16 {
		return errors.New("max_jobs must be between 1 and 16")
	}
	if c.MetricsInterval.Duration < time.Second {
		return errors.New("metrics_interval must be at least 1s")
	}
	if c.MetricsHistory < 1 || c.MetricsHistory > 10000 {
		return errors.New("metrics_history must be between 1 and 10000")
	}
	if c.ActionTimeout.Duration <= 0 {
		return errors.New("action_timeout must be positive")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("unknown log_level %q", c.LogLevel)
	}
	return nil
}

// SocketFileMode parses SocketMode.
func (c Config) SocketFileMode() (os.FileMode, error) {
	var mode uint32
	if _, err := fmt.Sscanf(c.SocketMode, "%o", &mode); err != nil {
		return 0, fmt.Errorf("invalid socket_mode %q", c.SocketMode)
	}
	if mode&0o007 != 0 {
		return 0, errors.New("socket_mode must not grant access to other users")
	}
	return os.FileMode(mode), nil
}

// ReadToken loads the shared secret from TokenFile.
// It refuses tokens that are too short or files readable by everyone.
func (c Config) ReadToken() (string, error) {
	info, err := os.Stat(c.TokenFile)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	if info.Mode().Perm()&0o004 != 0 {
		return "", fmt.Errorf("token file %s must not be world-readable", c.TokenFile)
	}
	data, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if len(token) < 32 {
		return "", errors.New("agent token must be at least 32 characters")
	}
	return token, nil
}
