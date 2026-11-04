package pgmgr

import (
	"context"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestRoutinesSequencesPartitions(t *testing.T) {
	c := startCluster(t)
	h := testhost.New(t)
	c.admin(t, h.Env)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	ctx := context.Background()
	mustCall(t, r, "pg.role_create", map[string]any{"name": "owner_r", "password": pwA})
	mustCall(t, r, "pg.database_create", map[string]any{"name": "db_r", "owner": "owner_r"})
	a, err := c.as("owner_r", pwA, "db_r")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(ctx)
	for _, q := range []string{
		`CREATE FUNCTION add_one(i int) RETURNS int LANGUAGE sql IMMUTABLE AS 'SELECT i + 1'`,
		`CREATE FUNCTION add_one(i bigint) RETURNS bigint LANGUAGE sql AS 'SELECT i + 1'`,
		`CREATE PROCEDURE do_nothing() LANGUAGE plpgsql AS $$ BEGIN NULL; END $$`,
		`CREATE TABLE inv (id serial PRIMARY KEY, n int)`,
		`INSERT INTO inv (n) SELECT g FROM generate_series(1, 10) g`,
		`CREATE SEQUENCE ticket_seq START 100`,
		`CREATE TABLE ev (at date, v int) PARTITION BY RANGE (at)`,
		`CREATE TABLE ev_old (at date, v int)`,
	} {
		if _, err := a.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	call2 := func(name string, p map[string]any) (map[string]any, error) {
		p["database"] = "db_r"
		out, err := call(t, r, name, p)
		if err != nil {
			return nil, err
		}
		return asMap(t, out), nil
	}
	must := func(name string, p map[string]any) map[string]any {
		t.Helper()
		out, err := call2(name, p)
		if err != nil {
			t.Fatalf("%s %v: %v", name, p, err)
		}
		return out
	}

	// Routines: both overloads, the procedure; definition; drop by OID only.
	rt := must("pg.routines", map[string]any{})["routines"].([]any)
	if len(rt) != 3 {
		t.Fatalf("routines = %v", rt)
	}
	var intOID, procOID float64
	for _, it := range rt {
		m := it.(map[string]any)
		if m["name"] == "add_one" && m["arguments"] == "i integer" {
			intOID = m["oid"].(float64)
			if m["volatility"] != "immutable" || m["returns"] != "integer" || m["language"] != "sql" {
				t.Errorf("add_one(int) = %v", m)
			}
		}
		if m["name"] == "do_nothing" {
			procOID = m["oid"].(float64)
			if m["kind"] != "procedure" {
				t.Errorf("procedure kind = %v", m["kind"])
			}
		}
	}
	if def := must("pg.routine_definition", map[string]any{"oid": intOID})["definition"].(string); len(def) < 20 {
		t.Errorf("definition = %q", def)
	}
	if got := must("pg.routines", map[string]any{"search": "do_"})["routines"].([]any); len(got) != 1 {
		t.Errorf("search: %v", got)
	}
	if _, err := call2("pg.routine_drop", map[string]any{"oid": intOID}); err == nil {
		t.Error("drop without confirm must be refused")
	}
	if _, err := call2("pg.routine_drop", map[string]any{"oid": 1259, "confirm": true}); err == nil {
		t.Error("a system routine/relation OID must not be droppable")
	}
	if out := must("pg.routine_drop", map[string]any{"oid": intOID, "confirm": true}); out["sql"] != `DROP FUNCTION add_one(integer)` {
		t.Errorf("drop SQL = %v", out["sql"])
	}
	must("pg.routine_drop", map[string]any{"oid": procOID, "confirm": true})
	if len(must("pg.routines", map[string]any{})["routines"].([]any)) != 1 {
		t.Error("expected one routine left (the bigint overload)")
	}

	// Sequences: the serial's and the manual one, with owner; sync follows the column; set value; alter; drop rules.
	seqs := must("pg.sequences", map[string]any{})["sequences"].([]any)
	owned := map[string]string{}
	for _, it := range seqs {
		m := it.(map[string]any)
		owned[m["name"].(string)] = m["owned_by"].(string)
	}
	if owned["inv_id_seq"] != "inv.id" || owned["ticket_seq"] != "" {
		t.Errorf("sequences = %v", owned)
	}
	must("pg.sequence_alter", map[string]any{"op": "sync", "schema": "public", "name": "inv_id_seq"})
	var next int
	if err := a.QueryRow(ctx, `SELECT nextval('inv_id_seq')`).Scan(&next); err != nil || next != 11 {
		t.Errorf("after sync nextval = %d (%v), want 11", next, err)
	}
	must("pg.sequence_alter", map[string]any{"op": "set_value", "schema": "public", "name": "ticket_seq", "value": 500})
	if err := a.QueryRow(ctx, `SELECT nextval('ticket_seq')`).Scan(&next); err != nil || next != 501 {
		t.Errorf("after set_value nextval = %d (%v), want 501", next, err)
	}
	must("pg.sequence_alter", map[string]any{"op": "alter", "schema": "public", "name": "ticket_seq", "increment": 10, "max": 1000, "cycle": true})
	must("pg.sequence_alter", map[string]any{"op": "create", "schema": "public", "name": "fresh_seq", "start": 7, "increment": 2})
	if err := a.QueryRow(ctx, `SELECT nextval('fresh_seq')`).Scan(&next); err != nil || next != 7 {
		t.Errorf("fresh_seq = %d (%v)", next, err)
	}
	for name, p := range map[string]map[string]any{
		"drop owned":     {"op": "drop", "schema": "public", "name": "inv_id_seq"},
		"sync unowned":   {"op": "sync", "schema": "public", "name": "ticket_seq"},
		"unknown":        {"op": "drop", "schema": "public", "name": "nothing"},
		"system schema":  {"op": "drop", "schema": "pg_catalog", "name": "x"},
		"zero increment": {"op": "alter", "schema": "public", "name": "ticket_seq", "increment": 0},
		"nothing":        {"op": "alter", "schema": "public", "name": "ticket_seq"},
		"injection":      {"op": "create", "schema": "public", "name": "x; DROP TABLE inv", "start": 1},
	} {
		if out, err := call2("pg.sequence_alter", p); err == nil && name != "injection" {
			t.Errorf("%s: must be refused (%v)", name, out)
		}
	}
	if err := a.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE tablename = 'inv'`).Scan(&next); err != nil || next != 1 {
		t.Fatal("the injection attempt dropped a table")
	}
	must("pg.sequence_alter", map[string]any{"op": "drop", "schema": "public", "name": "fresh_seq"})

	// Partitions: create by range, attach an existing table, list, detach.
	alter := func(p map[string]any) (map[string]any, error) {
		p["database"], p["schema"], p["table"] = "db_r", "public", "ev"
		return call2("pg.table_alter", p)
	}
	if _, err := alter(map[string]any{"op": "create_partition", "new_name": "ev_2026", "extra": map[string]any{"bound": map[string]any{"kind": "range", "from": []any{"2026-01-01"}, "to": []any{"2027-01-01"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Exec(ctx, `INSERT INTO ev_old VALUES ('2025-05-05', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := alter(map[string]any{"op": "attach_partition", "name": "ev_old", "extra": map[string]any{"bound": map[string]any{"kind": "range", "from": []any{"2025-01-01"}, "to": []any{"2026-01-01"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := alter(map[string]any{"op": "create_partition", "new_name": "ev_overlap", "extra": map[string]any{"bound": map[string]any{"kind": "range", "from": []any{"2026-06-01"}, "to": []any{"2026-07-01"}}}}); err == nil {
		t.Error("an overlapping partition must be refused by the server")
	}
	if _, err := alter(map[string]any{"op": "create_partition", "new_name": "ev_x", "extra": map[string]any{"bound": map[string]any{"kind": "range", "from": []any{"2030-01-01'); DROP TABLE inv; --"}, "to": []any{"2031-01-01"}}}}); err == nil {
		t.Error("a bound that is not a date must fail without side effects")
	}
	parts := must("pg.partitions", map[string]any{"schema": "public", "table": "ev"})
	if parts["partitioned"] != true || parts["key"] != "RANGE (at)" || len(parts["partitions"].([]any)) != 2 {
		t.Errorf("partitions = %v", parts)
	}
	if got := must("pg.partitions", map[string]any{"schema": "public", "table": "inv"}); got["partitioned"] != false {
		t.Errorf("plain table reported partitioned: %v", got)
	}
	if _, err := alter(map[string]any{"op": "detach_partition", "name": "inv"}); err == nil {
		t.Error("detaching a table that is not a partition must be refused")
	}
	if _, err := alter(map[string]any{"op": "detach_partition", "name": "ev_old"}); err != nil {
		t.Fatal(err)
	}
	if len(must("pg.partitions", map[string]any{"schema": "public", "table": "ev"})["partitions"].([]any)) != 1 {
		t.Error("partition not detached")
	}
	if err := a.QueryRow(ctx, `SELECT count(*) FROM ev_old`).Scan(&next); err != nil || next != 1 {
		t.Errorf("detached table lost its rows: %d %v", next, err)
	}
}
