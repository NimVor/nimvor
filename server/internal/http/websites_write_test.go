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
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// This suite drives the website write routes end to end: CSRF, permission and
// tenant gates, form re-render on validation failure, the agent apply/delete
// calls, and the audit trail. The agent is a recording fake on a Unix socket.

type sitesAgent struct {
	mu          sync.Mutex
	actions     []string
	params      []map[string]any
	failApply   bool
	// failActions fails a specific action (agent error paths), e.g.
	// "sites.permissions" -> "the agent refused".
	failActions map[string]string
	// dbDown makes db.server_status report a stopped server.
	dbDown bool
}

func startSitesAgent(t *testing.T, failApply bool) (*sitesAgent, string, string) {
	t.Helper()
	sa := &sitesAgent{failApply: failApply}
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
		sa.mu.Lock()
		sa.actions = append(sa.actions, action)
		sa.params = append(sa.params, p)
		sa.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if msg, fail := sa.failActions[action]; fail {
			_, _ = w.Write([]byte(`{"success":false,"error":{"code":"boom","message":"` + msg + `","request_id":"t"}}`))
			return
		}
		if sa.failApply && (action == "sites.apply" || action == "sites.delete") {
			_, _ = w.Write([]byte(`{"success":false,"error":{"code":"boom","message":"the agent refused","request_id":"t"}}`))
			return
		}
		switch action {
		case "discovery.php":
			_, _ = w.Write([]byte(`{"success":true,"data":{"versions":[
				{"version":"8.2","fpm_binary":"/usr/sbin/php-fpm8.2"},
				{"version":"8.3.1","fpm_binary":"/usr/sbin/php-fpm8.3"}]}}`))
		case "agent.status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"sites_root":"/home/whacp"}}`))
		case "dns.zone_apply":
			_, _ = w.Write([]byte(`{"success":true,"data":{"serial":7}}`))
		case "sites.delete":
			_, _ = w.Write([]byte(`{"success":true,"data":{"warnings":["The access log was kept."]}}`))
		case "runtime.status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"arch":"x86_64",
				"node":[{"kind":"node","version":"20.1.0","bin":"/usr/bin/node","binary":"/usr/bin/node"},
				       {"kind":"node","version":"22.0.0","bin":"/usr/bin/node","binary":"/usr/bin/node"}],
				"python":[],
				"system":[{"kind":"python","version":"3.12","bin":"/usr/bin/python3","binary":"/usr/bin/python3"},
				       {"kind":"python","version":"3.11","bin":"/usr/bin/python3","binary":"/usr/bin/python3"}]}}`))
		case "sites.permissions":
			_, _ = w.Write([]byte(`{"success":true,"data":{"checked":120,"fixed":3,"skipped_count":2,"errors":[]}}`))
		case "workers.status":
			_, _ = w.Write([]byte(`{"success":true,"data":[{"unit":"whacp-worker-bob-app.service","active_state":"active","sub_state":"running","procs_total":2,"procs_running":2,"memory_bytes":12582912}]}`))
		case "db.dump":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"deadbeefcafe0001","file":"/backups/databases/a_main-20260101T000000Z.sql.gz"}}`))
		case "db.import_site":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"deadbeefcafe0002"}}`))
		case "jobs.log":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job":{"id":"deadbeefcafe0003","kind":"Dump a_main","target":"100","status":"succeeded","created_at":"2026-01-01T00:00:00Z"},"log":"Dump finished.\n","offset":42,"done":true}}`))
		case "db.overview":
			_, _ = w.Write([]byte(`{"success":true,"data":{"version":"10.11.6-MariaDB-0ubuntu0.22.04.1","databases":[{"name":"a_main","size_bytes":10485760,"tables":12},{"name":"b_main","size_bytes":524288,"tables":3},{"name":"legacy_shop","size_bytes":2097152,"tables":5}]}}`))
		case "db.browse_tables":
			_, _ = w.Write([]byte(`{"success":true,"data":{"database":"a_main","tables":[{"name":"posts","view":false,"engine":"InnoDB","rows":120,"bytes":1048576,"collation":"utf8mb4_unicode_ci","comment":""}],"server":"10.11.6-MariaDB","charset":"utf8mb4","collation":"utf8mb4_unicode_ci"}}`))
		case "db.browse_structure":
			_, _ = w.Write([]byte(`{"success":true,"data":{"table":"posts","columns":[{"name":"id","type":"int(11)"}],"indexes":[],"foreign_keys":[],"key":"id","create":"CREATE TABLE posts (id int)"}}`))
		case "db.browse_rows":
			_, _ = w.Write([]byte(`{"success":true,"data":{"table":"posts","rows":[{"key":"1","values":{"id":"1","title":"First"}}],"count":120}}`))
		case "db.query":
			_, _ = w.Write([]byte(`{"success":true,"data":{"results":[],"rows":0,"ms":2}}`))
		case "db.browse_save":
			_, _ = w.Write([]byte(`{"success":true,"data":{"saved":1}}`))
		case "db.browse_delete":
			_, _ = w.Write([]byte(`{"success":true,"data":{"deleted":1}}`))
		case "db.browse_alter":
			_, _ = w.Write([]byte(`{"success":true,"data":{"sql":"ALTER TABLE posts ADD COLUMN note varchar(255);"}}`))
		case "db.server_status":
			if sa.dbDown {
				_, _ = w.Write([]byte(`{"success":true,"data":{"running":false,"unit":"mariadb.service","drop_in":"/etc/mysql/mariadb.conf.d/zz-whacp.cnf","managed":{},"error":"Connection refused (111)"},"settings":[{"key":"max_connections","label":"Maximum connections","help":"Connections the server accepts at once.","kind":"int","dynamic":true,"group":"Connections"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"running":true,"unit":"mariadb.service","drop_in":"/etc/mysql/mariadb.conf.d/zz-whacp.cnf","managed":{"max_connections":"200"},"config_error":"","variables":{"version":"10.11.6-MariaDB","version_comment":"mariadb.org binary distribution","socket":"/run/mysqld/mysqld.sock","port":"3306","bind_address":"127.0.0.1","datadir":"/var/lib/mysql","log_error":"/var/log/mysql/error.log","have_ssl":"YES","max_connections":"151","innodb_buffer_pool_size":"134217728","table_open_cache":"4000","character_set_server":"utf8mb4"},"status":{"Uptime":"86400","Threads_connected":"12","Max_used_connections":"40","Threads_running":"2","Questions":"1000000","Slow_queries":"3","Aborted_connects":"1","Bytes_received":"1048576","Bytes_sent":"5242880","Open_tables":"100","Com_select":"900000","Com_insert":"90000","Com_update":"9000","Com_delete":"1000","Table_locks_waited":"5","Innodb_buffer_pool_pages_total":"8192","Innodb_buffer_pool_pages_free":"4096","Innodb_buffer_pool_read_requests":"10000","Innodb_buffer_pool_reads":"10","Created_tmp_tables":"50","Created_tmp_disk_tables":"2"},"databases":7,"users":11,"bytes":1073741824,"collations":{"utf8mb4":["utf8mb4_general_ci","utf8mb4_unicode_ci"],"utf8mb3":["utf8mb3_general_ci"]},"settings":[{"key":"max_connections","label":"Maximum connections","help":"Connections the server accepts at once (one more is kept for administrators).","kind":"int","dynamic":true,"group":"Connections"},{"key":"innodb_buffer_pool_size","label":"InnoDB buffer pool","help":"Memory for cached InnoDB data and indexes, e.g. 1G.","kind":"size","dynamic":true,"group":"Memory and storage"},{"key":"skip_name_resolve","label":"Skip host name lookups","help":"Match remote users by IP address only (faster, no DNS).","kind":"bool","dynamic":false,"group":"Connections"},{"key":"character_set_server","label":"Default character set","help":"For new databases created without one.","kind":"choice","choices":["utf8mb4","utf8mb3","latin1"],"dynamic":true,"group":"Character set"},{"key":"collation_server","label":"Default collation","help":"Must belong to the character set.","kind":"choice","dynamic":true,"group":"Character set"}]}}`))
		case "db.processlist":
			_, _ = w.Write([]byte(`{"success":true,"data":{"connections":[{"id":7,"user":"u_bob","host":"localhost","db":"a_main","command":"Query","time":3,"state":"Sending data","info":"SELECT * FROM posts","system":false},{"id":3,"user":"event_scheduler","host":"localhost","db":"","command":"Daemon","time":86400,"state":"","info":"","system":true}]}}`))
		case "db.server_settings":
			_, _ = w.Write([]byte(`{"success":true,"data":{"changed":["max_connections"],"applied":["max_connections"],"restart_needed":[],"restarted":false}}`))
		case "db.server_restart":
			_, _ = w.Write([]byte(`{"success":true,"data":{"restarted":true,"seconds":2}}`))
		case "db.server_flush":
			_, _ = w.Write([]byte(`{"success":true,"data":{"flushed":"privileges"}}`))
		case "db.kill":
			_, _ = w.Write([]byte(`{"success":true,"data":{"killed":7,"query_only":false}}`))
		case "logs.sources":
			_, _ = w.Write([]byte(`{"success":true,"data":[{"id":"nginx:access","label":"nginx access","group":"Web server","kind":"file","size":1024,"exists":true},{"id":"system:mysql","label":"MySQL error log","group":"Database server","kind":"file","size":4096,"exists":true},{"id":"journal:mariadb.service","label":"mariadb.service journal","group":"Database server","kind":"journal","size":0,"exists":true},{"id":"system:auth","label":"Authentication log","group":"Security","kind":"file","size":512,"exists":true}]}`))
		case "files.list":
			_, _ = w.Write([]byte(`{"success":true,"data":{"path":"","entries":[{"name":"hello.txt","type":"file","size":11,"mtime":1700000000,"mode":"0644"},{"name":"app","type":"dir","size":0,"mtime":1700000000,"mode":"0755"}]}}`))
		case "files.read":
			_, _ = w.Write([]byte(`{"success":true,"data":{"path":"hello.txt","content":"hello world","mtime":1700000000}}`))
		case "files.write":
			_, _ = w.Write([]byte(`{"success":true,"data":{"path":"hello.txt","mtime":1700000100}}`))
		case "files.rename":
			_, _ = w.Write([]byte(`{"success":true,"data":{"from":"a.txt","to":"b.txt"}}`))
		case "files.extract":
			_, _ = w.Write([]byte(`{"success":true,"data":{"job_id":"0123456789abcdef","path":"archive.zip"}}`))
		case "files.upload":
			data, _ := p["data"].(string)
			size := 0
			if b, err := base64.StdEncoding.DecodeString(data); err == nil {
				size = len(b)
			}
			finalStr := "false"
			if p["final"] == true {
				finalStr = "true"
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"path":"up/file.txt","offset":0,"final":` + finalStr + `,"size":` + strconv.Itoa(size) + `}}`))
		case "files.download":
			_, _ = w.Write([]byte(`{"success":true,"data":{"data":"aGVsbG8gd29ybGQ=","size":11,"eof":true}}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"data":{},"request_id":"t"}`))
		}
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return sa, socket, tokenFile
}

func (sa *sitesAgent) called() []string {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	return append([]string(nil), sa.actions...)
}

func (sa *sitesAgent) param(i int, key string) any {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	if i >= len(sa.params) {
		return nil
	}
	return sa.params[i][key]
}

// sitesEnv builds a server with a recording agent, a customer (bob) who owns
// website 10 (bob.example), and — when isolation — a second customer (alice)
// who owns website 20. Returns the handler, bob's session cookie and DB.
func sitesEnv(t *testing.T, isolation, failAgent bool) (http.Handler, *http.Cookie, *sitesAgent, *sql.DB) {
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
		(2,'customer','Customer','own',''),(5,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),(3,'databases.view','dv','db'),(4,'workers.manage','wm','auto')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (2,1),(2,2),(2,3),(2,4),(5,1)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(2,'alice','a@x','Alice',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'carol','c@x','Carol',?,5,1,'2026-01-01T00:00:00Z','','',NULL)`,
		loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (10,1,1,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,10,'bob.example',1)`)
	if isolation {
		exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
			VALUES (20,1,2,'alice.example','u_alice','/home/whacp/alice.example','/home/whacp/alice.example/public_html','php','8.3.1','active','','')`)
		exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,20,'alice.example',1)`)
	}

	sa, socket, tokenFile := startSitesAgent(t, failAgent)
	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()

	cookie, token := getLogin(t, h)
	// The signed-in state (user + rotated CSRF) is sealed into the cookie the
	// login POST response sets — that one, not the pre-login form cookie.
	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {"bob"}, "password": {loginPassword}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login = %d", rr.Code)
	}
	cookie = rr.Result().Cookies()[0]
	return h, cookie, sa, db
}

func siteFormToken(t *testing.T, h http.Handler, cookie *http.Cookie, path string) (string, *http.Cookie) {
	t.Helper()
	rr := get(t, h, path, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, rr.Code, rr.Body.String())
	}
	m := csrfRe.FindStringSubmatch(rr.Body.String())
	if m == nil {
		t.Fatalf("no CSRF token on %s", path)
	}
	refreshed := cookie
	if cs := rr.Result().Cookies(); len(cs) > 0 {
		refreshed = cs[0]
	}
	return m[1], refreshed
}

func TestWebsitesNewForm(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	rr := get(t, h, "/websites/new", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"Create website", `name="domain"`, `name="php_version"`, "8.3.1", "PHP application"} {
		if !strings.Contains(body, want) {
			t.Errorf("form missing %q", want)
		}
	}
}

func TestWebsitesStoreValidationRerendersWithOldInput(t *testing.T) {
	h, cookie, sa, _ := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/websites/new")
	rr := postForm(h, "/websites/new", url.Values{
		"_token": {token}, "domain": {"no-dots"}, "kind": {"php"},
		"system_user": {"u_x"}, "new_system_user": {"1"},
		"php_version": {"8.3.1"}, "harden": {"on"}, "php_profile": {"recommended"},
		"memory_limit_mb": {"256"}, "upload_max_mb": {"64"}, "max_execution_seconds": {"60"},
		"max_children": {"5"}, "idle_timeout_seconds": {"10"}, "max_requests": {"500"},
		"pm": {"ondemand"}, "client_max_body_mb": {"64"},
	}, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("validation re-render = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Enter a valid domain name") {
		t.Error("field error not rendered")
	}
	if !strings.Contains(body, `value="no-dots"`) {
		t.Error("old input not preserved")
	}
	for _, a := range sa.called() {
		if a == "sites.apply" {
			t.Errorf("agent apply must not be called on a rejected form: %v", sa.called())
		}
	}
}

func TestWebsitesStoreRequiresCSRF(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	rr := postForm(h, "/websites/new", url.Values{"domain": {"x.example"}}, cookie)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing CSRF = %d, want 400", rr.Code)
	}
}

func TestWebsitesStoreHappyPath(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/websites/new")
	rr := postForm(h, "/websites/new", url.Values{
		"_token": {token}, "domain": {"new.example"}, "kind": {"php"},
		"system_user": {"u_new"}, "new_system_user": {"1"},
		"php_version": {"8.3.1"}, "harden": {"on"}, "php_profile": {"recommended"},
		"memory_limit_mb": {"256"}, "upload_max_mb": {"64"}, "max_execution_seconds": {"60"},
		"max_children": {"5"}, "idle_timeout_seconds": {"10"}, "max_requests": {"500"},
		"pm": {"ondemand"}, "client_max_body_mb": {"64"},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/11" {
		t.Fatalf("store = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	acts := sa.called()
	found := false
	for i, a := range acts {
		if a == "sites.apply" {
			found = true
			if sa.param(i, "new_user") != true {
				t.Errorf("apply must carry new_user: %v", sa.params[i])
			}
			if sa.param(i, "domain") != "new.example" {
				t.Errorf("apply domain = %v", sa.param(i, "domain"))
			}
		}
	}
	if !found {
		t.Fatalf("sites.apply never called: %v", acts)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM websites WHERE id = 11`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "active" {
		t.Errorf("status = %q", status)
	}
	var action, result string
	if err := db.QueryRow(`SELECT action, result FROM audit_logs WHERE action = 'website.create'`).Scan(&action, &result); err != nil {
		t.Fatalf("no website.create audit row: %v", err)
	}
	if result != "success" {
		t.Errorf("audit result = %q", result)
	}
}

func TestWebsitesStorePlanLimitRerenders(t *testing.T) {
	// bob's plan is max_websites=5 and he owns 1 site — tighten it to 1.
	h, cookie, sa, dbh := sitesEnv(t, false, false)
	if _, err := dbh.Exec(`UPDATE plans SET max_websites = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	token, cookie := siteFormToken(t, h, cookie, "/websites/new")
	rr := postForm(h, "/websites/new", url.Values{
		"_token": {token}, "domain": {"second.example"}, "kind": {"php"},
		"system_user": {"u_s"}, "new_system_user": {"1"}, "php_version": {"8.3.1"},
		"memory_limit_mb": {"256"}, "upload_max_mb": {"64"}, "max_execution_seconds": {"60"},
		"max_children": {"5"}, "pm": {"ondemand"}, "client_max_body_mb": {"64"},
	}, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("limit re-render = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "allows at most 1 websites") {
		t.Errorf("limit message missing: %s", rr.Body.String())
	}
	for _, a := range sa.called() {
		if a == "sites.apply" {
			t.Errorf("agent apply must not be called when the plan limit blocks: %v", sa.called())
		}
	}
}

func TestWebsitesStoreAgentFailureFlashes(t *testing.T) {
	h, cookie, _, db := sitesEnv(t, false, true)
	token, cookie := siteFormToken(t, h, cookie, "/websites/new")
	rr := postForm(h, "/websites/new", url.Values{
		"_token": {token}, "domain": {"x.example"}, "kind": {"php"},
		"system_user": {"u_x"}, "new_system_user": {"1"}, "php_version": {"8.3.1"},
		"memory_limit_mb": {"256"}, "upload_max_mb": {"64"}, "max_execution_seconds": {"60"},
		"max_children": {"5"}, "pm": {"ondemand"}, "client_max_body_mb": {"64"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("agent failure = %d, want redirect with flash", rr.Code)
	}
	// The failed create must not leave a row behind.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM websites`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("websites = %d after failed create (want 1)", n)
	}
	var result string
	if err := db.QueryRow(`SELECT result FROM audit_logs WHERE action = 'website.create'`).Scan(&result); err != nil {
		t.Fatalf("no failure audit row: %v", err)
	}
	if result != "failure" {
		t.Errorf("audit = %q", result)
	}
}

func TestWebsiteRoutesAreTenantScoped(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, true, false) // alice owns website 20
	for _, path := range []string{
		"/sites/20",
		"/sites/20/settings",
		"/sites/20/subdomains",
		"/sites/20/addons",
		"/sites/999",
	} {
		if rr := get(t, h, path, cookie); rr.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 (other tenant / missing)", path, rr.Code)
		}
	}
	// bob's own site is visible.
	if rr := get(t, h, "/sites/10", cookie); rr.Code != http.StatusOK {
		t.Errorf("GET /sites/10 = %d", rr.Code)
	}
}

func TestWebsiteToggle(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/sites/10")
	rr := postForm(h, "/sites/10/toggle", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("toggle = %d", rr.Code)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM websites WHERE id = 10`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "disabled" {
		t.Errorf("status = %q, want disabled", status)
	}
	last := sa.called()
	i := len(last) - 1
	if last[i] != "sites.apply" || sa.param(i, "enabled") != false {
		t.Errorf("agent apply wrong: %s %v", last[i], sa.param(i, "enabled"))
	}
	var action string
	if err := db.QueryRow(`SELECT action FROM audit_logs WHERE action = 'website.disable'`).Scan(&action); err != nil {
		t.Fatalf("no website.disable audit row: %v", err)
	}
	// Toggle back on.
	token2, cookie2 := siteFormToken(t, h, cookie, "/sites/10")
	if rr := postForm(h, "/sites/10/toggle", url.Values{"_token": {token2}}, cookie2); rr.Code != http.StatusSeeOther {
		t.Fatalf("toggle-on = %d", rr.Code)
	}
	if err := db.QueryRow(`SELECT status FROM websites WHERE id = 10`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "active" {
		t.Errorf("status = %q, want active", status)
	}
}

func TestWebsiteToggleRequiresCSRF(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	if rr := postForm(h, "/sites/10/toggle", url.Values{}, cookie); rr.Code != http.StatusBadRequest {
		t.Fatalf("toggle without CSRF = %d, want 400", rr.Code)
	}
}

func TestWebsiteDeleteRequiresTypedConfirmation(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/sites/10")
	// Wrong confirmation: the site survives.
	rr := postForm(h, "/sites/10/delete", url.Values{"_token": {token}, "confirm_domain": {"nope"}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("bad confirm = %d", rr.Code)
	}
	if n, _ := countRows(t, db, `SELECT COUNT(*) FROM websites WHERE id = 10`); n != 1 {
		t.Fatal("site deleted without correct confirmation")
	}
	for _, a := range sa.called() {
		if a == "sites.delete" {
			t.Errorf("agent must not be called on a bad confirm: %v", sa.called())
		}
	}
	// Correct confirmation: the site goes.
	token2, cookie2 := siteFormToken(t, h, cookie, "/sites/10")
	rr = postForm(h, "/sites/10/delete", url.Values{
		"_token": {token2}, "confirm_domain": {"bob.example"}, "remove_files": {"1"},
	}, cookie2)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/websites" {
		t.Fatalf("delete = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	if n, _ := countRows(t, db, `SELECT COUNT(*) FROM websites WHERE id = 10`); n != 0 {
		t.Fatal("site row still present")
	}
	acts := sa.called()
	if len(acts) != 1 || acts[0] != "sites.delete" {
		t.Fatalf("agent actions = %v", acts)
	}
	if sa.param(0, "remove_files") != true || sa.param(0, "remove_user") != false {
		t.Errorf("agent delete params wrong: %v", sa.params[0])
	}
	var result string
	if err := db.QueryRow(`SELECT result FROM audit_logs WHERE action = 'website.delete'`).Scan(&result); err != nil {
		t.Fatalf("no website.delete audit row: %v", err)
	}
}

func TestSubdomainStore(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/sites/10/subdomains")
	rr := postForm(h, "/sites/10/subdomains", url.Values{"_token": {token}, "subdomain": {"blog"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/subdomains" {
		t.Fatalf("store subdomain = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	var domain string
	if err := db.QueryRow(`SELECT domain FROM websites WHERE parent_id = 10`).Scan(&domain); err != nil {
		t.Fatalf("child missing: %v", err)
	}
	if domain != "blog.bob.example" {
		t.Errorf("child domain = %q", domain)
	}
	// "www" is the website's own alias, never a subdomain.
	token2, cookie2 := siteFormToken(t, h, cookie, "/sites/10/subdomains")
	rr = postForm(h, "/sites/10/subdomains", url.Values{"_token": {token2}, "subdomain": {"www"}}, cookie2)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Use letters, digits and hyphens") {
		t.Errorf("www subdomain must be rejected: %d", rr.Code)
	}
	apply := 0
	for _, a := range sa.called() {
		if a == "sites.apply" {
			apply++
		}
	}
	if apply != 1 {
		t.Errorf("agent apply count = %d (want 1 for the first child)", apply)
	}
}

func TestSubdomainStoreRequiresCSRF(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	if rr := postForm(h, "/sites/10/subdomains", url.Values{"subdomain": {"blog"}}, cookie); rr.Code != http.StatusBadRequest {
		t.Fatalf("subdomain store without CSRF = %d, want 400", rr.Code)
	}
}

func TestAddonStore(t *testing.T) {
	h, cookie, _, db := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/sites/10/addons")
	rr := postForm(h, "/sites/10/addons", url.Values{"_token": {token}, "domain": {"ADDON.example"}, "www": {"on"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/addons" {
		t.Fatalf("store addon = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	var domain, alias string
	if err := db.QueryRow(`SELECT domain FROM websites WHERE parent_id = 10`).Scan(&domain); err != nil {
		t.Fatalf("addon missing: %v", err)
	}
	if domain != "addon.example" {
		t.Errorf("addon domain = %q", domain)
	}
	if err := db.QueryRow(`SELECT name FROM domains WHERE name = 'www.addon.example'`).Scan(&alias); err != nil {
		t.Fatalf("www alias missing: %v", err)
	}
	// A subdomain of the parent is not an addon.
	token2, cookie2 := siteFormToken(t, h, cookie, "/sites/10/addons")
	rr = postForm(h, "/sites/10/addons", url.Values{"_token": {token2}, "domain": {"x.bob.example"}}, cookie2)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "add it as a subdomain") {
		t.Errorf("addon under parent must be rejected: %d", rr.Code)
	}
}

func TestWebsitesNewRequiresManagePermission(t *testing.T) {
	// carol has websites.view only: the create form and its POST are denied.
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
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES (5,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES (1,'websites.view','v','web')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (5,1)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at)
		VALUES (1,'carol','c@x','Carol',?,5,1,'2026-01-01T00:00:00Z','','')`, loginHash)

	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(config.Config{Production: true, KeyFile: kf}, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	cookie, token := getLogin(t, h)
	if rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {"carol"}, "password": {loginPassword}}, cookie); rr.Code != http.StatusSeeOther {
		t.Fatalf("login = %d", rr.Code)
	}
	if rr := get(t, h, "/websites/new", cookie); rr.Code != http.StatusForbidden {
		t.Errorf("GET /websites/new = %d, want 403", rr.Code)
	}
	token2, _ := csrfTokenFromLogin(t, h, cookie)
	rr := postForm(h, "/websites/new", url.Values{"_token": {token2}, "domain": {"x.example"}}, cookie)
	if rr.Code != http.StatusForbidden {
		t.Errorf("POST /websites/new = %d, want 403", rr.Code)
	}
}

func countRows(t *testing.T, db *sql.DB, q string) (int, error) {
	t.Helper()
	var n int
	err := db.QueryRow(q).Scan(&n)
	return n, err
}

// csrfTokenFromLogin returns a CSRF token for an authenticated session by
// loading the website list (any form page mints one).
func csrfTokenFromLogin(t *testing.T, h http.Handler, cookie *http.Cookie) (string, *http.Cookie) {
	t.Helper()
	return siteFormToken(t, h, cookie, "/websites")
}

