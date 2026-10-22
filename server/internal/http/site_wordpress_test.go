package http

// This suite drives the WordPress toolkit's list and scan actions
// (WordPressController index / scanAll / siteList / scanSite): the
// all-installations list (the filters, the visibility split), the
// per-website list (the single-installation redirect), and the scans
// (the agent's reply stored per website, the stale rows removed, the
// not-scanned websites reported). The agent is the recording fake
// (the wordpress.scan fixture in startSitesAgent).

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// wpSeedInstall seeds one wordpress_installs row.
func wpSeedInstall(t *testing.T, db *sql.DB, websiteID int64, path, version, latest string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO wordpress_installs
		(server_id, website_id, path, version, latest, plugin_updates, theme_updates, issues, scanned_at)
		VALUES (1, ?, ?, ?, ?, 1, 0, 0, '2026-10-20T09:30:00Z')`, websiteID, path, version, latest)
	if err != nil {
		t.Fatal(err)
	}
}

func TestWordPressIndex(t *testing.T) {
	h, _, db := mongoWEnv(t)
	// bob's website (10) has one outdated installation; carol's (20) one
	// that is current.
	wpSeedInstall(t, db, 10, "", "6.7.1", "6.8")
	wpSeedInstall(t, db, 20, "blog", "6.8", "")
	// A second, current installation on bob's website.
	wpSeedInstall(t, db, 10, "docs", "6.8", "")

	// The admin sees every installation the websites may hold.
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/wordpress", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/wordpress = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"bob.example", "carol.example",
		"6.7.1", "6.8 available", // the outdated one is flagged
		"All <span class=\"badge rounded-pill text-bg-light border fw-normal\">3</span>",
		"href=\"/sites/10/wordpress/",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("index missing %q", want)
		}
	}

	// A customer only sees their own installations.
	bob := loginAsUser(t, h, "bob")
	rr = get(t, h, "/wordpress", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("bob's index = %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "carol.example") {
		t.Error("bob sees a foreign installation")
	}
	// The viewer has no websites.manage: no scan form.
	carol := loginAsUser(t, h, "carol")
	rr = get(t, h, "/wordpress", carol)
	if rr.Code != http.StatusOK {
		t.Fatalf("carol's index = %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "Scan all websites") {
		t.Error("carol (no websites.manage) sees the scan form")
	}

	// The outdated filter.
	rr = get(t, h, "/wordpress?filter=outdated", admin)
	page = rr.Body.String()
	if !strings.Contains(page, "bob.example") || strings.Contains(page, "carol.example") {
		t.Errorf("outdated filter wrong: %s", page)
	}
	// The issues filter (none seeded) shows nothing in this list.
	rr = get(t, h, "/wordpress?filter=issues", admin)
	if !strings.Contains(rr.Body.String(), "Nothing in this list") {
		t.Error("issues filter missing its empty state")
	}
}

func TestWordPressSiteList(t *testing.T) {
	h, _, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	// A single installation is opened directly.
	rr := get(t, h, "/sites/10/wordpress", bob)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/wordpress/1" {
		t.Errorf("single install = %d %q, want 303 /sites/10/wordpress/1", rr.Code, rr.Header().Get("Location"))
	}
	// ?list=1 keeps the list.
	rr = get(t, h, "/sites/10/wordpress?list=1", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("list = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "/sites/10/wordpress/1\">Manage") {
		t.Error("list missing the install row")
	}
	// The viewer gets a 403.
	rr = get(t, h, "/sites/10/wordpress", loginAsUser(t, h, "carol"))
	if rr.Code != http.StatusForbidden {
		t.Errorf("carol's site list = %d, want 403", rr.Code)
	}
}

func TestWordPressScanAll(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	admin := loginAsUser(t, h, "admin")

	token, admin := siteFormToken(t, h, admin, "/wordpress")
	rr := postForm(h, "/wordpress/scan", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/wordpress" {
		t.Fatalf("scan = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, admin, "/wordpress", "2 website folder(s) scanned: 1 WordPress installation(s) found.")

	// The found installation is stored under the website whose root holds
	// it (bob.example's root; the document root does not hold it).
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM wordpress_installs WHERE website_id = 10 AND path = ''`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("install rows = %d", n)
	}
	if n := mongoAuditCount(t, db, "wordpress.scan"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	// The agent got both visible websites.
	if got := sa.param(0, "sites"); len(got.([]any)) != 2 {
		t.Errorf("agent got %v sites", got)
	}

	// A customer scans only their own website.
	bob := loginAsUser(t, h, "bob")
	token, bob = siteFormToken(t, h, bob, "/wordpress")
	rr = postForm(h, "/wordpress/scan", url.Values{"_token": {token}}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("bob's scan = %d", rr.Code)
	}
	last := len(sa.called()) - 1
	if sites, _ := sa.param(last, "sites").([]any); len(sites) != 1 {
		t.Errorf("bob's scan sent %d websites", len(sites))
	}
}

func TestWordPressScanSite(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")

	// A stale row on the website is removed by the scan.
	wpSeedInstall(t, db, 10, "old_wp", "6.0", "")

	token, bob := siteFormToken(t, h, bob, "/sites/10/wordpress?list=1")
	rr := postForm(h, "/sites/10/wordpress/scan", url.Values{"_token": {token}}, bob)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/wordpress" {
		t.Fatalf("scan = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, bob, "/sites/10/wordpress?list=1", "1 website folder(s) scanned: 1 WordPress installation(s) found.")

	// The agent got only the scanned website (not carol's).
	last := len(sa.called()) - 1
	if sites, _ := sa.param(last, "sites").([]any); len(sites) != 1 {
		t.Errorf("site scan sent %d websites", len(sites))
	}
	var stale int
	if err := db.QueryRow(`SELECT COUNT(*) FROM wordpress_installs WHERE website_id = 10 AND path = 'old_wp'`).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Error("the stale install row survived the scan")
	}

	// A website whose scan failed is reported, and its rows are kept.
	h2, sa2, db2 := mongoWEnv(t)
	wpSeedInstall(t, db2, 10, "keep", "6.1", "")
	sa2.wpScanReply = `{"installs":[],"errors":[{"domain":"bob.example","error":"the folder could not be read"}],"latest":""}`
	bob2 := loginAsUser(t, h2, "bob")
	token, bob2 = siteFormToken(t, h2, bob2, "/sites/10/wordpress?list=1")
	rr = postForm(h2, "/sites/10/wordpress/scan", url.Values{"_token": {token}}, bob2)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/wordpress?list=1" {
		t.Fatalf("failed scan = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h2, bob2, "/sites/10/wordpress?list=1", "Not scanned: bob.example: the folder could not be read")
	var kept int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM wordpress_installs WHERE website_id = 10 AND path = 'keep'`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != 1 {
		t.Error("a failed scan removed the website's installs")
	}
}

func TestWordPressScanErrors(t *testing.T) {
	h, sa, _ := mongoWEnv(t)
	sa.failActions["wordpress.scan"] = "The websites could not be scanned."
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/wordpress")
	rr := postForm(h, "/wordpress/scan", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/wordpress" {
		t.Fatalf("scan = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, admin, "/wordpress", "The websites could not be scanned.")
}
