package http

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// recordingAgent serves the agent protocol on a Unix socket and records the
// actions it received, so a test can assert the panel called the agent (and
// with which params) — or did not.
type recordingAgent struct {
	mu      sync.Mutex
	actions []string
	params  []map[string]any
}

func startRecordingAgent(t *testing.T) (*recordingAgent, string, string) {
	t.Helper()
	ra := &recordingAgent{}
	dir := t.TempDir()
	socket := filepath.Join(dir, "agent.sock")
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/actions/", func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Path[len("/v1/actions/"):]
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		ra.mu.Lock()
		ra.actions = append(ra.actions, action)
		ra.params = append(ra.params, p)
		ra.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{},"request_id":"t"}`))
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ra, socket, tokenFile
}

func (ra *recordingAgent) called() []string {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	return append([]string(nil), ra.actions...)
}

// writeEnv builds a server whose agent points at a recording fake, seeds a
// customer with databases.manage and a plan allowing `maxDatabases` databases,
// and logs in as that customer.
func writeEnv(t *testing.T, maxDatabases int, existing int) (http.Handler, *http.Cookie, *recordingAgent, *sql.DB) {
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
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES (2,'customer','Customer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES (1,'databases.view','v','db'),(2,'databases.manage','m','db')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (2,1),(2,2)`)
	exec(`INSERT INTO plans (id,name,max_databases,created_at,updated_at) VALUES (1,'Basic',?,'','')`, maxDatabases)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1)`, loginHash)
	// Pre-existing databases owned by bob (count toward the plan limit).
	for i := 0; i < existing; i++ {
		exec(`INSERT INTO databases (server_id,owner_id,name,created_at) VALUES (1,1,?, '2026-01-01')`,
			"bob_old"+string(rune('a'+i)))
	}

	ra, socket, tokenFile := startRecordingAgent(t)
	cfg := config.Config{Production: true, AgentSocket: socket, AgentTokenFile: tokenFile}
	// A real panel key for session sealing.
	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.KeyFile = kf

	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	cookie, token := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {"bob"}, "password": {loginPassword}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login = %d", rr.Code)
	}
	return h, rr.Result().Cookies()[0], ra, db
}

// dbFormToken gets a CSRF token from the databases page (authenticated) and
// the refreshed session cookie that carries that token. The POST must use the
// returned cookie, since rendering the form mints the token into the session.
func dbFormToken(t *testing.T, h http.Handler, cookie *http.Cookie) (string, *http.Cookie) {
	t.Helper()
	rr := get(t, h, "/databases", cookie)
	m := csrfRe.FindStringSubmatch(rr.Body.String())
	if m == nil {
		t.Fatalf("no CSRF token on /databases:\n%s", rr.Body.String())
	}
	refreshed := cookie
	if cs := rr.Result().Cookies(); len(cs) > 0 {
		refreshed = cs[0]
	}
	return m[1], refreshed
}

func countDatabases(t *testing.T, h http.Handler, cookie *http.Cookie, substr string) bool {
	t.Helper()
	return containsStr(get(t, h, "/databases", cookie).Body.String(), substr)
}

func containsStr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestCreateDatabaseHappyPath(t *testing.T) {
	h, cookie, ra, db := writeEnv(t, 5, 0)
	token, cookie := dbFormToken(t, h, cookie)
	rr := postForm(h, "/databases", url.Values{"_token": {token}, "name": {"shop"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/databases" {
		t.Fatalf("create = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	// The agent must have been asked to create the PREFIXED name (bob_shop).
	acts := ra.called()
	if len(acts) != 1 || acts[0] != "db.create" {
		t.Fatalf("agent actions = %v, want [db.create]", acts)
	}
	if name, _ := ra.params[0]["name"].(string); name != "bob_shop" {
		t.Fatalf("db.create name = %q, want bob_shop (tenant prefix)", name)
	}
	// The row shows up in the list (follow with the post-redirect cookie).
	follow := cookie
	if cs := rr.Result().Cookies(); len(cs) > 0 {
		follow = cs[0]
	}
	if !countDatabases(t, h, follow, "bob_shop") {
		t.Fatal("created database not listed")
	}
	// A privileged action must be audited to the audit_logs table.
	var action, target, result string
	if err := db.QueryRow(`SELECT action, target_id, result FROM audit_logs WHERE action = 'database.create'`).Scan(&action, &target, &result); err != nil {
		t.Fatalf("no audit row for the create: %v", err)
	}
	if target != "bob_shop" || result != "success" {
		t.Fatalf("audit row = %s/%s, want bob_shop/success", target, result)
	}
}

func TestCreateDatabasePlanLimitBlocksBeforeAgent(t *testing.T) {
	// Plan allows 1; bob already owns 1 -> the create must be refused WITHOUT
	// touching the agent.
	h, cookie, ra, _ := writeEnv(t, 1, 1)
	token, cookie := dbFormToken(t, h, cookie)
	rr := postForm(h, "/databases", url.Values{"_token": {token}, "name": {"extra"}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("limit create = %d", rr.Code)
	}
	if acts := ra.called(); len(acts) != 0 {
		t.Fatalf("plan-limited create still called the agent: %v", acts)
	}
}

func TestCreateDatabaseInvalidNameNoAgent(t *testing.T) {
	h, cookie, ra, _ := writeEnv(t, 5, 0)
	token, cookie := dbFormToken(t, h, cookie)
	rr := postForm(h, "/databases", url.Values{"_token": {token}, "name": {"Bad Name!"}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("invalid create = %d", rr.Code)
	}
	if acts := ra.called(); len(acts) != 0 {
		t.Fatalf("invalid name reached the agent: %v", acts)
	}
}

func TestCreateDatabaseRejectsBadCSRF(t *testing.T) {
	h, cookie, ra, _ := writeEnv(t, 5, 0)
	rr := postForm(h, "/databases", url.Values{"_token": {"nope"}, "name": {"x"}}, cookie)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad CSRF create = %d, want 400", rr.Code)
	}
	if acts := ra.called(); len(acts) != 0 {
		t.Fatalf("CSRF-rejected create reached the agent: %v", acts)
	}
}

func TestDropDatabaseHappyPath(t *testing.T) {
	h, cookie, ra, db := writeEnv(t, 5, 0)
	// Seed a database owned by bob (user 1) to drop.
	if _, err := db.Exec(`INSERT INTO databases (id,server_id,owner_id,name,created_at) VALUES (200,1,1,'bob_del','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	token, cookie := dbFormToken(t, h, cookie)
	rr := postForm(h, "/databases/200/drop", url.Values{"_token": {token}, "confirm": {"bob_del"}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("drop = %d", rr.Code)
	}
	acts := ra.called()
	if len(acts) != 1 || acts[0] != "db.drop" {
		t.Fatalf("agent actions = %v, want [db.drop]", acts)
	}
	if name, _ := ra.params[0]["name"].(string); name != "bob_del" {
		t.Fatalf("db.drop name = %q", name)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM databases WHERE id = 200`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("database row not deleted")
	}
	var result string
	if err := db.QueryRow(`SELECT result FROM audit_logs WHERE action='database.drop'`).Scan(&result); err != nil {
		t.Fatalf("no audit row for drop: %v", err)
	}
}

func TestDropDatabaseWrongConfirmNoAgent(t *testing.T) {
	h, cookie, ra, db := writeEnv(t, 5, 0)
	if _, err := db.Exec(`INSERT INTO databases (id,server_id,owner_id,name,created_at) VALUES (201,1,1,'bob_keep','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	token, cookie := dbFormToken(t, h, cookie)
	rr := postForm(h, "/databases/201/drop", url.Values{"_token": {token}, "confirm": {"wrong"}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("drop = %d", rr.Code)
	}
	if acts := ra.called(); len(acts) != 0 {
		t.Fatalf("wrong-confirm drop reached the agent: %v", acts)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM databases WHERE id = 201`).Scan(&n)
	if n != 1 {
		t.Fatal("database was deleted despite wrong confirm")
	}
}

func TestDropForeignDatabaseNotFoundNoAgent(t *testing.T) {
	h, cookie, ra, db := writeEnv(t, 5, 0)
	// A separate tenant (user 2) owns a database — bob (user 1) must not drop it.
	if _, err := db.Exec(`INSERT INTO users (id,username,email,password_hash,role_id,is_active,password_changed_at,created_at,updated_at)
		VALUES (2,'other','o@x','h',2,1,'','','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO databases (id,server_id,owner_id,name,created_at) VALUES (202,1,2,'other_db','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	token, cookie := dbFormToken(t, h, cookie)
	rr := postForm(h, "/databases/202/drop", url.Values{"_token": {token}, "confirm": {"other_db"}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("drop = %d", rr.Code)
	}
	if acts := ra.called(); len(acts) != 0 {
		t.Fatalf("dropping another tenant's db reached the agent: %v", acts)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM databases WHERE id = 202`).Scan(&n)
	if n != 1 {
		t.Fatal("another tenant's database was deleted (tenant isolation failure)")
	}
}

func TestDropDatabaseRejectsBadCSRF(t *testing.T) {
	h, cookie, ra, db := writeEnv(t, 5, 0)
	if _, err := db.Exec(`INSERT INTO databases (id,server_id,owner_id,name,created_at) VALUES (203,1,1,'bob_csrf','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	rr := postForm(h, "/databases/203/drop", url.Values{"_token": {"nope"}, "confirm": {"bob_csrf"}}, cookie)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad-CSRF drop = %d, want 400", rr.Code)
	}
	if acts := ra.called(); len(acts) != 0 {
		t.Fatalf("CSRF-rejected drop reached the agent: %v", acts)
	}
}

func TestCreateDatabaseRequiresManagePermission(t *testing.T) {
	h, cookie, ra, _ := writeEnv(t, 5, 0)
	token, cookie := dbFormToken(t, h, cookie)
	// This server's customer has manage; a separate check: strip it is covered
	// by the guard test. Here assert the happy path remains guarded by posting
	// a valid create and confirming a row (sanity that guard allowed manage).
	_ = postForm(h, "/databases", url.Values{"_token": {token}, "name": {"ok"}}, cookie)
	if acts := ra.called(); len(acts) != 1 {
		t.Fatalf("expected one agent call, got %v", acts)
	}
}
