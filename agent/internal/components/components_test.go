package components

import (
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func byID(cs []Component) map[string]Component {
	m := map[string]Component{}
	for _, c := range cs {
		m[c.ID] = c
	}
	return m
}

func step(c Component, id string) Step {
	for _, s := range c.Steps {
		if s.ID == id {
			return s
		}
	}
	return Step{}
}

// Rocky Linux 9 situation: the web server runs but its ports are closed,
// mail is installed but not set up nor started at boot.
func TestStatusSteps(t *testing.T) {
	h := testhost.New(t)
	h.Runner.Outputs = map[string]string{
		"sshd -T":            "port 22\n",
		"ufw status verbose": "Status: active\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n",
		"ufw show added":     "Added user rules (see 'ufw status' for running firewall):\nufw allow 22/tcp\n",
	}
	h.Respond = func(id string, a []string) (string, error, bool) {
		switch {
		case id == "dpkg-query":
			var b strings.Builder
			for _, p := range a {
				switch p {
				case "nginx", "postfix", "dovecot-imapd", "ufw":
					b.WriteString(p + "\tii \t1.0\n")
				}
			}
			return b.String(), nil, true
		case id == "systemctl" && len(a) > 0 && a[0] == "show":
			var b strings.Builder
			for _, u := range a {
				switch u {
				case "nginx.service":
					b.WriteString("Id=nginx.service\nLoadState=loaded\nActiveState=active\nUnitFileState=enabled\n\n")
				case "postfix.service", "dovecot.service":
					b.WriteString("Id=" + u + "\nLoadState=loaded\nActiveState=active\nUnitFileState=disabled\n\n")
				case "ufw.service":
					b.WriteString("Id=ufw.service\nLoadState=loaded\nActiveState=active\nUnitFileState=enabled\n\n")
				}
			}
			return b.String(), nil, true
		}
		return "", nil, false
	}
	cs := byID(Status(t.Context(), h.Env))

	web := cs["web"]
	if !web.Installed || step(web, "running").Status != OK || step(web, "boot").Status != OK {
		t.Fatalf("web: %+v", web)
	}
	if p := step(web, "ports"); p.Status != Todo || strings.Join(p.Items, " ") != "80/tcp 443/tcp" {
		t.Fatalf("web ports: %+v", p)
	}
	mail := cs["mail"]
	if !mail.Installed || mail.Ready {
		t.Fatalf("mail: %+v", mail)
	}
	if s := step(mail, "installed"); s.Status != Todo || !strings.Contains(s.Detail, "opendkim") {
		t.Errorf("mail packages: %+v", s)
	}
	if s := step(mail, "setup"); s.Status != Todo || !strings.Contains(s.Detail, "Set up and start") {
		t.Errorf("mail setup: %+v", s)
	}
	if s := step(mail, "boot"); s.Status != Todo || strings.Join(s.Items, " ") != "postfix.service dovecot.service" {
		t.Errorf("mail boot: %+v", s)
	}
	if strings.Contains(strings.Join(mail.Units, " "), "opendkim") {
		t.Error("OpenDKIM's unit listed before email is set up")
	}
	// Not installed: nothing else applies.
	dns := cs["dns"]
	if dns.Installed || step(dns, "installed").Status != Todo || step(dns, "ports").Status != NA || step(dns, "running").Status != NA {
		t.Errorf("dns: %+v", dns)
	}
	if fw := cs["firewall"]; step(fw, "running").Status != OK {
		t.Errorf("firewall: %+v", fw)
	}
	// Local-only services have no ports to open.
	if s := step(cs["database"], "ports"); s.Status != NA {
		t.Errorf("database ports: %+v", s)
	}
}
