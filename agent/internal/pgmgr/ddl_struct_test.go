package pgmgr

import (
	"context"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestIndexesConstraintsTriggers(t *testing.T) {
	c := startCluster(t)
	h := testhost.New(t)
	c.admin(t, h.Env)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	ctx := context.Background()
	mustCall(t, r, "pg.role_create", map[string]any{"name": "owner_i", "password": pwA})
	mustCall(t, r, "pg.database_create", map[string]any{"name": "db_i", "owner": "owner_i"})
	a, err := c.as("owner_i", pwA, "db_i")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(ctx)
	for _, q := range []string{
		`CREATE TABLE users (id int PRIMARY KEY, email text, name text)`,
		`CREATE TABLE orders (id int, user_id int, qty int, note text)`,
		`INSERT INTO users VALUES (1, 'a@x', 'A'), (2, 'b@x', 'B')`,
		`INSERT INTO orders VALUES (1, 1, 5, 'x'), (2, 2, 7, 'y'), (3, 1, 9, 'z')`,
		`CREATE FUNCTION noop() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$`,
		`CREATE TRIGGER t_noop BEFORE INSERT ON orders FOR EACH ROW EXECUTE FUNCTION noop()`,
	} {
		if _, err := a.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	alter := func(table string, p map[string]any) (map[string]any, error) {
		p["database"], p["schema"], p["table"] = "db_i", "public", table
		out, err := call(t, r, "pg.table_alter", p)
		if err != nil {
			return nil, err
		}
		return asMap(t, out), nil
	}
	must := func(table string, p map[string]any) map[string]any {
		t.Helper()
		out, err := alter(table, p)
		if err != nil {
			t.Fatalf("%s %v: %v", table, p, err)
		}
		return out
	}
	details := func(table string) map[string]any {
		return asMap(t, mustCall(t, r, "pg.table_details", map[string]any{"database": "db_i", "schema": "public", "table": table}))
	}
	names := func(list any, key string) []string {
		var out []string
		for _, it := range list.([]any) {
			out = append(out, it.(map[string]any)[key].(string))
		}
		return out
	}
	has := func(list []string, want string) bool {
		for _, s := range list {
			if s == want {
				return true
			}
		}
		return false
	}

	// Indexes: plain, unique, descending, partial, included, concurrent.
	dry := must("orders", map[string]any{"op": "create_index", "name": "idx_q", "dry_run": true, "extra": map[string]any{
		"unique": false, "index_columns": []any{map[string]any{"name": "qty", "order": "desc", "nulls": "last"}}, "include": []any{"note"},
		"predicate": map[string]any{"column": "note", "op": "ne", "value": "it's"}}})
	for _, want := range []string{`CREATE INDEX "idx_q" ON "public"."orders" USING btree ("qty" DESC NULLS LAST) INCLUDE ("note") WHERE "note" <> 'it''s'`} {
		if dry["sql"] != want {
			t.Errorf("index SQL:\n got %v\nwant %s", dry["sql"], want)
		}
	}
	must("orders", map[string]any{"op": "create_index", "name": "idx_q", "extra": map[string]any{"index_columns": []any{map[string]any{"name": "qty", "order": "desc"}}, "include": []any{"note"}}})
	must("orders", map[string]any{"op": "create_index", "name": "uq_user_qty", "extra": map[string]any{"unique": true, "index_columns": []any{map[string]any{"name": "user_id"}, map[string]any{"name": "qty"}}}})
	must("orders", map[string]any{"op": "create_index", "name": "idx_note_conc", "extra": map[string]any{"concurrent": true, "index_columns": []any{map[string]any{"name": "note"}}}})
	must("orders", map[string]any{"op": "rename_index", "name": "idx_note_conc", "new_name": "idx_note"})
	must("orders", map[string]any{"op": "reindex_index", "name": "idx_note", "extra": map[string]any{"concurrent": true}})
	d := details("orders")
	ix := names(d["indexes"], "name")
	if !has(ix, "idx_q") || !has(ix, "idx_note") || !has(ix, "uq_user_qty") || has(ix, "idx_note_conc") {
		t.Errorf("indexes = %v", ix)
	}
	for _, it := range d["indexes"].([]any) {
		m := it.(map[string]any)
		if m["name"] == "idx_q" && (m["method"] != "btree" || m["valid"] != true || m["bytes"].(float64) <= 0) {
			t.Errorf("idx_q = %v", m)
		}
	}
	must("orders", map[string]any{"op": "drop_index", "name": "idx_note"})

	// Refused index requests.
	for name, p := range map[string]map[string]any{
		"unknown column":      {"op": "create_index", "extra": map[string]any{"index_columns": []any{map[string]any{"name": "nope"}}}},
		"injection in column": {"op": "create_index", "extra": map[string]any{"index_columns": []any{map[string]any{"name": `qty"); DROP TABLE orders; --`}}}},
		"unknown method":      {"op": "create_index", "extra": map[string]any{"method": "btree); DROP", "index_columns": []any{map[string]any{"name": "qty"}}}},
		"unique hash":         {"op": "create_index", "extra": map[string]any{"method": "hash", "unique": true, "index_columns": []any{map[string]any{"name": "qty"}}}},
		"bad order":           {"op": "create_index", "extra": map[string]any{"index_columns": []any{map[string]any{"name": "qty", "order": "sideways"}}}},
		"drop other table's":  {"op": "drop_index", "name": "users_pkey"},
		"drop missing":        {"op": "drop_index", "name": "nope"},
		"bad predicate op":    {"op": "create_index", "extra": map[string]any{"index_columns": []any{map[string]any{"name": "qty"}}, "predicate": map[string]any{"column": "qty", "op": "sql", "value": "1"}}},
		"predicate column":    {"op": "create_index", "extra": map[string]any{"index_columns": []any{map[string]any{"name": "qty"}}, "predicate": map[string]any{"column": "zz", "op": "eq", "value": "1"}}},
	} {
		if _, err := alter("orders", p); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	if _, err := alter("users", map[string]any{"op": "drop_index", "name": "users_pkey"}); err == nil || !strings.Contains(err.Error(), "constraint") {
		t.Errorf("an index that backs a primary key must not be dropped on its own: %v", err)
	}

	// Constraints.
	must("orders", map[string]any{"op": "add_constraint", "name": "ck_qty", "extra": map[string]any{"kind": "check", "predicate": map[string]any{"column": "qty", "op": "ge", "value": "0"}}})
	must("orders", map[string]any{"op": "add_constraint", "name": "pk_orders", "extra": map[string]any{"kind": "primary", "columns": []any{"id"}}})
	must("users", map[string]any{"op": "add_constraint", "name": "uq_email", "extra": map[string]any{"kind": "unique", "columns": []any{"email"}}})
	must("orders", map[string]any{"op": "add_constraint", "name": "fk_user", "extra": map[string]any{"kind": "foreign", "columns": []any{"user_id"}, "ref_schema": "public", "ref_table": "users", "ref_columns": []any{"id"}, "on_delete": "cascade"}})
	if _, err := alter("orders", map[string]any{"op": "add_constraint", "name": "ck_big", "extra": map[string]any{"kind": "check", "predicate": map[string]any{"column": "qty", "op": "gt", "value": "100"}}}); err == nil {
		t.Error("a CHECK that existing rows violate must be refused by the server")
	}
	must("orders", map[string]any{"op": "add_constraint", "name": "ck_big", "extra": map[string]any{"kind": "check", "not_valid": true, "predicate": map[string]any{"column": "qty", "op": "gt", "value": "100"}}})
	cons := details("orders")["constraints"]
	for _, it := range cons.([]any) {
		m := it.(map[string]any)
		if m["name"] == "ck_big" && m["validated"] != false {
			t.Errorf("NOT VALID constraint reported validated: %v", m)
		}
	}
	if _, err := alter("orders", map[string]any{"op": "validate_constraint", "name": "ck_big"}); err == nil {
		t.Error("validating a constraint existing rows violate must fail")
	}
	must("orders", map[string]any{"op": "drop_constraint", "name": "ck_big"})
	must("orders", map[string]any{"op": "rename_constraint", "name": "ck_qty", "new_name": "ck_qty_pos"})
	if got := names(details("orders")["constraints"], "name"); !has(got, "ck_qty_pos") || !has(got, "fk_user") || !has(got, "pk_orders") || has(got, "ck_big") {
		t.Errorf("constraints = %v", got)
	}
	for name, p := range map[string]map[string]any{
		"unknown kind":       {"op": "add_constraint", "extra": map[string]any{"kind": "exclude"}},
		"fk to nowhere":      {"op": "add_constraint", "extra": map[string]any{"kind": "foreign", "columns": []any{"user_id"}, "ref_schema": "public", "ref_table": "nothing", "ref_columns": []any{"id"}}},
		"fk bad action":      {"op": "add_constraint", "extra": map[string]any{"kind": "foreign", "columns": []any{"user_id"}, "ref_schema": "public", "ref_table": "users", "ref_columns": []any{"id"}, "on_delete": "cascade; drop table users"}},
		"fk column mismatch": {"op": "add_constraint", "extra": map[string]any{"kind": "foreign", "columns": []any{"user_id", "qty"}, "ref_schema": "public", "ref_table": "users", "ref_columns": []any{"id"}}},
		"drop other's":       {"op": "drop_constraint", "name": "users_pkey"},
	} {
		if _, err := alter("orders", p); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	// Dropping the parent's key while a foreign key needs it needs CASCADE; without it the server says no.
	if _, err := alter("users", map[string]any{"op": "drop_constraint", "name": "users_pkey"}); err == nil {
		t.Error("dropping a referenced key without CASCADE must fail")
	}

	// Triggers.
	trig := details("orders")["triggers"].([]any)
	if len(trig) != 1 || trig[0].(map[string]any)["name"] != "t_noop" || trig[0].(map[string]any)["enabled"] != "enabled" {
		t.Fatalf("triggers = %v", trig)
	}
	must("orders", map[string]any{"op": "disable_trigger", "name": "t_noop"})
	if got := details("orders")["triggers"].([]any)[0].(map[string]any)["enabled"]; got != "disabled" {
		t.Errorf("trigger state = %v", got)
	}
	must("orders", map[string]any{"op": "enable_trigger", "name": "t_noop"})
	if _, err := alter("orders", map[string]any{"op": "drop_trigger", "name": "RI_ConstraintTrigger_c_1"}); err == nil {
		t.Error("internal triggers must not be touched")
	}
	must("orders", map[string]any{"op": "drop_trigger", "name": "t_noop"})
	if len(details("orders")["triggers"].([]any)) != 0 {
		t.Error("trigger not dropped")
	}
}
