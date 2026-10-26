package http

// This suite drives the server-level MongoDB area (batch 66,
// MongoController): the page with its six sections and their states
// (installed / missing / stopped / config / log), creating and dropping
// databases and users, the per-database notes kept in settings, the
// permission split between mongo.view and mongo.manage (including the
// browse endpoint, where a viewer may read but never write), the collection
// export download, and the saved remote servers with their validation and
// encrypted passwords. The agent is the shared recording fake (mongo.*
// fixtures in startSitesAgent).

import (
	"database/sql"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// mongoAdminEnv seeds the administrator (every MongoDB permission), bob the
// customer (mongo.view only) and carol the viewer (no MongoDB permission),
// then builds the handler. It also returns the recording agent and the DB.
func mongoAdminEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES
		(1,'admin','Administrator','all',''),(2,'customer','Customer','own',''),(5,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),
		(31,'mongo.view','mv','mongo'),(32,'mongo.manage','mm','mongo'),
		(33,'packages.manage','pm','server'),(34,'services.manage','sm','server')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES
		(1,1),(1,2),(1,31),(1,32),(1,33),(1,34),
		(2,1),(2,31),(5,1)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'carol','c@x','Carol',?,5,1,'2026-01-01T00:00:00Z','','',NULL)`,
		loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,10,'bob.example',1)`)

	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sa, socket, tokenFile := startSitesAgent(t, false)
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler(), sa, db
}

// mongoAdminPost fetches a CSRF token from the page, then POSTs the form.
func mongoAdminPost(t *testing.T, h http.Handler, cookie *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	token, cookie := siteFormToken(t, h, cookie, "/mongo")
	form.Set("_token", token)
	return postForm(h, path, form, cookie)
}

// mongoActionRan reports whether the fake agent saw an action.
func mongoActionRan(sa *sitesAgent, action string) bool {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	for _, a := range sa.actions {
		if a == action {
			return true
		}
	}
	return false
}

// mongoActionParams returns the parameters of the first call to action.
func mongoActionParams(sa *sitesAgent, action string) map[string]any {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	for i, a := range sa.actions {
		if a == action && i < len(sa.params) {
			return sa.params[i]
		}
	}
	return nil
}

// mongoServerAuditCount is the number of audit rows for an action against
// server 1 (the per-website suite already defines mongoAuditCount).
func mongoServerAuditCount(t *testing.T, db *sql.DB, action string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = ? AND target_id = '1'`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMongoIndexDatabases(t *testing.T) {
	h, _, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/mongo", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /mongo = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"<title>MongoDB · WhaCP</title>",
		"7.0.14",
		`href="/mongo?section=users"`,
		`href="/mongo?section=backups"`,
		`href="/mongo?section=remotes"`,
		`href="/mongo?section=config"`,
		`href="/mongo?section=log"`,
		// The administrator sees every database, including the ones that
		// are not prefixed with a website's system user.
		"u_bob_shop",
		"other_app",
		"120.6 KB", // formatBytes(123456)
		"/mongo/databases",
		"/mongo/databases/drop",
		"/mongo/backups/delete",
		"/mongo/browser",
	} {
		if !pageHas(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// Only the databases section is loaded, so no users table yet.
	if pageHas(page, "u_bob_ro") {
		t.Error("the users of the databases tab should not be listed")
	}
}

func TestMongoIndexUsersSection(t *testing.T) {
	h, _, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/mongo?section=users", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /mongo?section=users = %d", rr.Code)
	}
	page := rr.Body.String()
	for _, want := range []string{"u_bob_ro", "dbOwner", "/mongo/users", "/mongo/users/password", "/mongo/users/drop"} {
		if !pageHas(page, want) {
			t.Errorf("users section missing %q", want)
		}
	}
}

func TestMongoIndexInvalidSectionFallsBack(t *testing.T) {
	h, _, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/mongo?section=nonsense", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /mongo?section=nonsense = %d", rr.Code)
	}
	if !pageHas(rr.Body.String(), "/mongo/databases") {
		t.Error("an unknown section should fall back to the databases tab")
	}
}

func TestMongoIndexNotInstalled(t *testing.T) {
	h, sa, _ := mongoAdminEnv(t)
	sa.mongoStatus = "missing"
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/mongo", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /mongo = %d", rr.Code)
	}
	page := rr.Body.String()
	for _, want := range []string{"MongoDB is not installed on this server.", "/mongo/repository", "/mongo/install"} {
		if !pageHas(page, want) {
			t.Errorf("install card missing %q", want)
		}
	}
}

func TestMongoIndexServiceDown(t *testing.T) {
	h, sa, _ := mongoAdminEnv(t)
	sa.mongoStatus = "down"
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/mongo", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /mongo = %d", rr.Code)
	}
	page := rr.Body.String()
	if !pageHas(page, "MongoDB is installed but the service is not running.") {
		t.Error("a stopped service should be reported")
	}
	if !pageHas(page, "/mongo/service") || !pageHas(page, `value="start"`) {
		t.Error("a stopped service should offer a start button")
	}
}

func TestMongoIndexConfigSection(t *testing.T) {
	h, _, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/mongo?section=config", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /mongo?section=config = %d", rr.Code)
	}
	page := rr.Body.String()
	for _, want := range []string{
		"/etc/mongod.conf", "/mongo/config", "/mongo/config-file",
		"/mongo/admin-password", "/mongo/auth", "net:", "127.0.0.1",
	} {
		if !pageHas(page, want) {
			t.Errorf("config section missing %q", want)
		}
	}
}

func TestMongoIndexLogSection(t *testing.T) {
	h, _, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/mongo?section=log", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /mongo?section=log = %d", rr.Code)
	}
	if !pageHas(rr.Body.String(), "waiting for connections") {
		t.Error("the log section should show the log tail")
	}
}

func TestMongoCreateDatabaseWithUser(t *testing.T) {
	h, sa, db := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := mongoAdminPost(t, h, admin, "/mongo/databases", url.Values{
		"name": {"shop"}, "user": {"shopadmin"}, "password": {"s3cret-pass"}, "role": {"dbOwner"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/databases = %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Location") != "/mongo" {
		t.Errorf("redirect = %q", rr.Header().Get("Location"))
	}
	if !mongoActionRan(sa, "mongo.database_create") {
		t.Error("the database was not created")
	}
	if got := mongoActionParams(sa, "mongo.database_create"); got["name"] != "shop" {
		t.Errorf("database_create name = %v", got["name"])
	}
	if got := mongoActionParams(sa, "mongo.user_create"); got["role"] != "dbOwner" || got["db"] != "shop" {
		t.Errorf("user_create params = %v", got)
	}
	for _, action := range []string{"mongo.database_create", "mongo.user_create"} {
		if mongoServerAuditCount(t, db, action) != 1 {
			t.Errorf("expected one %s audit row", action)
		}
	}
}

func TestMongoCreateDatabaseDefaultsRole(t *testing.T) {
	h, sa, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := mongoAdminPost(t, h, admin, "/mongo/databases", url.Values{
		"name": {"shop"}, "user": {"shopadmin"}, "password": {"s3cret-pass"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST = %d", rr.Code)
	}
	if got := mongoActionParams(sa, "mongo.user_create"); got["role"] != "readWrite" {
		t.Errorf("default role = %v, want readWrite", got["role"])
	}
}

func TestMongoDropDatabase(t *testing.T) {
	h, sa, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := mongoAdminPost(t, h, admin, "/mongo/databases/drop", url.Values{
		"name": {"other_app"}, "confirm": {"other_app"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/databases/drop = %d", rr.Code)
	}
	got := mongoActionParams(sa, "mongo.database_drop")
	if got["name"] != "other_app" || got["confirm"] != "other_app" {
		t.Errorf("drop params = %v", got)
	}
}

func TestMongoBulkDrop(t *testing.T) {
	h, sa, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := mongoAdminPost(t, h, admin, "/mongo/databases/bulk-drop", url.Values{
		"names[]": {"shop", "logs"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST bulk-drop = %d", rr.Code)
	}
	sa.mu.Lock()
	drops := 0
	for _, a := range sa.actions {
		if a == "mongo.database_drop" {
			drops++
		}
	}
	sa.mu.Unlock()
	if drops != 2 {
		t.Errorf("dropped %d databases, want 2", drops)
	}
}

func TestMongoBulkDropRequiresSelection(t *testing.T) {
	h, sa, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := mongoAdminPost(t, h, admin, "/mongo/databases/bulk-drop", url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST bulk-drop = %d", rr.Code)
	}
	if mongoActionRan(sa, "mongo.database_drop") {
		t.Error("nothing selected must not drop anything")
	}
}

func TestMongoCSRFRejected(t *testing.T) {
	h, _, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := postForm(h, "/mongo/users", url.Values{"user": {"x"}, "db": {"y"}, "password": {"z"}}, admin)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("POST without a token = %d, want 400", rr.Code)
	}
}

func TestMongoManageRequired(t *testing.T) {
	h, sa, _ := mongoAdminEnv(t)
	// bob holds mongo.view only.
	bob := loginAsUser(t, h, "bob")
	if rr := get(t, h, "/mongo", bob); rr.Code != http.StatusOK {
		t.Fatalf("GET /mongo as a viewer = %d, want 200", rr.Code)
	}
	for _, path := range []string{
		"/mongo/databases", "/mongo/users", "/mongo/config",
		"/mongo/backup", "/mongo/remotes", "/mongo/install", "/mongo/service",
	} {
		rr := mongoAdminPost(t, h, bob, path, url.Values{"name": {"x"}, "user": {"u"}, "db": {"d"}})
		if rr.Code != http.StatusForbidden {
			t.Errorf("POST %s as a viewer = %d, want 403", path, rr.Code)
		}
	}
	if mongoActionRan(sa, "mongo.database_create") {
		t.Error("a viewer must not reach the agent")
	}
	// carol holds no MongoDB permission at all.
	carol := loginAsUser(t, h, "carol")
	if rr := get(t, h, "/mongo", carol); rr.Code != http.StatusForbidden {
		t.Errorf("GET /mongo without mongo.view = %d, want 403", rr.Code)
	}
}

func TestMongoPageHidesManagementForViewer(t *testing.T) {
	h, _, _ := mongoAdminEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/mongo", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /mongo as a viewer = %d", rr.Code)
	}
	page := rr.Body.String()
	if pageHas(page, "/mongo/databases/drop") {
		t.Error("a viewer must not be offered the drop form")
	}
	if !pageHas(page, "u_bob_shop") {
		t.Error("a viewer can still read the database list")
	}
}

func TestMongoNoteSavedAndRead(t *testing.T) {
	h, _, db := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := mongoAdminPost(t, h, admin, "/mongo/note", url.Values{
		"name": {"shop"}, "note": {"the shop's catalogue"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/note = %d", rr.Code)
	}
	var raw string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'mongo.note.1.shop'`).Scan(&raw); err != nil {
		t.Fatalf("the note was not stored: %v", err)
	}
	if raw != `"the shop's catalogue"` {
		t.Errorf("stored note = %s, want the JSON string", raw)
	}
	page := get(t, h, "/mongo", admin).Body.String()
	if !pageHas(page, "the shop&#39;s catalogue") {
		t.Error("the saved note should be rendered on the databases tab")
	}
}

func TestMongoNoteCappedAt500(t *testing.T) {
	h, _, db := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	long := strings.Repeat("x", 600)
	rr := mongoAdminPost(t, h, admin, "/mongo/note", url.Values{"name": {"shop"}, "note": {long}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/note = %d", rr.Code)
	}
	var raw string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'mongo.note.1.shop'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 502 { // the 500 characters plus the two quotes
		t.Errorf("stored note length = %d, want 502", len(raw))
	}
}

func TestMongoBrowseReadAllowedForViewer(t *testing.T) {
	h, _, _ := mongoAdminEnv(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/mongo/browser")
	rr := postJSON(t, h, "/mongo/browse", map[string]any{
		"op": "documents", "db": "u_bob_shop", "collection": "coll_a",
	}, bob, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /mongo/browse (documents) = %d: %s", rr.Code, rr.Body.String())
	}
	if !pageHas(rr.Body.String(), `"success":true`) {
		t.Errorf("body = %s", rr.Body.String())
	}
}

func TestMongoBrowseWriteRefusedForViewer(t *testing.T) {
	h, sa, _ := mongoAdminEnv(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/mongo/browser")
	rr := postJSON(t, h, "/mongo/browse", map[string]any{
		"op": "coll_drop", "db": "u_bob_shop", "collection": "coll_a",
	}, bob, token)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("POST /mongo/browse (coll_drop) as a viewer = %d, want 403", rr.Code)
	}
	if mongoActionRan(sa, "mongo.collection_drop") {
		t.Error("a viewer must not reach the agent for a write operation")
	}
}

func TestMongoBrowseUnknownOp(t *testing.T) {
	h, _, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/mongo/browser")
	rr := postJSON(t, h, "/mongo/browse", map[string]any{"op": "nope", "db": "shop"}, admin, token)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown op = %d, want 400", rr.Code)
	}
	if !pageHas(rr.Body.String(), "Unknown operation.") {
		t.Errorf("body = %s", rr.Body.String())
	}
}

func TestMongoBrowseAdminWrite(t *testing.T) {
	h, sa, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/mongo/browser")
	rr := postJSON(t, h, "/mongo/browse", map[string]any{
		"op": "coll_empty", "db": "shop", "collection": "coll_a",
	}, admin, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /mongo/browse (coll_empty) = %d: %s", rr.Code, rr.Body.String())
	}
	if !mongoActionRan(sa, "mongo.collection_empty") {
		t.Error("the collection was not emptied")
	}
	if !pageHas(rr.Body.String(), `"deleted":12`) {
		t.Errorf("body = %s", rr.Body.String())
	}
}

func TestMongoBrowserPage(t *testing.T) {
	h, _, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/mongo/browser", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /mongo/browser = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		`data-api="/mongo/browse"`,
		`data-export="/mongo/export"`,
		`href="/mongo"`,
		"other_app", // the administrator's list is not prefix-filtered
	} {
		if !pageHas(page, want) {
			t.Errorf("browser page missing %q", want)
		}
	}
}

func TestMongoExportDownload(t *testing.T) {
	h, _, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/mongo/export?db=shop&collection=coll_a&format=jsonl", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /mongo/export = %d: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/x-ndjson") {
		t.Errorf("content type = %q", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "shop.coll_a.jsonl") {
		t.Errorf("content disposition = %q", cd)
	}
	if rr.Body.String() != "[{}]" {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestMongoRemoteAddAndDelete(t *testing.T) {
	h, sa, db := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := mongoAdminPost(t, h, admin, "/mongo/remotes", url.Values{
		"name": {"backup host"}, "host": {"10.0.0.9"}, "port": {"27018"},
		"username": {"reader"}, "password": {"rem0te-pass"}, "auth_db": {"admin"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/remotes = %d: %s", rr.Code, rr.Body.String())
	}
	if !mongoActionRan(sa, "mongo.remote_ping") {
		t.Error("the connection should be tested before it is saved")
	}
	var name, enc string
	var tls int
	if err := db.QueryRow(`SELECT name, password_enc, tls FROM mongo_remotes`).Scan(&name, &enc, &tls); err != nil {
		t.Fatalf("no stored remote: %v", err)
	}
	if name != "backup host" {
		t.Errorf("stored name = %q", name)
	}
	if enc == "" || enc == "rem0te-pass" {
		t.Errorf("the password must be sealed, got %q", enc)
	}
	if tls != 0 {
		t.Errorf("tls = %d", tls)
	}

	// The page lists it, without the password.
	page := get(t, h, "/mongo?section=remotes", admin).Body.String()
	for _, want := range []string{"backup host", "10.0.0.9:27018", "reader"} {
		if !pageHas(page, want) {
			t.Errorf("remotes section missing %q", want)
		}
	}
	if pageHas(page, "rem0te-pass") {
		t.Error("the password must never reach the browser")
	}

	var id int64
	if err := db.QueryRow(`SELECT id FROM mongo_remotes`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	rr = mongoAdminPost(t, h, admin, "/mongo/remotes/delete", url.Values{"id": {strconv.FormatInt(id, 10)}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/remotes/delete = %d", rr.Code)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mongo_remotes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("the remote was not removed")
	}
}

func TestMongoRemoteValidation(t *testing.T) {
	h, _, db := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := mongoAdminPost(t, h, admin, "/mongo/remotes", url.Values{
		"name": {"bad"}, "host": {"not a host!"}, "port": {"27017"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/remotes = %d", rr.Code)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mongo_remotes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("an invalid host must not be stored")
	}
	page := get(t, h, "/mongo?section=remotes", admin).Body.String()
	if !pageHas(page, "Enter a valid host name or IP address.") {
		t.Error("the validation message should be flashed")
	}
}

func TestMongoRemoteTestAndPull(t *testing.T) {
	h, sa, db := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	if _, err := db.Exec(`INSERT INTO mongo_remotes (id, server_id, name, host, port, username, password_enc, auth_db, tls, created_at, updated_at)
		VALUES (7, 1, 'backup host', '10.0.0.9', 27017, 'reader', '', 'admin', 0, '', '')`); err != nil {
		t.Fatal(err)
	}
	rr := mongoAdminPost(t, h, admin, "/mongo/remotes/test", url.Values{"id": {"7"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/remotes/test = %d", rr.Code)
	}
	page := get(t, h, "/mongo?section=remotes", admin).Body.String()
	if !pageHas(page, "MongoDB 7.0.14, 3 database(s)") {
		t.Error("the ping result should be flashed")
	}

	rr = mongoAdminPost(t, h, admin, "/mongo/remotes/pull", url.Values{
		"id": {"7"}, "remote_db": {"ledger"}, "target_db": {"ledger_copy"}, "drop": {"1"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/remotes/pull = %d", rr.Code)
	}
	got := mongoActionParams(sa, "mongo.remote_pull")
	if got["remote_db"] != "ledger" || got["target_db"] != "ledger_copy" || got["drop"] != true {
		t.Errorf("pull params = %v", got)
	}
}

func TestMongoRemoteDatabasesJSON(t *testing.T) {
	h, _, db := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	if _, err := db.Exec(`INSERT INTO mongo_remotes (id, server_id, name, host, port, username, password_enc, auth_db, tls, created_at, updated_at)
		VALUES (7, 1, 'backup host', '10.0.0.9', 27017, 'reader', '', 'admin', 0, '', '')`); err != nil {
		t.Fatal(err)
	}
	token, admin := siteFormToken(t, h, admin, "/mongo?section=remotes")
	req := httptest.NewRequest("POST", "/mongo/remotes/databases", strings.NewReader("id=7"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", token)
	req.AddCookie(admin)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /mongo/remotes/databases = %d: %s", rr.Code, rr.Body.String())
	}
	if !pageHas(rr.Body.String(), `"name":"ledger"`) {
		t.Errorf("body = %s", rr.Body.String())
	}
}

func TestMongoInstallAndRepository(t *testing.T) {
	h, sa, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := mongoAdminPost(t, h, admin, "/mongo/repository", url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/repository = %d", rr.Code)
	}
	if !mongoActionRan(sa, "mongo.repository.enable") {
		t.Error("the repository was not enabled")
	}
	rr = mongoAdminPost(t, h, admin, "/mongo/install", url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/install = %d", rr.Code)
	}
	if !mongoActionRan(sa, "mongo.install") {
		t.Error("the install job was not started")
	}
}

func TestMongoServiceControl(t *testing.T) {
	h, sa, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := mongoAdminPost(t, h, admin, "/mongo/service", url.Values{"op": {"restart"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/service = %d", rr.Code)
	}
	got := mongoActionParams(sa, "services.control")
	if got["unit"] != "mongod.service" || got["op"] != "restart" {
		t.Errorf("services.control params = %v", got)
	}
	rr = mongoAdminPost(t, h, admin, "/mongo/service", url.Values{"op": {"explode"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/service (bad op) = %d", rr.Code)
	}
	page := get(t, h, "/mongo", admin).Body.String()
	if !pageHas(page, "Unknown service operation.") {
		t.Error("an unknown operation should be rejected")
	}
}

func TestMongoAdminPasswordAndAuth(t *testing.T) {
	h, sa, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	if rr := mongoAdminPost(t, h, admin, "/mongo/admin-password", url.Values{"password": {"n3w-pass"}}); rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/admin-password = %d", rr.Code)
	}
	if !mongoActionRan(sa, "mongo.admin_password") {
		t.Error("the admin password was not set")
	}
	if rr := mongoAdminPost(t, h, admin, "/mongo/auth", url.Values{"enable": {"1"}}); rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/auth = %d", rr.Code)
	}
	if got := mongoActionParams(sa, "mongo.auth_set"); got["enable"] != true {
		t.Errorf("auth_set params = %v", got)
	}
}

func TestMongoBackupRestoreDelete(t *testing.T) {
	h, sa, _ := mongoAdminEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := mongoAdminPost(t, h, admin, "/mongo/backup", url.Values{"db": {"shop"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/backup = %d", rr.Code)
	}
	if !mongoActionRan(sa, "mongo.backup") {
		t.Error("the backup was not started")
	}
	rr = mongoAdminPost(t, h, admin, "/mongo/restore", url.Values{
		"file": {mongoTestShopBackup}, "db": {"shop"}, "drop": {"1"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/restore = %d", rr.Code)
	}
	if got := mongoActionParams(sa, "mongo.restore"); got["drop"] != true {
		t.Errorf("restore params = %v", got)
	}
	rr = mongoAdminPost(t, h, admin, "/mongo/backups/delete", url.Values{"file": {mongoTestShopBackup}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /mongo/backups/delete = %d", rr.Code)
	}
	if !mongoActionRan(sa, "mongo.backup_delete") {
		t.Error("the backup was not deleted")
	}
}
