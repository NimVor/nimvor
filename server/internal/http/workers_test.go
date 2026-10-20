package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

func TestSiteWorkersScopedAndForeign404(t *testing.T) {
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
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES (1,'workers.manage','v','H'),(2,'websites.view','v','H')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (2,1),(2,2)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'custA','ca@x','Customer A',?,2,1,'2026-01-01T00:00:00Z','',''),
		(2,'custB','cb@x','Customer B',?,2,1,'2026-01-01T00:00:00Z','','')`, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,status,system_user,root,document_root,kind,created_at,updated_at) VALUES
		(10,1,1,'a.example','active','ua','/srv/a','/srv/a/pub','php','',''),
		(11,1,2,'b.example','active','ub','/srv/b','/srv/b/pub','php','','')`)
	exec(`INSERT INTO workers (id,server_id,website_id,slug,name,directory,command,restart,procs,runtime,enabled,created_at,updated_at) VALUES
		(1,1,10,'queue','Queue worker','/srv/a','php artisan queue:work','always',2,'php',1,'',''),
		(2,1,11,'bsecret','B secret worker','/srv/b','php b.php','always',1,'php',1,'','')`)

	s, err := New(testConfig(t), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	c, tok := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {tok}, "username": {"custA"}, "password": {loginPassword}}, c)
	cookie := rr.Result().Cookies()[0]

	body := get(t, h, "/sites/10/workers", cookie).Body.String()
	if !strings.Contains(body, "Queue worker") {
		t.Fatalf("custA missing own worker:\n%s", body)
	}
	foreign := get(t, h, "/sites/11/workers", cookie)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("custA viewing custB's workers = %d, want 404", foreign.Code)
	}
	if strings.Contains(foreign.Body.String(), "B secret worker") {
		t.Fatalf("TENANT LEAK: custA saw custB's worker:\n%s", foreign.Body.String())
	}
}
