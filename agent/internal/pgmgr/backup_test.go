package pgmgr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func waitJob(t *testing.T, h *testhost.Host, out any) jobs.Job {
	t.Helper()
	id := asMap(t, out)["job_id"].(string)
	j, err := h.Env.Jobs.Wait(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestBackupRestoreAndMonitor(t *testing.T) {
	if _, err := os.Stat("/usr/bin/pg_dump"); err != nil {
		t.Skip("pg_dump not installed")
	}
	c := startCluster(t)
	h := testhost.New(t)
	h.Env.Runner = execx.NewSystemRunner(2 * time.Minute)
	c.admin(t, h.Env)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	ctx := context.Background()
	mustCall(t, r, "pg.role_create", map[string]any{"name": "owner_a", "password": pwA})
	mustCall(t, r, "pg.role_create", map[string]any{"name": "reader_a", "password": pwB})
	mustCall(t, r, "pg.database_create", map[string]any{"name": "db_a", "owner": "owner_a"})
	mustCall(t, r, "pg.database_create", map[string]any{"name": "db_b", "owner": "owner_a"})
	mustCall(t, r, "pg.grant_set", map[string]any{"database": "db_a", "role": "reader_a", "level": "readonly"})
	a, err := c.as("owner_a", pwA, "db_a")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`CREATE TABLE t (id int PRIMARY KEY, v text)`, `INSERT INTO t SELECT g, 'row ' || g FROM generate_series(1, 50) g`} {
		if _, err := a.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		var n int
		if err := a.QueryRow(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// A backup is a checksummed archive, listed for its database only.
	j := waitJob(t, h, mustCall(t, r, "pg.backup_create", map[string]any{"database": "db_a"}))
	if j.Status != jobs.Succeeded {
		l, _, _ := h.Env.Jobs.Log(j.ID, 0)
		t.Fatalf("backup %s: %s\n%s", j.Status, j.Error, l)
	}
	list := asMap(t, mustCall(t, r, "pg.backup_list", map[string]any{"database": "db_a"}))["backups"].([]any)
	if len(list) != 1 {
		t.Fatalf("backups = %v", list)
	}
	b := list[0].(map[string]any)
	file := b["file"].(string)
	if b["size"].(float64) < 100 || len(b["sha256"].(string)) != 64 || !strings.HasSuffix(file, "_db_a.dump") {
		t.Fatalf("backup = %v", b)
	}
	if other := asMap(t, mustCall(t, r, "pg.backup_list", map[string]any{"database": "db_b"}))["backups"].([]any); len(other) != 0 {
		t.Fatalf("db_b lists db_a's backup: %v", other)
	}
	if fi, err := os.Stat(filepath.Join(h.Base, "var/backups/nimvor/postgres", file)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("archive mode: %v %v", fi, err)
	}

	// Change the data, then restore: the data comes back, the access level survives, and the state before is kept.
	if _, err := a.Exec(ctx, `DELETE FROM t WHERE id > 10; DROP TABLE IF EXISTS extra; CREATE TABLE extra (x int)`); err != nil {
		t.Fatal(err)
	}
	if count() != 10 {
		t.Fatalf("setup count = %d", count())
	}
	j = waitJob(t, h, mustCall(t, r, "pg.backup_restore", map[string]any{"database": "db_a", "file": file}))
	if j.Status != jobs.Succeeded {
		l, _, _ := h.Env.Jobs.Log(j.ID, 0)
		t.Fatalf("restore %s: %s\n%s", j.Status, j.Error, l)
	}
	if count() != 50 {
		t.Fatalf("restored count = %d", count())
	}
	safety := j.Result.(map[string]any)["safety_backup"].(string)
	if safety == file {
		t.Fatal("the safety backup overwrote the backup being restored")
	}
	if n := len(asMap(t, mustCall(t, r, "pg.backup_list", map[string]any{"database": "db_a"}))["backups"].([]any)); n != 2 {
		t.Fatalf("expected the original and the safety backup, got %d", n)
	}
	rd, err := c.as("reader_a", pwB, "db_a")
	if err != nil {
		t.Fatalf("reader lost access after restore: %v", err)
	}
	var n int
	if err := rd.QueryRow(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil || n != 50 {
		t.Fatalf("reader after restore: %d %v", n, err)
	}
	rd.Close(ctx)

	// A damaged archive is refused and nothing changes.
	arch := filepath.Join(h.Base, "var/backups/nimvor/postgres", file)
	data, _ := os.ReadFile(arch)
	data[len(data)/2] ^= 0xff
	os.WriteFile(arch, data, 0o600)
	if _, err := a.Exec(ctx, `DELETE FROM t WHERE id > 20`); err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, h, mustCall(t, r, "pg.backup_restore", map[string]any{"database": "db_a", "file": file}))
	if j.Status == jobs.Succeeded || !strings.Contains(j.Error, "checksum") || count() != 20 {
		t.Fatalf("damaged restore: %s %q count=%d", j.Status, j.Error, count())
	}

	// Names are checked: another database's archive, traversal, and non-archives.
	for _, bad := range []map[string]any{
		{"database": "db_b", "file": file},
		{"database": "db_a", "file": "../../etc/passwd"},
		{"database": "db_a", "file": "20260101-000000_db_a.dump.sha256"},
		{"database": "postgres", "file": "x"},
		{"database": "Db A"},
	} {
		for _, act := range []string{"pg.backup_restore", "pg.backup_delete"} {
			if _, err := call(t, r, act, bad); err == nil {
				t.Fatalf("%s accepted %v", act, bad)
			}
		}
	}

	// Retention keeps the newest N; delete removes the archive and its checksum.
	waitJob(t, h, mustCall(t, r, "pg.backup_create", map[string]any{"database": "db_a"}))
	waitJob(t, h, mustCall(t, r, "pg.backup_create", map[string]any{"database": "db_a"}))
	pr := asMap(t, mustCall(t, r, "pg.backup_prune", map[string]any{"database": "db_a", "keep": 2}))
	if len(pr["deleted"].([]any)) != 2 {
		t.Fatalf("prune = %v", pr)
	}
	left := asMap(t, mustCall(t, r, "pg.backup_list", map[string]any{"database": "db_a"}))["backups"].([]any)
	if len(left) != 2 {
		t.Fatalf("after prune: %v", left)
	}
	del := left[0].(map[string]any)["file"].(string)
	mustCall(t, r, "pg.backup_delete", map[string]any{"database": "db_a", "file": del})
	if _, err := os.Stat(filepath.Join(h.Base, "var/backups/nimvor/postgres", del+".sha256")); err == nil {
		t.Fatal("checksum file left behind")
	}
	a.Close(ctx)

	// Monitoring: counts and sizes, never statement text.
	m := asMap(t, mustCall(t, r, "pg.monitor", map[string]any{}))
	if m["version"] == "" || m["max_connections"].(float64) < 1 || len(m["databases"].([]any)) < 2 {
		t.Fatalf("monitor = %v", m)
	}
	if strings.Contains(strings.ToLower(jsonOf(t, m)), "select count") {
		t.Fatal("monitor returned statement text")
	}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
