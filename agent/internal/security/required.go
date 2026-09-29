package security

import (
	"context"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// RequiredPort is a port the firewall must keep open for WhaCP or a
// service it runs. Critical ports (SSH, the panel, the agent's remote
// listener) are what keeps the server manageable: they are opened before
// the firewall is turned on and their last rule cannot be deleted while it
// is on. The others are opened too when the firewall is turned on (services
// that are listening), and may be closed later on purpose.
type RequiredPort struct {
	Port     string `json:"port"` // "8443" or "30000:30049"
	Proto    string `json:"proto"`
	From     string `json:"from,omitempty"` // "" = anywhere
	Service  string `json:"service"`
	Critical bool   `json:"critical"`
	Open     bool   `json:"open"` // an allow rule covers it
}

// PanelSites are the nginx sites the installer writes for the panel.
var PanelSites = []string{"/etc/nginx/sites-available/whacp-panel.conf", "/etc/nginx/whacp-available/whacp-panel.conf"}

var panelListenRe = regexp.MustCompile(`(?m)^\s*listen\s+(?:\[[^\]]*\]:|[0-9.]+:)?([0-9]{1,5})\s+[^;]*ssl`)

// servicePorts: well-known ports of the services WhaCP manages, opened when
// something listens on them on a public address.
var servicePorts = []struct {
	port  int
	proto string
	name  string
}{
	{80, "tcp", "Websites (HTTP)"}, {443, "tcp", "Websites and webmail (HTTPS)"}, {443, "udp", "Websites (HTTP/3)"},
	{25, "tcp", "Mail (SMTP)"}, {465, "tcp", "Mail submission (SMTPS)"}, {587, "tcp", "Mail submission"},
	{110, "tcp", "POP3"}, {995, "tcp", "POP3S"}, {143, "tcp", "IMAP"}, {993, "tcp", "IMAPS"}, {4190, "tcp", "Mail filters (ManageSieve)"},
	{21, "tcp", "FTP"}, {53, "tcp", "DNS"}, {53, "udp", "DNS"},
}

var passiveRe = regexp.MustCompile(`(?m)^\s*PassivePortRange\s+([0-9]{1,5})\s+([0-9]{1,5})\s*$`)

// RequiredPorts lists what the firewall must allow, with whether a rule
// already does.
func RequiredPorts(ctx context.Context, env *host.Env, rules []Rule) []RequiredPort {
	var out []RequiredPort
	add := func(p RequiredPort) {
		for _, o := range out {
			if o.Port == p.Port && o.Proto == p.Proto && o.From == p.From {
				return
			}
		}
		out = append(out, p)
	}
	add(RequiredPort{Port: strconv.Itoa(sshPort(ctx, env)), Proto: "tcp", Service: "SSH", Critical: true})
	for _, site := range PanelSites {
		if b, err := env.Root.ReadFile(site); err == nil {
			for _, m := range panelListenRe.FindAllStringSubmatch(string(b), -1) {
				add(RequiredPort{Port: m[1], Proto: "tcp", Service: "WhaCP panel (HTTPS)", Critical: true})
			}
		}
	}
	// A remote agent (multi-server): its listener, from the panel servers only.
	if _, port, err := net.SplitHostPort(env.Cfg.ListenTCP); err == nil && port != "" {
		host, _, _ := net.SplitHostPort(env.Cfg.ListenTCP)
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			clients := env.Cfg.AllowedClients
			if len(clients) == 0 {
				clients = []string{""}
			}
			for _, c := range clients {
				add(RequiredPort{Port: port, Proto: "tcp", From: c, Service: "WhaCP agent (panel server)", Critical: true})
			}
		}
	}
	listening := map[string]bool{}
	for _, l := range Listening(env) {
		if !l.Local {
			listening[strconv.Itoa(l.Port)+"/"+l.Proto] = true
		}
	}
	for _, s := range servicePorts {
		if listening[strconv.Itoa(s.port)+"/"+s.proto] {
			add(RequiredPort{Port: strconv.Itoa(s.port), Proto: s.proto, Service: s.name})
		}
	}
	if listening["21/tcp"] {
		if b, err := env.Root.ReadFile(env.M.FTPDir + "/pure-ftpd.conf"); err == nil {
			if m := passiveRe.FindStringSubmatch(string(b)); m != nil {
				lo, _ := strconv.Atoi(m[1])
				hi, _ := strconv.Atoi(m[2])
				if lo > 0 && hi > lo && hi < 65536 {
					add(RequiredPort{Port: m[1] + ":" + m[2], Proto: "tcp", Service: "FTP passive data"})
				}
			}
		}
	}
	for i := range out {
		out[i].Open = covered(out[i], rules)
	}
	return out
}

// portAliases: ufw application profiles and firewalld services by the
// ports they open.
var portAliases = map[string][]string{
	"openssh": {"22/tcp"}, "ssh": {"22/tcp"},
	"nginx full": {"80/tcp", "443/tcp"}, "nginx http": {"80/tcp"}, "nginx https": {"443/tcp"},
	"http": {"80/tcp"}, "https": {"443/tcp"}, "http3": {"443/udp"},
	"smtp": {"25/tcp"}, "smtps": {"465/tcp"}, "smtp-submission": {"587/tcp"},
	"pop3": {"110/tcp"}, "pop3s": {"995/tcp"}, "imap": {"143/tcp"}, "imaps": {"993/tcp"},
	"managesieve": {"4190/tcp"}, "ftp": {"21/tcp"}, "dns": {"53/tcp", "53/udp"},
	"postfix": {"25/tcp"}, "postfix smtps": {"465/tcp"}, "postfix submission": {"587/tcp"},
	"dovecot imap": {"143/tcp"}, "dovecot secure imap": {"993/tcp"}, "dovecot pop3": {"110/tcp"}, "dovecot secure pop3": {"995/tcp"},
	"bind9": {"53/tcp", "53/udp"},
}

// covered reports whether an allow rule opens the port (from anywhere, or
// from the required source).
func covered(p RequiredPort, rules []Rule) bool {
	for _, r := range rules {
		if r.Action != "allow" && r.Action != "limit" {
			continue
		}
		if r.Direction != "" && r.Direction != "in" {
			continue
		}
		if r.From != "" && r.From != "any" && r.From != "Anywhere" && r.From != p.From {
			continue
		}
		if r.Proto != "" && r.Proto != p.Proto {
			continue
		}
		rp := strings.Replace(r.Port, "-", ":", 1)
		if names, ok := portAliases[strings.ToLower(rp)]; ok {
			for _, n := range names {
				if n == p.Port+"/"+p.Proto {
					return true
				}
			}
			continue
		}
		if rp == "" && r.From != "" && r.From == p.From {
			return true // everything from that source
		}
		if rp == p.Port || portIn(p.Port, rp) {
			return true
		}
	}
	return false
}

// portIn: a single port (or range) inside a rule's range.
func portIn(port, rng string) bool {
	lo, hi, ok := strings.Cut(rng, ":")
	if !ok {
		return false
	}
	a, _ := strconv.Atoi(lo)
	b, _ := strconv.Atoi(hi)
	plo, phi, isRange := strings.Cut(port, ":")
	x, _ := strconv.Atoi(plo)
	y := x
	if isRange {
		y, _ = strconv.Atoi(phi)
	}
	return a > 0 && x >= a && y <= b
}

// Input is the rule that opens the port.
func (p RequiredPort) Input() RuleInput {
	return RuleInput{Action: "allow", Port: p.Port, Proto: p.Proto, From: p.From, Comment: strings.NewReplacer("(", "", ")", "", "+", "").Replace(p.Service)}
}
