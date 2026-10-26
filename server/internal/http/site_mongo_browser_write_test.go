package http

// This suite drives the site MongoDB database browser (SiteMongoController
// browser / browse / export): the tool page (the database list, the
// ?db= selection, the not-running state), the JSON operation endpoint
// (a representative op per family, the ownership guard, the unknown op,
// the bad request, CSRF), and the export download (headers, the file
// name, the jsonl format, the ownership guard). The agent is the
// recording fake (the mongo.* fixtures in startSitesAgent).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mongoBrowse posts one operation to the JSON endpoint (with the page's
// CSRF token) and decodes the reply.
func mongoBrowse(t *testing.T, h http.Handler, cookie *http.Cookie, body any) (int, map[string]any) {
	t.Helper()
	token, cookie := siteFormToken(t, h, cookie, "/sites/10/mongo/browser")
	rr := postJSON(t, h, "/sites/10/mongo/browse", body, cookie, token)
	var out map[string]any
	_ = json.NewDecoder(rr.Body).Decode(&out)
	return rr.Code, out
}

// mongoAgentCalled counts the recorded calls of an agent action.
func mongoAgentCalled(sa *sitesAgent, action string) int {
	n := 0
	for _, a := range sa.called() {
		if a == action {
			n++
		}
	}
	return n
}

func TestSiteMongoBrowserPage(t *testing.T) {
	h, _, _ := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/mongo/browser", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/mongo/browser = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		`data-api="/sites/10/mongo/browse"`,
		`data-export="/sites/10/mongo/export"`,
		`data-can-delete="1"`,
		`<option value="u_bob_shop">u_bob_shop</option>`,
		`<option value="u_bob_logs">u_bob_logs</option>`,
		`href="/sites/10/mongo"`,
		`/assets/js/mongobrowser.js`,
	} {
		if !pageHas(page, want) {
			t.Errorf("browser page missing %q", want)
		}
	}
	// Databases outside the account's prefix are not offered.
	if pageHas(page, "other_app") {
		t.Error("the browser offers a foreign database")
	}

	// ?db= preselects a database (and data-current carries it).
	rr = get(t, h, "/sites/10/mongo/browser?db=u_bob_shop", bob)
	page = rr.Body.String()
	for _, want := range []string{`data-current="u_bob_shop"`, `<option value="u_bob_shop" selected>u_bob_shop</option>`} {
		if !pageHas(page, want) {
			t.Errorf("browser page (?db=u_bob_shop) missing %q", want)
		}
	}
	// A database outside the list never becomes the selection.
	rr = get(t, h, "/sites/10/mongo/browser?db=other_app", bob)
	if !pageHas(rr.Body.String(), `data-current=""`) {
		t.Error("a foreign ?db= became the selection")
	}
}

func TestSiteMongoBrowserPageStates(t *testing.T) {
	// MongoDB not running: the page still loads, with no databases.
	h, sa, _ := mongoWEnv(t)
	sa.mongoStatus = "down"
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/mongo/browser", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("= %d", rr.Code)
	}
	if pageHas(rr.Body.String(), "u_bob_shop") {
		t.Error("databases listed while MongoDB is not running")
	}
	if !pageHas(rr.Body.String(), `data-current=""`) {
		t.Error("a selection without a database list")
	}
	// The viewer cannot open the browser.
	rr = get(t, h, "/sites/10/mongo/browser", loginAsUser(t, h, "carol"))
	if rr.Code != http.StatusForbidden {
		t.Errorf("carol's browser page = %d, want 403", rr.Code)
	}
}

func TestSiteMongoBrowserCollections(t *testing.T) {
	h, sa, _ := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	code, out := mongoBrowse(t, h, bob, map[string]any{"op": "collections", "db": "u_bob_shop"})
	if code != http.StatusOK || out["success"] != true {
		t.Fatalf("collections = %d %v", code, out)
	}
	data := out["data"].(map[string]any)
	cols := data["collections"].([]any)
	if len(cols) != 2 || cols[0].(map[string]any)["name"] != "coll_a" {
		t.Errorf("collections = %v", cols)
	}
	if got := agentParam(t, sa, "mongo.collections", "db"); fmt.Sprint(got) != "u_bob_shop" {
		t.Errorf("agent got db %v", got)
	}
}

func TestSiteMongoBrowserDocuments(t *testing.T) {
	h, sa, _ := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	code, out := mongoBrowse(t, h, bob, map[string]any{
		"op": "documents", "db": "u_bob_shop", "collection": "coll_a",
		"filter": `{"n":1}`, "sort": `{"n":-1}`, "projection": `{"n":1}`,
		"skip": 10, "limit": 25,
	})
	if code != http.StatusOK || out["success"] != true {
		t.Fatalf("documents = %d %v", code, out)
	}
	data := out["data"].(map[string]any)
	if data["total"] != float64(2) || data["documents"] == nil {
		t.Errorf("documents reply = %v", data)
	}
	for _, key := range []string{"db", "collection", "filter", "sort", "projection", "skip", "limit"} {
		if got := agentParam(t, sa, "mongo.documents", key); got == nil {
			t.Errorf("the agent never got %q", key)
		}
	}
	if got := agentParam(t, sa, "mongo.documents", "skip"); fmt.Sprint(got) != "10" {
		t.Errorf("agent got skip %v", got)
	}
	if got := agentParam(t, sa, "mongo.documents", "limit"); fmt.Sprint(got) != "25" {
		t.Errorf("agent got limit %v", got)
	}
}

func TestSiteMongoBrowserMutations(t *testing.T) {
	h, _, db := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")

	// Create a collection (audited).
	code, out := mongoBrowse(t, h, bob, map[string]any{"op": "coll_create", "db": "u_bob_shop", "collection": "coll_c"})
	if code != http.StatusOK || out["data"].(map[string]any)["created"] != "coll_c" {
		t.Fatalf("coll_create = %d %v", code, out)
	}
	if n := mongoAuditCount(t, db, "mongo.collection_create"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}

	// Empty a collection (the deleted count is passed through).
	code, out = mongoBrowse(t, h, bob, map[string]any{"op": "coll_empty", "db": "u_bob_shop", "collection": "coll_a"})
	if code != http.StatusOK || out["data"].(map[string]any)["deleted"] != float64(12) {
		t.Fatalf("coll_empty = %d %v", code, out)
	}

	// Insert a document (the inserted id is passed through).
	code, out = mongoBrowse(t, h, bob, map[string]any{
		"op": "insert", "db": "u_bob_shop", "collection": "coll_a",
		"document": `{"name":"x"}`,
	})
	if code != http.StatusOK || out["data"].(map[string]any)["inserted_id"] != "1" {
		t.Fatalf("insert = %d %v", code, out)
	}

	// Create a user on the database: the name is scoped to the prefix.
	code, out = mongoBrowse(t, h, bob, map[string]any{
		"op": "user_create", "db": "u_bob_shop",
		"user": "newuser", "password": "secret123", "role": "read",
	})
	if code != http.StatusOK || out["data"].(map[string]any)["user"] != "u_bob_newuser" {
		t.Fatalf("user_create = %d %v", code, out)
	}

	// The users tab lists the database's users.
	code, out = mongoBrowse(t, h, bob, map[string]any{"op": "users", "db": "u_bob_shop"})
	if code != http.StatusOK || out["data"].(map[string]any)["users"] == nil {
		t.Fatalf("users = %d %v", code, out)
	}

	// Stats come through as the agent's object.
	code, out = mongoBrowse(t, h, bob, map[string]any{"op": "collection_stats", "db": "u_bob_shop", "collection": "coll_a"})
	if code != http.StatusOK || out["data"].(map[string]any)["count"] != float64(2) {
		t.Fatalf("collection_stats = %d %v", code, out)
	}
}

func TestSiteMongoBrowserErrors(t *testing.T) {
	h, sa, _ := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")

	// An unknown operation is a 400.
	code, out := mongoBrowse(t, h, bob, map[string]any{"op": "nope", "db": "u_bob_shop"})
	if code != http.StatusBadRequest || !strings.Contains(out["error"].(map[string]any)["message"].(string), "Unknown operation") {
		t.Errorf("unknown op = %d %v", code, out)
	}
	// A database outside the prefix is refused before the agent.
	code, out = mongoBrowse(t, h, bob, map[string]any{"op": "collections", "db": "other_app"})
	if code != http.StatusForbidden {
		t.Errorf("foreign db = %d, want 403", code)
	}
	if !strings.Contains(out["error"].(map[string]any)["message"].(string), "does not belong") {
		t.Errorf("foreign db message = %v", out["error"])
	}
	// A missing database is refused the same way.
	code, out = mongoBrowse(t, h, bob, map[string]any{"op": "collections"})
	if code != http.StatusForbidden {
		t.Errorf("missing db = %d, want 403", code)
	}
	if mongoAgentCalled(sa, "mongo.collections") != 0 {
		t.Error("the agent was called for a foreign database")
	}
	// A malformed body is a 400.
	token, _ := siteFormToken(t, h, bob, "/sites/10/mongo/browser")
	req := httptest.NewRequest("POST", "/sites/10/mongo/browse", strings.NewReader("{nope"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", token)
	req.AddCookie(bob)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad body = %d, want 400", rr.Code)
	}
	// Without a CSRF token the reply says so.
	rr = postJSON(t, h, "/sites/10/mongo/browse", map[string]any{"op": "collections", "db": "u_bob_shop"}, bob, "")
	if rr.Code != http.StatusBadRequest || !pageHas(rr.Body.String(), "Page expired") {
		t.Errorf("no CSRF = %d %s", rr.Code, rr.Body.String())
	}
	// The viewer cannot browse.
	rr = postJSON(t, h, "/sites/10/mongo/browse", map[string]any{"op": "collections", "db": "u_bob_shop"}, loginAsUser(t, h, "carol"), "")
	if rr.Code != http.StatusForbidden {
		t.Errorf("carol's browse = %d, want 403", rr.Code)
	}
}

func TestSiteMongoBrowserAgentError(t *testing.T) {
	h, sa, _ := mongoWEnv(t)
	sa.failActions["mongo.collections"] = "The index could not be read."
	bob := loginAsUser(t, h, "bob")
	code, out := mongoBrowse(t, h, bob, map[string]any{"op": "collections", "db": "u_bob_shop"})
	if code != http.StatusBadGateway {
		t.Fatalf("agent error = %d, want 502", code)
	}
	if !strings.Contains(out["error"].(map[string]any)["message"].(string), "could not be read") {
		t.Errorf("agent error message = %v", out["error"])
	}
}

func TestSiteMongoExport(t *testing.T) {
	h, _, _ := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")

	rr := get(t, h, "/sites/10/mongo/export?db=u_bob_shop&collection=coll_a", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("export = %d: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); cd != `attachment; filename="u_bob_shop.coll_a.json"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if rr.Body.String() != "[{}]" {
		t.Errorf("body = %q", rr.Body.String())
	}

	// JSONL changes the extension and the content type.
	rr = get(t, h, "/sites/10/mongo/export?db=u_bob_shop&collection=coll_a&format=jsonl", bob)
	if cd := rr.Header().Get("Content-Disposition"); cd != `attachment; filename="u_bob_shop.coll_a.jsonl"` {
		t.Errorf("jsonl Content-Disposition = %q", cd)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/x-ndjson; charset=utf-8" {
		t.Errorf("jsonl Content-Type = %q", ct)
	}

	// Unsafe characters do not reach the file name.
	rr = get(t, h, "/sites/10/mongo/export?db=u_bob_shop&collection=a%2Fb%20c", bob)
	if cd := rr.Header().Get("Content-Disposition"); cd != `attachment; filename="u_bob_shop.a_b_c.json"` {
		t.Errorf("safe-name Content-Disposition = %q", cd)
	}

	// A foreign database is refused with a flashed error, back at the
	// browser page.
	rr = get(t, h, "/sites/10/mongo/export?db=other_app&collection=coll_a", bob)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/mongo/browser?db=other_app" {
		t.Errorf("foreign export = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, bob, "/sites/10/mongo/browser?db=other_app", "does not belong")

	// An agent failure is flashed back at the browser page, too.
	h2, sa, _ := mongoWEnv(t)
	sa.failActions["mongo.export"] = "The collection could not be read."
	bob2 := loginAsUser(t, h2, "bob")
	rr = get(t, h2, "/sites/10/mongo/export?db=u_bob_shop&collection=coll_a", bob2)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/mongo/browser?db=u_bob_shop" {
		t.Errorf("agent-error export = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h2, bob2, "/sites/10/mongo/browser?db=u_bob_shop", "could not be read")
}
