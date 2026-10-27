package http

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
)

// wpSchedSeed adds a PHP website with a WordPress installation under
// vulnerability protection.
func wpSchedSeed(t *testing.T, s *Server) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO websites (id, server_id, owner_id, domain, system_user, root, document_root, kind, php_version, status, enabled, created_at, updated_at) VALUES (970, 1, 2, 'wp.example', 'wpuser', '/h/wp', '/h/wp/public', 'php', '8.3', 'active', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO wordpress_installs (id, server_id, website_id, path, version, latest, scanned_at, vuln_protection) VALUES (970, 1, 970, '', '6.7.1', '6.8', '2026-10-20T09:30:00Z', 1)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestWordPressSchedulerVulnerabilityCheck(t *testing.T) {
	s, db, sa := rolesEnvServerAgent(t, true)
	wpSchedSeed(t, s)
	sa.setReply("wordpress.info", defaultWpInfoReply)
	old := hosting.WpVulnFetch
	hosting.WpVulnFetch = func(rawurl string) (string, bool) {
		if strings.HasSuffix(rawurl, "core/6.7.1/") {
			return `{"data":{"vulnerability":[{"name":"SQL Injection","operator":{"min_operator":"ge","min_version":"6.0","max_operator":"lt","max_version":"6.7.2"},"impact":{"cvss":{"severity":"h","score":"8.1"}},"source":[{"id":"CVE-2026-1234","link":"https://www.wpvulnerability.net/vulnerabilities/1234"}]}]}}`, true
		}
		return `{"data":{"vulnerability":[]}}`, true
	}
	t.Cleanup(func() { hosting.WpVulnFetch = old })

	r := schedulerRequest(t)
	out := s.wordpressScheduler(r).DailyVulnerabilityCheck(context.Background())
	if len(out) != 1 || !strings.Contains(out[0], "wp.example: 1 known vulnerability") {
		t.Fatalf("lines = %q", out)
	}
	var n int
	var owner int64
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(owner_id),0) FROM notifications WHERE kind = 'wordpress'`).Scan(&n, &owner); err != nil {
		t.Fatal(err)
	}
	if n != 1 || owner != 2 {
		t.Errorf("notifications = %d for owner %d, want 1 for the website's owner", n, owner)
	}

	// Checked just now: nothing is due, and the same finding is not
	// notified twice.
	if out := s.wordpressScheduler(r).DailyVulnerabilityCheck(context.Background()); len(out) != 0 {
		t.Errorf("second pass = %q", out)
	}
	if _, err := db.Exec(`UPDATE wordpress_installs SET vulns_checked_at = '2020-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	s.wordpressScheduler(r).DailyVulnerabilityCheck(context.Background())
	if err := db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE kind = 'wordpress'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("notifications after a repeat = %d, want 1", n)
	}
}

func TestWordPressSchedulerAutoHarden(t *testing.T) {
	s, db, sa := rolesEnvServerAgent(t, true)
	wpSchedSeed(t, s)
	sa.setReply("wordpress.security", `{"measures":[],"problems":["config not writable"]}`)
	for _, q := range []string{
		`DELETE FROM wordpress_installs`,
		`INSERT INTO agent_jobs (server_id, job_id, kind, status, created_at) VALUES (1, 'j-ok', 'apps.install', 'succeeded', ''), (1, 'j-bad', 'apps.install', 'failed', ''), (1, 'j-run', 'apps.install', 'running', '')`,
		`INSERT INTO app_installs (website_id, app, path, job_id, hardened, created_at) VALUES (970, 'wordpress', 'blog', 'j-ok', 0, ''), (970, 'wordpress', 'old', 'j-bad', 0, ''), (970, 'wordpress', 'new', 'j-run', 0, '')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	r := schedulerRequest(t)

	// Switched off: every settled row is marked, nothing is applied.
	if out := s.wordpressScheduler(r).AutoHarden(context.Background(), false); len(out) != 0 {
		t.Fatalf("disabled lines = %q", out)
	}
	if containsAction(sa.called(), "wordpress.security") {
		t.Error("the measures were applied with the setting off")
	}
	var hardened int
	if err := db.QueryRow(`SELECT COUNT(*) FROM app_installs WHERE hardened = 1`).Scan(&hardened); err != nil || hardened != 2 {
		t.Fatalf("hardened rows = %d, %v; want the two settled ones", hardened, err)
	}

	// On: only a succeeded job is hardened, once the row is unmarked.
	if _, err := db.Exec(`UPDATE app_installs SET hardened = 0`); err != nil {
		t.Fatal(err)
	}
	out := s.wordpressScheduler(r).AutoHarden(context.Background(), true)
	if len(out) != 1 || !strings.Contains(out[0], "wp.example/blog: hardened (config not writable)") {
		t.Fatalf("lines = %q", out)
	}
	var installs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM wordpress_installs WHERE website_id = 970 AND path = 'blog'`).Scan(&installs); err != nil || installs != 1 {
		t.Errorf("installation rows = %d, %v", installs, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM app_installs WHERE hardened = 0`).Scan(&hardened); err != nil || hardened != 1 {
		t.Errorf("rows still waiting = %d, %v; want the running one", hardened, err)
	}
}

// schedulerRequest is the request the scheduler passes run with.
func schedulerRequest(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSchedulerSettlesJobsAndNotifies(t *testing.T) {
	s, db, sa := rolesEnvServerAgent(t, true)
	wpSchedSeed(t, s)
	for _, q := range []string{
		`INSERT INTO agent_jobs (server_id, job_id, kind, target_type, target_id, user_id, created_at) VALUES
		 (1, 'aaaaaaaaaaaaaaa1', 'Backup wp.example', 'website', '970', NULL, '2099-01-01T00:00:00Z'),
		 (1, 'aaaaaaaaaaaaaaa2', 'Cron: nightly', 'website', '970', NULL, '2099-01-01T00:00:00Z'),
		 (1, 'aaaaaaaaaaaaaaa3', 'Install x', 'server', '1', NULL, '2099-01-01T00:00:00Z'),
		 (1, 'aaaaaaaaaaaaaaa4', 'Lost', 'server', '1', NULL, '2099-01-01T00:00:00Z'),
		 (1, 'aaaaaaaaaaaaaaa5', 'Still running', 'server', '1', NULL, '2099-01-01T00:00:00Z')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	sa.jobs["aaaaaaaaaaaaaaa1"] = `{"success":true,"data":{"status":"succeeded","error":""}}`
	sa.jobs["aaaaaaaaaaaaaaa2"] = `{"success":true,"data":{"status":"succeeded","error":""}}`
	sa.jobs["aaaaaaaaaaaaaaa3"] = `{"success":true,"data":{"status":"failed","error":"disk full"}}`
	sa.jobs["aaaaaaaaaaaaaaa5"] = `{"success":true,"data":{"status":"running","error":""}}`

	var lines []string
	for _, p := range schedulerPasses {
		if p.name == "jobs" {
			lines = p.run(context.Background(), s, schedulerRequest(t))
		}
	}
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	rows := map[string]string{}
	rs, _ := db.Query(`SELECT job_id, status || '/' || COALESCE(settled_at,'-') FROM agent_jobs`)
	for rs.Next() {
		var id, v string
		_ = rs.Scan(&id, &v)
		rows[id] = v
	}
	rs.Close()
	if !strings.HasPrefix(rows["aaaaaaaaaaaaaaa1"], "succeeded/20") || !strings.HasPrefix(rows["aaaaaaaaaaaaaaa4"], "unknown/20") || !strings.HasSuffix(rows["aaaaaaaaaaaaaaa5"], "/-") {
		t.Errorf("job rows = %v", rows)
	}
	var titles string
	_ = db.QueryRow(`SELECT group_concat(title, '|') FROM (SELECT title FROM notifications WHERE kind = 'job' ORDER BY title)`).Scan(&titles)
	// The quick cron job's success is quiet; the failure carries the error.
	if titles != "Backup wp.example finished|Install x failed" {
		t.Errorf("notifications = %q", titles)
	}
}

func TestSchedulerMailLimitAlerts(t *testing.T) {
	s, db, sa := rolesEnvServerAgent(t, true)
	wpSchedSeed(t, s)
	sa.rateData = `{"config":{"enabled":true,"per_hour":10,"accounts":{}},"accounts":["wpuser"],"service":{"active":true,"postfix":true},
		"usage":{"hour":1,"accounts":[],"events":[{"uid":1001,"name":"wpuser","time":` + strconv.FormatInt(time.Now().Unix()-60, 10) + `,"limit":10},{"uid":1002,"name":"old","time":1000,"limit":10}]}}`
	run := func() []string {
		for _, p := range schedulerPasses {
			if p.name == "mail limits" {
				return p.run(context.Background(), s, schedulerRequest(t))
			}
		}
		return nil
	}
	if out := run(); len(out) != 1 || out[0] != "Mail sending limit reached: wpuser" {
		t.Fatalf("lines = %q", out)
	}
	if out := run(); len(out) != 0 {
		t.Errorf("repeat = %q", out)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE kind = 'mail.limit'`).Scan(&n)
	if n != 2 {
		t.Errorf("notifications = %d, want the administrators' and the owner's", n)
	}
}
