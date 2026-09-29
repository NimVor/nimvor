// Package tlsprofile holds the TLS settings every WhaCP virtual host
// includes: one Nginx snippet (whacp-tls.conf next to the site folders),
// so a server-wide change is one file, checked with nginx -t.
package tlsprofile

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/changeset"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Profile is the server-wide TLS configuration.
type Profile struct {
	// Protocols: "tls12" (TLS 1.2 and 1.3) or "tls13" (TLS 1.3 only).
	Protocols string `json:"protocols"`
	// Ciphers for TLS 1.2: "default" (Nginx's) or "intermediate"
	// (Mozilla's intermediate list: forward secrecy and AEAD only).
	Ciphers string `json:"ciphers"`
	// SessionTickets: off keeps forward secrecy across resumed sessions.
	SessionTickets bool `json:"session_tickets"`
}

// Default is what a new server gets: TLS 1.2 and 1.3 with Mozilla's
// intermediate ciphers (forward secrecy and AEAD only).
var Default = Profile{Protocols: "tls12", Ciphers: "intermediate", SessionTickets: true}

// Legacy is what WhaCP wrote before the default changed: a server whose
// snippet exists without a saved profile keeps it until the administrator
// changes the TLS configuration.
var Legacy = Profile{Protocols: "tls12", Ciphers: "default", SessionTickets: true}

// Mozilla's intermediate TLS 1.2 ciphers (TLS 1.3 suites are fixed by OpenSSL).
const intermediateCiphers = "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384:DHE-RSA-CHACHA20-POLY1305"

// LegacyBlock is what vhosts carried before the include (replaced on migration).
const LegacyBlock = "    ssl_protocols TLSv1.2 TLSv1.3;\n    ssl_prefer_server_ciphers off;\n    ssl_session_cache shared:WhaCPSSL:10m;\n    ssl_session_timeout 1d;\n"

// Validate checks a profile.
func (p Profile) Validate() error {
	if p.Protocols != "tls12" && p.Protocols != "tls13" {
		return fmt.Errorf("protocols: tls12 or tls13")
	}
	if p.Ciphers != "default" && p.Ciphers != "intermediate" {
		return fmt.Errorf("ciphers: default or intermediate")
	}
	return nil
}

// Render is the snippet's content (server context).
func Render(p Profile) string {
	var b strings.Builder
	b.WriteString("# Managed by WhaCP (SSL/TLS → TLS configuration). Included by every WhaCP website; changes here are overwritten.\n")
	if p.Protocols == "tls13" {
		b.WriteString("ssl_protocols TLSv1.3;\n")
	} else {
		b.WriteString("ssl_protocols TLSv1.2 TLSv1.3;\n")
	}
	if p.Ciphers == "intermediate" && p.Protocols != "tls13" {
		b.WriteString("ssl_ciphers " + intermediateCiphers + ";\n")
	}
	b.WriteString("ssl_prefer_server_ciphers off;\nssl_session_cache shared:WhaCPSSL:10m;\nssl_session_timeout 1d;\n")
	if !p.SessionTickets {
		b.WriteString("ssl_session_tickets off;\n")
	}
	return b.String()
}

// Path is the snippet's location.
func Path(e *host.Env) string {
	return filepath.Join(filepath.Dir(e.M.NginxAvailable), "whacp-tls.conf")
}

// Include is the vhost line.
func Include(e *host.Env) string { return "    include " + Path(e) + ";\n" }

func statePath(e *host.Env) string { return filepath.Join(e.Cfg.StateDir, "tls-profile.json") }

// Load returns the saved profile, or what the existing snippet was written
// with, or the default for a new server.
func Load(e *host.Env) Profile {
	p := Default
	if e.Root.Exists(Path(e)) {
		p = Legacy
	}
	if b, err := e.Root.ReadFile(statePath(e)); err == nil {
		var q Profile
		if json.Unmarshal(b, &q) == nil && q.Validate() == nil {
			p = q
		}
	}
	return p
}

// Ensure adds the snippet to set when it does not exist yet, so a vhost
// that includes it never fails nginx -t.
func Ensure(e *host.Env, set *changeset.Set) {
	if !e.Root.Exists(Path(e)) {
		set.Write(Path(e), []byte(Render(Load(e))), 0o644, agentfs.RootOwner)
	}
}

// Save adds the profile and its snippet to set.
func Save(e *host.Env, set *changeset.Set, p Profile) {
	b, _ := json.MarshalIndent(p, "", "  ")
	set.Write(statePath(e), b, 0o600, agentfs.RootOwner)
	set.Write(Path(e), []byte(Render(p)), 0o644, agentfs.RootOwner)
}
