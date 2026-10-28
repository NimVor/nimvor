package mongomgr

import (
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/execx"
)

// TestAdminPasswordAndAuthToggle covers the guarded authentication flow: set an
// admin password (which stores the connection string), then enable auth; and
// the guardrail that refuses to enable auth before an admin exists.
func TestAdminPasswordAndAuthToggle(t *testing.T) {
	h, r := setup(t)
	writeHostFile(t, h, ConfigFile, "net:\n  port: 27017\n  bindIp: 127.0.0.1\nsecurity:\n  authorization: disabled\n")
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "systemctl" {
			for _, a := range args {
				if a == "is-active" {
					return "active", nil
				}
			}
			return "", nil
		}
		return "print('ok')", nil
	}

	// Enabling auth before any admin exists is refused.
	if _, err := call(t, r, "mongo.auth_set", `{"enable":true}`); err == nil {
		t.Fatal("enabling auth without an admin must be refused")
	}

	// Setting the admin password runs a secret (stdin) script and stores the
	// connection string with the password percent-encoded out of the command line.
	if _, err := call(t, r, "mongo.admin_password", `{"password":"s3cr3t-admin-pw"}`); err != nil {
		t.Fatalf("admin_password: %v", err)
	}
	if n := len(h.Runner.Stdins); n == 0 || !strings.Contains(h.Runner.Stdins[n-1], "createUser") {
		t.Fatalf("admin script not fed on stdin: %v", h.Runner.Stdins)
	}
	uri := strings.TrimSpace(readHostFile(t, h, AdminURIFile))
	if !strings.Contains(uri, "nimvorAdmin:s3cr3t-admin-pw@127.0.0.1:27017") || !strings.Contains(uri, "authSource=admin") {
		t.Fatalf("admin URI: %q", uri)
	}
	for _, c := range h.Runner.Calls {
		if strings.Contains(c, "s3cr3t-admin-pw") {
			t.Errorf("the password must not appear on the command line: %s", c)
		}
	}

	// A too-short password is rejected.
	if _, err := call(t, r, "mongo.admin_password", `{"password":"short"}`); err == nil {
		t.Error("a short admin password must be rejected")
	}

	// Now that an admin exists, auth can be enabled: the config file gains
	// authorization: enabled and mongod is restarted.
	if _, err := call(t, r, "mongo.auth_set", `{"enable":true}`); err != nil {
		t.Fatalf("auth_set enable: %v", err)
	}
	if !strings.Contains(readHostFile(t, h, ConfigFile), "authorization: enabled") {
		t.Fatalf("auth not enabled in config: %s", readHostFile(t, h, ConfigFile))
	}

	// Disabling auth flips it back and drops the stored credentials.
	if _, err := call(t, r, "mongo.auth_set", `{"enable":false}`); err != nil {
		t.Fatalf("auth_set disable: %v", err)
	}
	if !strings.Contains(readHostFile(t, h, ConfigFile), "authorization: disabled") {
		t.Fatalf("auth not disabled in config: %s", readHostFile(t, h, ConfigFile))
	}
	if h.Env.Root.Exists(AdminURIFile) {
		t.Error("the stored admin credentials should be removed when auth is off")
	}
}
