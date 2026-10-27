// Package mcmgr reports on and configures the local Memcached server.
//
// Memcached's configuration file belongs to the distribution package, so
// WhaCP only changes it when the caller explicitly confirms. It edits just
// the memory size, connection limit and listen addresses, keeps a backup,
// restarts Memcached and checks that it answers; on any failure the
// previous file is restored and Memcached restarted again. Memcached has
// no authentication, so only loopback listen addresses are accepted.
package mcmgr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/changeset"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Settings are the options WhaCP manages.
type Settings struct {
	MemoryMB int      `json:"memory_mb"`
	MaxConns int      `json:"max_conns"`
	Listen   []string `json:"listen"`
	Port     int      `json:"port"`
	Socket   string   `json:"socket,omitempty"`
}

// sysconfig reports whether the file uses RHEL's KEY="value" format.
func sysconfig(path string) bool { return filepath.Base(path) != "memcached.conf" }

// Parse reads settings from either file format.
func Parse(data, path string) Settings {
	s := Settings{MemoryMB: 64, MaxConns: 1024, Port: 11211}
	if sysconfig(path) {
		for _, line := range strings.Split(data, "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok || strings.HasPrefix(k, "#") {
				continue
			}
			v = strings.Trim(v, `"'`)
			switch k {
			case "PORT":
				s.Port, _ = strconv.Atoi(v)
			case "CACHESIZE":
				s.MemoryMB, _ = strconv.Atoi(v)
			case "MAXCONN":
				s.MaxConns, _ = strconv.Atoi(v)
			case "OPTIONS":
				f := strings.Fields(v)
				for i := 0; i+1 < len(f); i++ {
					switch f[i] {
					case "-l":
						s.Listen = append(s.Listen, strings.Split(f[i+1], ",")...)
					case "-s":
						s.Socket = f[i+1]
					}
				}
			}
		}
		return s
	}
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(f[0], "#") {
			continue
		}
		switch f[0] {
		case "-m", "--memory-limit":
			s.MemoryMB, _ = strconv.Atoi(f[1])
		case "-c", "--conn-limit":
			s.MaxConns, _ = strconv.Atoi(f[1])
		case "-p", "--port":
			s.Port, _ = strconv.Atoi(f[1])
		case "-l", "--listen":
			s.Listen = append(s.Listen, strings.Split(f[1], ",")...)
		case "-s", "--unix-socket":
			s.Socket = f[1]
		}
	}
	return s
}

// Render rewrites the managed options in data, keeping everything else.
func Render(data, path string, s Settings) string {
	if sysconfig(path) {
		set := map[string]string{"CACHESIZE": strconv.Itoa(s.MemoryMB), "MAXCONN": strconv.Itoa(s.MaxConns)}
		var out []string
		seen := map[string]bool{}
		for _, line := range strings.Split(strings.TrimRight(data, "\n"), "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
			switch {
			case ok && set[k] != "":
				out = append(out, fmt.Sprintf("%s=%q", k, set[k]))
				seen[k] = true
			case ok && k == "OPTIONS":
				out = append(out, fmt.Sprintf("OPTIONS=%q", replaceListen(strings.Trim(v, `"'`), s.Listen)))
				seen[k] = true
			default:
				out = append(out, line)
			}
		}
		for _, k := range []string{"CACHESIZE", "MAXCONN"} {
			if !seen[k] {
				out = append(out, fmt.Sprintf("%s=%q", k, set[k]))
			}
		}
		if !seen["OPTIONS"] {
			out = append(out, fmt.Sprintf("OPTIONS=%q", replaceListen("", s.Listen)))
		}
		return strings.Join(out, "\n") + "\n"
	}
	var out []string
	done := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(data, "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) >= 1 && !strings.HasPrefix(f[0], "#") {
			switch f[0] {
			case "-m", "--memory-limit":
				if !done["m"] {
					out = append(out, "-m "+strconv.Itoa(s.MemoryMB))
					done["m"] = true
				}
				continue
			case "-c", "--conn-limit":
				if !done["c"] {
					out = append(out, "-c "+strconv.Itoa(s.MaxConns))
					done["c"] = true
				}
				continue
			case "-l", "--listen":
				if !done["l"] {
					for _, a := range s.Listen {
						out = append(out, "-l "+a)
					}
					done["l"] = true
				}
				continue
			}
		}
		out = append(out, line)
	}
	if !done["m"] {
		out = append(out, "-m "+strconv.Itoa(s.MemoryMB))
	}
	if !done["c"] {
		out = append(out, "-c "+strconv.Itoa(s.MaxConns))
	}
	if !done["l"] {
		for _, a := range s.Listen {
			out = append(out, "-l "+a)
		}
	}
	return strings.Join(out, "\n") + "\n"
}

var listenRe = regexp.MustCompile(`(^|\s)-l\s+\S+`)

func replaceListen(opts string, listen []string) string {
	opts = strings.TrimSpace(listenRe.ReplaceAllString(opts, ""))
	l := "-l " + strings.Join(listen, ",")
	if opts == "" {
		return l
	}
	return l + " " + opts
}

// Validate checks new settings. Only loopback addresses are allowed:
// Memcached has no authentication.
func (s Settings) Validate() error {
	if s.MemoryMB < 16 || s.MemoryMB > 262144 {
		return actions.InvalidParams("memory_mb: 16–262144")
	}
	if s.MaxConns < 16 || s.MaxConns > 65536 {
		return actions.InvalidParams("max_conns: 16–65536")
	}
	if len(s.Listen) == 0 || len(s.Listen) > 4 {
		return actions.InvalidParams("listen: 1–4 addresses")
	}
	for _, a := range s.Listen {
		ip := net.ParseIP(a)
		if a == "localhost" {
			continue
		}
		if ip == nil || !ip.IsLoopback() {
			return actions.InvalidParams("listen: only loopback addresses (127.0.0.1, ::1) — Memcached has no authentication")
		}
	}
	return nil
}

// Stats connects and runs "stats" (and "stats settings").
func Stats(ctx context.Context, s Settings, root func(string) (string, error)) (map[string]string, map[string]string, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	var c net.Conn
	var err error
	if s.Socket != "" {
		real, perr := root(s.Socket)
		if perr != nil {
			return nil, nil, perr
		}
		c, err = d.DialContext(ctx, "unix", real)
	} else {
		addr := "127.0.0.1"
		for _, a := range s.Listen {
			if a == "::1" && len(s.Listen) == 1 {
				addr = "::1"
			}
		}
		c, err = d.DialContext(ctx, "tcp", net.JoinHostPort(addr, strconv.Itoa(s.Port)))
	}
	if err != nil {
		return nil, nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(c)
	read := func(cmd string) (map[string]string, error) {
		if _, err := fmt.Fprintf(c, "%s\r\n", cmd); err != nil {
			return nil, err
		}
		out := map[string]string{}
		for i := 0; i < 500; i++ {
			line, err := r.ReadString('\n')
			if err != nil {
				return nil, err
			}
			line = strings.TrimSpace(line)
			if line == "END" {
				return out, nil
			}
			if strings.HasPrefix(line, "ERROR") || strings.HasPrefix(line, "CLIENT_ERROR") || strings.HasPrefix(line, "SERVER_ERROR") {
				return nil, fmt.Errorf("%s", line)
			}
			if f := strings.Fields(line); len(f) >= 3 && f[0] == "STAT" {
				out[f[1]] = strings.Join(f[2:], " ")
			}
		}
		return out, nil
	}
	stats, err := read("stats")
	if err != nil {
		return nil, nil, err
	}
	settings, _ := read("stats settings")
	return stats, settings, nil
}

func flushAll(ctx context.Context, s Settings, root func(string) (string, error)) error {
	d := net.Dialer{Timeout: 3 * time.Second}
	network, addr := "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(s.Port))
	if s.Socket != "" {
		real, err := root(s.Socket)
		if err != nil {
			return err
		}
		network, addr = "unix", real
	}
	c, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(c, "flush_all\r\n")
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return err
	}
	if strings.TrimSpace(line) != "OK" {
		return fmt.Errorf("%s", strings.TrimSpace(line))
	}
	return nil
}

// Register adds the Memcached actions.
func Register(r *actions.Registry, env *host.Env) {
	path := env.M.MemcachedConfig
	unit := env.M.MemcachedUnit
	load := func() (Settings, string, bool) {
		b, err := env.Root.ReadFile(path)
		if err != nil {
			return Settings{MemoryMB: 64, MaxConns: 1024, Port: 11211, Listen: []string{"127.0.0.1"}}, "", false
		}
		return Parse(string(b), path), string(b), true
	}
	r.Register(actions.Action{Name: "memcached.status", Description: "Whether Memcached is installed and running, its settings and statistics.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		s, _, installed := load()
		out := map[string]any{"installed": installed, "config_file": path, "settings": s, "active": false}
		if !installed {
			return out, nil
		}
		out["active"] = env.UnitActive(ctx, unit)
		stats, settings, err := Stats(ctx, s, env.Root.Path)
		if err != nil {
			out["error"] = "Memcached is not answering: " + err.Error()
			return out, nil
		}
		out["stats"], out["runtime"] = stats, settings
		return out, nil
	}})
	r.Register(actions.Action{Name: "memcached.configure", Mutating: true, Description: "Set Memcached's memory, connection limit and (loopback) listen addresses in its package configuration file after explicit confirmation; restarts and verifies, rolls back on failure.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			MemoryMB int      `json:"memory_mb"`
			MaxConns int      `json:"max_conns"`
			Listen   []string `json:"listen"`
			Confirm  bool     `json:"confirm_edit_package_config"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !in.Confirm {
			return nil, actions.InvalidParams("confirm_edit_package_config: " + path + " belongs to the Memcached package; confirm that WhaCP may edit it (a backup is kept)")
		}
		cur, data, installed := load()
		if !installed {
			return nil, actions.Failed("Memcached is not installed.", "Install it under Packages first.")
		}
		next := cur
		next.MemoryMB, next.MaxConns, next.Listen = in.MemoryMB, in.MaxConns, in.Listen
		if err := next.Validate(); err != nil {
			return nil, err
		}
		cs := changeset.New(agentfs.Writer{Root: env.Root, Policy: agentfs.Policy{Patterns: []string{path, env.Cfg.StateDir + "/**"}}, Owners: env.Writer.Owners},
			filepath.Join(env.Cfg.StateDir, "config-backups"), "memcached")
		cs.Write(path, []byte(Render(data, path, next)), 0o644, agentfs.Keep)
		restartAndCheck := func() error {
			if err := env.Systemctl(ctx, "restart", unit); err != nil {
				return err
			}
			var last error
			for i := 0; i < 20; i++ {
				if _, _, last = Stats(ctx, next, env.Root.Path); last == nil {
					return nil
				}
				time.Sleep(250 * time.Millisecond)
			}
			return fmt.Errorf("memcached did not answer after the restart: %v", last)
		}
		applied, err := cs.Commit(restartAndCheck)
		if err != nil {
			// The old file is back; bring the service back with it.
			env.Systemctl(ctx, "restart", unit)
			return nil, actions.Failed("Memcached did not start with the new settings, so the previous configuration was restored.", err.Error())
		}
		return map[string]any{"settings": next, "changed": applied.Changed, "backup": applied.Backup}, nil
	}})
	r.Register(actions.Action{Name: "memcached.flush", Mutating: true, Description: "Remove every item from Memcached (requires confirm).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Confirm string `json:"confirm"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.Confirm != "flush" {
			return nil, actions.InvalidParams(`confirm must be "flush"`)
		}
		s, _, _ := load()
		if err := flushAll(ctx, s, env.Root.Path); err != nil {
			return nil, actions.Failed("flush_all failed.", err.Error())
		}
		return map[string]any{"flushed": true}, nil
	}})
}
