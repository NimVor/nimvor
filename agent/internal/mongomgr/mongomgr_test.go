package mongomgr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func setup(t *testing.T) (*testhost.Host, *actions.Registry) {
	t.Helper()
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	return h, r
}

func call(t *testing.T, r *actions.Registry, name, params string) (any, error) {
	t.Helper()
	a, lerr := r.Lookup(name)
	if lerr != nil {
		t.Fatalf("no action %s: %v", name, lerr)
	}
	return a.Handler(context.Background(), json.RawMessage(params))
}

func TestStatus(t *testing.T) {
	h, r := setup(t)
	h.Runner.Unavailable["mongosh"] = true
	res, _ := call(t, r, "mongo.status", `{}`)
	if res.(Status).Installed {
		t.Fatal("status should report not installed")
	}
	delete(h.Runner.Unavailable, "mongosh")
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "systemctl" && len(args) >= 2 && args[0] == "is-active" {
			return "active\n", nil
		}
		if id == "mongosh" {
			return "7.0.14\n", nil
		}
		return "", nil
	}
	res, err := call(t, r, "mongo.status", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	st := res.(Status)
	if !st.Installed || !st.Running || st.Version != "7.0.14" {
		t.Fatalf("status: %+v", st)
	}
}

func TestOverviewParsesDatabasesAndUsers(t *testing.T) {
	h, r := setup(t)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id != "mongosh" {
			return "", nil
		}
		script := args[len(args)-1]
		switch {
		case strings.Contains(script, "listDatabases"):
			return `[{"name":"admin","sizeOnDisk":40960,"empty":false},{"name":"shop","sizeOnDisk":8192,"empty":false},{"name":"local","sizeOnDisk":1,"empty":false}]`, nil
		case strings.Contains(script, "usersInfo"):
			return `[{"user":"root","db":"admin","roles":[{"role":"root","db":"admin"}]},{"user":"shopuser","db":"shop","roles":[{"role":"readWrite","db":"shop"}]}]`, nil
		}
		return "", nil
	}
	res, err := call(t, r, "mongo.overview", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	dbs := m["databases"].([]Database)
	if len(dbs) != 1 || dbs[0].Name != "shop" || dbs[0].Size != 8192 {
		t.Fatalf("databases (internal not filtered?): %+v", dbs)
	}
	users := m["users"].([]User)
	if len(users) != 1 || users[0].User != "shopuser" || users[0].DB != "shop" || users[0].Roles[0] != "readWrite" {
		t.Fatalf("users: %+v", users)
	}
}

// mongosh renders a BSON Long with JSON.stringify as an object
// {"low":..,"high":..,"unsigned":..} rather than a plain number, so
// sizeOnDisk arrives as an object. The listing must still parse (it used to
// fail with "unexpected output").
func TestOverviewParsesMongoshLongSizes(t *testing.T) {
	h, r := setup(t)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id != "mongosh" {
			return "", nil
		}
		script := args[len(args)-1]
		switch {
		case strings.Contains(script, "listDatabases"):
			return `[{"name":"admin","sizeOnDisk":{"high":0,"low":135168,"unsigned":false},"empty":false},` +
				`{"name":"shop","sizeOnDisk":{"high":0,"low":8192,"unsigned":false},"empty":false}]`, nil
		case strings.Contains(script, "usersInfo"):
			return `[]`, nil
		}
		return "", nil
	}
	res, err := call(t, r, "mongo.overview", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	dbs := res.(map[string]any)["databases"].([]Database)
	if len(dbs) != 1 || dbs[0].Name != "shop" || dbs[0].Size != 8192 {
		t.Fatalf("mongosh Long sizes not parsed: %+v", dbs)
	}
}

// The database browser lists collections, reads documents with an EJSON
// filter, and deletes a document by _id. collections comes over --eval;
// documents and document_delete are fed on stdin (so the filter never hits
// the command line).
func TestBrowserCollectionsDocumentsAndDelete(t *testing.T) {
	h, r := setup(t)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id != "mongosh" {
			return "", nil
		}
		// stdin-fed scripts (documents, delete) are drained by the fake runner
		// into Stdins before the handler runs; --eval scripts are in args.
		script := strings.Join(args, " ")
		if n := len(h.Runner.Stdins); n > 0 {
			script += " " + h.Runner.Stdins[n-1]
		}
		switch {
		case strings.Contains(script, "getCollectionNames"):
			return `[{"name":"orders","count":3},{"name":"items","count":10}]`, nil
		case strings.Contains(script, "deleteOne"):
			return `{"deleted":1}`, nil
		case strings.Contains(script, "insertOne"):
			return `{"inserted_id":"64f1"}`, nil
		case strings.Contains(script, "replaceOne"):
			return `{"matched":1,"modified":1}`, nil
		case strings.Contains(script, "aggregate(p"):
			return `{"documents":[{"name":"a","total":3}],"ms":4}`, nil
		case strings.Contains(script, ".find(EJSON.parse") && strings.Contains(script, "count:docs.length,content"):
			return `{"count":2,"content":"[{\"_id\":\"a\"},{\"_id\":\"b\"}]"}`, nil
		case strings.Contains(script, "find(q)"):
			return `{"documents":[{"_id":"64f0","name":"a"}],"total":1}`, nil
		}
		return "", nil
	}

	cols := mustCall(t, r, "mongo.collections", `{"db":"shop"}`)["collections"].([]Collection)
	if len(cols) != 2 || cols[0].Name != "orders" || cols[0].Count != 3 {
		t.Fatalf("collections: %+v", cols)
	}

	docs := mustCall(t, r, "mongo.documents", `{"db":"shop","collection":"orders","filter":"{\"name\":\"a\"}","limit":50}`)
	if docs["total"].(int64) != 1 {
		t.Fatalf("documents total: %+v", docs)
	}
	if !strings.Contains(string(docs["documents"].(json.RawMessage)), `"name":"a"`) {
		t.Fatalf("documents payload: %s", docs["documents"])
	}

	del := mustCall(t, r, "mongo.document_delete", `{"db":"shop","collection":"orders","id":"\"64f0\""}`)
	if del["deleted"].(int64) != 1 {
		t.Fatalf("delete: %+v", del)
	}

	ins := mustCall(t, r, "mongo.document_insert", `{"db":"shop","collection":"orders","document":"{\"name\":\"a\"}"}`)
	if !strings.Contains(string(ins["inserted_id"].(json.RawMessage)), "64f1") {
		t.Fatalf("insert: %v", ins["inserted_id"])
	}
	upd := mustCall(t, r, "mongo.document_update", `{"db":"shop","collection":"orders","id":"\"64f0\"","document":"{\"name\":\"b\"}"}`)
	if upd["matched"].(int64) != 1 || upd["modified"].(int64) != 1 {
		t.Fatalf("update: %+v", upd)
	}

	agg := mustCall(t, r, "mongo.aggregate", `{"db":"shop","collection":"orders","pipeline":"[{\"$match\":{\"name\":\"a\"}}]"}`)
	if !strings.Contains(string(agg["documents"].(json.RawMessage)), `"a"`) {
		t.Fatalf("aggregate documents: %s", agg["documents"])
	}
	if _, err := call(t, r, "mongo.aggregate", `{"db":"shop","collection":"orders","pipeline":"{}"}`); err == nil {
		t.Error("a non-array pipeline must be rejected")
	}

	exp := mustCall(t, r, "mongo.export", `{"db":"shop","collection":"orders","format":"json"}`)
	if exp["count"].(int) != 2 || !strings.Contains(exp["content"].(string), `"_id":"a"`) {
		t.Fatalf("export: %+v", exp)
	}
	if _, err := call(t, r, "mongo.export", `{"db":"shop","collection":"orders","format":"xml"}`); err == nil {
		t.Error("an unknown export format must be rejected")
	}

	// Bad JSON / missing _id are refused before mongosh runs.
	if _, err := call(t, r, "mongo.document_insert", `{"db":"shop","collection":"orders","document":"not json"}`); err == nil {
		t.Error("a non-JSON document must be rejected")
	}
	if _, err := call(t, r, "mongo.document_insert", `{"db":"shop","collection":"orders","document":"[1,2]"}`); err == nil {
		t.Error("a non-object document must be rejected")
	}
	if _, err := call(t, r, "mongo.document_update", `{"db":"shop","collection":"orders","id":"bad","document":"{}"}`); err == nil {
		t.Error("a non-EJSON _id must be rejected")
	}

	// A bad filter and a bad collection name are refused before mongosh runs.
	if _, err := call(t, r, "mongo.documents", `{"db":"shop","collection":"orders","filter":"not json"}`); err == nil {
		t.Error("a non-JSON filter must be rejected")
	}
	if _, err := call(t, r, "mongo.documents", `{"db":"shop","collection":"system.users"}`); err == nil {
		t.Error("a system collection must be rejected")
	}
	if _, err := call(t, r, "mongo.document_delete", `{"db":"shop","collection":"orders","id":"nope"}`); err == nil {
		t.Error("a non-EJSON _id must be rejected")
	}
}

const sampleConf = "net:\n  port: 27017\n  bindIp: 127.0.0.1\nstorage:\n  dbPath: /var/lib/mongodb\nsystemLog:\n  destination: file\n  path: /var/log/mongodb/mongod.log\nsecurity:\n  authorization: disabled\n"

func writeHostFile(t *testing.T, h *testhost.Host, path, body string) {
	t.Helper()
	full := filepath.Join(h.Base, strings.TrimPrefix(path, "/"))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigGetParsesTheFile(t *testing.T) {
	h, r := setup(t)
	writeHostFile(t, h, ConfigFile, sampleConf)
	cfg := mustCallAny(t, r, "mongo.config_get", `{}`).(Config)
	if cfg.Port != 27017 || cfg.BindIp != "127.0.0.1" || cfg.DBPath != "/var/lib/mongodb" || cfg.Authorization {
		t.Fatalf("parsed config: %+v", cfg)
	}
	if !strings.Contains(cfg.Raw, "authorization: disabled") {
		t.Fatal("raw file not returned")
	}
}

func TestConfigSetRewritesAndRestarts(t *testing.T) {
	h, r := setup(t)
	writeHostFile(t, h, ConfigFile, sampleConf)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "systemctl" && len(args) >= 2 && args[0] == "is-active" {
			return "active\n", nil
		}
		return "", nil
	}
	cfg := mustCallAny(t, r, "mongo.config_set", `{"port":27018,"bind_ip":"0.0.0.0","authorization":true}`).(Config)
	if cfg.Port != 27018 || cfg.BindIp != "0.0.0.0" || !cfg.Authorization {
		t.Fatalf("returned config: %+v", cfg)
	}
	on := readHostFile(t, h, ConfigFile)
	if !strings.Contains(on, "port: 27018") || !strings.Contains(on, "bindIp: 0.0.0.0") || !strings.Contains(on, "authorization: enabled") {
		t.Fatalf("file not rewritten:\n%s", on)
	}

	// A bad port is refused before anything is written.
	if _, err := call(t, r, "mongo.config_set", `{"port":0,"bind_ip":"127.0.0.1"}`); err == nil {
		t.Error("a bad port must be rejected")
	}
}

func TestConfigSetRollsBackWhenRestartFails(t *testing.T) {
	h, r := setup(t)
	writeHostFile(t, h, ConfigFile, sampleConf)
	var restarts int
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "systemctl" && len(args) >= 1 && args[0] == "restart" {
			restarts++
			if restarts == 1 {
				return "job failed", fmt.Errorf("exit status 1")
			}
			return "", nil // the rollback restart succeeds
		}
		if id == "systemctl" && len(args) >= 2 && args[0] == "is-active" {
			return "active\n", nil
		}
		return "", nil
	}
	if _, err := call(t, r, "mongo.config_set", `{"port":27099,"bind_ip":"127.0.0.1","authorization":true}`); err == nil {
		t.Fatal("a failed restart must return an error")
	}
	if on := readHostFile(t, h, ConfigFile); strings.Contains(on, "27099") || !strings.Contains(on, "port: 27017") {
		t.Fatalf("the previous configuration was not restored:\n%s", on)
	}
	if restarts < 2 {
		t.Fatalf("expected a rollback restart, got %d restarts", restarts)
	}
}

func TestLogTail(t *testing.T) {
	h, r := setup(t)
	writeHostFile(t, h, ConfigFile, sampleConf)
	writeHostFile(t, h, "/var/log/mongodb/mongod.log", "line one\nline two\nline three\n")
	res := mustCall(t, r, "mongo.log", `{"lines":2}`)
	if res["path"].(string) != "/var/log/mongodb/mongod.log" {
		t.Fatalf("path: %v", res["path"])
	}
	if res["lines"].(string) != "line two\nline three" {
		t.Fatalf("tail: %q", res["lines"])
	}
}

func readHostFile(t *testing.T, h *testhost.Host, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.Base, strings.TrimPrefix(path, "/")))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestIndexesListCreateDrop(t *testing.T) {
	h, r := setup(t)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id != "mongosh" {
			return "", nil
		}
		script := strings.Join(args, " ")
		if n := len(h.Runner.Stdins); n > 0 {
			script += " " + h.Runner.Stdins[n-1]
		}
		switch {
		case strings.Contains(script, "getIndexes"):
			return `[{"v":2,"key":{"_id":1},"name":"_id_"},{"v":2,"key":{"email":1},"name":"email_1","unique":true}]`, nil
		case strings.Contains(script, "createIndex"):
			return `{"name":"email_1"}`, nil
		case strings.Contains(script, "dropIndex"):
			return `{"ok":1}`, nil
		}
		return "", nil
	}

	idx := mustCall(t, r, "mongo.indexes", `{"db":"shop","collection":"users"}`)
	if !strings.Contains(string(idx["indexes"].(json.RawMessage)), `"email_1"`) {
		t.Fatalf("indexes: %s", idx["indexes"])
	}
	cr := mustCall(t, r, "mongo.index_create", `{"db":"shop","collection":"users","keys":"{\"email\":1}","options":"{\"unique\":true}"}`)
	if cr["name"].(string) != "email_1" {
		t.Fatalf("create: %+v", cr)
	}
	if _, err := call(t, r, "mongo.index_drop", `{"db":"shop","collection":"users","name":"email_1"}`); err != nil {
		t.Fatalf("drop: %v", err)
	}

	// The _id index may not be dropped; empty keys and bad names are refused.
	if _, err := call(t, r, "mongo.index_drop", `{"db":"shop","collection":"users","name":"_id_"}`); err == nil {
		t.Error("dropping _id_ must be refused")
	}
	if _, err := call(t, r, "mongo.index_create", `{"db":"shop","collection":"users","keys":"{}"}`); err == nil {
		t.Error("empty keys must be refused")
	}
	if _, err := call(t, r, "mongo.index_create", `{"db":"shop","collection":"users","keys":"not json"}`); err == nil {
		t.Error("non-JSON keys must be refused")
	}
}

func mustCallAny(t *testing.T, r *actions.Registry, name, params string) any {
	t.Helper()
	res, err := call(t, r, name, params)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func mustCall(t *testing.T, r *actions.Registry, name, params string) map[string]any {
	t.Helper()
	res, err := call(t, r, name, params)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res.(map[string]any)
}

// A failure listing users must not hide the databases: the overview still
// returns the databases and reports the users error.
func TestOverviewReturnsDatabasesWhenUsersFail(t *testing.T) {
	h, r := setup(t)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id != "mongosh" {
			return "", nil
		}
		script := args[len(args)-1]
		if strings.Contains(script, "listDatabases") {
			return `[{"name":"shop","sizeOnDisk":8192,"empty":false}]`, nil
		}
		if strings.Contains(script, "usersInfo") {
			return "not authorized", fmt.Errorf("exit status 1")
		}
		return "", nil
	}
	res, err := call(t, r, "mongo.overview", `{}`)
	if err != nil {
		t.Fatalf("overview should not fail when only users fail: %v", err)
	}
	m := res.(map[string]any)
	if dbs := m["databases"].([]Database); len(dbs) != 1 || dbs[0].Name != "shop" {
		t.Fatalf("databases: %+v", dbs)
	}
	if m["users_error"] == nil {
		t.Fatal("a users error should be reported")
	}
}

func TestDatabaseCreateAndDrop(t *testing.T) {
	h, r := setup(t)
	var scripts []string
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "mongosh" {
			scripts = append(scripts, args[len(args)-1])
		}
		return "", nil
	}
	if _, err := call(t, r, "mongo.database_create", `{"name":"shop"}`); err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 1 || !strings.Contains(scripts[0], "getSiblingDB('shop')") || !strings.Contains(scripts[0], "createCollection('_whacp')") {
		t.Fatalf("create script: %v", scripts)
	}
	// Dangerous or malformed names are refused before mongosh runs.
	for _, bad := range []string{`{"name":"a'b"}`, `{"name":"a.b"}`, `{"name":"a/b"}`, `{"name":""}`, `{"name":"a b"}`} {
		if _, err := call(t, r, "mongo.database_create", bad); err == nil {
			t.Errorf("accepted bad name: %s", bad)
		}
	}
	// Drop requires the confirmation to match.
	if _, err := call(t, r, "mongo.database_drop", `{"name":"shop","confirm":"nope"}`); err == nil {
		t.Error("drop without a matching confirm should fail")
	}
	if _, err := call(t, r, "mongo.database_drop", `{"name":"shop","confirm":"shop"}`); err != nil {
		t.Fatal(err)
	}
	if last := scripts[len(scripts)-1]; !strings.Contains(last, "dropDatabase()") {
		t.Fatalf("drop script: %s", last)
	}
}

func TestUserCreateValidationAndScript(t *testing.T) {
	h, r := setup(t)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) { return "", nil }
	if _, err := call(t, r, "mongo.user_create", `{"user":"shopuser","password":"longenough1","db":"shop","role":"readWrite"}`); err != nil {
		t.Fatal(err)
	}
	// The password-bearing script is fed on stdin, so it (and the password)
	// never reach the command line / process list.
	if len(h.Runner.Stdins) != 1 {
		t.Fatalf("the user script was not fed on stdin: %v", h.Runner.Stdins)
	}
	script := h.Runner.Stdins[0]
	for _, want := range []string{"getSiblingDB('shop')", "createUser(", "'shopuser'", "role:'readWrite'", "JSON.parse("} {
		if !strings.Contains(script, want) {
			t.Errorf("user script lacks %q:\n%s", want, script)
		}
	}
	for _, c := range h.Runner.Calls {
		if strings.Contains(c, "longenough1") || strings.Contains(c, "--eval") {
			t.Errorf("the password/script must not appear on the command line: %s", c)
		}
	}
	for _, bad := range []string{
		`{"user":"ok","password":"short","db":"shop","role":"readWrite"}`,
		`{"user":"ok","password":"longenough1","db":"shop","role":"superuser"}`,
		`{"user":"bad'user","password":"longenough1","db":"shop"}`,
		`{"user":"ok","password":"longenough1","db":"a b"}`,
	} {
		if _, err := call(t, r, "mongo.user_create", bad); err == nil {
			t.Errorf("accepted bad user input: %s", bad)
		}
	}
}

func TestActionsNeedMongoInstalled(t *testing.T) {
	h, r := setup(t)
	h.Runner.Unavailable["mongosh"] = true
	for _, a := range []string{"mongo.overview", "mongo.database_create", "mongo.user_create"} {
		params := `{}`
		if a == "mongo.database_create" {
			params = `{"name":"shop"}`
		}
		if a == "mongo.user_create" {
			params = `{"user":"u","password":"longenough1","db":"shop"}`
		}
		if _, err := call(t, r, a, params); err == nil {
			t.Errorf("%s should require MongoDB installed", a)
		}
	}
}
