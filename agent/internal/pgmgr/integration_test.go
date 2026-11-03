package pgmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// These tests run against a real, disposable PostgreSQL cluster started as the postgres account. They are
// skipped where that is not possible (not root, or PostgreSQL's server programs are missing).

type cluster struct {
	port int
	dir  string
}

func startCluster(t *testing.T) *cluster {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root to run PostgreSQL as the postgres account")
	}
	bins, _ := filepath.Glob("/usr/lib/postgresql/*/bin/initdb")
	if len(bins) == 0 {
		t.Skip("PostgreSQL server programs not installed")
	}
	bin := filepath.Dir(bins[len(bins)-1])
	dir, err := os.MkdirTemp("", "nimvor-pgtest-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	pu, err := user.Lookup("postgres")
	if err != nil {
		t.Skip("no postgres account")
	}
	uid, _ := strconv.Atoi(pu.Uid)
	gid, _ := strconv.Atoi(pu.Gid)
	if err := os.Chown(dir, uid, gid); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	data := filepath.Join(dir, "data")
	run := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command("runuser", append([]string{"-u", "postgres", "--", filepath.Join(bin, name)}, args...)...)
		cmd.Dir = "/"
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	run("initdb", "-D", data, "-A", "trust", "--auth-host=scram-sha-256", "-E", "UTF8", "--locale=C.UTF-8")
	run("pg_ctl", "-D", data, "-w", "-o", fmt.Sprintf("-p %d -k %s -c listen_addresses=127.0.0.1 -c password_encryption=scram-sha-256", port, dir), "-l", filepath.Join(dir, "log"), "start")
	t.Cleanup(func() { run("pg_ctl", "-D", data, "-m", "immediate", "stop") })
	return &cluster{port: port, dir: dir}
}

// admin creates the provisioning role the way bootstrapAdmin does (without superuser rights).
func (c *cluster) admin(t *testing.T, env *host.Env) {
	t.Helper()
	sql := fmt.Sprintf("CREATE ROLE %s LOGIN CREATEDB CREATEROLE NOSUPERUSER PASSWORD 'admin-secret-for-tests'; GRANT pg_monitor TO %s; GRANT CONNECT ON DATABASE postgres TO %s; ALTER ROLE %s SET createrole_self_grant = 'set, inherit';"+hardenSQL, AdminRole, AdminRole, AdminRole, AdminRole)
	cmd := exec.Command("runuser", "-u", "postgres", "--", "psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-h", c.dir, "-p", fmt.Sprint(c.port), "-d", "postgres", "-c", sql)
	cmd.Dir = "/"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("admin role: %v\n%s", err, out)
	}
	b, _ := json.Marshal(Target{Host: "127.0.0.1", Port: c.port, User: AdminRole, Password: "admin-secret-for-tests"})
	if _, err := env.Writer.MkdirAll(filepath.Dir(AdminFile), 0o700, agentfs.RootOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Writer.WriteFile(AdminFile, b, 0o600, agentfs.RootOwner); err != nil {
		t.Fatal(err)
	}
}

func (c *cluster) as(user, password, db string) (*session, error) {
	conn, err := connect(context.Background(), Target{Host: "127.0.0.1", Port: c.port, User: user, Password: password, SSLMode: "disable"}, db)
	if err != nil {
		return nil, err
	}
	return &session{Conn: conn}, nil
}

func call(t *testing.T, r *actions.Registry, name string, params map[string]any) (any, error) {
	t.Helper()
	b, _ := json.Marshal(params)
	a, err := r.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return a.Handler(context.Background(), b)
}

func mustCall(t *testing.T, r *actions.Registry, name string, params map[string]any) any {
	t.Helper()
	out, err := call(t, r, name, params)
	if err != nil {
		t.Fatalf("%s %v: %v", name, params, err)
	}
	return out
}

const (
	pwA = "alpha-password-123"
	pwB = "bravo-password-123"
	pwR = "reader-password-123"
)

func TestProvisioningAndIsolation(t *testing.T) {
	c := startCluster(t)
	h := testhost.New(t)
	c.admin(t, h.Env)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	ctx := context.Background()

	// Two customers, each with a user and a database.
	mustCall(t, r, "pg.role_create", map[string]any{"name": "cust_a", "password": pwA})
	mustCall(t, r, "pg.role_create", map[string]any{"name": "cust_b", "password": pwB})
	mustCall(t, r, "pg.database_create", map[string]any{"name": "db_a", "owner": "cust_a"})
	mustCall(t, r, "pg.database_create", map[string]any{"name": "db_b", "owner": "cust_b"})

	// Creating twice reports a conflict instead of silently succeeding.
	if _, err := call(t, r, "pg.role_create", map[string]any{"name": "cust_a", "password": pwA}); err == nil {
		t.Error("duplicate user accepted")
	}
	if _, err := call(t, r, "pg.database_create", map[string]any{"name": "db_a", "owner": "cust_a"}); err == nil {
		t.Error("duplicate database accepted")
	}

	// A customer works in their own database ...
	a, err := c.as("cust_a", pwA, "db_a")
	if err != nil {
		t.Fatalf("cust_a cannot enter db_a: %v", err)
	}
	if _, err := a.Exec(ctx, `CREATE TABLE items (id serial PRIMARY KEY, name text)`); err != nil {
		t.Fatalf("cust_a cannot create a table: %v", err)
	}
	if _, err := a.Exec(ctx, `INSERT INTO items (name) VALUES ('x')`); err != nil {
		t.Fatal(err)
	}
	a.Close(ctx)

	// ... and cannot enter another customer's, nor the system databases' schema objects.
	if s, err := c.as("cust_a", pwA, "db_b"); err == nil {
		s.Close(ctx)
		t.Fatal("cust_a entered db_b")
	}
	if s, err := c.as("cust_b", pwB, "db_a"); err == nil {
		s.Close(ctx)
		t.Fatal("cust_b entered db_a")
	}
	if s, err := c.as("cust_a", pwA, "template1"); err == nil {
		s.Close(ctx)
		t.Fatal("cust_a entered template1")
	}
	// The public schema is not open to anyone but the owner: a role without grants cannot even connect, and
	// a role that connects to "postgres" cannot create objects there.
	if s, err := c.as("cust_a", pwA, "postgres"); err == nil {
		s.Close(ctx)
		t.Error("cust_a entered the postgres database")
	}

	// Read-only access: connects, reads existing and future tables, cannot write.
	mustCall(t, r, "pg.role_create", map[string]any{"name": "reader", "password": pwR})
	if s, err := c.as("reader", pwR, "db_a"); err == nil {
		s.Close(ctx)
		t.Fatal("reader entered db_a before any grant")
	}
	mustCall(t, r, "pg.grant_set", map[string]any{"database": "db_a", "role": "reader", "level": "readonly"})
	rd, err := c.as("reader", pwR, "db_a")
	if err != nil {
		t.Fatalf("reader cannot enter db_a after the grant: %v", err)
	}
	var n int
	if err := rd.QueryRow(ctx, `SELECT count(*) FROM items`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("reader cannot read: %v %d", err, n)
	}
	if _, err := rd.Exec(ctx, `INSERT INTO items (name) VALUES ('y')`); err == nil {
		t.Error("read-only user wrote")
	}
	if _, err := rd.Exec(ctx, `CREATE TABLE nope (x int)`); err == nil {
		t.Error("read-only user created a table")
	}
	rd.Close(ctx)
	// A table the owner creates later is readable too (default privileges).
	a, _ = c.as("cust_a", pwA, "db_a")
	if _, err := a.Exec(ctx, `CREATE TABLE later (v int); INSERT INTO later VALUES (7)`); err != nil {
		t.Fatal(err)
	}
	a.Close(ctx)
	rd, _ = c.as("reader", pwR, "db_a")
	if err := rd.QueryRow(ctx, `SELECT v FROM later`).Scan(&n); err != nil || n != 7 {
		t.Errorf("reader cannot read a table created after the grant: %v", err)
	}
	rd.Close(ctx)

	// Read-write: may write, still may not create objects.
	mustCall(t, r, "pg.grant_set", map[string]any{"database": "db_a", "role": "reader", "level": "readwrite"})
	rd, _ = c.as("reader", pwR, "db_a")
	if _, err := rd.Exec(ctx, `INSERT INTO items (name) VALUES ('y')`); err != nil {
		t.Errorf("read-write user cannot write: %v", err)
	}
	if _, err := rd.Exec(ctx, `CREATE TABLE nope (x int)`); err == nil {
		t.Error("read-write user created a table")
	}
	rd.Close(ctx)
	// Lowering the level takes the write access away again.
	mustCall(t, r, "pg.grant_set", map[string]any{"database": "db_a", "role": "reader", "level": "readonly"})
	rd, _ = c.as("reader", pwR, "db_a")
	if _, err := rd.Exec(ctx, `INSERT INTO items (name) VALUES ('z')`); err == nil {
		t.Error("write still possible after lowering to read-only")
	}
	rd.Close(ctx)
	if out := mustCall(t, r, "pg.grant_list", map[string]any{"database": "db_a"}).(map[string]any); fmt.Sprint(out["users"]) != "[reader]" {
		t.Errorf("grant_list = %v", out["users"])
	}
	// Removing the access.
	mustCall(t, r, "pg.grant_set", map[string]any{"database": "db_a", "role": "reader", "level": "none"})
	if s, err := c.as("reader", pwR, "db_a"); err == nil {
		s.Close(ctx)
		t.Error("reader still connects after level none")
	}

	// A password change takes effect.
	mustCall(t, r, "pg.role_password", map[string]any{"name": "cust_b", "password": "bravo-NEW-password-9"})
	if s, err := c.as("cust_b", pwB, "db_b"); err == nil {
		s.Close(ctx)
		t.Error("old password still works")
	}
	if s, err := c.as("cust_b", "bravo-NEW-password-9", "db_b"); err != nil {
		t.Errorf("new password does not work: %v", err)
	} else {
		s.Close(ctx)
	}

	// The overview shows both databases and users, marks the system ones, and never contains a password.
	ov := mustCall(t, r, "pg.overview", map[string]any{})
	b, _ := json.Marshal(ov)
	for _, want := range []string{"db_a", "db_b", "cust_a", "cust_b", "reader"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("overview lacks %s: %s", want, b)
		}
	}
	for _, secret := range []string{pwA, pwB, pwR, "admin-secret-for-tests"} {
		if strings.Contains(string(b), secret) {
			t.Error("a password is in the overview")
		}
	}

	// A user that owns a database cannot be dropped; once the database is gone it can; deleting twice is fine.
	if _, err := call(t, r, "pg.role_delete", map[string]any{"name": "cust_a"}); err == nil {
		t.Error("an owner was dropped while owning a database")
	}
	mustCall(t, r, "pg.database_delete", map[string]any{"name": "db_a"})
	mustCall(t, r, "pg.database_delete", map[string]any{"name": "db_a"})
	mustCall(t, r, "pg.role_delete", map[string]any{"name": "cust_a"})
	mustCall(t, r, "pg.role_delete", map[string]any{"name": "cust_a"})
	mustCall(t, r, "pg.role_delete", map[string]any{"name": "reader"})
	// Deleting a database ends a connection that is still open.
	open, err := c.as("cust_b", "bravo-NEW-password-9", "db_b")
	if err != nil {
		t.Fatal(err)
	}
	mustCall(t, r, "pg.database_delete", map[string]any{"name": "db_b"})
	if _, err := open.Exec(ctx, "SELECT 1"); err == nil {
		t.Error("a connection to a deleted database still works")
	}

	// Names that would break out of SQL are refused before anything runs.
	for _, bad := range []string{`x"; DROP DATABASE postgres; --`, "x y", "Upper", "postgres"} {
		if _, err := call(t, r, "pg.database_create", map[string]any{"name": bad, "owner": "cust_b"}); err == nil {
			t.Errorf("database name %q accepted", bad)
		}
	}
	// pg.test reaches the server and says the provisioning account is not a superuser.
	tst := mustCall(t, r, "pg.test", map[string]any{}).(map[string]any)
	if tst["superuser"] != false {
		t.Errorf("provisioning role is a superuser: %v", tst)
	}
	_ = time.Second
}
