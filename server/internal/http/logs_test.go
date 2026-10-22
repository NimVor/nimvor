package http

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// logsAgent is a recording fake for the logs agent actions.
type logsAgent struct {
	mu          sync.Mutex
	calls       []string
	params      []map[string]any
	failActions map[string]string
}

func (la *logsAgent) called() []string {
	la.mu.Lock()
	defer la.mu.Unlock()
	return append([]string(nil), la.calls...)
}

// param returns a parameter of the i-th call (nil when absent).
func (la *logsAgent) param(i int, key string) any {
	la.mu.Lock()
	defer la.mu.Unlock()
	if i >= len(la.params) {
		return nil
	}
	return la.params[i][key]
}

func lastCallIndex(actions []string, action string) int {
	for i := len(actions) - 1; i >= 0; i-- {
		if actions[i] == action {
			return i
		}
	}
	return -1
}

func startLogsAgent(t *testing.T) (*logsAgent, string, string) {
	t.Helper()
	la := &logsAgent{}
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
		la.mu.Lock()
		la.calls = append(la.calls, action)
		la.params = append(la.params, p)
		msg, fail := la.failActions[action]
		la.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if fail {
			_, _ = w.Write([]byte(`{"success":false,"error":{"code":"boom","message":"` + msg + `","request_id":"t"}}`))
			return
		}
		switch action {
		case "logs.sources":
			// A domain in the params means the per-site list; otherwise the
			// server-wide list (one source per category, like the agent's).
			if _, hasDomain := p["domain"]; hasDomain {
				_, _ = w.Write([]byte(`{"success":true,"data":[
					{"id":"site:bob.example:access.log","label":"access.log","group":"bob.example","kind":"file","size":2048,"exists":true},
					{"id":"site:bob.example:error.log","label":"error.log","group":"bob.example","kind":"file","size":0,"exists":true}],
				"request_id":"t"}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":[
				{"id":"nginx:access","label":"Nginx access log","group":"Web server","kind":"file","size":4096,"exists":true},
				{"id":"nginx:error","label":"Nginx error log","group":"Web server","kind":"file","size":0,"exists":true},
				{"id":"php:8.3.1","label":"PHP 8.3.1 FPM","group":"PHP","kind":"file","size":2048,"exists":true},
				{"id":"system:mail","label":"mail","group":"System","kind":"file","size":512,"exists":true},
				{"id":"system:mysql","label":"mysql","group":"System","kind":"file","size":1024,"exists":true},
				{"id":"system:auth","label":"auth","group":"System","kind":"file","size":0,"exists":true},
				{"id":"system:syslog","label":"syslog","group":"System","kind":"file","size":256,"exists":true},
				{"id":"system:letsencrypt","label":"letsencrypt","group":"System","kind":"file","size":0,"exists":true},
				{"id":"journal:whacp-agent.service","label":"whacp-agent.service","group":"Services","kind":"journal","size":0,"exists":true},
				{"id":"journal:redis.service","label":"redis.service","group":"Services","kind":"journal","size":0,"exists":true}],
			"request_id":"t"}`))
		case "logs.tail":
			_, _ = w.Write([]byte(`{"success":true,"data":{"lines":["l1","l2"],"offset":42,"size":100},"request_id":"t"}`))
		case "logs.download":
			offset := int64(0)
			if f, ok := p["offset"].(float64); ok {
				offset = int64(f)
			}
			if offset == 0 {
				_, _ = w.Write([]byte(`{"success":true,"data":{"data":"` + base64.StdEncoding.EncodeToString([]byte("hello")) + `","size":10,"eof":false,"name":"access.log"},"request_id":"t"}`))
			} else {
				_, _ = w.Write([]byte(`{"success":true,"data":{"data":"` + base64.StdEncoding.EncodeToString([]byte("world")) + `","size":10,"eof":true,"name":"access.log"},"request_id":"t"}`))
			}
		default:
			_, _ = w.Write([]byte(`{"success":true,"data":{},"request_id":"t"}`))
		}
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return la, socket, tokenFile
}

// logsEnv builds a server with an administrator, two customers (bob owns
// site 10 bob.example, alice owns site 20 alice.example, each with one
// worker) and a viewer (no logs.view). panelLog is written to a temp file
// and configured as the panel log. Returns the handler, the admin cookie,
// the agent and the DB.
func logsEnv(t *testing.T, panelLog string) (http.Handler, *http.Cookie, *logsAgent, *sql.DB) {
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
		(1,'administrator','Admin','all',''),
		(2,'customer','Customer','own',''),
		(5,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),(3,'logs.view','lv','monitor')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES
		(1,1),(1,2),(1,3),(2,1),(2,2),(2,3),(5,1)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'admin','adm@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'alice','a@x','Alice',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(4,'eve','e@x','Eve',?,5,1,'2026-01-01T00:00:00Z','','',NULL)`,
		loginHash, loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','',''),
		(20,1,3,'alice.example','u_alice','/home/whacp/alice.example','/home/whacp/alice.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id, website_id, name, is_primary) VALUES
		(1,10,'bob.example',1),(1,20,'alice.example',1)`)
	exec(`INSERT INTO workers (id, server_id, website_id, slug, name, command, restart, enabled, created_at, updated_at)
		VALUES (1, 1, 10, 'bob-app', 'Bob worker', 'node app.js', 'always', 1, '', ''),
		(2, 1, 20, 'alice-app', 'Alice worker', 'node app.js', 'always', 1, '', '')`)

	la, socket, tokenFile := startLogsAgent(t)
	logFile := filepath.Join(t.TempDir(), "panel.log")
	if err := os.WriteFile(logFile, []byte(panelLog), 0o600); err != nil {
		t.Fatal(err)
	}
	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile, LogFile: logFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	return h, loginAs(t, h, "admin"), la, db
}

// TestLogsPageRequiresPermission: a role without logs.view is refused.
func TestLogsPageRequiresPermission(t *testing.T) {
	h, _, _, _ := logsEnv(t, "")
	eve := loginAs(t, h, "eve")
	if rr := get(t, h, "/sites/10/logs", eve); rr.Code != http.StatusForbidden {
		t.Fatalf("GET /sites/10/logs (viewer) = %d, want 403", rr.Code)
	}
}

// TestLogsPageTenant: a foreign website's logs read as not found.
func TestLogsPageTenant(t *testing.T) {
	h, _, _, _ := logsEnv(t, "")
	alice := loginAs(t, h, "alice")
	if rr := get(t, h, "/sites/10/logs", alice); rr.Code != http.StatusNotFound {
		t.Fatalf("GET /sites/10/logs (foreign) = %d, want 404", rr.Code)
	}
}

// TestLogsPage: the site's own sources from the agent plus one entry per
// worker, grouped for the viewer.
func TestLogsPage(t *testing.T) {
	h, _, _, _ := logsEnv(t, "")
	bob := loginAs(t, h, "bob")
	rr := get(t, h, "/sites/10/logs", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /sites/10/logs = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`value="site:bob.example:access.log"`,
		`value="site:bob.example:error.log"`,
		`value="journal:whacp-worker-bob-app.service"`,
		"Worker: Bob worker",
		`label="Workers"`,
		"access.log (2.0 KB)",
		`data-tail="/logs/tail"`,
		`data-download="/logs/download"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("page missing %q", want)
		}
	}
	if strings.Contains(body, "alice") || strings.Contains(body, "journal:whacp-worker-alice-app") {
		t.Fatal("the page listed another tenant's worker")
	}
}

// TestLogsPageAgentError: the agent being down is an alert, not a 500 — the
// worker entries still render.
func TestLogsPageAgentError(t *testing.T) {
	h, _, la, _ := logsEnv(t, "")
	la.failActions = map[string]string{"logs.sources": "the agent is down"}
	bob := loginAs(t, h, "bob")
	rr := get(t, h, "/sites/10/logs", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /sites/10/logs (agent down) = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "The server could not list the logs:") || !strings.Contains(body, "the agent is down") {
		t.Fatalf("agent error alert missing: %s", body)
	}
	if !strings.Contains(body, "journal:whacp-worker-bob-app.service") {
		t.Fatal("worker entries missing despite the agent failure")
	}
}

// TestLogsTailAuthorization: the per-source allowed() matrix.
func TestLogsTailAuthorization(t *testing.T) {
	const panel = "alpha\nbeta\n"
	h, admin, _, _ := logsEnv(t, panel)
	bob := loginAs(t, h, "bob")
	cases := []struct {
		name   string
		user   *http.Cookie
		source string
		want   int
	}{
		{"own site log", bob, "site:bob.example:access.log", http.StatusOK},
		{"foreign site log", bob, "site:alice.example:access.log", http.StatusForbidden},
		{"unknown domain", bob, "site:unknown.example:access.log", http.StatusForbidden},
		{"server log", bob, "nginx:access", http.StatusForbidden},
		{"own worker journal", bob, "journal:whacp-worker-bob-app.service", http.StatusOK},
		{"foreign worker journal", bob, "journal:whacp-worker-alice-app.service", http.StatusForbidden},
		{"other unit journal", bob, "journal:other-unit.service", http.StatusForbidden},
		{"panel log", bob, "panel", http.StatusForbidden},
		{"staff: foreign site log", admin, "site:alice.example:access.log", http.StatusOK},
		{"staff: server log", admin, "nginx:access", http.StatusOK},
		{"staff: panel log", admin, "panel", http.StatusOK},
	}
	for _, c := range cases {
		rr := get(t, h, "/logs/tail?source="+c.source, c.user)
		if rr.Code != c.want {
			t.Fatalf("%s: tail source=%q = %d, want %d", c.name, c.source, rr.Code, c.want)
		}
	}
	// The refusal answers JSON the viewer can display.
	rr := get(t, h, "/logs/tail?source=nginx:access", bob)
	if !strings.Contains(rr.Body.String(), `"You may not read this log."`) {
		t.Fatalf("403 body = %s", rr.Body.String())
	}
}

// TestLogsTailParams: lines clamping and the follow parameters reach the
// agent; an agent failure is a 502.
func TestLogsTailParams(t *testing.T) {
	h, _, la, _ := logsEnv(t, "")
	bob := loginAs(t, h, "bob")
	src := "site:bob.example:access.log"
	get(t, h, "/logs/tail?source="+src, bob)
	get(t, h, "/logs/tail?source="+src+"&lines=5", bob)
	get(t, h, "/logs/tail?source="+src+"&lines=99999", bob)
	get(t, h, "/logs/tail?source="+src+"&lines=&filter=foo&follow=1&offset=7", bob)

	// lines: default 200, clamped up 10, clamped down 5000, "" -> 10.
	var lines []float64
	for _, a := range la.called() {
		if a == "logs.tail" {
			lines = append(lines, la.param(len(lines), "lines").(float64))
		}
	}
	if lines[0] != 200 || lines[1] != 10 || lines[2] != 5000 || lines[3] != 10 {
		t.Fatalf("lines = %v, want [200 10 5000 10]", lines)
	}
	if la.param(3, "filter") != "foo" || la.param(3, "follow") != true || la.param(3, "offset") != float64(7) {
		t.Fatalf("follow params = %v %v %v", la.param(3, "filter"), la.param(3, "follow"), la.param(3, "offset"))
	}
	// A successful tail passes the agent's data through.
	rr := get(t, h, "/logs/tail?source="+src, bob)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `{"success":true,"data":{"lines":["l1","l2"]`) {
		t.Fatalf("tail = %d %s", rr.Code, rr.Body.String())
	}
	// An agent failure is a 502 with the agent's message.
	la.failActions = map[string]string{"logs.tail": "the journal could not be read"}
	rr = get(t, h, "/logs/tail?source="+src, bob)
	if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "the journal could not be read") {
		t.Fatalf("agent-failure tail = %d %s", rr.Code, rr.Body.String())
	}
}

// TestLogsPanelTail: the panel log is staff-only and tails the file like
// PHP's panelTail (last n, case-insensitive filter, follow from offset).
func TestLogsPanelTail(t *testing.T) {
	panel := "alpha\nbeta\nalpha line\n" // 22 bytes
	h, admin, _, _ := logsEnv(t, panel)
	bob := loginAs(t, h, "bob")

	rr := get(t, h, "/logs/tail?source=panel", admin)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"lines":["alpha","beta","alpha line"]`) {
		t.Fatalf("panel tail = %d %s", rr.Code, rr.Body.String())
	}
	rr = get(t, h, "/logs/tail?source=panel&filter=ALPHA", admin)
	if !strings.Contains(rr.Body.String(), `"lines":["alpha","alpha line"]`) {
		t.Fatalf("panel filter = %s", rr.Body.String())
	}
	rr = get(t, h, "/logs/tail?source=panel&lines=2", admin)
	if !strings.Contains(rr.Body.String(), `"lines":["beta","alpha line"]`) {
		t.Fatalf("panel lines=2 = %s", rr.Body.String())
	}
	// follow from 0: everything, and the new offset.
	rr = get(t, h, "/logs/tail?source=panel&follow=1&offset=0", admin)
	body := rr.Body.String()
	if !strings.Contains(body, `"lines":["alpha","beta","alpha line"]`) || !strings.Contains(body, `"offset":22`) {
		t.Fatalf("panel follow = %s", body)
	}
	// Bob cannot read it (asserted again on the tail endpoint).
	if rr := get(t, h, "/logs/tail?source=panel", bob); rr.Code != http.StatusForbidden {
		t.Fatalf("panel tail (customer) = %d, want 403", rr.Code)
	}
}

// TestLogsDownload: the file streams in chunks with the download headers;
// cross-tenant sources, the panel log and agent failures are 404.
func TestLogsDownload(t *testing.T) {
	h, admin, la, _ := logsEnv(t, "")
	bob := loginAs(t, h, "bob")

	rr := get(t, h, "/logs/download?source=site:bob.example:access.log", bob)
	if rr.Code != http.StatusOK || rr.Body.String() != "helloworld" {
		t.Fatalf("download = %d %q", rr.Code, rr.Body.String())
	}
	if cd := rr.Header().Get("Content-Disposition"); cd != `attachment; filename="access.log"` {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	// The two chunks were fetched at offset 0 and 5.
	var offsets []float64
	for i, a := range la.called() {
		if a == "logs.download" {
			offsets = append(offsets, la.param(i, "offset").(float64))
		}
	}
	if len(offsets) != 2 || offsets[0] != 0 || offsets[1] != 5 {
		t.Fatalf("logs.download offsets = %v, want [0 5]", offsets)
	}
	if rr := get(t, h, "/logs/download?source=site:alice.example:access.log", bob); rr.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant download = %d, want 404", rr.Code)
	}
	if rr := get(t, h, "/logs/download?source=panel", admin); rr.Code != http.StatusNotFound {
		t.Fatalf("panel download = %d, want 404", rr.Code)
	}
	la.failActions = map[string]string{"logs.download": "the log file is not readable"}
	if rr := get(t, h, "/logs/download?source=site:bob.example:access.log", admin); rr.Code != http.StatusNotFound {
		t.Fatalf("agent-failure download = %d, want 404", rr.Code)
	}
}

// TestLogsIndexRequiresStaff: server logs are for roles with server-wide
// scope — a customer with logs.view gets the 403 page.
func TestLogsIndexRequiresStaff(t *testing.T) {
	h, _, _, _ := logsEnv(t, "")
	bob := loginAs(t, h, "bob")
	rr := get(t, h, "/logs", bob)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("GET /logs (customer) = %d, want 403", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Server logs are only available to administrators.") {
		t.Fatalf("403 page = %s", rr.Body.String())
	}
}

// TestLogsIndexRequiresPermission: a role without logs.view is refused.
func TestLogsIndexRequiresPermission(t *testing.T) {
	h, _, _, _ := logsEnv(t, "")
	eve := loginAs(t, h, "eve")
	if rr := get(t, h, "/logs", eve); rr.Code != http.StatusForbidden {
		t.Fatalf("GET /logs (viewer) = %d, want 403", rr.Code)
	}
}

// TestLogsIndexPage: the server-wide sources from the agent plus the panel
// log, grouped, with the category links.
func TestLogsIndexPage(t *testing.T) {
	h, admin, _, _ := logsEnv(t, "")
	rr := get(t, h, "/logs", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /logs = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`value="nginx:access"`,
		`value="php:8.3.1"`,
		`value="system:mail"`,
		`value="journal:redis.service"`,
		`value="panel"`,
		"WhaCP panel log",
		`label="Web server"`,
		`label="Services"`,
		`label="WhaCP"`,
		`data-tail="/logs/tail"`,
		`data-download="/logs/download"`,
		`/logs?view=web`,
		`/logs?view=whacp`,
		"Web server, PHP, database, system and WhaCP logs",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("page missing %q", want)
		}
	}
	// The per-site sources are not on the global list.
	if strings.Contains(body, "bob.example") {
		t.Fatal("the global list leaked per-site sources")
	}
}

// TestLogsIndexCategoryFilter: ?view=<kind> narrows to that category (and
// renames the page); an unknown view leaves the list untouched.
func TestLogsIndexCategoryFilter(t *testing.T) {
	h, admin, _, _ := logsEnv(t, "")
	rr := get(t, h, "/logs?view=web", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /logs?view=web = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `<h1>Web server logs</h1>`) {
		t.Fatalf("category title missing: %s", body)
	}
	for _, want := range []string{`value="nginx:access"`, `value="nginx:error"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("web view missing %q", want)
		}
	}
	for _, not := range []string{`value="php:8.3.1"`, `value="panel"`, `value="journal:redis.service"`} {
		if strings.Contains(body, not) {
			t.Fatalf("web view leaked %q", not)
		}
	}
	// The whacp category holds the agent journal and the panel log.
	rr = get(t, h, "/logs?view=whacp", admin)
	body = rr.Body.String()
	if !strings.Contains(body, `value="journal:whacp-agent.service"`) || !strings.Contains(body, `value="panel"`) {
		t.Fatalf("whacp view incomplete: %s", body)
	}
	if !strings.Contains(body, `<h1>WhaCP logs</h1>`) {
		t.Fatal("whacp title missing")
	}
	// An unknown view does not filter (PHP: isset check).
	rr = get(t, h, "/logs?view=bogus", admin)
	if !strings.Contains(rr.Body.String(), `value="nginx:access"`) || !strings.Contains(rr.Body.String(), `value="panel"`) {
		t.Fatal("bogus view filtered the list")
	}
}

// TestLogsIndexAgentError: an agent outage is an alert; the panel log is
// still listed (it does not need the agent).
func TestLogsIndexAgentError(t *testing.T) {
	h, admin, la, _ := logsEnv(t, "")
	la.failActions = map[string]string{"logs.sources": "the agent is down"}
	rr := get(t, h, "/logs", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /logs (agent down) = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "The server could not list the logs:") || !strings.Contains(body, "the agent is down") {
		t.Fatalf("agent error alert missing: %s", body)
	}
	if !strings.Contains(body, `value="panel"`) {
		t.Fatal("the panel log is missing despite the agent failure")
	}
}
