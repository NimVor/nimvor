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

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
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
		for _, db := range []string{"whacp_br", "whacp_br_other"} {
			a, _ := r.Lookup("db.drop")
			a.Handler(ctx, json.RawMessage(`{"name":"`+db+`","confirm":"`+db+`"}`))
		}
	})
	do("db.create", `{"name":"whacp_br"}`)
	do("db.create", `{"name":"whacp_br_other"}`)
	if _, err := c.Query(ctx, "CREATE TABLE whacp_br_other.secret (v TEXT); INSERT INTO whacp_br_other.secret VALUES ('top secret');"); err != nil {
		t.Fatal(err)
	}

	// SQL console: several statements, NULL vs 'NULL', binary data.
	q := do("db.query", `{"database":"whacp_br","sql":"CREATE TABLE items (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(50), note TEXT NULL, data BLOB NULL); INSERT INTO items (name, note, data) VALUES ('a', NULL, 0x00FF), ('b', 'NULL', NULL), ('c\\ttab', 'x', NULL); SELECT * FROM items ORDER BY id"}`)
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
	for _, sql := range []string{"SELECT * FROM whacp_br_other.secret", "USE whacp_br_other", "CREATE DATABASE whacp_br_evil", "SET GLOBAL max_connections = 5", "SELECT * FROM mysql.user", "SELECT LOAD_FILE('/etc/shadow') IS NULL AS blocked"} {
		q := do("db.query", `{"database":"whacp_br","sql":`+jsonString(sql)+`}`)
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
	q = do("db.query", `{"database":"whacp_br","sql":"SELECT 1; SELECT nope"}`)
	if len(q["results"].([]any)) != 1 || !strings.Contains(q["error"].(string), "nope") || q["error_statement"].(float64) != 2 {
		t.Fatalf("partial = %+v", q)
	}

	// Tables, structure, rows.
	tb := do("db.browse_tables", `{"database":"whacp_br"}`)
	if ts := tb["tables"].([]any); len(ts) != 1 || ts[0].(map[string]any)["name"] != "items" {
		t.Fatalf("tables = %+v", tb)
	}
	st := do("db.browse_structure", `{"database":"whacp_br","table":"items"}`)
	if k := st["key"].([]any); len(k) != 1 || k[0] != "id" || !strings.Contains(st["create"].(string), "CREATE TABLE `items`") {
		t.Fatalf("structure = %+v", st)
	}
	pg := do("db.browse_rows", `{"database":"whacp_br","table":"items","limit":2,"order":"name","desc":true}`)
	if pg["total"].(float64) != 3 || len(pg["rows"].([]any)) != 2 || pg["rows"].([]any)[0].([]any)[1] != "c\ttab" {
		t.Fatalf("rows page = %+v", pg)
	}
	pg = do("db.browse_rows", `{"database":"whacp_br","table":"items","filter":{"column":"note","op":"null"}}`)
	if pg["total"].(float64) != 1 || pg["keys"].([]any)[0].(map[string]any)["id"] != "1" {
		t.Fatalf("filtered = %+v", pg)
	}
	if _, err := call("db.browse_rows", `{"database":"whacp_br","table":"items","order":"id; DROP TABLE items"}`); err == nil {
		t.Fatal("unknown order column accepted")
	}

	// Edit, insert, delete.
	do("db.browse_save", `{"database":"whacp_br","table":"items","key":{"id":"2"},"values":{"note":null,"data":{"hex":"cafe"},"name":"it's"}}`)
	row := do("db.browse_row", `{"database":"whacp_br","table":"items","key":{"id":"2"}}`)
	rr := row["rows"].([]any)[0].([]any)
	if rr[1] != "it's" || rr[2] != nil || rr[3].(map[string]any)["hex"] != "cafe" {
		t.Fatalf("saved row = %+v", rr)
	}
	ins := do("db.browse_save", `{"database":"whacp_br","table":"items","insert":true,"values":{"name":"new","id":{"default":true}}}`)
	if ins["insert_id"].(float64) != 4 {
		t.Fatalf("insert = %+v", ins)
	}
	del := do("db.browse_delete", `{"database":"whacp_br","table":"items","keys":[{"id":"1"},{"id":"4"}]}`)
	if del["deleted"].(float64) != 2 {
		t.Fatalf("delete = %+v", del)
	}
	if _, err := call("db.browse_save", `{"database":"whacp_br","table":"items","key":{"name":"b"},"values":{"note":"x"}}`); err == nil {
		t.Fatal("non-key column accepted as key")
	}

	// A view created from the browser keeps working afterwards.
	do("db.query", `{"database":"whacp_br","sql":"CREATE VIEW v AS SELECT name FROM items"}`)
	if out, err := c.Query(ctx, "SELECT COUNT(*) FROM whacp_br.v;"); err != nil || out[0][0] != "2" {
		t.Fatalf("view: %v %v", out, err)
	}

	// Between requests the account is locked and hidden from the overview.
	lk, err := c.Query(ctx, "SHOW CREATE USER "+account(BrowserAccount("whacp_br"), "localhost")+";")
	if err != nil || len(lk) != 1 || !strings.Contains(lk[0][0], "ACCOUNT LOCK") {
		t.Fatalf("browser account not locked: %v %v", lk, err)
	}
	ov := do("db.overview", `{}`)
	for _, u := range ov["users"].([]any) {
		if strings.HasPrefix(u.(map[string]any)["user"].(string), BrowserPrefix) {
			t.Fatalf("browser account listed: %+v", u)
		}
	}
	if _, err := call("db.user_create", `{"user":"whacp_b_x","host":"localhost","password":"xxxxxxxxxxxxxxxx"}`); err == nil {
		t.Fatal("reserved user name accepted")
	}
	// Dropping the database drops its browser account.
	do("db.drop", `{"name":"whacp_br","confirm":"whacp_br"}`)
	if ok, _ := c.userExists(ctx, BrowserAccount("whacp_br"), "localhost"); ok {
		t.Fatal("browser account left behind")
	}
	var ae *actions.Error
	if _, err := call("db.browse_tables", `{"database":"whacp_br"}`); !errors.As(err, &ae) {
		t.Fatalf("missing database: %v", err)
	}
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }
