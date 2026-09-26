//go:build realhost

package dbmgr

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// Requires a local MariaDB/MySQL reachable as root over its socket.
func TestRealHostDatabaseLifecycle(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := filesystem.Host()
	cfg := config.Default()
	cfg.BackupDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(30*time.Second), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)
	ctx := context.Background()
	do := func(name, params string) any {
		t.Helper()
		a, _ := r.Lookup(name)
		res, err := a.Handler(ctx, json.RawMessage(params))
		if err != nil {
			var ae *actions.Error
			if e, ok := err.(*actions.Error); ok {
				ae = e
			}
			t.Fatalf("%s: %v %+v", name, err, ae)
		}
		return res
	}
	t.Cleanup(func() {
		a, _ := r.Lookup("db.drop")
		a.Handler(ctx, json.RawMessage(`{"name":"whacp_rt","confirm":"whacp_rt"}`))
		a.Handler(ctx, json.RawMessage(`{"name":"whacp_rt2","confirm":"whacp_rt2"}`))
		u, _ := r.Lookup("db.user_drop")
		u.Handler(ctx, json.RawMessage(`{"user":"whacp_rt","host":"localhost"}`))
	})
	do("db.create", `{"name":"whacp_rt"}`)
	do("db.user_create", `{"user":"whacp_rt","host":"localhost","password":"It's-a-S3cret\\pw"}`)
	do("db.grant", `{"user":"whacp_rt","host":"localhost","database":"whacp_rt","privileges":"all"}`)
	c := Client{Env: env}
	// The new user can log in with the tricky password and create a table.
	out, err := env.Runner.RunWith(ctx, "mysql", execx.Opts{Stdin: strings.NewReader("CREATE TABLE t (id INT); INSERT INTO t VALUES (42);")}, "--user=whacp_rt", "--password=It's-a-S3cret\\pw", "whacp_rt")
	if err != nil {
		t.Fatalf("login as new user: %s", out)
	}
	ov := do("db.overview", `{}`).(map[string]any)
	found := false
	for _, u := range ov["users"].([]User) {
		if u.User == "whacp_rt" && u.Grants["whacp_rt"] == "all" {
			found = true
		}
	}
	if !found {
		t.Fatalf("grant not reported: %+v", ov["users"])
	}
	do("db.grant", `{"user":"whacp_rt","host":"localhost","database":"whacp_rt","privileges":"readonly"}`)
	do("db.user_password", `{"user":"whacp_rt","host":"localhost","password":"another-L0ng-password"}`)

	dest := cfg.BackupDir + "/databases/whacp_rt.sql.gz"
	res := do("db.dump", `{"name":"whacp_rt","dest":"`+dest+`"}`).(map[string]any)
	j, _ := jm.Wait(ctx, res["job_id"].(string))
	if j.Status != "succeeded" {
		l, _, _ := jm.Log(j.ID, 0)
		t.Fatalf("dump: %+v\n%s", j, l)
	}
	do("db.create", `{"name":"whacp_rt2"}`)
	res = do("db.import", `{"name":"whacp_rt2","source":"`+dest+`"}`).(map[string]any)
	j, _ = jm.Wait(ctx, res["job_id"].(string))
	if j.Status != "succeeded" {
		l, _, _ := jm.Log(j.ID, 0)
		t.Fatalf("import: %+v\n%s", j, l)
	}
	rows, err := c.Query(ctx, "SELECT id FROM whacp_rt2.t;")
	if err != nil || len(rows) != 1 || rows[0][0] != "42" {
		t.Fatalf("restored data = %v %v", rows, err)
	}

	// An SQL file with the client's shell commands must not run them (the
	// client runs as root): the import stops and nothing is executed.
	marks := t.TempDir()
	evil := cfg.BackupDir + "/databases/evil.sql"
	sql := "CREATE TABLE evil (a INT);\n\\! touch " + marks + "/shell\nsystem touch " + marks + "/system\nINSERT INTO evil VALUES (1);\n"
	if err := os.WriteFile(evil, []byte(sql), 0o600); err != nil {
		t.Fatal(err)
	}
	res = do("db.import", `{"name":"whacp_rt2","source":"`+evil+`"}`).(map[string]any)
	j, _ = jm.Wait(ctx, res["job_id"].(string))
	if left, _ := os.ReadDir(marks); len(left) != 0 || j.Status != "failed" {
		l, _, _ := jm.Log(j.ID, 0)
		t.Fatalf("client commands ran or import did not stop: status %s, created %v\n%s", j.Status, left, l)
	}
}
