package security

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestDescribeRules(t *testing.T) {
	cases := map[string]Rule{
		"allow 22/tcp":                      {Port: "22", Proto: "tcp", Direction: "in"},
		"limit 22/tcp comment 'SSH access'": {Port: "22", Proto: "tcp", Direction: "in", Comment: "SSH access"},
		"allow 80":                          {Port: "80", Direction: "in"},
		"allow OpenSSH":                     {Port: "OpenSSH", Direction: "in"},
		"allow from 203.0.113.4 to any port 3306 proto tcp": {Port: "3306", Proto: "tcp", From: "203.0.113.4", Direction: "in"},
		"deny from 198.51.100.0/24":                         {From: "198.51.100.0/24", Direction: "in"},
		"deny out 25":                                       {Port: "25", Direction: "out"},
		"allow in on eth0 to any port 8000:8100 proto udp":  {Port: "8000:8100", Proto: "udp", Direction: "in"},
		"allow proto tcp from any to any port 80,443":       {Port: "80,443", Proto: "tcp", Direction: "in"},
		"route allow in on eth1 out on eth0 to any port 53": {Port: "53", Direction: "route"},
	}
	for spec, want := range cases {
		rules := ParseAdded("ufw "+spec+"\n", 22)
		if len(rules) != 1 {
			t.Fatalf("%s: %v", spec, rules)
		}
		r := rules[0]
		if r.Port != want.Port || r.Proto != want.Proto || r.From != want.From || r.Direction != want.Direction || r.Comment != want.Comment {
			t.Errorf("%s: port %q proto %q from %q dir %q comment %q", spec, r.Port, r.Proto, r.From, r.Direction, r.Comment)
		}
	}
	fw := map[string]Rule{
		"service http":       {Port: "http"},
		"port 8000-8100/udp": {Port: "8000:8100", Proto: "udp"},
		`rich rule family="ipv4" source address="203.0.113.4" port port="3306" protocol="tcp" accept`: {Port: "3306", Proto: "tcp", From: "203.0.113.4"},
		`rich rule family="ipv4" source address="198.51.100.0/24" drop`:                               {From: "198.51.100.0/24"},
	}
	for spec, want := range fw {
		r := Rule{Spec: spec}
		describeFirewalld(&r)
		if r.Port != want.Port || r.Proto != want.Proto || r.From != want.From || r.Direction != "in" {
			t.Errorf("%s: port %q proto %q from %q", spec, r.Port, r.Proto, r.From)
		}
	}
}

func TestListeningPorts(t *testing.T) {
	tcp := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1 0000000000000000 100 0 0 10 0
   1: 0100007F:0CEA 00000000:0000 0A 00000000:00000000 00:00000000 00000000   110        0 2 1 0000000000000000 100 0 0 10 0
   2: 0100007F:0CEA 0100007F:A1B2 01 00000000:00000000 00:00000000 00000000   110        0 3 1 0000000000000000 100 0 0 10 0
`
	got := ParseProcNet([]byte(tcp), "0A")
	if len(got) != 2 || got[0].Port != 22 || got[0].Local || got[1].Port != 3306 || !got[1].Local {
		t.Fatalf("tcp: %+v", got)
	}
	tcp6 := "header\n   0: 00000000000000000000000001000000:1F90 00000000000000000000000000000000:0000 0A x\n   1: 00000000000000000000000000000000:0050 00000000000000000000000000000000:0000 0A x\n"
	got = ParseProcNet([]byte(tcp6), "0A")
	if len(got) != 2 || got[0].Port != 8080 || !got[0].Local || got[1].Port != 80 || got[1].Local {
		t.Fatalf("tcp6: %+v", got)
	}

	h := testhost.New(t)
	os.MkdirAll(filepath.Join(h.Base, "proc/net"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "proc/net/tcp"), []byte(tcp), 0o644)
	os.WriteFile(filepath.Join(h.Base, "proc/net/tcp6"), []byte("header\n   0: 00000000000000000000000000000000:0CEA 00000000000000000000000000000000:0000 0A x\n"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "proc/net/udp"), []byte("header\n   0: 00000000:0035 00000000:0000 07 x\n"), 0o644)
	l := Listening(h.Env)
	// 3306 listens on loopback (IPv4) and everywhere (IPv6): reachable.
	want := []Listener{{"udp", 53, false}, {"tcp", 22, false}, {"tcp", 3306, false}}
	if len(l) != 3 {
		t.Fatalf("listening: %+v", l)
	}
	for _, w := range want {
		found := false
		for _, x := range l {
			found = found || x == w
		}
		if !found {
			t.Errorf("missing %+v in %+v", w, l)
		}
	}
}

func TestSSHLogins(t *testing.T) {
	loc := time.FixedZone("server", 2*3600)
	now := time.Date(2026, 9, 27, 14, 0, 0, 0, loc)
	ts := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).Unix(), 10) + ".000000" }
	journal := strings.Join([]string{
		ts(-30*time.Hour) + " host sshd[2]: Failed password for root from 198.51.100.7 port 50111 ssh2",
		ts(-20*time.Hour) + " host sshd[3]: Accepted publickey for admin from 203.0.113.9 port 51000 ssh2: ED25519 SHA256:x",
		ts(-2*time.Hour) + " host sshd-session[4]: Invalid user oracle from 198.51.100.8 port 40000",
		ts(-1*time.Hour) + " host sshd-session[5]: Failed password for invalid user test from 198.51.100.9 port 40001 ssh2",
		ts(-30*time.Minute) + " host sshd-session[6]: Accepted publickey for root from 203.0.113.10 port 52000 ssh2",
		ts(-10*time.Minute) + " host sshd[7]: Connection closed by 203.0.113.10 port 52000",
		"garbage",
	}, "\n") + "\n"
	h := testhost.New(t)
	h.Runner.Handler = func(id string, _ execx.Opts, args []string) (string, error) { return journal, nil }
	res := Logins(context.Background(), h.Env, now)
	if !res.Available || res.Today != [2]int{1, 2} || res.Yesterday != [2]int{1, 1} || res.Week != [2]int{2, 3} {
		t.Fatalf("counts: %+v", res)
	}
	if res.LastSuccess == nil || res.LastSuccess.User != "root" || res.LastSuccess.IP != "203.0.113.10" || res.LastSuccess.Port != 52000 || res.LastSuccess.Method != "publickey" {
		t.Errorf("last success: %+v", res.LastSuccess)
	}
	if res.LastFailure == nil || res.LastFailure.User != "test" || res.LastFailure.IP != "198.51.100.9" {
		t.Errorf("last failure: %+v", res.LastFailure)
	}
	call := h.Runner.Calls[len(h.Runner.Calls)-1]
	if !strings.Contains(call, "SYSLOG_IDENTIFIER=sshd SYSLOG_IDENTIFIER=sshd-session") || !strings.Contains(call, "--since 2026-09-20 12:00:00 UTC") {
		t.Errorf("journalctl call: %s", call)
	}
}
