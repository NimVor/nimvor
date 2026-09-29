// Package hulk is the agent side of brute force protection (Security →
// cPHulk Brute Force Protection): it reads failed logins from the logs of
// the login services and applies the panel's blocks with nftables. The
// panel decides what to block; the agent only reports and enforces.
package hulk

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Event is one login attempt read from a log.
type Event struct {
	ID          string `json:"id"`
	Time        int64  `json:"time"`
	Service     string `json:"service"`     // system, mail, ftp, webmail
	AuthService string `json:"authservice"` // sshd, dovecot, postfix, pure-ftpd, webmaild
	User        string `json:"user"`
	IP          string `json:"ip"`
	OK          bool   `json:"ok"` // a successful login (root logins, for notifications)
}

// MaxEvents bounds one answer; the panel asks again from the last time.
const MaxEvents = 20000

var (
	sshAccepted = regexp.MustCompile(`Accepted (\S+) for (\S+) from (\S+) port \d+`)
	sshFailed   = regexp.MustCompile(`(?:Failed \S+ for (?:invalid user )?(\S+)|Invalid user (\S*)) from (\S+) port \d+`)
	// Dovecot 2.3 "Aborted login (auth failed, …)" / "Disconnected (auth failed, …)",
	// 2.4 "Login aborted: … (auth_failed)".
	dovecotFailed = regexp.MustCompile(`^(imap|pop3|submission|managesieve)-login: .*auth[ _]failed.*?user=<([^>]*)>.*?rip=([0-9A-Fa-f.:]+)`)
	postfixFailed = regexp.MustCompile(`warning: [^\[\s]*\[([0-9A-Fa-f.:]+)\]: SASL \S+ authentication failed`)
	ftpFailed     = regexp.MustCompile(`\(\?@([0-9A-Fa-f.:]+)\) \[WARNING\] Authentication failed for user \[([^\]]*)\]`)
	// Roundcube: [29-Sep-2026 17:39:56 +0000]: <sess> IMAP Error: Login failed for bob@example.com against localhost from 203.0.113.5.
	webmailFailed = regexp.MustCompile(`^\[([^\]]+)\]: .*Login failed for (\S*) (?:against \S+ )?from ([0-9A-Fa-f.:]+)`)
)

// normIP checks and normalizes an address. Loopback attempts are kept
// (username protection for local addresses), except Dovecot's: those are
// webmail's, which Roundcube's log reports with the visitor's address.
func normIP(s, authservice string) (string, bool) {
	a, err := netip.ParseAddr(strings.TrimSuffix(s, "."))
	if err != nil || a.IsUnspecified() || (a.IsLoopback() && strings.HasPrefix(authservice, "dovecot")) {
		return "", false
	}
	return a.Unmap().String(), true
}

func eventID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:12])
}

// parseJournal turns `journalctl -o short-unix` lines of the login services
// into events.
func parseJournal(out []byte, since int64) []Event {
	var evs []Event
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() && len(evs) < MaxEvents {
		line := sc.Text()
		ts, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		sec, err := strconv.ParseFloat(ts, 64)
		if err != nil || int64(sec) < since {
			continue
		}
		// "<host> <identifier>[pid]: <message>"
		_, rest, _ = strings.Cut(rest, " ")
		ident, msg, ok := strings.Cut(rest, ": ")
		if !ok {
			continue
		}
		if i := strings.IndexByte(ident, '['); i >= 0 {
			ident = ident[:i]
		}
		ev := Event{Time: int64(sec)}
		switch {
		case ident == "sshd" || ident == "sshd-session":
			if m := sshAccepted.FindStringSubmatch(msg); m != nil {
				ev.Service, ev.AuthService, ev.User, ev.IP, ev.OK = "system", "sshd", m[2], m[3], true
			} else if m := sshFailed.FindStringSubmatch(msg); m != nil {
				ev.Service, ev.AuthService, ev.User, ev.IP = "system", "sshd", m[1]+m[2], m[3]
			}
		case ident == "dovecot":
			if m := dovecotFailed.FindStringSubmatch(msg); m != nil {
				ev.Service, ev.AuthService, ev.User, ev.IP = "mail", "dovecot ("+m[1]+")", m[2], m[3]
			}
		case strings.HasPrefix(ident, "postfix/"):
			if m := postfixFailed.FindStringSubmatch(msg); m != nil {
				ev.Service, ev.AuthService, ev.IP = "mail", "postfix (SMTP AUTH)", m[1]
			}
		case ident == "pure-ftpd":
			if m := ftpFailed.FindStringSubmatch(msg); m != nil {
				ev.Service, ev.AuthService, ev.User, ev.IP = "ftp", "pure-ftpd", m[2], m[1]
			}
		}
		if ev.Service == "" {
			continue
		}
		ip, ok := normIP(ev.IP, ev.AuthService)
		if !ok {
			continue
		}
		ev.IP = ip
		ev.ID = eventID(line)
		evs = append(evs, ev)
	}
	return evs
}

// parseWebmail reads Roundcube's error log.
func parseWebmail(b []byte, since int64) []Event {
	var evs []Event
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() && len(evs) < MaxEvents {
		m := webmailFailed.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		t, err := time.Parse("02-Jan-2006 15:04:05 -0700", m[1])
		if err != nil || t.Unix() < since {
			continue
		}
		ip, ok := normIP(m[3], "webmaild")
		if !ok {
			continue
		}
		evs = append(evs, Event{ID: eventID("webmail", sc.Text()), Time: t.Unix(), Service: "webmail", AuthService: "webmaild", User: m[2], IP: ip})
	}
	return evs
}

// Failures reads the login attempts since a Unix time (at most a day back).
func Failures(ctx context.Context, env *host.Env, since int64, now time.Time) ([]Event, bool) {
	if min := now.Add(-24 * time.Hour).Unix(); since < min {
		since = min
	}
	var evs []Event
	available := false
	if env.Runner.Available("journalctl") {
		out := &capped{max: 64 << 20}
		_, err := env.Runner.RunWith(ctx, "journalctl", execx.Opts{Timeout: 60 * time.Second, Stdout: out},
			"-q", "--no-pager", "-o", "short-unix", "--since", time.Unix(since, 0).UTC().Format("2006-01-02 15:04:05")+" UTC",
			"SYSLOG_IDENTIFIER=sshd", "SYSLOG_IDENTIFIER=sshd-session", "SYSLOG_IDENTIFIER=dovecot",
			"SYSLOG_IDENTIFIER=postfix/smtpd", "SYSLOG_IDENTIFIER=postfix/submission/smtpd", "SYSLOG_IDENTIFIER=postfix/smtps/smtpd",
			"SYSLOG_IDENTIFIER=pure-ftpd")
		if err == nil {
			available = true
			evs = append(evs, parseJournal(out.buf.Bytes(), since)...)
		}
	}
	if b, err := tail(env, env.M.WebmailData+"/logs/errors.log", 4<<20); err == nil {
		evs = append(evs, parseWebmail(b, since)...)
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].Time < evs[j].Time })
	if len(evs) > MaxEvents {
		evs = evs[:MaxEvents]
	}
	return evs, available
}

// tail returns the last n bytes of a file (whole lines). The file is in a
// folder the webmail account writes: a symbolic link is not followed.
func tail(env *host.Env, path string, n int64) ([]byte, error) {
	real, err := env.Root.Path(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(real, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	off := st.Size() - n
	if off < 0 {
		off = 0
	}
	b := make([]byte, st.Size()-off)
	k, _ := f.ReadAt(b, off)
	b = b[:k]
	if off > 0 {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return b, nil
}

// capped keeps the first max bytes written to it.
type capped struct {
	buf bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}
