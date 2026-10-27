package http

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
)

// fakeRemoteAgent is a TLS agent recording the actions it is asked for.
type fakeRemoteAgent struct {
	*httptest.Server
	mu      sync.Mutex
	actions []string
	auth    []string
}

func newFakeRemoteAgent(t *testing.T) *fakeRemoteAgent {
	t.Helper()
	f := &fakeRemoteAgent{}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		switch {
		case r.URL.Path == "/v1/health":
			_, _ = w.Write([]byte(`{"success":true,"data":{"version":"` + PanelVersion + `"}}`))
		case strings.HasPrefix(r.URL.Path, "/v1/actions/"):
			action := strings.TrimPrefix(r.URL.Path, "/v1/actions/")
			f.mu.Lock()
			f.actions = append(f.actions, action)
			f.mu.Unlock()
			if action == "system.info" {
				_, _ = w.Write([]byte(`{"success":true,"data":{"system":{"hostname":"web2"}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"installed":false}}`))
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeRemoteAgent) did(action string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.actions {
		if a == action {
			return true
		}
	}
	return false
}

func TestRemoteServerConnectSwitchAndRemove(t *testing.T) {
	s, db, local := rolesEnvServerAgent(t, true)
	h := s.Handler()
	admin := loginAsUser(t, h, "admin")
	remote := newFakeRemoteAgent(t)
	token := strings.Repeat("t", 40)
	form := func(pin string) url.Values {
		return url.Values{"name": {"web2"}, "url": {remote.URL}, "pin": {pin}, "token": {token}}
	}
	post := func(path string, v url.Values) {
		t.Helper()
		tok, c := siteFormToken(t, h, admin, "/servers")
		v.Set("_token", tok)
		rr := postForm(h, path, v, c)
		admin = refreshCookie(c, rr)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("POST %s = %d", path, rr.Code)
		}
	}

	// A wrong pin is refused before anything is stored, and no token is sent.
	post("/servers", form("sha256//AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="))
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM servers WHERE kind = 'remote'`).Scan(&n)
	if n != 0 || len(remote.auth) != 0 {
		t.Fatalf("remote rows = %d, requests with auth = %v", n, remote.auth)
	}
	if page := get(t, h, "/servers", admin).Body.String(); !pageHas(page, "different certificate") {
		t.Errorf("the pin mismatch is not explained: %s", page)
	}

	// The right pin connects; the token is stored encrypted.
	post("/servers", form(agent.PinOf(remote.Certificate())))
	var enc, pin string
	if err := db.QueryRow(`SELECT token_enc, fingerprint FROM servers WHERE name = 'web2'`).Scan(&enc, &pin); err != nil {
		t.Fatal(err)
	}
	if enc == "" || strings.Contains(enc, token) || pin != agent.PinOf(remote.Certificate()) {
		t.Fatalf("stored token %q, pin %q", enc, pin)
	}
	if body := get(t, h, "/servers", admin).Body.String(); strings.Contains(body, enc) || strings.Contains(body, token) {
		t.Error("SECURITY: the servers page shows the token")
	}

	// Check records the state.
	post("/servers/2/check", url.Values{})
	var status string
	_ = db.QueryRow(`SELECT status FROM servers WHERE id = 2`).Scan(&status)
	if status != "online" {
		t.Errorf("status after check = %q", status)
	}

	// Before switching, pages talk to the local agent.
	get(t, h, "/ffmpeg", admin)
	if remote.did("ffmpeg.status") || !containsAction(local.called(), "ffmpeg.status") {
		t.Fatal("the local server was not asked")
	}
	// After switching, to the remote one, with the stored token.
	post("/servers/switch", url.Values{"server_id": {"2"}})
	get(t, h, "/ffmpeg", admin)
	if !remote.did("ffmpeg.status") {
		t.Fatal("the remote server was not asked")
	}
	if got := remote.auth[len(remote.auth)-1]; got != "Bearer "+token {
		t.Errorf("Authorization = %q", got)
	}
	if !pageHas(get(t, h, "/", admin).Body.String(), "web2") {
		t.Error("the server selector is missing")
	}

	// Back to the local server; a server with websites cannot be removed.
	post("/servers/switch", url.Values{"server_id": {"1"}})
	if _, err := db.Exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,enabled,created_at,updated_at)
		VALUES (31,2,1,'r.example','u','/r','/r/p','php','8.3','active',1,'','')`); err != nil {
		t.Fatal(err)
	}
	post("/servers/2/delete", url.Values{})
	_ = db.QueryRow(`SELECT COUNT(*) FROM servers WHERE id = 2`).Scan(&n)
	if n != 1 {
		t.Fatal("a server with websites was removed")
	}
	_, _ = db.Exec(`DELETE FROM websites WHERE id = 31`)
	post("/servers/2/delete", url.Values{})
	_ = db.QueryRow(`SELECT COUNT(*) FROM servers WHERE id = 2`).Scan(&n)
	if n != 0 {
		t.Error("the server was not removed")
	}
}

func TestSwitchToUnknownServerIsRefused(t *testing.T) {
	s, _, _ := rolesEnvServerAgent(t, true)
	h := s.Handler()
	admin := loginAsUser(t, h, "admin")
	tok, c := siteFormToken(t, h, admin, "/servers")
	rr := postForm(h, "/servers/switch", url.Values{"_token": {tok}, "server_id": {"99"}}, c)
	c = refreshCookie(c, rr)
	if page := get(t, h, "/servers", c).Body.String(); !pageHas(page, "That server does not exist.") {
		t.Errorf("no refusal shown: %s", page)
	}
}

func TestSchedulerRunsOnRemoteServers(t *testing.T) {
	s, db, local := rolesEnvServerAgent(t, true)
	remote := newFakeRemoteAgent(t)
	enc, err := s.crypto.Encrypt([]byte(strings.Repeat("t", 40)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO servers (id,name,kind,url,token_enc,fingerprint,status,created_at) VALUES (2,'web2','remote',?,?,?,'online','')`,
		remote.URL, enc, agent.PinOf(remote.Certificate())); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO servers (id,name,kind,url,token_enc,fingerprint,status,created_at) VALUES (3,'gone','remote','https://127.0.0.1:1',?,?,'online','')`,
		enc, agent.PinOf(remote.Certificate())); err != nil {
		t.Fatal(err)
	}
	out := s.RunScheduler(t.Context())
	if !remote.did("modsec.trim") || !containsAction(local.called(), "modsec.trim") {
		t.Errorf("passes ran on remote=%v local=%v", remote.actions, local.called())
	}
	if remote.did("hulk.failures") {
		t.Error("brute force protection ran on a remote server")
	}
	var status string
	_ = db.QueryRow(`SELECT status FROM servers WHERE id = 3`).Scan(&status)
	joined := strings.Join(out, "\n")
	if status != "offline" || !strings.Contains(joined, "[gone] not reachable") {
		t.Errorf("unreachable server: status %q, output:\n%s", status, joined)
	}
}
