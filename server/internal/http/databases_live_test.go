package http

// This suite covers the live db.overview section of the databases page: the
// per-database size + status columns, the server label, the "could not be
// queried" fallback, and the unmanaged-databases section (all-scope only).

import (
	"net/http"
	"strings"
	"testing"
)

func TestDatabasesPageShowsLiveSizes(t *testing.T) {
	h, _, _ := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/databases", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/databases = %d", rr.Code)
	}
	page := rr.Body.String()
	for _, want := range []string{"MariaDB 10.11.6", "10.0 MB", "12 tables"} {
		if !strings.Contains(page, want) {
			t.Errorf("databases page missing %q:\n%s", want, page)
		}
	}
	// bob's a_main is in the overview (OK, not Missing) and the
	// unmanaged section is all-scope only.
	for _, notWant := range []string{"Missing", "Other databases on this server"} {
		if strings.Contains(page, notWant) {
			t.Errorf("databases page should not contain %q", notWant)
		}
	}
}

func TestDatabasesPageShowsMissingAndUnmanaged(t *testing.T) {
	h, _, _ := dbUsersEnv(t, "")
	admin := loginAsUser(t, h, "admin")
	page := get(t, h, "/databases", admin).Body.String()
	for _, want := range []string{
		// c_main is managed but absent from the live overview.
		"Missing",
		// legacy_shop exists on the server but is not under WhaCP management.
		"Other databases on this server", "legacy_shop", "2.0 MB",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("databases page missing %q:\n%s", want, page)
		}
	}
	// a_main / b_main are listed by the server, so they are OK, not Missing.
	if strings.Count(page, "Missing") != 1 {
		t.Errorf("expected exactly one Missing, got %d", strings.Count(page, "Missing"))
	}
}

func TestDatabasesPageServerDown(t *testing.T) {
	h, sa, _ := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	sa.failActions = map[string]string{"db.overview": "the socket is closed"}
	page := get(t, h, "/databases", bob).Body.String()
	for _, want := range []string{
		"The database server could not be queried",
		"Database server unavailable",
		"unknown",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("databases page missing %q:\n%s", want, page)
		}
	}
	// The sizes fall back to the WhaCP records (no live numbers).
	if strings.Contains(page, "10.0 MB") {
		t.Errorf("databases page shows a live size while the server is down")
	}
	if len(sa.called()) == 0 {
		t.Fatal("db.overview was never called")
	}
}

func TestSiteDatabasesShowsSizesNoUnmanaged(t *testing.T) {
	h, _, _ := dbUsersEnv(t, "")
	admin := loginAsUser(t, h, "admin")
	page := get(t, h, "/sites/10/databases", admin).Body.String()
	for _, want := range []string{"MariaDB 10.11.6", "10.0 MB", "12 tables"} {
		if !strings.Contains(page, want) {
			t.Errorf("site databases page missing %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "Other databases on this server") {
		t.Errorf("site databases page must not list unmanaged databases")
	}
}

func TestDatabasesPageUnmanagedOnlyForAllScope(t *testing.T) {
	h, _, _ := dbUsersEnv(t, "")
	alice := loginAsUser(t, h, "alice")
	page := get(t, h, "/databases", alice).Body.String()
	for _, notWant := range []string{"Other databases on this server", "legacy_shop"} {
		if strings.Contains(page, notWant) {
			t.Errorf("non-admin page should not contain %q", notWant)
		}
	}
}
