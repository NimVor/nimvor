package sysmgr

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// server.hostname_references finds a previous host name wherever it still
// appears in the managed configuration, and flags the mail server as stale
// when Postfix still greets with it.
func TestHostnameReferences(t *testing.T) {
	h := testhost.New(t)
	old := "vmi3612939.contaboserver.net"
	write := func(rel, body string) {
		p := filepath.Join(h.Base, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	write("etc/hostname", "vc100.oursvrs.com\n")
	write("etc/hosts", "127.0.0.1 localhost\n194.146.12.157 "+old+" "+old+"\n")
	write("etc/nginx/sites-available/nimvor-hostname.conf", "server {\n  server_name "+old+";\n}\n")
	write("etc/postfix/main.cf", "myhostname = "+old+"\nmydestination = localhost\n")
	write("etc/bind/nimvor/db.shop.test", "@ IN MX 10 "+old+".\nmail IN A 194.146.12.157\n")
	write("etc/nimvor-mail/aliases", "postmaster: root\n")

	r := actions.NewRegistry(true)
	Register(r, h.Env)
	res, err := call(t, r, "server.hostname_references", `{"old":"`+old+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["current"] != "vc100.oursvrs.com" || m["mail_hostname"] != old || m["mail_stale"] != true {
		t.Fatalf("summary wrong: %+v", m)
	}
	cats := map[string]bool{}
	for _, f := range m["findings"].([]hostnameRef) {
		cats[f.Category] = true
	}
	for _, want := range []string{"hosts", "nginx", "dns", "mail"} {
		if !cats[want] {
			t.Errorf("no %s finding; got %+v", want, m["findings"])
		}
	}

	// A host name that appears nowhere yields no findings and is not stale.
	res2, _ := call(t, r, "server.hostname_references", `{"old":"gone.example.net"}`)
	m2 := res2.(map[string]any)
	if m2["count"].(int) != 0 || m2["mail_stale"] != false {
		t.Fatalf("expected no findings: %+v", m2)
	}

	// An empty or malformed name is refused.
	if _, err := call(t, r, "server.hostname_references", `{"old":""}`); err == nil {
		t.Error("accepted an empty host name")
	}
}
