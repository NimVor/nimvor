// Package components reports, for each part of a hosting server (web
// server, PHP, databases, email, DNS, FTP, caches, security), every step
// it needs: its packages installed, WhaCP's setup done, its services
// running and starting at boot, and its ports open in the firewall. The
// panel's Components page shows them with a Fix action per step; the
// actions themselves are the existing ones (packages.install,
// services.control, firewall.allow_ports, each component's setup page).
// It only reads.
package components

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/avmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/dnsmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/ftpmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/mailmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/pkgs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/security"
	"github.com/mchkhawarshahzad/whacp/agent/internal/systemd"
)

// Step states.
const (
	OK   = "ok"   // done
	Todo = "todo" // needs doing (Fix)
	NA   = "na"   // does not apply (yet)
)

// Step is one requirement of a component.
type Step struct {
	ID     string `json:"id"` // installed, setup, running, boot, ports
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	// Items the Fix acts on: missing packages, units, closed ports
	// ("443/tcp").
	Items []string `json:"items,omitempty"`
}

// Component is one part of the server and its steps.
type Component struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Installed: its main package (or, for PHP, one version with PHP-FPM).
	Installed bool     `json:"installed"`
	Packages  []string `json:"packages"` // WhaCP package names (packages.install)
	Units     []string `json:"units"`
	Steps     []Step   `json:"steps"`
	// Ready: every step that applies is done.
	Ready bool `json:"ready"`
}

// def describes a component.
type def struct {
	id, name, desc string
	packages       []string // WhaCP names; the first is the main one
	units          func(env *host.Env, installed map[string]bool) []string
	ports          func(env *host.Env) []security.PortSpec
	// setup: done (ok) and what to do when not; nil when WhaCP needs no
	// setup step.
	setup func(env *host.Env) (bool, string)
}

func fixed(u ...string) func(*host.Env, map[string]bool) []string {
	return func(*host.Env, map[string]bool) []string { return u }
}

func tcp(ports ...string) func(*host.Env) []security.PortSpec {
	return func(*host.Env) []security.PortSpec {
		out := make([]security.PortSpec, 0, len(ports))
		for _, p := range ports {
			proto := "tcp"
			if strings.HasSuffix(p, "/udp") {
				proto = "udp"
			}
			out = append(out, security.PortSpec{Port: strings.TrimSuffix(strings.TrimSuffix(p, "/udp"), "/tcp"), Proto: proto})
		}
		return out
	}
}

// Defs are the components, in the order the page shows them.
var defs = []def{
	{id: "web", name: "Web server", desc: "Nginx serves the websites and the panel.", packages: []string{"nginx"},
		units: func(env *host.Env, _ map[string]bool) []string { return []string{env.M.NginxUnit} }, ports: tcp("80", "443")},
	{id: "php", name: "PHP", desc: "PHP-FPM runs the websites' PHP code, one service per version."},
	{id: "database", name: "Database server", desc: "MariaDB for the websites' databases (local connections only).", packages: []string{"mariadb-server"},
		units: func(env *host.Env, _ map[string]bool) []string { return env.M.MySQLUnit }},
	{id: "mail", name: "Email", desc: "Postfix sends and receives mail, Dovecot keeps the mailboxes, OpenDKIM signs, Rspamd filters spam.",
		packages: []string{"postfix", "dovecot-imapd", "dovecot-lmtpd", "dovecot-sieve", "opendkim", "rspamd"},
		units: func(env *host.Env, inst map[string]bool) []string {
			u := []string{"postfix.service", "dovecot.service"}
			if mailmgr.Configured(env) {
				u = append(u, mailmgr.DKIMUnit)
			}
			if inst["rspamd"] {
				u = append(u, "rspamd.service")
			}
			return u
		},
		ports: tcp("25", "465", "587", "993", "995", "143", "110", "4190"),
		setup: func(env *host.Env) (bool, string) {
			return mailmgr.Configured(env), "Set up email on the Email page (Set up and start)."
		}},
	{id: "dns", name: "DNS server", desc: "BIND answers for the zones hosted here (only needed when this server is the domains' nameserver).", packages: []string{"bind9"},
		units: func(env *host.Env, _ map[string]bool) []string { return []string{env.M.DNSUnit} }, ports: tcp("53", "53/udp"),
		setup: func(env *host.Env) (bool, string) {
			return dnsmgr.Configured(env), "Turn on DNS hosting on the DNS page."
		}},
	{id: "ftp", name: "FTP server", desc: "FTPS accounts for websites (SFTP needs no FTP server).", packages: []string{"pure-ftpd"},
		units: func(env *host.Env, _ map[string]bool) []string {
			if ftpmgr.Configured(env) {
				return []string{ftpmgr.Unit}
			}
			return nil
		},
		ports: func(env *host.Env) []security.PortSpec {
			p := []security.PortSpec{{Port: "21", Proto: "tcp"}}
			if r := ftpmgr.PassiveRange(env); r != "" {
				p = append(p, security.PortSpec{Port: r, Proto: "tcp"})
			}
			return p
		},
		setup: func(env *host.Env) (bool, string) {
			return ftpmgr.Configured(env), "Set up the FTP server on the FTP page."
		}},
	{id: "redis", name: "Redis", desc: "Object cache for websites (local connections only).", packages: []string{"redis-server"},
		units: func(env *host.Env, _ map[string]bool) []string { return []string{env.M.RedisUnit} }},
	{id: "memcached", name: "Memcached", desc: "Memory cache for websites (local connections only).", packages: []string{"memcached"},
		units: func(env *host.Env, _ map[string]bool) []string { return []string{env.M.MemcachedUnit} }},
	{id: "firewall", name: "Firewall", desc: "UFW, or firewalld on AlmaLinux, Rocky Linux and RHEL.", packages: []string{"ufw"},
		units: func(env *host.Env, _ map[string]bool) []string {
			if env.Platform.PackageManager() == "dnf" {
				return []string{"firewalld.service"}
			}
			return []string{"ufw.service"}
		}},
	{id: "fail2ban", name: "Fail2ban", desc: "Blocks addresses that keep failing to sign in.", packages: []string{"fail2ban"}, units: fixed("fail2ban.service")},
	{id: "clamav", name: "Malware scanner", desc: "ClamAV and its signature updates.", packages: []string{"clamav", "clamav-freshclam"},
		units: func(*host.Env, map[string]bool) []string { return avmgr.FreshclamUnits[:1] }},
	{id: "certbot", name: "Let's Encrypt", desc: "Free certificates for websites (AutoSSL).", packages: []string{"certbot"}},
}

// Status reports every component.
func Status(ctx context.Context, env *host.Env) []Component {
	// Packages of every component in one query.
	var names []string
	for _, d := range defs {
		for _, p := range d.packages {
			if _, ok := env.Platform.PackageName(p); ok {
				names = append(names, p)
			}
		}
	}
	installed := map[string]bool{}
	for n := range pkgs.Installed(ctx, env, names) {
		installed[n] = true
	}
	// Units of every component in one query.
	unitsOf := map[string][]string{}
	var all []string
	php := env.Discovery.PHP()
	for _, d := range defs {
		var u []string
		switch {
		case d.id == "php":
			for _, v := range php.Versions {
				if v.FPMBinary != "" {
					u = append(u, env.M.FPMUnit(v.Version))
				}
			}
		case d.units != nil:
			u = d.units(env, installed)
		}
		unitsOf[d.id] = u
		all = append(all, u...)
	}
	units := map[string]systemd.Service{}
	if st, err := (systemd.Client{Runner: env.Runner}).Status(ctx, all); err == nil {
		for _, s := range st {
			units[s.Unit] = s
		}
	}
	fw, fwErr := security.Firewall(ctx, env)
	out := make([]Component, 0, len(defs))
	for _, d := range defs {
		c := Component{ID: d.id, Name: d.name, Description: d.desc, Packages: []string{}, Units: []string{}}
		// Installed.
		var missing []string
		for _, p := range d.packages {
			if _, ok := env.Platform.PackageName(p); !ok {
				continue // nothing to install here (e.g. ufw on RHEL is firewalld)
			}
			c.Packages = append(c.Packages, p)
			if !installed[p] {
				missing = append(missing, p)
			}
		}
		switch {
		case d.id == "php":
			c.Installed = len(unitsOf["php"]) > 0
			st := Step{ID: "installed", Status: OK, Detail: fmt.Sprintf("%d version(s) with PHP-FPM", len(unitsOf["php"]))}
			if !c.Installed {
				st = Step{ID: "installed", Status: Todo, Detail: "No PHP version with PHP-FPM: install one on the PHP page."}
			}
			c.Steps = append(c.Steps, st)
		case len(c.Packages) > 0:
			c.Installed = installed[c.Packages[0]]
			st := Step{ID: "installed", Status: OK}
			if len(missing) > 0 {
				st = Step{ID: "installed", Status: Todo, Items: missing, Detail: "Not installed: " + strings.Join(missing, ", ")}
			}
			c.Steps = append(c.Steps, st)
		}
		// Units that exist (a unit WhaCP writes at setup only after it).
		var present []string
		for _, u := range unitsOf[d.id] {
			if s, ok := units[u]; ok && s.LoadState != "not-found" {
				present = append(present, u)
				c.Units = append(c.Units, s.Unit)
			}
		}
		if d.id == "database" && len(present) > 1 {
			present = present[:1] // mariadb.service and its alias
			c.Units = c.Units[:1]
		}
		if !c.Installed {
			for _, id := range []string{"setup", "running", "boot", "ports"} {
				c.Steps = append(c.Steps, Step{ID: id, Status: NA})
			}
			out = append(out, c)
			continue
		}
		// Set up.
		if d.setup != nil {
			ok, todo := d.setup(env)
			st := Step{ID: "setup", Status: OK}
			if !ok {
				st = Step{ID: "setup", Status: Todo, Detail: todo}
			}
			c.Steps = append(c.Steps, st)
		} else {
			c.Steps = append(c.Steps, Step{ID: "setup", Status: NA})
		}
		// Running and at boot.
		var stopped, noBoot []string
		for _, u := range present {
			s := units[u]
			if d.id == "firewall" {
				continue // the firewall's own state below
			}
			if s.ActiveState != "active" {
				stopped = append(stopped, u)
			}
			if s.UnitFileState != "enabled" && s.UnitFileState != "static" && s.UnitFileState != "alias" && s.UnitFileState != "generated" {
				noBoot = append(noBoot, u)
			}
		}
		running := Step{ID: "running", Status: OK}
		boot := Step{ID: "boot", Status: OK}
		switch {
		case d.id == "firewall":
			if fwErr == nil && !fw.Active {
				running = Step{ID: "running", Status: Todo, Detail: "The firewall is off: turn it on on the Firewall page (SSH and the panel are opened first)."}
			}
			for _, u := range present {
				if s := units[u]; s.UnitFileState != "enabled" && fw.Active {
					boot = Step{ID: "boot", Status: Todo, Items: []string{u}, Detail: "Not started at boot: " + u}
				}
			}
		case len(present) == 0:
			running, boot = Step{ID: "running", Status: NA}, Step{ID: "boot", Status: NA}
		default:
			if len(stopped) > 0 {
				running = Step{ID: "running", Status: Todo, Items: stopped, Detail: "Not running: " + strings.Join(stopped, ", ")}
			}
			if len(noBoot) > 0 {
				boot = Step{ID: "boot", Status: Todo, Items: noBoot, Detail: "Not started at boot: " + strings.Join(noBoot, ", ")}
			}
		}
		c.Steps = append(c.Steps, running, boot)
		// Ports.
		ports := Step{ID: "ports", Status: NA}
		if d.ports != nil {
			switch {
			case fwErr != nil || !fw.Installed:
				ports.Detail = "No firewall to check."
			case !fw.Active:
				ports.Detail = "The firewall is off: every port is reachable."
			default:
				var closed []string
				for _, p := range d.ports(env) {
					if !security.PortCovered(p, fw.Rules) {
						closed = append(closed, p.Port+"/"+p.Proto)
					}
				}
				ports.Status = OK
				if len(closed) > 0 {
					ports = Step{ID: "ports", Status: Todo, Items: closed, Detail: "Closed in the firewall: " + strings.Join(closed, ", ")}
				}
			}
		}
		c.Steps = append(c.Steps, ports)
		c.Ready = true
		for _, s := range c.Steps {
			if s.Status == Todo {
				c.Ready = false
			}
		}
		out = append(out, c)
	}
	return out
}

// Register adds components.status.
func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "components.status", Description: "Each part of the server (web, PHP, database, email, DNS, FTP, caches, security) with its steps: installed, set up, running, started at boot, ports open in the firewall (read-only).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return Status(ctx, env), nil
	}})
}
