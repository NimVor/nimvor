package pgmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(v)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBrowserAsOwner(t *testing.T) {
	c := startCluster(t)
	h := testhost.New(t)
	c.admin(t, h.Env)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	ctx := context.Background()
	mustCall(t, r, "pg.role_create", map[string]any{"name": "owner_a", "password": pwA})
	mustCall(t, r, "pg.database_create", map[string]any{"name": "db_a", "owner": "owner_a"})

	// The customer's own objects, created as the customer.
	a, err := c.as("owner_a", pwA, "db_a")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE items (id serial PRIMARY KEY, name text, qty int, meta jsonb, blob bytea, created timestamptz DEFAULT now())`,
		`INSERT INTO items (name, qty, meta, blob) SELECT 'item ' || g, g, jsonb_build_object('n', g), '\xdeadbeef' FROM generate_series(1, 120) g`,
		`INSERT INTO items (name, qty) VALUES (NULL, NULL), ('100% sure', 7), ('under_score', 8), (repeat('x', 5000), 9)`,
		`CREATE TABLE "Weird""Name" ("se lect" int PRIMARY KEY, "Mixed" text)`,
		`INSERT INTO "Weird""Name" VALUES (1, 'a')`,
		`CREATE TABLE nokey (x int, y text)`,
		`INSERT INTO nokey VALUES (1, 'one'), (2, 'two')`,
		`CREATE TABLE uniq (code text NOT NULL UNIQUE, label text)`,
		`INSERT INTO uniq VALUES ('a', 'A')`,
		`CREATE VIEW v_items AS SELECT id, name FROM items`,
		`CREATE SCHEMA app`,
		`CREATE TABLE app.notes (id int PRIMARY KEY, body text)`,
	} {
		if _, err := a.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	a.Close(ctx)

	// The table list: user schemas only, with kinds and whether rows can be edited.
	tl := asMap(t, mustCall(t, r, "pg.tables", map[string]any{"database": "db_a"}))
	b, _ := json.Marshal(tl)
	for _, want := range []string{`"items"`, `Weird\"Name`, `"nokey"`, `"v_items"`, `"notes"`, `"public"`, `"app"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("table list lacks %s: %s", want, b)
		}
	}
	if strings.Contains(string(b), "pg_catalog") || strings.Contains(string(b), "information_schema") {
		t.Errorf("system schemas are listed: %s", b)
	}

	// Pages, sorting, NULLs, long values.
	pg := asMap(t, mustCall(t, r, "pg.browse", map[string]any{"database": "db_a", "schema": "public", "table": "items", "limit": 10, "sort": "qty", "desc": true}))
	rows := pg["rows"].([]any)
	if len(rows) != 10 || pg["total"].(float64) != 124 || pg["editable"] != true {
		t.Fatalf("browse: %d rows, total %v, editable %v", len(rows), pg["total"], pg["editable"])
	}
	if first := rows[0].([]any); first[2] != "120" {
		t.Errorf("sorted descending by qty, first = %v", first)
	}
	if fmt.Sprint(pg["key"]) != "[id]" {
		t.Errorf("key = %v", pg["key"])
	}
	// NULL is JSON null, not an empty string; the long value is cut and marked.
	all := asMap(t, mustCall(t, r, "pg.browse", map[string]any{"database": "db_a", "schema": "public", "table": "items", "limit": 500, "sort": "id"}))
	var sawNull bool
	for _, row := range all["rows"].([]any) {
		cells := row.([]any)
		if cells[1] == nil && cells[2] == nil {
			sawNull = true
		}
		if s, ok := cells[1].(string); ok && strings.HasPrefix(s, "xxxx") && len(s) != cellLimit {
			t.Errorf("long value is %d characters, want %d", len(s), cellLimit)
		}
	}
	if !sawNull {
		t.Error("a NULL cell did not come back as null")
	}
	if len(all["truncated"].([]any)) != 1 {
		t.Errorf("truncated cells: %v", all["truncated"])
	}
	// bytea comes as hex text.
	if cell := rows[0].([]any)[4]; cell != `\xdeadbeef` {
		t.Errorf("bytea = %v", cell)
	}
	// Offset pages do not overlap.
	p2 := asMap(t, mustCall(t, r, "pg.browse", map[string]any{"database": "db_a", "schema": "public", "table": "items", "limit": 10, "offset": 10, "sort": "id"}))
	if p2["rows"].([]any)[0].([]any)[0] != "11" {
		t.Errorf("second page starts at %v", p2["rows"].([]any)[0])
	}
	// Search treats % and _ literally.
	for q, want := range map[string]float64{"100%": 1, "under_": 1, "item 1": 1 + 10 + 11 + 0, "nomatch-zzz": 0} {
		got := asMap(t, mustCall(t, r, "pg.browse", map[string]any{"database": "db_a", "schema": "public", "table": "items", "search": q}))["total"].(float64)
		if q == "item 1" {
			continue // matches 'item 1', 'item 10'..'item 19', 'item 100'..'item 119': counted below
		}
		if got != want {
			t.Errorf("search %q found %v, want %v", q, got, want)
		}
	}
	if got := asMap(t, mustCall(t, r, "pg.browse", map[string]any{"database": "db_a", "schema": "public", "table": "items", "search": "item 1"}))["total"].(float64); got != 1+10+21 {
		t.Errorf("search 'item 1' found %v", got)
	}
	// Filters.
	f := func(flt map[string]any) float64 {
		return asMap(t, mustCall(t, r, "pg.browse", map[string]any{"database": "db_a", "schema": "public", "table": "items", "filters": []any{flt}}))["total"].(float64)
	}
	if f(map[string]any{"column": "qty", "op": "eq", "value": "5"}) != 1 || f(map[string]any{"column": "name", "op": "null"}) != 1 || f(map[string]any{"column": "qty", "op": "notnull"}) != 123 {
		t.Error("filters do not count as expected")
	}

	// Read-only tables: no key, a view; a unique key of non-null columns is enough.
	for _, tc := range []struct {
		schema, table string
		editable      bool
	}{{"public", "nokey", false}, {"public", "v_items", false}, {"public", "uniq", true}, {"app", "notes", true}, {"public", `Weird"Name`, true}} {
		pg := asMap(t, mustCall(t, r, "pg.browse", map[string]any{"database": "db_a", "schema": tc.schema, "table": tc.table}))
		if pg["editable"] != tc.editable {
			t.Errorf("%s.%s editable = %v, want %v (%v)", tc.schema, tc.table, pg["editable"], tc.editable, pg["reason"])
		}
	}
	if _, err := call(t, r, "pg.row_delete", map[string]any{"database": "db_a", "schema": "public", "table": "nokey", "key": map[string]any{"x": "1"}}); err == nil {
		t.Error("a row of a table without a key was deleted")
	}
	if _, err := call(t, r, "pg.row_update", map[string]any{"database": "db_a", "schema": "public", "table": "v_items", "key": map[string]any{"id": "1"}, "values": map[string]any{"name": "x"}}); err == nil {
		t.Error("a view row was updated")
	}

	// Editing: insert, update, delete, with types cast by the server and NULLs kept.
	mustCall(t, r, "pg.row_insert", map[string]any{"database": "db_a", "schema": "public", "table": "items", "values": map[string]any{"name": "new", "qty": "500", "meta": `{"k": [1,2]}`, "blob": nil}})
	got := asMap(t, mustCall(t, r, "pg.browse", map[string]any{"database": "db_a", "schema": "public", "table": "items", "filters": []any{map[string]any{"column": "qty", "op": "eq", "value": "500"}}}))
	row := got["rows"].([]any)[0].([]any)
	if row[1] != "new" || row[4] != nil || !strings.Contains(row[3].(string), "[1, 2]") {
		t.Fatalf("inserted row = %v", row)
	}
	id := row[0].(string)
	mustCall(t, r, "pg.row_update", map[string]any{"database": "db_a", "schema": "public", "table": "items", "key": map[string]any{"id": id}, "values": map[string]any{"name": "renamed", "qty": nil}})
	got = asMap(t, mustCall(t, r, "pg.browse", map[string]any{"database": "db_a", "schema": "public", "table": "items", "filters": []any{map[string]any{"column": "id", "op": "eq", "value": id}}}))
	if row = got["rows"].([]any)[0].([]any); row[1] != "renamed" || row[2] != nil {
		t.Fatalf("updated row = %v", row)
	}
	if _, err := call(t, r, "pg.row_update", map[string]any{"database": "db_a", "schema": "public", "table": "items", "key": map[string]any{"id": "999999"}, "values": map[string]any{"name": "x"}}); err == nil {
		t.Error("updating a missing row succeeded")
	}
	if _, err := call(t, r, "pg.row_insert", map[string]any{"database": "db_a", "schema": "public", "table": "items", "values": map[string]any{"qty": "not a number"}}); err == nil {
		t.Error("a value of the wrong type was accepted")
	}
	mustCall(t, r, "pg.row_delete", map[string]any{"database": "db_a", "schema": "public", "table": "items", "key": map[string]any{"id": id}})
	if asMap(t, mustCall(t, r, "pg.browse", map[string]any{"database": "db_a", "schema": "public", "table": "items"}))["total"].(float64) != 124 {
		t.Error("the delete did not remove exactly the row")
	}

	// Names and values that try to break out are data, never SQL.
	for _, bad := range []map[string]any{
		{"schema": "public", "table": `items"; DROP TABLE items; --`},
		{"schema": "public", "table": "items", "sort": `id; DROP TABLE items`},
		{"schema": "public", "table": "items", "filters": []any{map[string]any{"column": `qty" OR 1=1 --`, "op": "eq", "value": "1"}}},
		{"schema": "public", "table": "items", "filters": []any{map[string]any{"column": "qty", "op": "; DROP", "value": "1"}}},
		{"schema": "pg_catalog", "table": "pg_class"},
		{"schema": "information_schema", "table": "tables"},
	} {
		if _, err := call(t, r, "pg.browse", merge(map[string]any{"database": "db_a"}, bad)); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if _, err := call(t, r, "pg.row_insert", map[string]any{"database": "db_a", "schema": "public", "table": "items", "values": map[string]any{`qty"); DROP TABLE items; --`: "1"}}); err == nil {
		t.Error("a column name with SQL was accepted")
	}
	if got := asMap(t, mustCall(t, r, "pg.browse", map[string]any{"database": "db_a", "schema": "public", "table": "items", "search": `'; DROP TABLE items; --`}))["total"].(float64); got != 0 {
		t.Errorf("injection through search matched %v rows", got)
	}
	if asMap(t, mustCall(t, r, "pg.browse", map[string]any{"database": "db_a", "schema": "public", "table": "items"}))["total"].(float64) != 124 {
		t.Error("the table changed during the injection attempts")
	}
	// A system database or another name never opens.
	for _, d := range []string{"postgres", "template1", "no_such_db"} {
		if _, err := call(t, r, "pg.tables", map[string]any{"database": d}); err == nil {
			t.Errorf("opened %s", d)
		}
	}
	// Structure: columns, key, indexes.
	st := asMap(t, mustCall(t, r, "pg.table_structure", map[string]any{"database": "db_a", "schema": "public", "table": "items"}))
	sb, _ := json.Marshal(st)
	for _, want := range []string{`"name":"id"`, `"key":true`, `integer`, `items_pkey`, `"editable":true`} {
		if !strings.Contains(string(sb), want) {
			t.Errorf("structure lacks %s: %s", want, sb)
		}
	}
}

func merge(a, b map[string]any) map[string]any {
	for k, v := range b {
		a[k] = v
	}
	return a
}

// A customer's trigger runs when the browser inserts a row. It must not be able to act as the provisioning role:
// RESET ROLE in a session logged in as the admin would return to it and could alter every customer's users.
func TestCustomerCodeCannotEscalate(t *testing.T) {
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

	a, err := c.as("owner_a", pwA, "db_a")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE items (id serial PRIMARY KEY, name text)`,
		`CREATE TABLE trig_log (msg text)`,
		`CREATE FUNCTION evil() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN
		   BEGIN RESET ROLE; INSERT INTO trig_log VALUES ('reset ok'); EXCEPTION WHEN others THEN INSERT INTO trig_log VALUES ('reset failed: ' || SQLERRM); END;
		   BEGIN EXECUTE 'ALTER ROLE owner_b PASSWORD ''hacked-password-123'''; INSERT INTO trig_log VALUES ('ALTER ROLE WORKED'); EXCEPTION WHEN others THEN INSERT INTO trig_log VALUES ('alter refused'); END;
		   BEGIN EXECUTE 'CREATE ROLE evil_r LOGIN'; INSERT INTO trig_log VALUES ('CREATE ROLE WORKED'); EXCEPTION WHEN others THEN INSERT INTO trig_log VALUES ('create refused'); END;
		   BEGIN EXECUTE 'CREATE DATABASE evil_db'; INSERT INTO trig_log VALUES ('CREATE DATABASE WORKED'); EXCEPTION WHEN others THEN INSERT INTO trig_log VALUES ('createdb refused'); END;
		   RETURN NEW;
		 END $$`,
		`CREATE TRIGGER t BEFORE INSERT ON items FOR EACH ROW EXECUTE FUNCTION evil()`,
	} {
		if _, err := a.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	a.Close(ctx)

	// The premise: a session that logs in as the provisioning role gets its powers back with RESET ROLE.
	adm, err := open(ctx, h.Env, nil, "db_a")
	if err != nil {
		t.Fatal(err)
	}
	adm.Exec(ctx, "SET ROLE owner_a")
	if _, err := adm.Exec(ctx, "RESET ROLE"); err == nil {
		if _, err := adm.Exec(ctx, "ALTER ROLE owner_b PASSWORD 'premise-check-password-1'"); err == nil {
			t.Log("confirmed: a provisioning-role session can alter another customer's user after RESET ROLE")
			adm.Exec(ctx, "ALTER ROLE owner_b PASSWORD '"+pwB+"'")
		}
	}
	adm.Close(ctx)

	// The browser fires the trigger.
	mustCall(t, r, "pg.row_insert", map[string]any{"database": "db_a", "schema": "public", "table": "items", "values": map[string]any{"name": "x"}})

	a, _ = c.as("owner_a", pwA, "db_a")
	rows, err := a.Query(ctx, `SELECT msg FROM trig_log`)
	if err != nil {
		t.Fatal(err)
	}
	var log []string
	for rows.Next() {
		var m string
		rows.Scan(&m)
		log = append(log, m)
	}
	rows.Close()
	a.Close(ctx)
	joined := strings.Join(log, "; ")
	if len(log) == 0 {
		t.Fatal("the trigger did not run")
	}
	for _, bad := range []string{"ALTER ROLE WORKED", "CREATE ROLE WORKED", "CREATE DATABASE WORKED"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("customer code escalated through the browser: %s", joined)
		}
	}
	// owner_b's password is unchanged, the role was not created.
	if s, err := c.as("owner_b", pwB, "db_b"); err != nil {
		t.Fatalf("owner_b's password was changed: %v", err)
	} else {
		s.Close(ctx)
	}
	var n int
	adm, _ = open(ctx, h.Env, nil, "postgres")
	adm.QueryRow(ctx, `SELECT count(*) FROM pg_roles WHERE rolname = 'evil_r'`).Scan(&n)
	adm.Close(ctx)
	if n != 0 {
		t.Fatal("a role was created by customer code")
	}
}
