package http

// Tests for the server-wide backups area (backups_handlers.go): the admin
// index page (admin sees all tabs; customer sees only own website backups),
// the storage-confirmation page rendering, and CSRF guards on mutating
// routes. Full agent-driven flows (key download, Google OAuth, server
// backup start/restore/delete) are exercised via integration with a fake
// agent — see the parallel site_backups_test.go for the pattern. These
// tests only cover the render and permission layer.

import (
	"net/http"
	"strings"
	"testing"
)

func TestBackupsIndexAdmin(t *testing.T) {
	h, _, _ := backupWEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/backups", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /backups (admin) = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Server configuration backups",
		"Remote storage",
		"Encryption key",
		"Website backups",
		"Back up now",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("admin page missing %q", want)
		}
	}
}

func TestBackupsIndexCustomer(t *testing.T) {
	h, _, _ := backupWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/backups", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /backups (bob) = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	// Customers never see the server-admin tabs.
	for _, mustNot := range []string{
		"Server configuration backups",
		"Remote storage",
		"Encryption key",
		"Account files",
	} {
		if strings.Contains(body, mustNot) {
			t.Errorf("customer page shows %q", mustNot)
		}
	}
	if !strings.Contains(body, "Website backups") {
		t.Error("customer page missing website backups section")
	}
}

func TestBackupsCreateServerCSRF(t *testing.T) {
	h, _, _ := backupWEnv(t)
	admin := loginAsUser(t, h, "admin")
	// POST without CSRF token -> 400.
	rr := postForm(h, "/backups/server", map[string][]string{}, admin)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("POST /backups/server (no csrf) = %d, want 400", rr.Code)
	}
}

func TestBackupsCreateServerForbiddenForCustomer(t *testing.T) {
	h, _, _ := backupWEnv(t)
	bob := loginAsUser(t, h, "bob")
	// With a CSRF token but no backups.manage server-wide (bob is a customer,
	// so the admin-only redirect lands him on /backups#server — but the guard
	// in backupAdminAct redirects back to /backups because only admins can
	// manage server backups; the POST form itself is not shown on his page.
	token, bob := siteFormToken(t, h, bob, "/backups")
	rr := postForm(h, "/backups/server", map[string][]string{"_token": {token}}, bob)
	// Either 403 via middleware guard or 303 back with flash from admin-only.
	if rr.Code != http.StatusSeeOther && rr.Code != http.StatusForbidden {
		t.Fatalf("POST /backups/server (customer) = %d", rr.Code)
	}
}

func TestBackupsConfirmRequiresPending(t *testing.T) {
	h, _, _ := backupWEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/backups/storages/confirm", admin)
	// Without a sealed pending storage -> redirect back to storages.
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("GET confirm (no pending) = %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.Contains(loc, "/backups") {
		t.Errorf("redirect = %q", loc)
	}
}
