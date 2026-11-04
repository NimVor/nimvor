package pgmgr

import (
	"context"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestObjectsAndOverview(t *testing.T) {
	c := startCluster(t)
	h := testhost.New(t)
	c.admin(t, h.Env)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	ctx := context.Background()
	mustCall(t, r, "pg.role_create", map[string]any{"name": "owner_o", "password": pwA})
	mustCall(t, r, "pg.database_create", map[string]any{"name": "db_o", "owner": "owner_o"})
	a, err := c.as("owner_o", pwA, "db_o")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE items (id serial PRIMARY KEY, name text UNIQUE, qty int CHECK (qty >= 0))`,
		`INSERT INTO items (name, qty) SELECT 'i' || g, g FROM generate_series(1, 300) g`,
		`CREATE INDEX items_qty ON items (qty)`,
		`CREATE TABLE log (id int, at timestamptz) PARTITION BY RANGE (at)`,
		`CREATE TABLE log_2026 PARTITION OF log FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')`,
		`CREATE VIEW v_items AS SELECT id, name FROM items`,
		`CREATE MATERIALIZED VIEW m_items AS SELECT qty, count(*) FROM items GROUP BY qty`,
		`CREATE TABLE "100%_odd" (x int)`,
		`CREATE TABLE plain (x int)`,
		`COMMENT ON TABLE plain IS 'a plain table'`,
		`CREATE FUNCTION touch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$`,
		`CREATE TRIGGER t_touch BEFORE INSERT ON plain FOR EACH ROW EXECUTE FUNCTION touch()`,
		`ANALYZE`,
	} {
		if _, err := a.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	a.Close(ctx)

	list := func(p map[string]any) map[string]any {
		p["database"] = "db_o"
		return asMap(t, mustCall(t, r, "pg.objects", p))
	}
	names := func(m map[string]any) []string {
		var out []string
		for _, it := range m["items"].([]any) {
			out = append(out, it.(map[string]any)["name"].(string))
		}
		return out
	}
	all := list(map[string]any{})
	if all["total"].(float64) != 7 {
		t.Fatalf("expected 7 objects, got %v: %v", all["total"], names(all))
	}
	byName := map[string]map[string]any{}
	for _, it := range all["items"].([]any) {
		m := it.(map[string]any)
		byName[m["name"].(string)] = m
	}
	if it := byName["items"]; it["kind"] != "table" || it["rows_estimate"].(float64) != 300 || it["indexes"].(float64) != 3 ||
		it["columns"].(float64) != 3 || it["has_primary_key"] != true || it["total_bytes"].(float64) <= it["data_bytes"].(float64)-1 {
		t.Errorf("items: %v", it)
	}
	if it := byName["log"]; it["kind"] != "partitioned table" || it["partitions"].(float64) != 1 {
		t.Errorf("log: %v", it)
	}
	if it := byName["log_2026"]; it["partition_of"] != "log" {
		t.Errorf("log_2026: %v", it)
	}
	if byName["v_items"]["kind"] != "view" || byName["m_items"]["kind"] != "materialized view" {
		t.Errorf("view kinds: %v %v", byName["v_items"], byName["m_items"])
	}
	if it := byName["plain"]; it["comment"] != "a plain table" || it["triggers"].(float64) != 1 {
		t.Errorf("plain: %v", it)
	}
	// Filters: kind, search (wildcards are literal), sort, paging.
	if got := names(list(map[string]any{"kind": "view"})); len(got) != 1 || got[0] != "v_items" {
		t.Errorf("kind filter: %v", got)
	}
	if got := names(list(map[string]any{"search": "100%_"})); len(got) != 1 || got[0] != "100%_odd" {
		t.Errorf("literal search: %v", got)
	}
	if got := names(list(map[string]any{"search": "%"})); len(got) != 1 {
		t.Errorf("a percent sign must match only a percent sign: %v", got)
	}
	if got := names(list(map[string]any{"sort": "rows", "dir": "desc", "limit": 1})); got[0] != "items" && got[0] != "m_items" {
		t.Errorf("sort by rows: %v", got)
	}
	pg := list(map[string]any{"limit": 3, "offset": 6})
	if len(pg["items"].([]any)) != 1 || pg["total"].(float64) != 7 {
		t.Errorf("paging: %v", pg)
	}
	if _, err := call(t, r, "pg.objects", map[string]any{"database": "db_o", "kind": "nonsense"}); err == nil {
		t.Error("an unknown kind must be refused")
	}
	if _, err := call(t, r, "pg.objects", map[string]any{"database": "no_such_db"}); err == nil {
		t.Error("an unknown database must be refused")
	}

	ov := asMap(t, mustCall(t, r, "pg.db_overview", map[string]any{"database": "db_o"}))
	if ov["tables"].(float64) != 5 || ov["views"].(float64) != 1 || ov["materialized_views"].(float64) != 1 || ov["owner"] != "owner_o" ||
		ov["functions"].(float64) < 1 || ov["size_bytes"].(float64) <= 0 {
		t.Errorf("overview: %v", ov)
	}
	if b := asMap(t, ov["capabilities"]); b["partitioning"] != true {
		t.Errorf("capabilities: %v", b)
	}
	if strings.Contains(strings.ToLower(asJSON(ov)), "password") {
		t.Errorf("the overview must not carry a password: %v", ov)
	}
}
