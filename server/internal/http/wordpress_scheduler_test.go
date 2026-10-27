package http

import (
	"context"
	"net/http"
	"strings"
	"testing"

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
