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
	if t0 := tb["tables"].([]any)[0].(map[string]any); t0["columns"].(float64) < 2 || t0["indexes"].(float64) < 1 || t0["data_bytes"].(float64) <= 0 || t0["engine"] == "" {
		t.Fatalf("table statistics = %+v", t0)
	}
	ovw := do("db.browse_overview", `{"database":"nimvor_br"}`)
	if ovw["tables"].(float64) != 1 || ovw["views"].(float64) != 0 || ovw["rows"].(float64) < 0 || ovw["server"] == "" || (ovw["family"] != "mysql" && ovw["family"] != "mariadb") || ovw["data_bytes"].(float64) <= 0 {
		t.Fatalf("overview = %+v", ovw)
	}
	if _, err := call("db.browse_overview", `{"database":"nimvor_br_other_not_mine; DROP"}`); err == nil {
		t.Fatal("overview accepted an invalid database name")
	}
	st := do("db.browse_structure", `{"database":"nimvor_br","table":"items"}`)
	if k := st["key"].([]any); len(k) != 1 || k[0] != "id" || !strings.Contains(st["create"].(string), "CREATE TABLE `items`") {
		t.Fatalf("structure = %+v", st)
	}
	// Bulk operations: confirmation, per-table results, REPAIR only where the engine supports it, injection-shaped names.
	do("db.query", `{"database":"nimvor_br","sql":"CREATE TABLE my_isam (a INT) ENGINE=MyISAM; INSERT INTO my_isam VALUES (1); CREATE TABLE `+"`we``ird; DROP TABLE items`"+` (x INT); CREATE VIEW v_items AS SELECT id FROM items"}`)
	if _, err := call("db.browse_bulk", `{"database":"nimvor_br","op":"drop","tables":["my_isam"]}`); err == nil {
		t.Fatal("drop without confirm accepted")
	}
	if _, err := call("db.browse_bulk", `{"database":"nimvor_br","op":"format","tables":["my_isam"],"confirm":true}`); err == nil {
		t.Fatal("unknown bulk op accepted")
	}
	bm := do("db.browse_bulk", `{"database":"nimvor_br","op":"repair","tables":["items","my_isam","v_items","missing"]}`)
	if bm["succeeded"].(float64) != 1 || bm["failed"].(float64) != 3 {
		t.Fatalf("repair mix = %+v", bm)
	}
	if m := bm["results"].([]any)[0].(map[string]any)["message"].(string); !strings.Contains(m, "does not support REPAIR") {
		t.Fatalf("InnoDB repair message = %q", m)
	}
	if bm := do("db.browse_bulk", `{"database":"nimvor_br","op":"optimize","tables":["items","my_isam"]}`); bm["failed"].(float64) != 0 {
		t.Fatalf("optimize = %+v", bm)
	}
	if bm := do("db.browse_bulk", `{"database":"nimvor_br","op":"check","tables":["items","v_items"]}`); bm["succeeded"].(float64) != 1 {
		t.Fatalf("check = %+v", bm)
	}
	dry := do("db.browse_bulk", `{"database":"nimvor_br","op":"drop","tables":["we`+"`"+`ird; DROP TABLE items"],"dry_run":true}`)
	if st := dry["results"].([]any)[0].(map[string]any)["statement"]; st != "DROP TABLE `we``ird; DROP TABLE items`" {
		t.Fatalf("dry-run statement = %v", st)
	}
	if bm := do("db.browse_bulk", `{"database":"nimvor_br","op":"truncate","tables":["my_isam","items"],"confirm":true}`); bm["succeeded"].(float64) != 2 {
		t.Fatalf("truncate = %+v", bm)
	}
	if bm := do("db.browse_bulk", `{"database":"nimvor_br","op":"drop","tables":["my_isam","v_items","we`+"`"+`ird; DROP TABLE items"],"confirm":true}`); bm["succeeded"].(float64) != 3 {
		t.Fatalf("drop = %+v", bm)
	}
	// Table options, duplicate, index rename and CHECK constraints on a real server.
	do("db.query", `{"database":"nimvor_br","sql":"CREATE TABLE opt (id INT AUTO_INCREMENT PRIMARY KEY, qty INT, KEY k_qty (qty)) ENGINE=InnoDB; INSERT INTO opt (qty) VALUES (5),(7)"}`)
	for _, p := range []string{
		`{"op":"comment_table","comment":"it's a table"}`, `{"op":"set_row_format","row_format":"dynamic"}`, `{"op":"set_charset","charset":"utf8mb4","collation":"utf8mb4_unicode_ci","convert":true}`,
		`{"op":"set_auto_increment","auto_increment":500}`, `{"op":"rename_index","name":"k_qty","new_name":"idx_qty"}`, `{"op":"add_check","name":"ck_qty","check":{"column":"qty","op":"ge","value":"0"}}`,
		`{"op":"duplicate_table","new_name":"opt_copy","with_data":true}`,
	} {
		do("db.browse_alter", `{"database":"nimvor_br","table":"opt",`+p[1:])
	}
	os := do("db.browse_structure", `{"database":"nimvor_br","table":"opt"}`)
	if !strings.Contains(os["create"].(string), "idx_qty") || !strings.Contains(os["create"].(string), "COMMENT='it''s a table'") || !strings.Contains(strings.ToLower(os["create"].(string)), "ck_qty") {
		t.Fatalf("structure after options = %v", os["create"])
	}
	if c := do("db.query", `{"database":"nimvor_br","sql":"SELECT COUNT(*) FROM opt_copy"}`); c["results"].([]any)[0].(map[string]any)["rows"].([]any)[0].([]any)[0] != "2" {
		t.Fatalf("duplicate copied the wrong number of rows: %+v", c)
	}
	// A CHECK the existing data violates is refused by the server and nothing changes.
	if ck := os["checks"].([]any); len(ck) != 1 || ck[0].(map[string]any)["name"] != "ck_qty" {
		t.Fatalf("checks listed = %+v", os["checks"])
	}
	if _, err := call("db.browse_alter", `{"database":"nimvor_br","table":"opt","op":"add_check","name":"ck_big","check":{"column":"qty","op":"gt","value":"100"}}`); err == nil {
		t.Fatal("a CHECK that existing rows violate was accepted")
	}
	do("db.browse_alter", `{"database":"nimvor_br","table":"opt","op":"drop_check","name":"ck_qty"}`)
	if _, err := call("db.browse_alter", `{"database":"nimvor_br","table":"opt","op":"set_engine","engine":"InnoDB; DROP TABLE opt"}`); err == nil {
		t.Fatal("engine injection accepted")
	}
	do("db.browse_bulk", `{"database":"nimvor_br","op":"drop","tables":["opt","opt_copy"],"confirm":true}`)
	// Routines, triggers and events: listing, definitions, enable/disable, drop (only by confirmed, existing names).
	do("db.query", `{"database":"nimvor_br","sql":"CREATE TABLE logt (id INT AUTO_INCREMENT PRIMARY KEY, m VARCHAR(20)); CREATE FUNCTION f_one() RETURNS INT DETERMINISTIC RETURN 1; CREATE PROCEDURE p_log(IN msg VARCHAR(20)) INSERT INTO logt (m) VALUES (msg); CREATE TRIGGER trg_log BEFORE INSERT ON logt FOR EACH ROW SET NEW.m = UPPER(NEW.m); CREATE EVENT ev_keep ON SCHEDULE EVERY 1 DAY DO DELETE FROM logt WHERE id < 0"}`)
	rt := do("db.browse_routines", `{"database":"nimvor_br"}`)
	if len(rt["routines"].([]any)) != 2 || len(rt["triggers"].([]any)) != 1 || len(rt["events"].([]any)) != 1 || rt["triggers"].([]any)[0].(map[string]any)["table"] != "logt" {
		t.Fatalf("routines listing = %+v", rt)
	}
	if def := do("db.browse_object_definition", `{"database":"nimvor_br","kind":"routine","type":"function","name":"f_one"}`)["definition"].(string); !strings.Contains(def, "f_one") {
		t.Fatalf("function definition = %q", def)
	}
	if def := do("db.browse_object_definition", `{"database":"nimvor_br","kind":"trigger","name":"trg_log"}`)["definition"].(string); !strings.Contains(def, "UPPER") {
		t.Fatalf("trigger definition = %q", def)
	}
	do("db.browse_object_op", `{"database":"nimvor_br","kind":"event","name":"ev_keep","op":"disable"}`)
	if ev := do("db.browse_routines", `{"database":"nimvor_br"}`)["events"].([]any)[0].(map[string]any)["status"]; ev != "DISABLED" {
		t.Fatalf("event status after disable = %v", ev)
	}
	do("db.browse_object_op", `{"database":"nimvor_br","kind":"event","name":"ev_keep","op":"enable"}`)
	for _, bad := range []string{
		`{"database":"nimvor_br","kind":"routine","type":"function","name":"f_one","op":"drop"}`,               // no confirm
		`{"database":"nimvor_br","kind":"routine","type":"function","name":"nope","op":"drop","confirm":true}`, // does not exist
		`{"database":"nimvor_br","kind":"routine","type":"function","name":"f_one` + "`" + `; DROP TABLE logt","op":"drop","confirm":true}`,
		`{"database":"nimvor_br","kind":"table","name":"logt","op":"drop","confirm":true}`, // not a droppable kind here
		`{"database":"nimvor_br","kind":"event","name":"ev_keep","op":"truncate"}`,
	} {
		if _, err := call("db.browse_object_op", bad); err == nil {
			t.Fatalf("accepted: %s", bad)
		}
	}
	for _, ok := range []string{`{"kind":"routine","type":"procedure","name":"p_log"}`, `{"kind":"routine","type":"function","name":"f_one"}`, `{"kind":"trigger","name":"trg_log"}`, `{"kind":"event","name":"ev_keep"}`} {
		do("db.browse_object_op", `{"database":"nimvor_br","op":"drop","confirm":true,`+ok[1:])
	}
	if rt := do("db.browse_routines", `{"database":"nimvor_br"}`); len(rt["routines"].([]any))+len(rt["triggers"].([]any))+len(rt["events"].([]any)) != 0 {
		t.Fatalf("objects left after drop: %+v", rt)
	}

	// Sequences (MariaDB only) and partitions.
	sq := do("db.browse_sequences", `{"database":"nimvor_br"}`)
	if sq["supported"] == true {
		do("db.browse_sequence_op", `{"database":"nimvor_br","op":"create","name":"seq_t","start":100,"increment":5}`)
		do("db.browse_sequence_op", `{"database":"nimvor_br","op":"set_value","name":"seq_t","value":500}`)
		do("db.browse_sequence_op", `{"database":"nimvor_br","op":"alter","name":"seq_t","max":100000,"cycle":true}`)
		l := do("db.browse_sequences", `{"database":"nimvor_br"}`)["sequences"].([]any)
		if len(l) != 1 || l[0].(map[string]any)["increment"].(float64) != 5 || l[0].(map[string]any)["max"].(float64) != 100000 || l[0].(map[string]any)["cycle"] != true {
			t.Fatalf("sequences = %+v", l)
		}
		for _, bad := range []string{`{"database":"nimvor_br","op":"drop","name":"seq_t"}`, `{"database":"nimvor_br","op":"drop","name":"items","confirm":true}`, `{"database":"nimvor_br","op":"alter","name":"seq_t","increment":0}`} {
			if _, err := call("db.browse_sequence_op", bad); err == nil {
				t.Fatalf("sequence op accepted: %s", bad)
			}
		}
		do("db.browse_sequence_op", `{"database":"nimvor_br","op":"drop","name":"seq_t","confirm":true}`)
	} else {
		if _, err := call("db.browse_sequence_op", `{"database":"nimvor_br","op":"create","name":"s"}`); err == nil {
			t.Fatal("sequence created on MySQL")
		}
	}
	do("db.query", `{"database":"nimvor_br","sql":"CREATE TABLE ev (id INT NOT NULL, yr INT NOT NULL) PARTITION BY RANGE (yr) (PARTITION p2024 VALUES LESS THAN (2025), PARTITION p2025 VALUES LESS THAN (2026)); INSERT INTO ev VALUES (1, 2024), (2, 2025)"}`)
	pt := do("db.browse_partitions", `{"database":"nimvor_br","table":"ev"}`)
	if pt["partitioned"] != true || pt["method"] != "RANGE" || len(pt["partitions"].([]any)) != 2 {
		t.Fatalf("partitions = %+v", pt)
	}
	do("db.browse_alter", `{"database":"nimvor_br","table":"ev","op":"add_partition","name":"p2026","part_kind":"range","part_value":"2027"}`)
	do("db.browse_alter", `{"database":"nimvor_br","table":"ev","op":"analyze_partition","name":"p2024,p2025"}`)
	do("db.browse_alter", `{"database":"nimvor_br","table":"ev","op":"truncate_partition","name":"p2024"}`)
	do("db.browse_alter", `{"database":"nimvor_br","table":"ev","op":"drop_partition","name":"p2026"}`)
	if c := do("db.query", `{"database":"nimvor_br","sql":"SELECT COUNT(*) FROM ev"}`); c["results"].([]any)[0].(map[string]any)["rows"].([]any)[0].([]any)[0] != "1" {
		t.Fatalf("partition truncate removed the wrong rows: %+v", c)
	}
	if _, err := call("db.browse_alter", `{"database":"nimvor_br","table":"ev","op":"drop_partition","name":"p2025`+"`"+`; DROP TABLE ev"}`); err == nil {
		t.Fatal("partition name injection accepted")
	}
	if np := do("db.browse_partitions", `{"database":"nimvor_br","table":"items"}`); np["partitioned"] != false {
		t.Fatalf("unpartitioned table reported partitioned: %+v", np)
	}
	do("db.browse_alter", `{"database":"nimvor_br","table":"ev","op":"remove_partitioning"}`)
	do("db.browse_bulk", `{"database":"nimvor_br","op":"drop","tables":["ev","logt"],"confirm":true}`)

	// The other database's tables are out of reach by name.
	if bm := do("db.browse_bulk", `{"database":"nimvor_br","op":"drop","tables":["secret"],"confirm":true}`); bm["succeeded"].(float64) != 0 {
		t.Fatalf("dropped a table of another database: %+v", bm)
	}
	// Restore the rows the following checks expect.
	do("db.query", `{"database":"nimvor_br","sql":"INSERT INTO items (name, note, data) VALUES ('a', NULL, 0x00FF), ('b', 'NULL', NULL), ('c\\ttab', 'x', NULL)"}`)

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
