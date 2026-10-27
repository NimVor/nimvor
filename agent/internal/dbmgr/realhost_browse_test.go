//go:build realhost

package dbmgr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

func TestRealHostDatabaseBrowser(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := filesystem.Host()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(config.Default(), root, platform.Detect(root), execx.NewSystemRunner(30*time.Second), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)
	ctx := context.Background()
	call := func(name, params string) (map[string]any, error) {
		a, err := r.Lookup(name)
		if err != nil {
			t.Fatalf("no action %s", name)
		}
		res, err := a.Handler(ctx, json.RawMessage(params))
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(res)
		var m map[string]any
		json.Unmarshal(b, &m)
		return m, nil
	}
	do := func(name, params string) map[string]any {
		t.Helper()
		m, err := call(name, params)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return m
	}
	c := Client{Env: env}
	t.Cleanup(func() {
		for _, db := range []string{"nimvor_br", "nimvor_br_other"} {
			a, _ := r.Lookup("db.drop")
			a.Handler(ctx, json.RawMessage(`{"name":"`+db+`","confirm":"`+db+`"}`))
		}
	})
	do("db.create", `{"name":"nimvor_br"}`)
	do("db.create", `{"name":"nimvor_br_other"}`)
	if _, err := c.Query(ctx, "CREATE TABLE nimvor_br_other.secret (v TEXT); INSERT INTO nimvor_br_other.secret VALUES ('top secret');"); err != nil {
		t.Fatal(err)
	}

	// SQL console: several statements, NULL vs 'NULL', binary data.
	q := do("db.query", `{"database":"nimvor_br","sql":"CREATE TABLE items (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(50), note TEXT NULL, data BLOB NULL); INSERT INTO items (name, note, data) VALUES ('a', NULL, 0x00FF), ('b', 'NULL', NULL), ('c\\ttab', 'x', NULL); SELECT * FROM items ORDER BY id"}`)
	res := q["results"].([]any)
	if len(res) != 3 || q["error"] != nil {
		t.Fatalf("query = %+v", q)
	}
	if res[1].(map[string]any)["affected_rows"].(float64) != 3 {
		t.Fatalf("insert result = %+v", res[1])
	}
	rows := res[2].(map[string]any)["rows"].([]any)
	r0, r1 := rows[0].([]any), rows[1].([]any)
	if r0[2] != nil || r1[2] != "NULL" || r0[3].(map[string]any)["hex"] != "00ff" || r0[0] != "1" {
		t.Fatalf("rows = %+v", rows)
	}

	// The browser account cannot see or touch another database, nor
	// global settings.
	for _, sql := range []string{"SELECT * FROM nimvor_br_other.secret", "USE nimvor_br_other", "CREATE DATABASE nimvor_br_evil", "SET GLOBAL max_connections = 5", "SELECT * FROM mysql.user", "SELECT LOAD_FILE('/etc/shadow') IS NULL AS blocked"} {
		q := do("db.query", `{"database":"nimvor_br","sql":`+jsonString(sql)+`}`)
		if strings.HasPrefix(sql, "SELECT LOAD_FILE") {
			if q["error"] == nil && q["results"].([]any)[0].(map[string]any)["rows"].([]any)[0].([]any)[0] != "1" {
				t.Fatalf("LOAD_FILE worked: %+v", q)
			}
			continue
		}
		if q["error"] == nil {
			t.Fatalf("%s was allowed: %+v", sql, q)
		}
	}
	// A failing statement reports the results before it.
	q = do("db.query", `{"database":"nimvor_br","sql":"SELECT 1; SELECT nope"}`)
	if len(q["results"].([]any)) != 1 || !strings.Contains(q["error"].(string), "nope") || q["error_statement"].(float64) != 2 {
		t.Fatalf("partial = %+v", q)
	}

	// Tables, structure, rows.
	tb := do("db.browse_tables", `{"database":"nimvor_br"}`)
	if ts := tb["tables"].([]any); len(ts) != 1 || ts[0].(map[string]any)["name"] != "items" {
		t.Fatalf("tables = %+v", tb)
	}
	st := do("db.browse_structure", `{"database":"nimvor_br","table":"items"}`)
	if k := st["key"].([]any); len(k) != 1 || k[0] != "id" || !strings.Contains(st["create"].(string), "CREATE TABLE `items`") {
		t.Fatalf("structure = %+v", st)
	}
	pg := do("db.browse_rows", `{"database":"nimvor_br","table":"items","limit":2,"order":"name","desc":true}`)
	if pg["total"].(float64) != 3 || len(pg["rows"].([]any)) != 2 || pg["rows"].([]any)[0].([]any)[1] != "c\ttab" {
		t.Fatalf("rows page = %+v", pg)
	}
	pg = do("db.browse_rows", `{"database":"nimvor_br","table":"items","filter":{"column":"note","op":"null"}}`)
	if pg["total"].(float64) != 1 || pg["keys"].([]any)[0].(map[string]any)["id"] != "1" {
		t.Fatalf("filtered = %+v", pg)
	}
	if _, err := call("db.browse_rows", `{"database":"nimvor_br","table":"items","order":"id; DROP TABLE items"}`); err == nil {
		t.Fatal("unknown order column accepted")
	}

	// Edit, insert, delete.
	do("db.browse_save", `{"database":"nimvor_br","table":"items","key":{"id":"2"},"values":{"note":null,"data":{"hex":"cafe"},"name":"it's"}}`)
	row := do("db.browse_row", `{"database":"nimvor_br","table":"items","key":{"id":"2"}}`)
	rr := row["rows"].([]any)[0].([]any)
	if rr[1] != "it's" || rr[2] != nil || rr[3].(map[string]any)["hex"] != "cafe" {
		t.Fatalf("saved row = %+v", rr)
	}
	ins := do("db.browse_save", `{"database":"nimvor_br","table":"items","insert":true,"values":{"name":"new","id":{"default":true}}}`)
	if ins["insert_id"].(float64) != 4 {
		t.Fatalf("insert = %+v", ins)
	}
	del := do("db.browse_delete", `{"database":"nimvor_br","table":"items","keys":[{"id":"1"},{"id":"4"}]}`)
	if del["deleted"].(float64) != 2 {
		t.Fatalf("delete = %+v", del)
	}
	if _, err := call("db.browse_save", `{"database":"nimvor_br","table":"items","key":{"name":"b"},"values":{"note":"x"}}`); err == nil {
		t.Fatal("non-key column accepted as key")
	}

	// Search across columns (wildcards literal), structure changes, foreign
	// keys, CSV export and import (all or nothing).
	pg = do("db.browse_rows", `{"database":"nimvor_br","table":"items","search":"IT'S"}`)
	if pg["total"].(float64) != 1 {
		t.Fatalf("search = %+v", pg)
	}
	if pg = do("db.browse_rows", `{"database":"nimvor_br","table":"items","search":"%"}`); pg["total"].(float64) != 0 {
		t.Fatalf("wildcard search matched: %+v", pg)
	}
	do("db.browse_alter", `{"database":"nimvor_br","table":"owners","op":"create_table","columns":[{"name":"id","type":"INT","unsigned":true,"auto_increment":true},{"name":"email","type":"VARCHAR","length":"190"}],"primary":["id"],"engine":"InnoDB"}`)
	do("db.browse_alter", `{"database":"nimvor_br","table":"items","op":"add_column","column":{"name":"owner_id","type":"INT","unsigned":true,"nullable":true,"default_kind":"null"},"after":"name"}`)
	do("db.browse_alter", `{"database":"nimvor_br","table":"items","op":"add_column","column":{"name":"status","type":"ENUM","values":["new","done"],"default_kind":"value","default":"new","comment":"state"}}`)
	do("db.browse_alter", `{"database":"nimvor_br","table":"items","op":"add_index","kind":"unique","name":"u_name","index_columns":["name"]}`)
	do("db.browse_alter", `{"database":"nimvor_br","table":"items","op":"add_foreign_key","name":"fk_owner","index_columns":["owner_id"],"ref_table":"owners","ref_columns":["id"],"on_delete":"set null"}`)
	do("db.browse_alter", `{"database":"nimvor_br","table":"items","op":"modify_column","name":"note","column":{"name":"remark","type":"VARCHAR","length":"100","nullable":true}}`)
	st = do("db.browse_structure", `{"database":"nimvor_br","table":"items"}`)
	fk := st["foreign_keys"].([]any)
	if len(fk) != 1 || fk[0].(map[string]any)["ref_table"] != "owners" || fk[0].(map[string]any)["on_delete"] != "SET NULL" {
		t.Fatalf("foreign keys = %+v", st["foreign_keys"])
	}
	var status *map[string]any
	for _, col := range st["columns"].([]any) {
		cm := col.(map[string]any)
		if cm["name"] == "status" {
			status = &cm
		}
		if cm["name"] == "note" {
			t.Fatal("note was not renamed")
		}
	}
	if status == nil || (*status)["base_type"] != "ENUM" || len((*status)["values"].([]any)) != 2 {
		t.Fatalf("status column = %+v", status)
	}
	if st["status"].(map[string]any)["engine"] != "InnoDB" {
		t.Fatalf("table status = %+v", st["status"])
	}
	// The foreign key is enforced.
	if q := do("db.query", `{"database":"nimvor_br","sql":"UPDATE items SET owner_id = 99"}`); q["error"] == nil {
		t.Fatal("foreign key not enforced")
	}
	// A reference to another database's table is not possible (names are one identifier).
	if _, err := call("db.browse_alter", `{"database":"nimvor_br","table":"items","op":"add_foreign_key","index_columns":["owner_id"],"ref_table":"nimvor_br_other.secret","ref_columns":["v"]}`); err == nil {
		t.Fatal("cross-database foreign key accepted")
	}
	ex := do("db.browse_export_csv", `{"database":"nimvor_br","table":"items"}`)
	if !strings.HasPrefix(ex["csv"].(string), "id,name,owner_id,remark,data,status\n") || !strings.Contains(ex["csv"].(string), "0xcafe") || !strings.Contains(ex["csv"].(string), `\N`) {
		t.Fatalf("csv = %q", ex["csv"])
	}
	im := do("db.browse_import_csv", `{"database":"nimvor_br","table":"items","header":true,"csv":"name;remark;skip\nimported;\\N;x\n\"semi;colon\";r;y\n","delimiter":";","columns":["name","remark",""]}`)
	if im["affected_rows"].(float64) != 2 {
		t.Fatalf("import = %+v", im)
	}
	if pg = do("db.browse_rows", `{"database":"nimvor_br","table":"items","filter":{"column":"name","op":"=","value":"semi;colon"}}`); pg["total"].(float64) != 1 {
		t.Fatalf("imported row = %+v", pg)
	}
	before := do("db.browse_rows", `{"database":"nimvor_br","table":"items"}`)["total"].(float64)
	if _, err := call("db.browse_import_csv", `{"database":"nimvor_br","table":"items","csv":"fresh\nimported\n","columns":["name"]}`); err == nil {
		t.Fatal("duplicate in unique index accepted")
	}
	if after := do("db.browse_rows", `{"database":"nimvor_br","table":"items"}`)["total"].(float64); after != before {
		t.Fatalf("failed import left rows: %v → %v", before, after)
	}
	if im = do("db.browse_import_csv", `{"database":"nimvor_br","table":"items","csv":"fresh\nimported\n","columns":["name"],"mode":"ignore"}`); im["affected_rows"].(float64) != 1 {
		t.Fatalf("import ignore = %+v", im)
	}
	do("db.browse_alter", `{"database":"nimvor_br","table":"items","op":"drop_foreign_key","name":"fk_owner"}`)
	do("db.browse_alter", `{"database":"nimvor_br","table":"items","op":"drop_index","name":"u_name"}`)
	do("db.browse_alter", `{"database":"nimvor_br","table":"items","op":"drop_column","name":"status"}`)
	if o := do("db.browse_alter", `{"database":"nimvor_br","table":"items","op":"optimize_table"}`); o["result"] == nil {
		t.Fatalf("optimize = %+v", o)
	}
	do("db.browse_alter", `{"database":"nimvor_br","table":"owners","op":"rename_table","new_name":"people"}`)
	do("db.browse_alter", `{"database":"nimvor_br","table":"people","op":"truncate_table"}`)
	do("db.browse_alter", `{"database":"nimvor_br","table":"people","op":"drop_table"}`)
	if out, err := c.Query(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = 'nimvor_br_other';"); err != nil || out[0][0] != "1" {
		t.Fatalf("other database touched: %v %v", out, err)
	}

	// A view created from the browser keeps working afterwards.
	do("db.query", `{"database":"nimvor_br","sql":"CREATE VIEW v AS SELECT name FROM items"}`)
	if out, err := c.Query(ctx, "SELECT COUNT(*) FROM nimvor_br.v;"); err != nil || out[0][0] != "5" {
		t.Fatalf("view: %v %v", out, err)
	}

	// Between requests the account is locked and hidden from the overview.
	lk, err := c.Query(ctx, "SHOW CREATE USER "+account(BrowserAccount("nimvor_br"), "localhost")+";")
	if err != nil || len(lk) != 1 || !strings.Contains(lk[0][0], "ACCOUNT LOCK") {
		t.Fatalf("browser account not locked: %v %v", lk, err)
	}
	ov := do("db.overview", `{}`)
	for _, u := range ov["users"].([]any) {
		if strings.HasPrefix(u.(map[string]any)["user"].(string), BrowserPrefix) {
			t.Fatalf("browser account listed: %+v", u)
		}
	}
	if _, err := call("db.user_create", `{"user":"nimvor_b_x","host":"localhost","password":"xxxxxxxxxxxxxxxx"}`); err == nil {
		t.Fatal("reserved user name accepted")
	}
	// Dropping the database drops its browser account.
	do("db.drop", `{"name":"nimvor_br","confirm":"nimvor_br"}`)
	if ok, _ := c.userExists(ctx, BrowserAccount("nimvor_br"), "localhost"); ok {
		t.Fatal("browser account left behind")
	}
	var ae *actions.Error
	if _, err := call("db.browse_tables", `{"database":"nimvor_br"}`); !errors.As(err, &ae) {
		t.Fatalf("missing database: %v", err)
	}
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }
