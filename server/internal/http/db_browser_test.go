package http

// This suite drives the database browser: the page shell (per-database and
// the all-scope server variant), the operation endpoint (op dispatch,
// agent parameters, audit for changing operations), and the refusals
// (foreign database, non-all-scope server browser, bad name, unknown op,
// missing CSRF).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postJSON is a JSON client (Content-Type: application/json + the
// X-CSRF-Token header, as whacp.js sends it).
func postJSON(t *testing.T, h http.Handler, path string, body any, cookie *http.Cookie, token string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-CSRF-Token", token)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestDbBrowserPage(t *testing.T) {
	h, _, _ := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/databases/100/browse", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/databases/100/browse = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		`data-api="/databases/100/browse/api"`,
		`data-database="a_main"`,
		`href="/sites/10/databases"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("browser page missing %q:\n%s", want, page)
		}
	}
}

func TestDbBrowserPageForeignIs404(t *testing.T) {
	h, _, _ := dbUsersEnv(t, "")
	alice := loginAsUser(t, h, "alice")
	if rr := get(t, h, "/databases/100/browse", alice); rr.Code != http.StatusNotFound {
		t.Fatalf("foreign browser page = %d, want 404", rr.Code)
	}
}

func TestDbBrowserServerPage(t *testing.T) {
	h, _, _ := dbUsersEnv(t, "")
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/databases/browse?name=legacy_shop", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("server browser page = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		`data-database="legacy_shop"`,
		`data-api="/databases/browse/api?name=legacy_shop"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("server browser page missing %q:\n%s", want, page)
		}
	}
}

func TestDbBrowserServerPageAdminOnly(t *testing.T) {
	h, _, _ := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	admin := loginAsUser(t, h, "admin")

	if rr := get(t, h, "/databases/browse?name=a_main", bob); rr.Code != http.StatusNotFound {
		t.Fatalf("non-admin server browser = %d, want 404", rr.Code)
	}
	// The name must be a bare identifier.
	if rr := get(t, h, "/databases/browse?name=bad-name", admin); rr.Code != http.StatusNotFound {
		t.Fatalf("bad name = %d, want 404", rr.Code)
	}
}

func TestDbBrowserApiTables(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/databases/100/browse")

	rr := postJSON(t, h, "/databases/100/browse/api", map[string]any{"op": "tables"}, bob, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("api tables = %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body["success"] != true {
		t.Fatalf("tables reply = %s", rr.Body.String())
	}
	if got := agentParam(t, sa, "db.browse_tables", "database"); got != "a_main" {
		t.Fatalf("db.browse_tables database = %v", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action LIKE 'database.browser_%'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("read-only op audited: %d rows %v", n, err)
	}
}

func TestDbBrowserApiQueryAudited(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/databases/100/browse")

	rr := postJSON(t, h, "/databases/100/browse/api", map[string]any{"op": "query", "sql": "SELECT 1"}, bob, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("api query = %d: %s", rr.Code, rr.Body.String())
	}
	if got := agentParam(t, sa, "db.query", "sql"); got != "SELECT 1" {
		t.Fatalf("db.query sql = %v", got)
	}
	var targetType, targetID, result, details string
	err := db.QueryRow(`SELECT target_type, target_id, result, details FROM audit_logs WHERE action = 'database.browser_query'`).
		Scan(&targetType, &targetID, &result, &details)
	if err != nil || targetType != "website" || targetID != "10" || result != "success" || !strings.Contains(details, "SELECT 1") {
		t.Errorf("query audit = %q %q %q %q %v", targetType, targetID, result, details, err)
	}
}

func TestDbBrowserApiSaveFailureAudited(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/databases/100/browse")
	sa.failActions = map[string]string{"db.browse_save": "the agent refused the change"}

	rr := postJSON(t, h, "/databases/100/browse/api", map[string]any{
		"op": "save", "table": "posts", "values": map[string]any{"id": "1", "title": "x"},
	}, bob, token)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("api save (agent down) = %d, want 502: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	errObj, _ := body["error"].(map[string]any)
	if body["success"] != false || errObj["message"] != "the agent refused the change" {
		t.Errorf("save error body = %v", body)
	}
	var result, details string
	err := db.QueryRow(`SELECT result, details FROM audit_logs WHERE action = 'database.browser_save'`).Scan(&result, &details)
	if err != nil || result != "failure" || !strings.Contains(details, "the agent refused the change") {
		t.Errorf("save audit = %q %q %v", result, details, err)
	}
}

func TestDbBrowserApiUnknownOp(t *testing.T) {
	h, sa, _ := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/databases/100/browse")

	rr := postJSON(t, h, "/databases/100/browse/api", map[string]any{"op": "nope"}, bob, token)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown op = %d, want 400", rr.Code)
	}
	if len(sa.called()) != 0 {
		t.Fatalf("agent called for an unknown op: %v", sa.called())
	}
}

func TestDbBrowserApiAlterDryRunNotAudited(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/databases/100/browse")

	rr := postJSON(t, h, "/databases/100/browse/api", map[string]any{
		"op": "alter", "alter_op": "add_column", "table": "posts", "column": "note", "dry_run": true,
	}, bob, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("api alter dry-run = %d: %s", rr.Code, rr.Body.String())
	}
	// alter_op travels to the agent as op.
	if got := agentParam(t, sa, "db.browse_alter", "op"); got != "add_column" {
		t.Fatalf("db.browse_alter op = %v", got)
	}
	if got := agentParam(t, sa, "db.browse_alter", "dry_run"); got != true {
		t.Fatalf("db.browse_alter dry_run = %v", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'database.browser_alter'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("dry-run alter audited: %d rows %v", n, err)
	}
}

func TestDbBrowserApiRequiresCSRF(t *testing.T) {
	h, sa, _ := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")

	rr := postJSON(t, h, "/databases/100/browse/api", map[string]any{"op": "tables"}, bob, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("api without token = %d, want 400", rr.Code)
	}
	if len(sa.called()) != 0 {
		t.Fatalf("agent called without a CSRF token: %v", sa.called())
	}
}

func TestDbBrowserApiServerVariant(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/databases/browse?name=legacy_shop")

	rr := postJSON(t, h, "/databases/browse/api?name=legacy_shop", map[string]any{"op": "query", "sql": "SELECT 2"}, admin, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("server api = %d: %s", rr.Code, rr.Body.String())
	}
	if got := agentParam(t, sa, "db.query", "database"); got != "legacy_shop" {
		t.Fatalf("server db.query database = %v", got)
	}
	// An unmanaged database is audited against the server, not a website.
	var targetType, targetID string
	err := db.QueryRow(`SELECT target_type, target_id FROM audit_logs WHERE action = 'database.browser_query'`).Scan(&targetType, &targetID)
	if err != nil || targetType != "server" || targetID != "1" {
		t.Errorf("server query audit = %q %q %v", targetType, targetID, err)
	}
}
