package webreport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func line(ip string, at time.Time, method, target string, status int, agent string) string {
	return fmt.Sprintf(`%s - - [%s] "%s %s HTTP/1.1" %d 512 "-" "%s"`, ip, at.Format("02/Jan/2006:15:04:05 -0700"), method, target, status, agent)
}

func TestParseLine(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	e, ok := ParseLine(line("203.0.113.9", now, "GET", "/wp-login.php?user=a&token=secret", 429, "Mozilla/5.0"))
	if !ok || e.IP != "203.0.113.9" || e.Path != "/wp-login.php" || e.Status != 429 || e.Agent != "Mozilla/5.0" || !e.Time.Equal(now) {
		t.Fatalf("%+v %v", e, ok)
	}
	if _, ok := ParseLine(line("2001:db8::1", now, "POST", "/x", 200, "a")); !ok {
		t.Error("IPv6 line rejected")
	}
	// 444 lines have no browser quoted the same way and an odd length.
	if e, ok := ParseLine(`198.51.100.1 - - [13/Nov/2026:12:00:00 +0000] "GET /a HTTP/1.1" 444 0`); !ok || e.Status != 444 {
		t.Errorf("short line: %+v %v", e, ok)
	}
	for _, bad := range []string{"", "garbage", `x - - [13/Nov/2026:12:00:00 +0000] "GET / HTTP/1.1" 200 1`, `1.2.3.4 - - [bad] "GET / HTTP/1.1" 200 1`} {
		if _, ok := ParseLine(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSummarise(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 30, 0, 0, time.UTC)
	var lines []string
	add := func(ip string, ago time.Duration, target string, status int, agent string) {
		lines = append(lines, line(ip, now.Add(-ago), "GET", target, status, agent))
	}
	for i := 0; i < 5; i++ {
		add("203.0.113.9", time.Duration(i)*time.Minute, "/wp-login.php?x=1", 429, "scanner/1")
	}
	add("203.0.113.9", 10*time.Minute, "/wp-admin/", 403, "scanner/1")
	add("198.51.100.4", 5*time.Minute, "/", 200, "Mozilla")
	add("198.51.100.4", 5*time.Minute, "/a.css", 200, "Mozilla")
	add("198.51.100.5", 70*time.Minute, "/", 301, "Mozilla")
	add("198.51.100.6", 90*time.Minute, "/", 500, "Mozilla")
	add("198.51.100.7", 3*time.Hour, "/", 200, "Mozilla")
	add("198.51.100.8", 26*time.Hour, "/", 200, "old")
	lines = append(lines, "not a log line", "")
	add("192.0.2.1", 2*time.Minute, "/x", 444, "bot")
	r := Summarise(lines, now, 24)
	if r.Requests != 12 || r.Refused != 7 || r.Limited != 5 || r.Forbidden != 1 || r.Closed != 1 {
		t.Errorf("totals: %+v", r)
	}
	if r.Classes[2] != 3 || r.Classes[3] != 1 || r.Classes[4] != 7 || r.Classes[5] != 1 {
		t.Errorf("classes: %v", r.Classes)
	}
	if len(r.RefusedIPs) == 0 || r.RefusedIPs[0].Key != "203.0.113.9" || r.RefusedIPs[0].Refused != 6 || r.IPs[0].Key != "203.0.113.9" {
		t.Errorf("ips: %+v / %+v", r.IPs, r.RefusedIPs)
	}
	if r.RefusedURLs[0].Key != "/wp-login.php" || r.RefusedURLs[0].Refused != 5 {
		t.Errorf("urls: %+v", r.RefusedURLs)
	}
	for _, u := range r.URLs {
		if strings.Contains(u.Key, "?") {
			t.Errorf("a query string was kept: %s", u.Key)
		}
	}
	if len(r.Agents) == 0 || r.Agents[0].Key != "scanner/1" || r.Agents[0].Total != 6 {
		t.Errorf("agents: %+v", r.Agents)
	}
	sum, ref := 0, 0
	for _, b := range r.Timeline {
		sum += b.Requests
		ref += b.Refused
	}
	if sum != r.Requests || ref != r.Refused || len(r.Timeline) != 25 {
		t.Errorf("timeline: %d buckets, %d requests, %d refused", len(r.Timeline), sum, ref)
	}
	// A shorter window.
	if r := Summarise(lines, now, 1); r.Requests != 9 {
		t.Errorf("1 hour: %d", r.Requests)
	}
	if r := Summarise(nil, now, 0); r.Hours != 24 || r.Requests != 0 {
		t.Errorf("empty: %+v", r)
	}
}

func TestAddressesAreBounded(t *testing.T) {
	now := time.Now()
	var lines []string
	for i := 0; i < MaxKeys+500; i++ {
		lines = append(lines, line(fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255), now, "GET", "/", 200, "a"))
	}
	r := Summarise(lines, now.Add(time.Minute), 1)
	if r.Requests != MaxKeys+500 || len(r.Addresses) != MaxKeys {
		t.Errorf("requests %d, addresses %d", r.Requests, len(r.Addresses))
	}
}

func TestActionReadsOnlyTheWebsitesLog(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, err := r.Lookup("webreport.site")
	if err != nil {
		t.Fatal(err)
	}
	if a.Mutating {
		t.Error("a report must not be a mutating action")
	}
	call := func(p string) (any, error) { return a.Handler(t.Context(), []byte(p)) }
	for _, bad := range []string{`{"domain":"../etc"}`, `{"domain":"a b"}`, `{"domain":"shop.test","hours":0.5}`, `{"domain":"shop.test","hours":500}`, `{"domain":"shop.test","hours":-1}`} {
		if _, err := call(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	// No log yet.
	res, err := call(`{"domain":"shop.test"}`)
	if err != nil || res.(Report).Exists {
		t.Fatalf("missing log: %v %v", res, err)
	}
	dir := filepath.Join(h.Env.Cfg.SitesRoot, "shop.test", "logs")
	real, _ := h.Env.Root.Path(dir)
	os.MkdirAll(real, 0o755)
	body := line("203.0.113.9", time.Now().Add(-time.Minute), "GET", "/login", 429, "x") + "\n" + line("198.51.100.1", time.Now(), "GET", "/", 200, "y") + "\n"
	os.WriteFile(filepath.Join(real, "access.log"), []byte(body), 0o644)
	res, err = call(`{"domain":"shop.test","hours":6}`)
	rep := res.(Report)
	if err != nil || !rep.Exists || rep.Requests != 2 || rep.Refused != 1 || rep.Hours != 6 {
		t.Fatalf("%+v %v", rep, err)
	}
}
