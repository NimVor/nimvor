package sysmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// Changing the host name rewrites /etc/hosts so the name resolves to the
// server's real address: Debian's 127.0.1.1 placeholder is dropped, a
// prior mapping of the name is replaced (not duplicated), and the
// operator's own entries and localhost are kept.
func TestSetHostnameUpdatesEtcHosts(t *testing.T) {
	h := testhost.New(t)
	hosts := filepath.Join(h.Base, "etc/hosts")
	os.WriteFile(hosts, []byte("127.0.0.1\tlocalhost\n127.0.1.1\told.example.com old\n::1\tlocalhost ip6-localhost\n10.9.9.9\tinternal.example.com\n"), 0o644)

	r := actions.NewRegistry(true)
	Register(r, h.Env)

	res, err := call(t, r, "server.set_hostname", `{"hostname":"mail.example.com","ip":"194.146.12.157"}`)
	if err != nil {
		t.Fatalf("set_hostname: %v", err)
	}
	m := res.(map[string]any)
	if m["hostname"] != "mail.example.com" || m["ip"] != "194.146.12.157" || m["hosts_updated"] != true {
		t.Fatalf("result = %+v", m)
	}
	if !h.Runner.Called("hostnamectl set-hostname -- mail.example.com") {
		t.Fatalf("hostnamectl not called: %v", h.Runner.Calls)
	}

	b, err := os.ReadFile(hosts)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(got, "127.0.1.1") {
		t.Errorf("Debian placeholder 127.0.1.1 left behind:\n%s", got)
	}
	if strings.Contains(got, "old.example.com") {
		t.Errorf("stale host name not removed:\n%s", got)
	}
	if !strings.Contains(got, "194.146.12.157\tmail.example.com mail") {
		t.Errorf("name not mapped to the server address:\n%s", got)
	}
	for _, keep := range []string{"127.0.0.1\tlocalhost", "::1\tlocalhost ip6-localhost", "10.9.9.9\tinternal.example.com"} {
		if !strings.Contains(got, keep) {
			t.Errorf("dropped %q:\n%s", keep, got)
		}
	}

	// Running again with the same values changes nothing.
	res2, err := call(t, r, "server.set_hostname", `{"hostname":"mail.example.com","ip":"194.146.12.157"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res2.(map[string]any)["hosts_updated"] != false {
		t.Errorf("second apply should be idempotent: %+v", res2)
	}
}

// Without an address, the name still resolves locally via the 127.0.1.1
// placeholder (better than not resolving at all), and a loopback address
// is refused.
func TestSetHostnameFallbackAndRejectsLoopback(t *testing.T) {
	h := testhost.New(t)
	hosts := filepath.Join(h.Base, "etc/hosts")
	os.WriteFile(hosts, []byte("127.0.0.1\tlocalhost\n"), 0o644)
	r := actions.NewRegistry(true)
	Register(r, h.Env)

	if _, err := call(t, r, "server.set_hostname", `{"hostname":"web1.example.com","ip":"127.0.0.1"}`); err == nil {
		t.Error("accepted a loopback address")
	}

	// With no address at all, applyEtcHosts falls back to the 127.0.1.1
	// placeholder so the name still resolves locally.
	if _, err := applyEtcHosts(h.Env, "web1.example.com", ""); err != nil {
		t.Fatalf("applyEtcHosts: %v", err)
	}
	b, _ := os.ReadFile(hosts)
	if !strings.Contains(string(b), "127.0.1.1\tweb1.example.com web1") {
		t.Errorf("no local fallback mapping:\n%s", string(b))
	}
}
