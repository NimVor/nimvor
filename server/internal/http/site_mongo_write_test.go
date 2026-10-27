package http

// This suite drives the per-website MongoDB management area
// (SiteMongoController + the write side of SiteMongoService): the page
// (databases / users / backups sections, the prefix filtering, the
// not-installed / not-running / agent-down states), creating + dropping
// the prefixed databases, creating users + password + drop (the ownership
// guard), and the backup actions (back up, restore, delete — the job
// flashes, the archive-name validation, the ownership guard). The agent is
// the recording fake (mongo.* fixtures in startSitesAgent).

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
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

const (
	mongoTestBackupJob     = "0123456789a0a001"
	mongoTestRestoreJob    = "0123456789a0a002"
	mongoTestShopBackup    = "20261020-093000_u_bob_shop.archive.gz"
	mongoTestForeignBackup = "20261019-120000_other_app.archive.gz"
)

// mongoWEnv seeds an administrator, a customer (bob, websites.manage, owns
// website 10 with system_user u_bob) and a read-only viewer (carol, owns
// website 20).
func mongoWEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
		(1,'websites.view','v','web'),(2,'websites.manage','m','web')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(2,1),(2,2),(5,1)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'carol','c@x','Carol',?,5,1,'2026-01-01T00:00:00Z','','',NULL)`,
		loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','',''),
		(20,1,3,'carol.example','u_carol','/home/whacp/carol.example','/home/whacp/carol.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,10,'bob.example',1),(1,20,'carol.example',1)`)

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

// mongoPost fetches the site's mongo page for a fresh CSRF token, then POSTs
// the form to path.
func mongoPost(t *testing.T, h http.Handler, cookie *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	token, cookie := siteFormToken(t, h, cookie, "/sites/10/mongo")
	form.Set("_token", token)
	return postForm(h, path, form, cookie)
}

// mongoAuditCount is the number of audit_logs rows with action for website 10.
func mongoAuditCount(t *testing.T, db *sql.DB, action string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = ? AND target_id = '10'`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSiteMongoPage(t *testing.T) {
	h, _, _ := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/mongo", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/mongo = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"<title>MongoDB · bob.example · WhaCP</title>",
		"prefixed with <code>u_bob_</code>",
		"mongodb://127.0.0.1:27017",
		"/sites/10/mongo?section=databases",
		"/sites/10/mongo?section=users",
		"/sites/10/mongo?section=backups",
		"u_bob_shop",
		"u_bob_logs",
		"120.6 KB", // formatBytes(123456)
		"0 B",      // the empty database
		"/sites/10/mongo/browser?db=u_bob_shop",
		"/sites/10/mongo/databases",
		"/sites/10/mongo/databases/drop",
	} {
		if !pageHas(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The foreign database is filtered out.
	if pageHas(page, "other_app") {
		t.Error("the foreign database must not be listed")
	}
}

func TestSiteMongoPageUsers(t *testing.T) {
	h, _, _ := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/mongo?section=users", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("users section = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"u_bob_admin",
		"dbOwner",
		"u_bob_ro",
		`data-user="u_bob_admin"`,
		`data-db="u_bob_shop"`,
		"/sites/10/mongo/users/password",
		"/sites/10/mongo/users/drop",
		"/sites/10/mongo/users",
	} {
		if !pageHas(page, want) {
			t.Errorf("users page missing %q", want)
		}
	}
	if pageHas(page, "other_user") {
		t.Error("the foreign user must not be listed")
	}
}

func TestSiteMongoPageBackups(t *testing.T) {
	h, _, _ := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/mongo?section=backups", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("backups section = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"u_bob_shop",
		"1.0 MB", // formatBytes(1048576)
		"2026-10-20 09:30",
		mongoTestShopBackup,
		"/sites/10/mongo/backup",
		"/sites/10/mongo/restore",
		"/sites/10/mongo/backups/delete",
	} {
		if !pageHas(page, want) {
			t.Errorf("backups page missing %q", want)
		}
	}
	if pageHas(page, mongoTestForeignBackup) {
		t.Error("the foreign backup must not be listed")
	}
}

func TestSiteMongoPageStates(t *testing.T) {
	t.Run("not installed", func(t *testing.T) {
		h, sa, _ := mongoWEnv(t)
		sa.mongoStatus = "missing"
		bob := loginAsUser(t, h, "bob")
		rr := get(t, h, "/sites/10/mongo", bob)
		if rr.Code != http.StatusOK {
			t.Fatalf("= %d", rr.Code)
		}
		if !pageHas(rr.Body.String(), "MongoDB is not available on this server") {
			t.Error("missing the not-installed state")
		}
	})
	t.Run("not running", func(t *testing.T) {
		h, sa, _ := mongoWEnv(t)
		sa.mongoStatus = "down"
		bob := loginAsUser(t, h, "bob")
		rr := get(t, h, "/sites/10/mongo", bob)
		if rr.Code != http.StatusOK {
			t.Fatalf("= %d", rr.Code)
		}
		if !pageHas(rr.Body.String(), "MongoDB is installed but not running") {
			t.Error("missing the not-running state")
		}
	})
	t.Run("agent down", func(t *testing.T) {
		h, sa, _ := mongoWEnv(t)
		sa.failActions = map[string]string{"mongo.status": "the agent is offline"}
		bob := loginAsUser(t, h, "bob")
		rr := get(t, h, "/sites/10/mongo", bob)
		if rr.Code != http.StatusOK {
			t.Fatalf("= %d", rr.Code)
		}
		if !pageHas(rr.Body.String(), "the agent is offline") {
			t.Error("missing the agent error")
		}
	})
}

func TestSiteMongoPageReadOnly(t *testing.T) {
	h, _, _ := mongoWEnv(t)
	carol := loginAsUser(t, h, "carol")
	rr := get(t, h, "/sites/20/mongo", carol)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer GET /sites/20/mongo = %d (want 403)", rr.Code)
	}
}

func TestSiteMongoCreateDatabase(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := mongoPost(t, h, bob, "/sites/10/mongo/databases", url.Values{"name": {"shop"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("create = %d: %s", rr.Code, rr.Body.String())
	}
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/mongo", "Created database u_bob_shop.")
	if got := agentParam(t, sa, "mongo.database_create", "name"); got != "u_bob_shop" {
		t.Errorf("agent name = %v", got)
	}
	if n := mongoAuditCount(t, db, "mongo.database_create"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
}

func TestSiteMongoCreateDatabasePrefixedInput(t *testing.T) {
	h, sa, _ := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := mongoPost(t, h, bob, "/sites/10/mongo/databases", url.Values{"name": {"u_bob_extra"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("create = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/mongo", "Created database u_bob_extra.")
	// The typed prefix is not doubled.
	if got := agentParam(t, sa, "mongo.database_create", "name"); got != "u_bob_extra" {
		t.Errorf("agent name = %v (prefix doubled)", got)
	}
}

func TestSiteMongoCreateDatabaseEmpty(t *testing.T) {
	h, sa, _ := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := mongoPost(t, h, bob, "/sites/10/mongo/databases", url.Values{"name": {"  "}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("create = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/mongo", "Enter a name.")
	sa.mu.Lock()
	for _, a := range sa.actions {
		if strings.HasPrefix(a, "mongo.database_create") {
			t.Error("an empty name must not reach the agent")
		}
	}
	sa.mu.Unlock()
}

func TestSiteMongoDropDatabase(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := mongoPost(t, h, bob, "/sites/10/mongo/databases/drop", url.Values{"name": {"u_bob_shop"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("drop = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/mongo", "Database dropped.")
	if got := agentParam(t, sa, "mongo.database_drop", "confirm"); got != "u_bob_shop" {
		t.Errorf("agent confirm = %v", got)
	}
	if n := mongoAuditCount(t, db, "mongo.database_drop"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
}

func TestSiteMongoDropDatabaseForeign(t *testing.T) {
	h, sa, _ := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := mongoPost(t, h, bob, "/sites/10/mongo/databases/drop", url.Values{"name": {"other_app"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("drop = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/mongo", "That database or user does not belong to this website.")
	sa.mu.Lock()
	for _, a := range sa.actions {
		if a == "mongo.database_drop" {
			t.Error("a foreign database must not reach the agent")
		}
	}
	sa.mu.Unlock()
}

func TestSiteMongoCreateUser(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := mongoPost(t, h, bob, "/sites/10/mongo/users", url.Values{
		"user": {"admin"}, "password": {"secret123"}, "db": {"u_bob_shop"}, "role": {"dbOwner"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("create user = %d: %s", rr.Code, rr.Body.String())
	}
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/mongo", "Created user u_bob_admin.")
	if got := agentParam(t, sa, "mongo.user_create", "user"); got != "u_bob_admin" {
		t.Errorf("agent user = %v", got)
	}
	if got := agentParam(t, sa, "mongo.user_create", "role"); got != "dbOwner" {
		t.Errorf("agent role = %v", got)
	}
	if n := mongoAuditCount(t, db, "mongo.user_create"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
}

func TestSiteMongoCreateUserForeignDB(t *testing.T) {
	h, _, _ := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := mongoPost(t, h, bob, "/sites/10/mongo/users", url.Values{
		"user": {"x"}, "password": {"secret123"}, "db": {"other_app"}, "role": {"read"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("create user = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/mongo", "That database or user does not belong to this website.")
}

func TestSiteMongoSetPassword(t *testing.T) {
	h, sa, _ := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := mongoPost(t, h, bob, "/sites/10/mongo/users/password", url.Values{
		"user": {"u_bob_admin"}, "db": {"u_bob_shop"}, "password": {"newsecret1"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("set password = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/mongo", "Password changed.")
	if got := agentParam(t, sa, "mongo.user_password", "user"); got != "u_bob_admin" {
		t.Errorf("agent user = %v", got)
	}
	// A foreign user is refused before the agent.
	rr = mongoPost(t, h, bob, "/sites/10/mongo/users/password", url.Values{
		"user": {"other_user"}, "db": {"u_bob_shop"}, "password": {"newsecret1"},
	})
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/mongo", "That database or user does not belong to this website.")
}

func TestSiteMongoDropUser(t *testing.T) {
	h, sa, _ := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := mongoPost(t, h, bob, "/sites/10/mongo/users/drop", url.Values{
		"user": {"u_bob_ro"}, "db": {"u_bob_shop"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("drop user = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/mongo", "User dropped.")
	if got := agentParam(t, sa, "mongo.user_drop", "user"); got != "u_bob_ro" {
		t.Errorf("agent user = %v", got)
	}
}

func TestSiteMongoBackup(t *testing.T) {
	h, _, db := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := mongoPost(t, h, bob, "/sites/10/mongo/backup", url.Values{"db": {"u_bob_shop"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("backup = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	rr = get(t, h, "/sites/10/mongo", bob)
	page := rr.Body.String()
	if !pageHas(page, "Backing up u_bob_shop…") {
		t.Error("missing the backup flash")
	}
	if !pageHas(page, "Job "+mongoTestBackupJob+" started in the background.") {
		t.Error("missing the job flash")
	}
	if n := mongoAuditCount(t, db, "mongo.backup"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	var tracked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_jobs WHERE job_id = ?`, mongoTestBackupJob).Scan(&tracked); err != nil {
		t.Fatal(err)
	}
	if tracked != 1 {
		t.Error("the backup job was not tracked")
	}
	// A foreign database is refused.
	rr = mongoPost(t, h, bob, "/sites/10/mongo/backup", url.Values{"db": {"other_app"}})
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/mongo", "That database or user does not belong to this website.")
}

func TestSiteMongoRestore(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := mongoPost(t, h, bob, "/sites/10/mongo/restore", url.Values{
		"file": {mongoTestShopBackup}, "drop": {"0"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("restore = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	rr = get(t, h, "/sites/10/mongo", bob)
	page := rr.Body.String()
	if !pageHas(page, "Restoring from the backup…") {
		t.Error("missing the restore flash")
	}
	if !pageHas(page, "Job "+mongoTestRestoreJob+" started in the background.") {
		t.Error("missing the job flash")
	}
	if got := agentParam(t, sa, "mongo.restore", "db"); got != "u_bob_shop" {
		t.Errorf("agent db = %v", got)
	}
	if n := mongoAuditCount(t, db, "mongo.restore"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	var tracked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_jobs WHERE job_id = ?`, mongoTestRestoreJob).Scan(&tracked); err != nil {
		t.Fatal(err)
	}
	if tracked != 1 {
		t.Error("the restore job was not tracked")
	}
	// A malformed archive name is refused.
	rr = mongoPost(t, h, bob, "/sites/10/mongo/restore", url.Values{"file": {"garbage.gz"}})
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/mongo", "That is not a backup archive.")
	// A foreign database's archive is refused.
	rr = mongoPost(t, h, bob, "/sites/10/mongo/restore", url.Values{"file": {mongoTestForeignBackup}})
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/mongo", "That database or user does not belong to this website.")
}

func TestSiteMongoDeleteBackup(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := mongoPost(t, h, bob, "/sites/10/mongo/backups/delete", url.Values{"file": {mongoTestShopBackup}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/mongo", "Backup deleted.")
	if got := agentParam(t, sa, "mongo.backup_delete", "file"); got != mongoTestShopBackup {
		t.Errorf("agent file = %v", got)
	}
	if n := mongoAuditCount(t, db, "mongo.backup_delete"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	// A foreign archive is refused.
	rr = mongoPost(t, h, bob, "/sites/10/mongo/backups/delete", url.Values{"file": {mongoTestForeignBackup}})
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/mongo", "That database or user does not belong to this website.")
}

func TestSiteMongoPostRequiresCSRF(t *testing.T) {
	h, _, _ := mongoWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := postForm(h, "/sites/10/mongo/databases", url.Values{"name": {"shop"}}, bob)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("create without CSRF token = %d (want 400)", rr.Code)
	}
}
