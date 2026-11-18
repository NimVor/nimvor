// Package realip restores the visitor's own address for websites behind
// Cloudflare. A proxied website sees every visitor at one of Cloudflare's
// addresses, so traffic limits, IP and country lists, the access log and
// the application's REMOTE_ADDR would all treat visitors as a handful of
// proxies. With this switched on, Nginx takes the address from the
// CF-Connecting-IP header, but only when the connection comes from one of
// Cloudflare's published networks (anyone else sending the header is
// ignored). The file is written once, at http level, checked with nginx -t
// and rolled back when Nginx does not accept or reload it.
package realip

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// File is the http-level Nginx file.
const File = "nimvor-00-realip.conf"

// Header is the request header Cloudflare puts the visitor's address in.
const Header = "CF-Connecting-IP"

// MaxRanges bounds a list sent by the panel (Cloudflare publishes about twenty).
const MaxRanges = 100

// Defaults are Cloudflare's published networks (https://www.cloudflare.com/ips/),
// used until the panel sends a newer list.
var Defaults = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18",
	"108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17",
	"162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32",
	"2a06:98c0::/29", "2c0f:f248::/32",
}

// State is what the agent remembers.
type State struct {
	Enabled bool     `json:"enabled"`
	Ranges  []string `json:"ranges,omitempty"` // empty: Defaults
}

var mu sync.Mutex

// Path is the Nginx file.
func Path(e *host.Env) string { return filepath.Join(e.M.NginxEnabled, File) }

func statePath(e *host.Env) string { return filepath.Join(e.Cfg.StateDir, "realip.json") }

// Load reads the state (switched off when there is none).
func Load(e *host.Env) State {
	var st State
	if b, err := e.Root.ReadFile(statePath(e)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

// effective is the list in use.
func (st State) effective() []string {
	if len(st.Ranges) > 0 {
		return st.Ranges
	}
	return Defaults
}

// Clean checks a list of networks and returns it in canonical form, sorted and
// without duplicates. A network wider than a /8 (IPv4) or /16 (IPv6) is refused:
// trusting that much of the Internet would let anyone choose the address.
func Clean(in []string) ([]string, error) {
	if len(in) > MaxRanges {
		return nil, fmt.Errorf("at most %d networks", MaxRanges)
	}
	seen := map[string]bool{}
	var out []string
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			if ip := net.ParseIP(s); ip != nil {
				if ip.To4() != nil {
					s += "/32"
				} else {
					s += "/128"
				}
			}
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("%q is not a network such as 173.245.48.0/20", raw)
		}
		ones, bits := n.Mask.Size()
		if (bits == 32 && ones < 8) || (bits == 128 && ones < 16) {
			return nil, fmt.Errorf("%s is too wide to trust", n.String())
		}
		if !seen[n.String()] {
			seen[n.String()] = true
			out = append(out, n.String())
		}
	}
	sort.Strings(out)
	return out, nil
}

// Render is the Nginx file for a state.
func Render(st State) string {
	var b strings.Builder
	b.WriteString("# Managed by NimVor: the visitor's address behind Cloudflare. Only connections from\n")
	b.WriteString("# Cloudflare's networks may set it; edit it under Server > Cloudflare, not here.\n")
	fmt.Fprintf(&b, "real_ip_header %s;\nreal_ip_recursive off;\n", Header)
	for _, r := range st.effective() {
		fmt.Fprintf(&b, "set_real_ip_from %s;\n", r)
	}
	return b.String()
}

// Status is what the panel shows.
func Status(e *host.Env) map[string]any {
	st := Load(e)
	return map[string]any{
		"enabled":       st.Enabled,
		"custom":        len(st.Ranges) > 0,
		"ranges":        st.effective(),
		"header":        Header,
		"file":          Path(e),
		"file_present":  e.Root.Exists(Path(e)),
		"default_count": len(Defaults),
	}
}

// Set switches the setting on or off (and replaces the list when ranges is not
// empty), then checks and reloads Nginx, undoing everything on failure.
func Set(ctx context.Context, e *host.Env, enabled bool, ranges []string) error {
	defer e.LockWebConfig()()
	mu.Lock()
	defer mu.Unlock()
	st := Load(e)
	st.Enabled = enabled
	if ranges != nil {
		st.Ranges = ranges
	}
	set := e.Changes("realip")
	if st.Enabled {
		set.Write(Path(e), []byte(Render(st)), 0o644, agentfs.RootOwner)
	} else if e.Root.Exists(Path(e)) {
		set.Remove(Path(e))
	}
	sb, _ := json.MarshalIndent(st, "", "  ")
	set.Write(statePath(e), sb, 0o600, agentfs.RootOwner)
	applied, err := set.Commit(func() error { return e.NginxTest(ctx) })
	if err != nil {
		return host.FailWithValidation("The visitor address setting was not changed. If Nginx already has real_ip settings (for example added by hand), remove them first", err)
	}
	if len(applied.Changed) > 0 {
		if err := e.ReloadNginx(ctx); err != nil {
			applied.Rollback()
			e.ReloadNginx(ctx)
			return host.FailWithValidation("Nginx did not reload; the previous setting was restored", err)
		}
	}
	return nil
}

// Register adds the actions.
func Register(r *actions.Registry, e *host.Env) {
	e.Writer.Policy.Patterns = append(e.Writer.Policy.Patterns, Path(e))
	r.Register(actions.Action{Name: "realip.status", Description: "Whether Nginx restores the visitor's address behind Cloudflare (from the CF-Connecting-IP header, trusted only from Cloudflare's networks) and the networks it trusts.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		return Status(e), nil
	}})
	r.Register(actions.Action{Name: "realip.set", Mutating: true, Description: "Switch on or off the restoring of the visitor's address behind Cloudflare, optionally replacing the trusted networks (Cloudflare's published list). Checked with nginx -t, rolled back on failure.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Enabled bool      `json:"enabled"`
			Ranges  *[]string `json:"ranges"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		var list []string
		if in.Ranges != nil {
			c, err := Clean(*in.Ranges)
			if err != nil {
				return nil, actions.InvalidParams(err.Error())
			}
			list = c
			if list == nil {
				list = []string{} // an empty list sent on purpose goes back to the built-in one
			}
		}
		if err := Set(ctx, e, in.Enabled, list); err != nil {
			return nil, err
		}
		return Status(e), nil
	}})
}
