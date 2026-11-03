package pgmgr

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func runSQL(t *testing.T, r *actions.Registry, db, mode, sql string, extra map[string]any) map[string]any {
	t.Helper()
	p := map[string]any{"database": db, "query": sql, "mode": mode}
	for k, v := range extra {
		p[k] = v
	}
	return asMap(t, mustCall(t, r, "pg.query", p))
}

func sqlError(res map[string]any) string {
	if e, ok := res["error"].(map[string]any); ok {
		return e["message"].(string)
	}
	return ""
}

func TestSQLEditor(t *testing.T) {
	c := startCluster(t)
	h := testhost.New(t)
	c.admin(t, h.Env)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	ctx := context.Background()
	mustCall(t, r, "pg.role_create", map[string]any{"name": "owner_a", "password": pwA})
	mustCall(t, r, "pg.role_create", map[string]any{"name": "owner_b", "password": pwB})
	mustCall(t, r, "pg.database_create", map[string]any{"name": "db_a", "owner": "owner_a"})
	mustCall(t, r, "pg.database_create", map[string]any{"name": "db_b", "owner": "owner_b"})
	a, _ := c.as("owner_a", pwA, "db_a")
	for _, q := range []string{`CREATE TABLE t (id int PRIMARY KEY, v text, n numeric, ts timestamptz, b bytea)`,
		`INSERT INTO t SELECT g, 'row ' || g, g * 1.5, '2026-01-02 03:04:05+00', '\xff00' FROM generate_series(1, 30) g`,
		`INSERT INTO t (id) VALUES (100)`} {
		if _, err := a.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	a.Close(ctx)

	// Read mode: a SELECT works, values exactly as PostgreSQL prints them, NULL stays NULL, types are named.
	res := runSQL(t, r, "db_a", "read", `SELECT id, v, n, ts, b, NULL::text AS nothing FROM t WHERE id IN (3, 100) ORDER BY id`, nil)
	if sqlError(res) != "" {
		t.Fatalf("select failed: %v", res["error"])
	}
	rs := res["results"].([]any)[0].(map[string]any)
	rows := rs["rows"].([]any)
	if len(rows) != 2 || rows[0].([]any)[2] != "4.5" || rows[0].([]any)[4] != `\xff00` || rows[1].([]any)[1] != nil || rows[0].([]any)[5] != nil {
		t.Fatalf("rows = %v", rows)
	}
	var types []string
	for _, c := range rs["columns"].([]any) {
		types = append(types, c.(map[string]any)["type"].(string))
	}
	if strings.Join(types, ",") != "integer,text,numeric,timestamp with time zone,bytea,text" {
		t.Errorf("types = %v", types)
	}

	// Read mode cannot change anything, whatever the text says (enforced by privileges).
	for _, q := range []string{`INSERT INTO t (id) VALUES (500)`, `UPDATE t SET v = 'x'`, `DELETE FROM t`, `DROP TABLE t`, `CREATE TABLE evil (x int)`, `TRUNCATE t`,
		`SELECT * FROM t; DROP TABLE t`, `WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d`, `COPY t TO '/tmp/x'`, `SET ROLE owner_a; DELETE FROM t`, `SET SESSION CHARACTERISTICS AS TRANSACTION READ WRITE; DELETE FROM t`, `BEGIN READ WRITE; DELETE FROM t; COMMIT`, `RESET ROLE; DELETE FROM t`} {
		out := runSQL(t, r, "db_a", "read", q, map[string]any{"script": strings.Contains(q, ";")})
		if sqlError(out) == "" {
			t.Errorf("read mode accepted: %s -> %v", q, out)
		}
	}
	if n := runSQL(t, r, "db_a", "read", `SELECT count(*) FROM t`, nil)["results"].([]any)[0].(map[string]any)["rows"].([]any)[0].([]any)[0]; n != "31" {
		t.Errorf("the table changed: count = %v", n)
	}

	// One statement unless script is on; a script returns one result per statement.
	if sqlError(runSQL(t, r, "db_a", "read", `SELECT 1; SELECT 2`, nil)) == "" {
		t.Error("two statements ran without the script option")
	}
	sc := runSQL(t, r, "db_a", "read", `SELECT 1 AS a; SELECT 2 AS b`, map[string]any{"script": true})
	if len(sc["results"].([]any)) != 2 {
		t.Errorf("script results: %v", sc)
	}

	// Write mode runs as the owner: objects it creates belong to the owner (visible to the owner's own login).
	if e := sqlError(runSQL(t, r, "db_a", "write", `CREATE TABLE made (x int); INSERT INTO made VALUES (1)`, map[string]any{"script": true})); e != "" {
		t.Fatalf("write mode failed: %s", e)
	}
	o, _ := c.as("owner_a", pwA, "db_a")
	var n int
	if err := o.QueryRow(ctx, `SELECT count(*) FROM made`).Scan(&n); err != nil || n != 1 {
		t.Errorf("the owner cannot read a table made in the editor: %v", err)
	}
	o.Close(ctx)
	// A table made later is readable in read mode too.
	if e := sqlError(runSQL(t, r, "db_a", "read", `SELECT count(*) FROM made`, nil)); e != "" {
		t.Errorf("read mode cannot read a table made later: %s", e)
	}

	// Escape attempts from write mode: nothing beyond the owner's own database.
	for _, q := range []string{`CREATE ROLE evil LOGIN`, `ALTER ROLE owner_b PASSWORD 'hacked-password-1'`, `CREATE DATABASE evil_db`, `DROP DATABASE db_b`,
		`SET ROLE nimvor_admin`, `SET ROLE owner_b`, `SET SESSION AUTHORIZATION nimvor_admin`, `SELECT rolpassword FROM pg_authid`,
		`COPY t TO PROGRAM 'id'`, `COPY t FROM '/etc/passwd'`, `CREATE EXTENSION plpython3u`, `ALTER SYSTEM SET work_mem = '1GB'`, `SELECT pg_read_file('/etc/passwd')`} {
		out := runSQL(t, r, "db_a", "write", q, nil)
		if sqlError(out) == "" {
			t.Errorf("write mode accepted: %s -> %v", q, out)
		}
	}
	// RESET ROLE returns to the dedicated account, which has no more power: still cannot touch other users.
	if e := sqlError(runSQL(t, r, "db_a", "write", `RESET ROLE; ALTER ROLE owner_b PASSWORD 'hacked-password-1'`, map[string]any{"script": true})); e == "" {
		t.Error("RESET ROLE gave the editor the power to alter another user")
	}
	if s, err := c.as("owner_b", pwB, "db_b"); err != nil {
		t.Errorf("owner_b was locked out: %v", err)
	} else {
		s.Close(ctx)
	}
	// db_b's data is not reachable from db_a's session.
	b, _ := c.as("owner_b", pwB, "db_b")
	b.Exec(ctx, `CREATE TABLE secret (s text); INSERT INTO secret VALUES ('b-only')`)
	b.Close(ctx)
	if sqlError(runSQL(t, r, "db_a", "write", `SELECT * FROM secret`, nil)) == "" {
		t.Error("db_a's editor read db_b's table")
	}
	if sqlError(runSQL(t, r, "db_a", "write", `SELECT * FROM dblink('dbname=db_b', 'select 1') AS t(x int)`, nil)) == "" {
		t.Error("cross-database access through dblink")
	}

	// Errors carry the position; rows and cells are capped; a timeout and a cancel stop a long statement.
	er := runSQL(t, r, "db_a", "read", `SELECT 1 FROM nosuchtable`, nil)["error"].(map[string]any)
	if er["code"] != "42P01" || er["position"].(float64) < 1 {
		t.Errorf("error = %v", er)
	}
	cap := runSQL(t, r, "db_a", "read", `SELECT g, repeat('y', 5000) FROM generate_series(1, 3000) g`, map[string]any{"limit": 100})["results"].([]any)[0].(map[string]any)
	if len(cap["rows"].([]any)) != 100 || cap["rows_capped"] != true || len(cap["truncated"].([]any)) != 100 {
		t.Errorf("cap: %d rows capped=%v truncated=%d", len(cap["rows"].([]any)), cap["rows_capped"], len(cap["truncated"].([]any)))
	}
	if cell := cap["rows"].([]any)[0].([]any)[1].(string); len([]rune(cell)) != queryCellLimit {
		t.Errorf("cell length %d", len(cell))
	}
	if e := sqlError(runSQL(t, r, "db_a", "read", `SELECT pg_sleep(5)`, map[string]any{"timeout_seconds": 1})); !strings.Contains(e, "timeout") {
		t.Errorf("timeout: %q", e)
	}
	var wg sync.WaitGroup
	var long map[string]any
	wg.Add(1)
	go func() {
		defer wg.Done()
		long = runSQL(t, r, "db_a", "read", `SELECT pg_sleep(30)`, map[string]any{"query_id": "0123456789abcdef"})
	}()
	time.Sleep(1500 * time.Millisecond)
	if got := asMap(t, mustCall(t, r, "pg.query_cancel", map[string]any{"database": "db_b", "query_id": "0123456789abcdef"}))["cancelled"]; got != false {
		t.Error("a query was cancelled through another database's name")
	}
	if got := asMap(t, mustCall(t, r, "pg.query_cancel", map[string]any{"database": "db_a", "query_id": "0123456789abcdef"}))["cancelled"]; got != true {
		t.Error("cancel found nothing")
	}
	wg.Wait()
	if e := sqlError(long); !strings.Contains(strings.ToLower(e), "cancel") {
		t.Errorf("cancelled statement reported %q", e)
	}

	// Explain: a plan; analyze of a write in read mode is refused.
	pl := runSQL(t, r, "db_a", "read", `SELECT * FROM t WHERE id = 3`, map[string]any{"explain": "plan"})
	if sqlError(pl) != "" || !strings.Contains(asJSON(pl), "Scan") {
		t.Errorf("explain: %v", pl)
	}
	if sqlError(runSQL(t, r, "db_a", "read", `DELETE FROM t`, map[string]any{"explain": "analyze"})) == "" {
		t.Error("EXPLAIN ANALYZE ran a delete in read mode")
	}

	// Input checks.
	for _, bad := range []map[string]any{{"database": "postgres", "query": "select 1"}, {"database": "db_a", "query": ""}, {"database": "db_a", "query": "select 1", "mode": "root"},
		{"database": "db_a", "query": "select 1", "explain": "x"}, {"database": "db_a", "query": strings.Repeat("x", 200000)}, {"database": "db_a", "query": "select 1\x00"}, {"database": "db_a", "query": "select 1", "query_id": "../etc"}} {
		if _, err := call(t, r, "pg.query", bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func asJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
