// Package webreport summarises a website's Nginx access log for the traffic
// report: requests over time, status classes, the busiest and the most
// refused addresses and URLs. It only reads, and only the website's own log.
package webreport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

const (
	// MaxBytes is how much of the end of the log is read (and MaxHours the
	// longest window): a bigger log is reported as truncated.
	MaxBytes = 48 << 20
	MaxHours = 168
	// MaxKeys bounds the memory of a report: further addresses and URLs
	// are counted in the totals only.
	MaxKeys = 50000
	TopN    = 20
)

// refused: 403 (forbidden or access list), 429 (traffic limit) and 444
// (connection closed). A 403 can also be a file the web server may not read.
func refused(status int) bool { return status == 403 || status == 429 || status == 444 }

// Entry is one request of the combined log format.
type Entry struct {
	IP     string
	Time   time.Time
	Method string
	Path   string
	Status int
	Agent  string
}

// combined: remote - user [time] "METHOD target PROTO" status bytes "referer" "agent".
var combined = regexp.MustCompile(`^(\S+) \S+ \S+ \[([^\]]+)\] "([A-Z]+) (\S*)[^"]*" (\d{3}) \S+(?: "[^"]*" "([^"]*)")?`)

// ParseLine reads one line (ok false for anything else).
func ParseLine(line string) (Entry, bool) {
	m := combined.FindStringSubmatch(line)
	if m == nil {
		return Entry{}, false
	}
	t, err := time.Parse("02/Jan/2006:15:04:05 -0700", m[2])
	if err != nil {
		return Entry{}, false
	}
	if _, err := netip.ParseAddr(m[1]); err != nil {
		return Entry{}, false
	}
	status, _ := strconv.Atoi(m[5])
	path := m[4]
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i] // query strings can hold tokens
	}
	if len(path) > 200 {
		path = path[:200] + "…"
	}
	agent := m[6]
	if len(agent) > 120 {
		agent = agent[:120] + "…"
	}
	return Entry{IP: m[1], Time: t, Method: m[3], Path: path, Status: status, Agent: agent}, true
}

// Count is a counted key.
type Count struct {
	Key     string `json:"key"`
	Total   int    `json:"total"`
	Refused int    `json:"refused"`
}

// Bucket is one hour.
type Bucket struct {
	Start    int64 `json:"start"`
	Requests int   `json:"requests"`
	Refused  int   `json:"refused"`
}

// Report is the summary.
type Report struct {
	From        int64    `json:"from"`
	To          int64    `json:"to"`
	Hours       int      `json:"hours"`
	Lines       int      `json:"lines"`
	Requests    int      `json:"requests"`
	Refused     int      `json:"refused"`
	Limited     int      `json:"limited"`
	Closed      int      `json:"closed"`
	Forbidden   int      `json:"forbidden"`
	Classes     [6]int   `json:"classes"` // index 2..5 = 2xx..5xx
	Timeline    []Bucket `json:"timeline"`
	IPs         []Count  `json:"ips"`
	RefusedIPs  []Count  `json:"refused_ips"`
	URLs        []Count  `json:"urls"`
	RefusedURLs []Count  `json:"refused_urls"`
	Agents      []Count  `json:"refused_agents"`
	Truncated   bool     `json:"truncated"`
	Exists      bool     `json:"exists"`
	// Addresses is every address seen with its counts (up to MaxKeys), for
	// the panel's country summary.
	Addresses []Count `json:"addresses"`
}

type counter struct {
	m map[string]*Count
}

func newCounter() *counter { return &counter{m: map[string]*Count{}} }

func (c *counter) add(key string, ref bool) {
	e := c.m[key]
	if e == nil {
		if len(c.m) >= MaxKeys {
			return
		}
		e = &Count{Key: key}
		c.m[key] = e
	}
	e.Total++
	if ref {
		e.Refused++
	}
}

func (c *counter) top(n int, refusedOnly bool) []Count {
	out := make([]Count, 0, len(c.m))
	for _, e := range c.m {
		if refusedOnly && e.Refused == 0 {
			continue
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Total, out[j].Total
		if refusedOnly {
			a, b = out[i].Refused, out[j].Refused
		}
		if a != b {
			return a > b
		}
		return out[i].Key < out[j].Key
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// Summarise reads lines (oldest first) and reports the window [now-hours, now].
func Summarise(lines []string, now time.Time, hours int) Report {
	if hours < 1 || hours > MaxHours {
		hours = 24
	}
	from := now.Add(-time.Duration(hours) * time.Hour)
	r := Report{From: from.Unix(), To: now.Unix(), Hours: hours, Exists: true}
	// Hourly buckets, aligned to full hours.
	first := from.Truncate(time.Hour)
	for t := first; !t.After(now); t = t.Add(time.Hour) {
		r.Timeline = append(r.Timeline, Bucket{Start: t.Unix()})
	}
	ips, urls, agents := newCounter(), newCounter(), newCounter()
	for _, line := range lines {
		e, ok := ParseLine(line)
		if !ok {
			continue
		}
		r.Lines++
		if e.Time.Before(from) || e.Time.After(now.Add(time.Minute)) {
			continue
		}
		r.Requests++
		ref := refused(e.Status)
		if c := e.Status / 100; c >= 2 && c <= 5 {
			r.Classes[c]++
		}
		if ref {
			r.Refused++
			switch e.Status {
			case 429:
				r.Limited++
			case 444:
				r.Closed++
			case 403:
				r.Forbidden++
			}
			agents.add(e.Agent, true)
		}
		if i := int(e.Time.Sub(first) / time.Hour); i >= 0 && i < len(r.Timeline) {
			r.Timeline[i].Requests++
			if ref {
				r.Timeline[i].Refused++
			}
		}
		ips.add(e.IP, ref)
		urls.add(e.Path, ref)
	}
	r.IPs, r.RefusedIPs = ips.top(TopN, false), ips.top(TopN, true)
	r.URLs, r.RefusedURLs = urls.top(TopN, false), urls.top(TopN, true)
	r.Agents = agents.top(10, true)
	r.Addresses = ips.top(0, false)
	return r
}

// readTail returns the last MaxBytes of the file as lines (the first, possibly
// partial, line dropped when the file was cut).
func readTail(path string) (lines []string, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	size := info.Size()
	start := int64(0)
	if size > MaxBytes {
		start, truncated = size-MaxBytes, true
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	if truncated {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	return strings.Split(string(buf), "\n"), truncated, nil
}

// Register adds the webreport.* actions.
func Register(r *actions.Registry, e *host.Env) {
	r.Register(actions.Action{Name: "webreport.site", Description: "Summary of a website's access log for a window of up to 7 days: requests over time, status classes, refused requests (403, 429, 444), the busiest and most refused addresses and URLs (without query strings) and the browsers most refused. Reads the website's own access log only.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
			Hours  int    `json:"hours"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("domain", in.Domain); err != nil {
			return nil, host.Invalid(err)
		}
		if in.Hours == 0 {
			in.Hours = 24
		}
		if in.Hours < 1 || in.Hours > MaxHours {
			return nil, actions.InvalidParams("hours: 1 to 168")
		}
		path := filepath.Join(e.Cfg.SitesRoot, in.Domain, "logs", "access.log")
		real, err := e.Root.Path(path)
		if err != nil {
			return nil, err
		}
		lines, truncated, err := readTail(real)
		if err != nil {
			if os.IsNotExist(err) {
				rep := Summarise(nil, time.Now(), in.Hours)
				rep.Exists = false
				return rep, nil
			}
			return nil, actions.Failed("The access log could not be read.", err.Error())
		}
		rep := Summarise(lines, time.Now(), in.Hours)
		rep.Truncated = truncated
		return rep, nil
	}})
}
