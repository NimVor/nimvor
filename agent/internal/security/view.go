package security

import (
	"bufio"
	"bytes"
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// describeUFW fills a rule's port, protocol, source, direction and comment
// from its `ufw show added` arguments (display only: the spec stays the
// rule's identity).
func describeUFW(r *Rule) {
	r.Direction = "in"
	args := r.Args
	if len(args) > 0 && args[0] == "route" {
		r.Direction = "route"
		args = args[1:]
	}
	if len(args) == 0 {
		return
	}
	for i := 1; i < len(args); i++ {
		a := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch a {
		case "in", "out":
			if r.Direction != "route" { // a routed rule names both interfaces
				r.Direction = a
			}
		case "on":
			next()
		case "log", "log-all":
		case "from":
			if v := next(); v != "any" {
				r.From = v
			}
		case "to":
			next()
		case "port", "app":
			r.Port = next()
		case "proto":
			r.Proto = next()
		case "comment":
			r.Comment = next()
		default:
			// Simple syntax: "22/tcp", "80", "OpenSSH".
			if r.Port == "" {
				port, proto, _ := strings.Cut(a, "/")
				r.Port, r.Proto = port, proto
			}
		}
	}
}

var (
	richSourceRe  = regexp.MustCompile(`source address="?([0-9a-fA-F.:/]+)"?`)
	richServiceRe = regexp.MustCompile(`service name="?([a-z0-9_-]+)"?`)
)

// describeFirewalld fills the display fields of a firewalld rule.
func describeFirewalld(r *Rule) {
	r.Direction = "in"
	kind, value, _ := strings.Cut(r.Spec, " ")
	switch kind {
	case "service":
		r.Port = value
	case "port":
		port, proto, _ := strings.Cut(value, "/")
		r.Port, r.Proto = strings.Replace(port, "-", ":", 1), proto
	case "rich":
		if m := richPortRe.FindStringSubmatch(value); m != nil {
			r.Port, r.Proto = m[1], m[2]
			if pm := regexp.MustCompile(`port port="?([0-9]+-[0-9]+)"?`).FindStringSubmatch(value); pm != nil {
				r.Port = strings.Replace(pm[1], "-", ":", 1)
			}
		} else if m := richServiceRe.FindStringSubmatch(value); m != nil {
			r.Port = m[1]
		}
		if m := richSourceRe.FindStringSubmatch(value); m != nil {
			r.From = m[1]
		}
	}
}

// Listener is a port a program listens on.
type Listener struct {
	Proto string `json:"proto"`
	Port  int    `json:"port"`
	Local bool   `json:"local"` // only on loopback addresses
}

// Listening reads the listening TCP and bound UDP ports from /proc/net.
func Listening(env *host.Env) []Listener {
	seen := map[string]*Listener{}
	for _, f := range []struct{ file, proto, state string }{
		{"/proc/net/tcp", "tcp", "0A"}, {"/proc/net/tcp6", "tcp", "0A"},
		{"/proc/net/udp", "udp", "07"}, {"/proc/net/udp6", "udp", "07"},
	} {
		b, err := env.Root.ReadFile(f.file)
		if err != nil {
			continue
		}
		for _, l := range ParseProcNet(b, f.state) {
			key := f.proto + "/" + strconv.Itoa(l.Port)
			if cur, ok := seen[key]; ok {
				cur.Local = cur.Local && l.Local
				continue
			}
			l.Proto = f.proto
			seen[key] = &l
		}
	}
	out := make([]Listener, 0, len(seen))
	for _, l := range seen {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Proto < out[j].Proto
	})
	return out
}

// ParseProcNet returns the local ports of /proc/net/{tcp,udp}[6] entries in
// the given state (0A: TCP listen, 07: UDP unconnected).
func ParseProcNet(b []byte, state string) []Listener {
	var out []Listener
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[3] != state {
			continue
		}
		addr, portHex, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(portHex, 16, 16)
		if err != nil || port == 0 {
			continue
		}
		out = append(out, Listener{Port: int(port), Local: loopbackHex(addr)})
	}
	return out
}

// loopbackHex reports whether a /proc/net address (little-endian words) is
// 127.0.0.0/8 or ::1 (also ::ffff:127.x).
func loopbackHex(a string) bool {
	switch len(a) {
	case 8:
		return strings.HasSuffix(a, "7F")
	case 32:
		return a == "00000000000000000000000001000000" || (a[:16] == "0000000000000000" && a[16:24] == "FFFF0000" && strings.HasSuffix(a, "7F"))
	}
	return false
}

// SSHLogin is one SSH login attempt.
type SSHLogin struct {
	Time   string `json:"time"`
	User   string `json:"user"`
	IP     string `json:"ip"`
	Port   int    `json:"port"`
	Method string `json:"method,omitempty"`
}

// SSHLogins counts SSH logins of the last seven days.
type SSHLogins struct {
	Available   bool      `json:"available"`
	Today       [2]int    `json:"today"`     // accepted, failed
	Yesterday   [2]int    `json:"yesterday"` // accepted, failed
	Week        [2]int    `json:"week"`      // accepted, failed
	LastSuccess *SSHLogin `json:"last_success"`
	LastFailure *SSHLogin `json:"last_failure"`
}

var (
	sshAcceptedRe = regexp.MustCompile(`Accepted (\S+) for (\S+) from (\S+) port (\d+)`)
	sshFailedRe   = regexp.MustCompile(`(?:Failed \S+ for (?:invalid user )?(\S+)|Invalid user (\S*)) from (\S+) port (\d+)`)
)

// sshLogCounter parses `journalctl -o short-unix` lines as they stream.
type sshLogCounter struct {
	now     time.Time
	res     SSHLogins
	partial []byte
}

func (c *sshLogCounter) Write(p []byte) (int, error) {
	c.partial = append(c.partial, p...)
	for {
		i := bytes.IndexByte(c.partial, '\n')
		if i < 0 {
			break
		}
		c.line(string(c.partial[:i]))
		c.partial = c.partial[i+1:]
	}
	if len(c.partial) > 64<<10 { // a line that never ends
		c.partial = c.partial[:0]
	}
	return len(p), nil
}

func (c *sshLogCounter) line(l string) {
	ts, msg, ok := strings.Cut(l, " ")
	if !ok {
		return
	}
	sec, err := strconv.ParseFloat(ts, 64)
	if err != nil {
		return
	}
	t := time.Unix(int64(sec), 0).In(c.now.Location())
	var ev *SSHLogin
	idx := 1 // 0: accepted, 1: failed
	if m := sshAcceptedRe.FindStringSubmatch(msg); m != nil {
		port, _ := strconv.Atoi(m[4])
		ev, idx = &SSHLogin{User: m[2], IP: m[3], Port: port, Method: m[1]}, 0
	} else if m := sshFailedRe.FindStringSubmatch(msg); m != nil {
		port, _ := strconv.Atoi(m[4])
		ev = &SSHLogin{User: m[1] + m[2], IP: m[3], Port: port}
	} else {
		return
	}
	ev.Time = t.UTC().Format(time.RFC3339)
	day := func(d time.Time) time.Time {
		y, m, dd := d.Date()
		return time.Date(y, m, dd, 0, 0, 0, 0, d.Location())
	}
	today := day(c.now)
	switch {
	case !t.Before(today):
		c.res.Today[idx]++
	case !t.Before(today.AddDate(0, 0, -1)):
		c.res.Yesterday[idx]++
	}
	c.res.Week[idx]++
	if idx == 0 {
		c.res.LastSuccess = ev
	} else {
		c.res.LastFailure = ev
	}
}

// Logins reads the SSH server's journal of the last seven days (streamed:
// a server under attack logs a lot).
func Logins(ctx context.Context, env *host.Env, now time.Time) SSHLogins {
	c := &sshLogCounter{now: now}
	if !env.Runner.Available("journalctl") {
		return c.res
	}
	since := now.AddDate(0, 0, -7).UTC().Format("2006-01-02 15:04:05") + " UTC"
	// sshd, and sshd-session (OpenSSH 9.8+) which logs the attempts.
	_, err := env.Runner.RunWith(ctx, "journalctl", execx.Opts{Stdout: c, Timeout: 30 * time.Second},
		"-q", "--no-pager", "-o", "short-unix", "--since", since, "SYSLOG_IDENTIFIER=sshd", "SYSLOG_IDENTIFIER=sshd-session")
	c.res.Available = err == nil
	return c.res
}
