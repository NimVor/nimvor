//go:build realhost

package security

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/dbmgr"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

// Against the real database server: each unsafe account is found by name,
// and the server's own accounts (e.g. MariaDB's locked mariadb.sys, which
// has no password) are not reported.
func TestRealHostDatabaseAccountChecks(t *testing.T) {
	if out, err := exec.Command("mysql", "-NBe", "SELECT 1").CombinedOutput(); err != nil {
		t.Skipf("needs a running MySQL/MariaDB with root access: %s", out)
	}
	root := filesystem.Host()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(30*time.Second), jm, log)
	c := dbmgr.Client{Env: env}
	ctx := context.Background()
	byID := func() map[string]Check {
		m := map[string]Check{}
		for _, ch := range databaseAccountChecks(ctx, c, Listening(env)) {
			m[ch.ID] = ch
		}
		return m
	}
	before := byID()
	for _, id := range []string{"database_network", "database_remote_admin", "database_empty_password", "database_global_privileges", "database_grant_option", "database_test"} {
		if _, ok := before[id]; !ok {
			t.Errorf("%s not reported: %+v", id, before)
		}
	}
	if strings.Contains(before["database_empty_password"].Detail, "mariadb.sys") {
		t.Error("the locked system account reported")
	}
	run := func(sql string) {
		if out, err := exec.Command("mysql", "-NBe", sql).CombinedOutput(); err != nil {
			t.Fatalf("%s: %s", sql, out)
		}
	}
	t.Cleanup(func() {
		exec.Command("mysql", "-NBe", "DROP USER IF EXISTS 'wcpa_remote'@'203.0.113.5', 'wcpa_empty'@'localhost', 'wcpa_global'@'localhost', 'wcpa_grant'@'localhost'; DROP DATABASE IF EXISTS wcpa_db; DROP DATABASE IF EXISTS test;").Run()
	})
	run("CREATE USER 'wcpa_remote'@'203.0.113.5' IDENTIFIED BY 'x-Remote-1'; GRANT ALL ON *.* TO 'wcpa_remote'@'203.0.113.5' WITH GRANT OPTION;")
	run("CREATE USER 'wcpa_empty'@'localhost';")
	run("CREATE USER 'wcpa_global'@'localhost' IDENTIFIED BY 'x-Global-1'; GRANT SELECT ON *.* TO 'wcpa_global'@'localhost';")
	run("CREATE DATABASE wcpa_db; CREATE USER 'wcpa_grant'@'localhost' IDENTIFIED BY 'x-Grant-1'; GRANT ALL ON wcpa_db.* TO 'wcpa_grant'@'localhost' WITH GRANT OPTION;")
	run("CREATE DATABASE test;")
	after := byID()
	for id, who := range map[string]string{"database_remote_admin": "wcpa_remote@203.0.113.5", "database_empty_password": "wcpa_empty@localhost", "database_global_privileges": "wcpa_global@localhost", "database_grant_option": "wcpa_grant@localhost"} {
		if ch := after[id]; ch.Status == "pass" || !strings.Contains(ch.Detail, who) {
			t.Errorf("%s: %+v", id, ch)
		}
	}
	if after["database_test"].Status != "warn" {
		t.Errorf("test database: %+v", after["database_test"])
	}
	for _, ch := range after {
		if strings.Contains(ch.Detail, "x-Remote-1") || strings.Contains(ch.Detail, "*") && strings.Contains(ch.Detail, "0x") {
			t.Errorf("secret in %s", ch.ID)
		}
	}
}
