package pgmgr

import (
	"context"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestBulkMaintenance(t *testing.T) {
	c := startCluster(t)
	h := testhost.New(t)
	c.admin(t, h.Env)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	ctx := context.Background()
	mustCall(t, r, "pg.role_create", map[string]any{"name": "owner_m", "password": pwA})
	mustCall(t, r, "pg.database_create", map[string]any{"name": "db_m", "owner": "owner_m"})
	a, err := c.as("owner_m", pwA, "db_m")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE a (id serial PRIMARY KEY, v text)`, `INSERT INTO a (v) SELECT 'x' FROM generate_series(1, 50)`,
		`CREATE TABLE b (id int)`, `INSERT INTO b VALUES (1)`,
		`CREATE TABLE "we""ird; DROP TABLE a" (x int)`,
		`CREATE VIEW va AS SELECT * FROM a`,
		`CREATE MATERIALIZED VIEW ma AS SELECT count(*) FROM a`,
		`CREATE TABLE child (a_id int REFERENCES a (id))`,
	} {
		if _, err := a.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	count := func(table string) int {
		var n int
		if err := a.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	bulk := func(p map[string]any) map[string]any {
		p["database"] = "db_m"
		return asMap(t, mustCall(t, r, "pg.objects_bulk", p))
	}
	item := func(n string) map[string]any { return map[string]any{"schema": "public", "name": n} }

	// Destructive operations need confirmation, other operations do not.
	if _, err := call(t, r, "pg.objects_bulk", map[string]any{"database": "db_m", "op": "drop", "items": []any{item("b")}}); err == nil {
		t.Fatal("drop without confirm must be refused")
	}
	if _, err := call(t, r, "pg.objects_bulk", map[string]any{"database": "db_m", "op": "format_disk", "items": []any{item("b")}, "confirm": true}); err == nil {
		t.Fatal("an unknown operation must be refused")
	}
	if _, err := call(t, r, "pg.objects_bulk", map[string]any{"database": "db_m", "op": "analyze", "items": []any{}}); err == nil {
		t.Fatal("an empty selection must be refused")
	}

	// Dry run returns the statements, runs nothing, and quotes the odd name.
	dry := bulk(map[string]any{"op": "drop", "items": []any{item("we\"ird; DROP TABLE a")}, "dry_run": true})
	stmt := dry["results"].([]any)[0].(map[string]any)["statement"].(string)
	if stmt != `DROP TABLE "public"."we""ird; DROP TABLE a"` {
		t.Errorf("statement = %s", stmt)
	}
	if count("a") != 50 {
		t.Fatal("a dry run changed data")
	}

	// Maintenance on a mix: a view cannot be vacuumed, an unknown name is reported, the rest succeed.
	res := bulk(map[string]any{"op": "vacuum_analyze", "items": []any{item("a"), item("va"), item("ma"), item("nope")}})
	if res["succeeded"].(float64) != 2 || res["failed"].(float64) != 2 {
		t.Errorf("vacuum mix: %v", res)
	}
	if res := bulk(map[string]any{"op": "reindex", "items": []any{item("a"), item("ma")}}); res["failed"].(float64) != 0 {
		t.Errorf("reindex: %v", res)
	}
	if res := bulk(map[string]any{"op": "refresh", "items": []any{item("ma"), item("a")}}); res["succeeded"].(float64) != 1 {
		t.Errorf("refresh: %v", res)
	}

	// Truncate: a referenced table fails without CASCADE and keeps its rows; with CASCADE and restart both empty.
	if res := bulk(map[string]any{"op": "truncate", "items": []any{item("a")}, "confirm": true}); res["failed"].(float64) != 1 || count("a") != 50 {
		t.Errorf("truncate referenced table: %v", res)
	}
	if res := bulk(map[string]any{"op": "truncate", "items": []any{item("a"), item("b")}, "confirm": true, "cascade": true, "restart_identity": true}); res["succeeded"].(float64) != 2 {
		t.Errorf("truncate cascade: %v", res)
	}
	if count("a") != 0 || count("b") != 0 {
		t.Error("tables are not empty")
	}
	var next int
	if _, err := a.Exec(ctx, `INSERT INTO a (v) VALUES ('first')`); err != nil {
		t.Fatal(err)
	}
	if err := a.QueryRow(ctx, `SELECT id FROM a`).Scan(&next); err != nil || next != 1 {
		t.Errorf("identity not restarted: %d %v", next, err)
	}

	// Drop: names are checked in the catalog; a system table and a path trick find nothing.
	if res := bulk(map[string]any{"op": "drop", "items": []any{map[string]any{"schema": "pg_catalog", "name": "pg_class"}, map[string]any{"schema": "public", "name": "a\x00b"}}, "confirm": true}); res["succeeded"].(float64) != 0 {
		t.Errorf("system object dropped: %v", res)
	}
	if res := bulk(map[string]any{"op": "drop", "items": []any{item("va"), item("ma"), item("child"), item("a")}, "confirm": true}); res["succeeded"].(float64) != 4 {
		t.Errorf("drop: %v", res)
	}
	var left int
	if err := a.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = 'public'`).Scan(&left); err != nil || left != 2 { // b and the odd one
		t.Errorf("tables left = %d (%v)", left, err)
	}
	a.Close(ctx)

	// Another customer's database is not reachable by name.
	if _, err := call(t, r, "pg.objects_bulk", map[string]any{"database": "no_such_db", "op": "analyze", "items": []any{item("b")}}); err == nil {
		t.Error("an unknown database must be refused")
	}
}
