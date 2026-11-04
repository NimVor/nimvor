package mongomgr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/testhost"
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
		case strings.Contains(script, "deleteMany({}).deletedCount"):
			return `{"deleted":5}`, nil
		case strings.Contains(script, "createCollection"), strings.Contains(script, ".drop()"), strings.Contains(script, "renameCollection"):
			return "", nil
		case strings.Contains(script, "inserted:ins,errors:errs"):
			return `{"inserted":2,"errors":1,"total":3}`, nil
		case strings.Contains(script, "deleteOne"):
			return `{"deleted":1}`, nil
		case strings.Contains(script, "insertOne"):
			return `{"inserted_id":"64f1"}`, nil
		case strings.Contains(script, "replaceOne"):
			return `{"matched":1,"modified":1}`, nil
		case strings.Contains(script, "$objectToArray"):
			return `[{"count":[{"n":2}],"fields":[{"field":"_id","total":2,"types":[{"type":"objectId","count":2}]},{"field":"age","total":1,"types":[{"type":"int","count":1}]}]}]`, nil
		case strings.Contains(script, "explain('queryPlanner')"):
			return `{"queryPlanner":{"winningPlan":{"stage":"COLLSCAN"}},"ok":1}`, nil
		case strings.Contains(script, "aggregate(p"):
			return `{"documents":[{"name":"a","total":3}],"ms":4}`, nil
		case strings.Contains(script, ".find(EJSON.parse") && strings.Contains(script, "count:docs.length,content"):
			return `{"count":2,"content":"[{\"_id\":\"a\"},{\"_id\":\"b\"}]"}`, nil
		case strings.Contains(script, "c.find(q"):
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

	// A sort and a projection are passed through to the find, as data.
	mustCall(t, r, "mongo.documents", `{"db":"shop","collection":"orders","sort":"{\"name\":1}","projection":"{\"name\":1}","limit":50}`)
	last := h.Runner.Stdins[len(h.Runner.Stdins)-1]
	if !strings.Contains(last, ".sort(EJSON.parse(") || !strings.Contains(last, `{\"name\":1}`) {
		t.Fatalf("documents sort/projection not in script: %s", last)
	}
	// A malformed sort is rejected before mongosh runs.
	if _, err := call(t, r, "mongo.documents", `{"db":"shop","collection":"orders","sort":"[1,2]"}`); err == nil {
		t.Error("a non-object sort must be rejected")
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

	exp2 := mustCall(t, r, "mongo.explain", `{"db":"shop","collection":"orders","pipeline":"[{\"$match\":{}}]"}`)
	if !strings.Contains(string(exp2["explain"].(json.RawMessage)), "COLLSCAN") {
		t.Fatalf("explain: %s", exp2["explain"])
	}
	if _, err := call(t, r, "mongo.explain", `{"db":"shop","collection":"orders","pipeline":"notjson"}`); err == nil {
		t.Error("an invalid explain pipeline must be rejected")
	}

	exp := mustCall(t, r, "mongo.export", `{"db":"shop","collection":"orders","format":"json"}`)
	if exp["count"].(int) != 2 || !strings.Contains(exp["content"].(string), `"_id":"a"`) {
		t.Fatalf("export: %+v", exp)
	}
	if _, err := call(t, r, "mongo.export", `{"db":"shop","collection":"orders","format":"xml"}`); err == nil {
		t.Error("an unknown export format must be rejected")
	}

	// Collection management: create, empty, rename, drop.
	mustCall(t, r, "mongo.collection_create", `{"db":"shop","collection":"carts"}`)
	emptied := mustCall(t, r, "mongo.collection_empty", `{"db":"shop","collection":"orders"}`)
	if emptied["deleted"].(int64) != 5 {
		t.Fatalf("empty: %+v", emptied)
	}
	mustCall(t, r, "mongo.collection_rename", `{"db":"shop","collection":"orders","name":"orders_old"}`)
	mustCall(t, r, "mongo.collection_drop", `{"db":"shop","collection":"carts"}`)
	if _, err := call(t, r, "mongo.collection_create", `{"db":"shop","collection":"system.evil"}`); err == nil {
		t.Error("a system collection name must be rejected")
	}

	// Schema: the sampled count and per-field types come back.
	sch := mustCall(t, r, "mongo.schema", `{"db":"shop","collection":"orders","sample":100}`)
	if sch["sampled"].(int) != 2 {
		t.Fatalf("schema sampled: %+v", sch)
	}
	fields := sch["fields"].([]map[string]any)
	if len(fields) != 2 || fields[0]["field"].(string) != "_id" {
		t.Fatalf("schema fields: %+v", fields)
	}

	// Import: a JSON array and JSONL both parse to documents and insert.
	imp := mustCall(t, r, "mongo.import", `{"db":"shop","collection":"orders","format":"json","content":"[{\"a\":1},{\"a\":2},{\"a\":3}]"}`)
	if imp["inserted"].(int) != 2 || imp["errors"].(int) != 1 || imp["total"].(int) != 3 {
		t.Fatalf("import: %+v", imp)
	}
	if _, err := call(t, r, "mongo.import", `{"db":"shop","collection":"orders","format":"jsonl","content":"{\"a\":1}\n{\"a\":2}"}`); err != nil {
		t.Fatalf("jsonl import: %v", err)
	}
	// Malformed uploads and non-object elements are rejected before the agent
	// touches MongoDB.
	for _, bad := range []string{
		`{"db":"shop","collection":"orders","format":"json","content":"not json"}`,
		`{"db":"shop","collection":"orders","format":"json","content":"[1,2,3]"}`,
		`{"db":"shop","collection":"orders","format":"json","content":"[]"}`,
		`{"db":"shop","collection":"orders","format":"jsonl","content":"{\"a\":1}\noops"}`,
		`{"db":"shop","collection":"orders","format":"csv","content":"a,b"}`,
	} {
		if _, err := call(t, r, "mongo.import", bad); err == nil {
			t.Errorf("a malformed import must be rejected: %s", bad)
		}
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

	// The raw config-file editor replaces the whole file and restarts.
	edited := "net:\n  port: 28000\n  bindIp: 127.0.0.1\n"
	if _, err := call(t, r, "mongo.config_write", `{"content":`+jsonString(edited)+`}`); err != nil {
		t.Fatalf("config_write: %v", err)
	}
	if on := readHostFile(t, h, ConfigFile); !strings.Contains(on, "port: 28000") {
		t.Fatalf("raw config not written:\n%s", on)
	}
	// Empty content is refused.
	if _, err := call(t, r, "mongo.config_write", `{"content":"  "}`); err == nil {
		t.Error("empty config content must be rejected")
	}
}

// jsonString quotes s as a JSON string literal for embedding in a params blob.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
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

func TestCollectionStats(t *testing.T) {
	h, r := setup(t)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id != "mongosh" {
			return "", nil
		}
		script := strings.Join(args, " ")
		if n := len(h.Runner.Stdins); n > 0 {
			script += " " + h.Runner.Stdins[n-1]
		}
		if strings.Contains(script, "collStats") {
			return `{"count":1248,"size":13002342,"storageSize":5242880,"avgObjSize":10418,` +
				`"nindexes":4,"totalIndexSize":262144,"capped":false,"type":"collection",` +
				`"validator":{"$jsonSchema":{"required":["email"]}},"validationLevel":"strict","validationAction":"error"}`, nil
		}
		return "", nil
	}
	out := mustCallAny(t, r, "mongo.collection_stats", `{"db":"shop","collection":"users"}`)
	raw, ok := out.(json.RawMessage)
	if !ok {
		t.Fatalf("expected json.RawMessage, got %T", out)
	}
	for _, want := range []string{`"count":1248`, `"nindexes":4`, `"validationLevel":"strict"`, `"$jsonSchema"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("stats missing %s: %s", want, raw)
		}
	}
	// Names are validated.
	if _, err := call(t, r, "mongo.collection_stats", `{"db":"bad space","collection":"users"}`); err == nil {
		t.Error("invalid db name must be refused")
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
	if len(scripts) != 1 || !strings.Contains(scripts[0], "getSiblingDB('shop')") || !strings.Contains(scripts[0], "createCollection('_nimvor')") {
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

func TestDatabaseUsersListsOneDatabase(t *testing.T) {
	h, r := setup(t)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "getUsers()") {
			return `[{"user":"shop_u","db":"shop","roles":[{"role":"readWrite","db":"shop"}]},{"user":"shop_ro","db":"shop","roles":[{"role":"read","db":"shop"}]}]`, nil
		}
		return "", nil
	}
	res := mustCall(t, r, "mongo.database_users", `{"db":"shop"}`)
	users := res["users"].([]map[string]any)
	if len(users) != 2 || users[0]["user"].(string) != "shop_u" {
		t.Fatalf("users: %+v", users)
	}
	if roles := users[0]["roles"].([]string); len(roles) != 1 || roles[0] != "readWrite" {
		t.Fatalf("roles: %+v", users[0]["roles"])
	}
	if _, err := call(t, r, "mongo.database_users", `{"db":"a b"}`); err == nil {
		t.Error("an invalid database name must be rejected")
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

func TestCollectionsDetailAndDbOverview(t *testing.T) {
	h, r := setup(t)
	var seen string
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id != "mongosh" {
			return "", nil
		}
		script := strings.Join(args, " ")
		if n := len(h.Runner.Stdins); n > 0 {
			script += " " + h.Runner.Stdins[n-1]
		}
		seen = script
		switch {
		case strings.Contains(script, "dbStats"):
			return `{"version":"7.0.5","dataSize":1000,"storageSize":4096,"indexSize":2048,"objects":10,"collections":2,"views":1,"timeseries":0,"indexes":3,"avgObjSize":100,"replicaSet":"rs0","primary":true,"secondary":false,"sharded":false,"hosts":["a:27017"]}`, nil
		case strings.Contains(script, "getCollectionInfos"):
			return `[{"name":"users","type":"collection","capped":false,"viewOn":null,"count":10,"size":1000,"storageSize":4096,"avgObjSize":100,"nindexes":2,"totalIndexSize":2048,"validated":true},` +
				`{"name":"active","type":"view","capped":false,"viewOn":"users","count":0,"size":0,"storageSize":0,"avgObjSize":0,"nindexes":0,"totalIndexSize":0,"validated":false}]`, nil
		}
		return "", nil
	}
	cols := mustCall(t, r, "mongo.collections_detail", `{"db":"shop"}`)["collections"].([]map[string]any)
	if len(cols) != 2 || cols[1]["type"] != "view" || cols[0]["validated"] != true {
		t.Errorf("collections: %v", cols)
	}
	if !strings.Contains(seen, "'shop'") || !strings.Contains(seen, "system.") {
		t.Errorf("script must select the database and skip system collections: %s", seen)
	}
	ov := mustCall(t, r, "mongo.db_overview", `{"db":"shop"}`)
	if ov["replicaSet"] != "rs0" || ov["database"] != "shop" || ov["objects"].(float64) != 10 {
		t.Errorf("overview: %v", ov)
	}
	for _, bad := range []string{`{"db":"bad space"}`, `{"db":"x\"; db.dropDatabase(); \""}`} {
		if _, err := call(t, r, "mongo.db_overview", bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
		if _, err := call(t, r, "mongo.collections_detail", bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}

func TestCollectionsBulk(t *testing.T) {
	h, r := setup(t)
	var seen string
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "mongosh" {
			seen = strings.Join(args, " ")
			return `[{"name":"a","ok":true},{"name":"gone","ok":false,"error":"there is no such collection"}]`, nil
		}
		return "", nil
	}
	res := mustCall(t, r, "mongo.collections_bulk", `{"db":"shop","op":"drop","collections":["a","gone","a"],"confirm":true}`)
	if res["succeeded"] != 1 || res["failed"] != 1 {
		t.Errorf("result: %v", res)
	}
	if !strings.Contains(seen, "['a','gone']") && !strings.Contains(seen, `'a','gone'`) {
		t.Errorf("duplicates must be folded and names quoted: %s", seen)
	}
	for _, bad := range []string{
		`{"db":"shop","op":"drop","collections":["a"]}`,                                         // no confirm
		`{"db":"shop","op":"drop","collections":["a'); db.dropDatabase(); ('"],"confirm":true}`, // injection
		`{"db":"shop","op":"drop","collections":["system.users"],"confirm":true}`,               // system collection
		`{"db":"shop","op":"eval","collections":["a"],"confirm":true}`,                          // unknown op
		`{"db":"shop","op":"validate","collections":[]}`,                                        // nothing selected
	} {
		before := seen
		if _, err := call(t, r, "mongo.collections_bulk", bad); err == nil {
			t.Errorf("must be refused: %s", bad)
		}
		if seen != before {
			t.Errorf("a refused request reached mongosh: %s", bad)
		}
	}
	if _, err := call(t, r, "mongo.collections_bulk", `{"db":"shop","op":"validate","collections":["a"]}`); err != nil {
		t.Errorf("validate needs no confirmation: %v", err)
	}
}

func TestCollectionCreateExOptionsAreBuiltFromValidatedValues(t *testing.T) {
	h, r := setup(t)
	var seen string
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "mongosh" {
			seen = strings.Join(args, " ")
		}
		return "", nil
	}
	mustCall(t, r, "mongo.collection_create_ex", `{"db":"shop","collection":"logs","kind":"capped","size_bytes":1048576,"max_docs":1000}`)
	if !strings.Contains(seen, `"capped":true`) || !strings.Contains(seen, `"size":1048576`) || !strings.Contains(seen, `"max":1000`) {
		t.Errorf("capped script: %s", seen)
	}
	mustCall(t, r, "mongo.collection_create_ex", `{"db":"shop","collection":"metrics","kind":"timeseries","time_field":"ts","meta_field":"sensor","granularity":"minutes","expire_seconds":86400}`)
	if !strings.Contains(seen, `"timeField":"ts"`) || !strings.Contains(seen, `"granularity":"minutes"`) || !strings.Contains(seen, `"expireAfterSeconds":86400`) {
		t.Errorf("timeseries script: %s", seen)
	}
	seen = ""
	for _, bad := range []string{
		`{"db":"shop","collection":"x","kind":"capped","size_bytes":10}`,
		`{"db":"shop","collection":"x","kind":"timeseries","time_field":"ts'); db.dropDatabase(); ('"}`,
		`{"db":"shop","collection":"x","kind":"timeseries","time_field":"ts","meta_field":"ts"}`,
		`{"db":"shop","collection":"x","kind":"timeseries","time_field":"ts","granularity":"years"}`,
		`{"db":"shop","collection":"x","kind":"view"}`,
		`{"db":"shop","collection":"system.x"}`,
	} {
		if _, err := call(t, r, "mongo.collection_create_ex", bad); err == nil {
			t.Errorf("must be refused: %s", bad)
		}
	}
	if seen != "" {
		t.Errorf("a refused request reached mongosh: %s", seen)
	}
}

func TestCollectionCopyReportsWhatItDid(t *testing.T) {
	h, r := setup(t)
	reply := `{"copied":12,"indexes":2}`
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) { return reply, nil }
	res := mustCall(t, r, "mongo.collection_copy", `{"db":"shop","collection":"a","new_name":"b"}`)
	if res["copied"].(float64) != 12 {
		t.Errorf("copy: %v", res)
	}
	reply = `{"error":"a collection with that name already exists"}`
	if _, err := call(t, r, "mongo.collection_copy", `{"db":"shop","collection":"a","new_name":"b"}`); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("an existing target must be reported: %v", err)
	}
	for _, bad := range []string{`{"db":"shop","collection":"a","new_name":"a"}`, `{"db":"shop","collection":"a","new_name":"system.b"}`, `{"db":"shop","collection":"a","new_name":"b'); x(('"}`} {
		if _, err := call(t, r, "mongo.collection_copy", bad); err == nil {
			t.Errorf("must be refused: %s", bad)
		}
	}
}

// scriptSpy records what reaches mongosh (arguments and stdin) and answers with reply.
func scriptSpy(h *testhost.Host, reply *string) *string {
	var seen string
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "mongosh" {
			seen = strings.Join(args, " ")
			if n := len(h.Runner.Stdins); n > 0 {
				seen += " " + h.Runner.Stdins[n-1]
			}
			return *reply, nil
		}
		return "", nil
	}
	return &seen
}

func TestDocumentsBulkDuplicateAndStructuredUpdate(t *testing.T) {
	h, r := setup(t)
	reply := `{"deleted":2}`
	seen := scriptSpy(h, &reply)
	res := mustCall(t, r, "mongo.documents_delete_many", `{"db":"shop","collection":"orders","ids":["{\"$oid\":\"507f1f77bcf86cd799439011\"}","5"]}`)
	if res["deleted"].(float64) != 2 || !strings.Contains(*seen, "$in:ids") {
		t.Errorf("delete many: %v / %s", res, *seen)
	}
	reply = `{"matched":4,"modified":3}`
	res = mustCall(t, r, "mongo.documents_update_many", `{"db":"shop","collection":"orders","filter":"{\"status\":\"new\"}","ops":[{"op":"set","field":"status","value":"\"seen\""},{"op":"inc","field":"stats.views","value":"1"},{"op":"unset","field":"tmp"},{"op":"rename","field":"old","value":"fresh"}]}`)
	if res["modified"].(float64) != 3 {
		t.Errorf("update many: %v", res)
	}
	for _, want := range []string{`$set`, `$inc`, `$unset`, `$rename`, `updateMany`} {
		if !strings.Contains(*seen, want) {
			t.Errorf("script lacks %s: %s", want, *seen)
		}
	}
	reply = `{"matched":9}`
	mustCall(t, r, "mongo.documents_update_many", `{"db":"shop","collection":"orders","filter":"{}","all":true,"dry_run":true}`)
	if !strings.Contains(*seen, "countDocuments") || strings.Contains(*seen, "updateMany") {
		t.Errorf("dry run must only count: %s", *seen)
	}
	reply = `{"inserted_id":"x"}`
	mustCall(t, r, "mongo.document_duplicate", `{"db":"shop","collection":"orders","id":"{\"$oid\":\"507f1f77bcf86cd799439011\"}"}`)
	reply = `{"error":"there is no such document."}`
	if _, err := call(t, r, "mongo.document_duplicate", `{"db":"shop","collection":"orders","id":"5"}`); err == nil {
		t.Error("duplicating a missing document must say so")
	}

	*seen = ""
	for name, bad := range map[string]string{
		"no ids":            `{"db":"shop","collection":"c","ids":[]}`,
		"too many ids":      `{"db":"shop","collection":"c","ids":[` + strings.Repeat(`"1",`, 500) + `"1"]}`,
		"id not json":       `{"db":"shop","collection":"c","ids":["not json"]}`,
		"empty filter":      `{"db":"shop","collection":"c","filter":"","ops":[{"op":"set","field":"a","value":"1"}]}`,
		"where":             `{"db":"shop","collection":"c","filter":"{\"$where\":\"sleep(10000)\"}","ops":[{"op":"set","field":"a","value":"1"}]}`,
		"function":          `{"db":"shop","collection":"c","filter":"{\"$expr\":{\"$function\":{}}}","ops":[{"op":"set","field":"a","value":"1"}]}`,
		"_id field":         `{"db":"shop","collection":"c","filter":"{\"a\":1}","ops":[{"op":"set","field":"_id","value":"1"}]}`,
		"operator field":    `{"db":"shop","collection":"c","filter":"{\"a\":1}","ops":[{"op":"set","field":"$where","value":"1"}]}`,
		"injected field":    `{"db":"shop","collection":"c","filter":"{\"a\":1}","ops":[{"op":"set","field":"a'); db.dropDatabase(); ('","value":"1"}]}`,
		"duplicate field":   `{"db":"shop","collection":"c","filter":"{\"a\":1}","ops":[{"op":"set","field":"a","value":"1"},{"op":"unset","field":"a"}]}`,
		"unknown op":        `{"db":"shop","collection":"c","filter":"{\"a\":1}","ops":[{"op":"mul","field":"a","value":"2"}]}`,
		"bad value":         `{"db":"shop","collection":"c","filter":"{\"a\":1}","ops":[{"op":"set","field":"a","value":"{broken"}]}`,
		"rename to _id":     `{"db":"shop","collection":"c","filter":"{\"a\":1}","ops":[{"op":"rename","field":"a","value":"_id"}]}`,
		"no ops":            `{"db":"shop","collection":"c","filter":"{\"a\":1}","ops":[]}`,
		"system collection": `{"db":"shop","collection":"system.users","filter":"{\"a\":1}","ops":[{"op":"set","field":"a","value":"1"}]}`,
	} {
		action := "mongo.documents_update_many"
		if strings.Contains(name, "id") && strings.HasPrefix(name, "no ids") || name == "too many ids" || name == "id not json" {
			action = "mongo.documents_delete_many"
		}
		if _, err := call(t, r, action, bad); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	if *seen != "" {
		t.Errorf("a refused request reached mongosh: %s", *seen)
	}
}

func TestIndexStatsHideValidationAndViews(t *testing.T) {
	h, r := setup(t)
	reply := `{"stats":[{"name":"a_1","ops":12,"since":"2026-01-01T00:00:00Z"}]}`
	seen := scriptSpy(h, &reply)
	if st := mustCall(t, r, "mongo.index_stats", `{"db":"shop","collection":"orders"}`); len(st["stats"].([]any)) != 1 || !strings.Contains(*seen, "$indexStats") {
		t.Errorf("index stats: %v / %s", st, *seen)
	}
	reply = `{"hidden":true}`
	mustCall(t, r, "mongo.index_hide", `{"db":"shop","collection":"orders","name":"a_1","hidden":true}`)
	if !strings.Contains(*seen, "hidden:true") || !strings.Contains(*seen, "collMod") {
		t.Errorf("hide: %s", *seen)
	}
	reply = `{"ok":true}`
	mustCall(t, r, "mongo.validation_set", `{"db":"shop","collection":"orders","validator":"{\"$jsonSchema\":{\"required\":[\"email\"]}}","level":"moderate","action":"warn"}`)
	for _, want := range []string{"collMod", "validationLevel", "moderate", "warn", "jsonSchema"} {
		if !strings.Contains(*seen, want) {
			t.Errorf("validation script lacks %s: %s", want, *seen)
		}
	}
	mustCall(t, r, "mongo.validation_set", `{"db":"shop","collection":"orders","validator":"","level":"off"}`)
	reply = `{"created":"active_orders"}`
	mustCall(t, r, "mongo.view_create", `{"db":"shop","name":"active_orders","view_on":"orders","pipeline":"[{\"$match\":{\"status\":\"active\"}}]"}`)
	if !strings.Contains(*seen, "createView") {
		t.Errorf("view: %s", *seen)
	}
	reply = `{"error":"a collection or view with that name already exists."}`
	if _, err := call(t, r, "mongo.view_create", `{"db":"shop","name":"orders","view_on":"orders"}`); err == nil {
		t.Error("an existing name must be reported")
	}
	*seen = ""
	for name, c := range map[string][2]string{
		"hide _id":           {"mongo.index_hide", `{"db":"shop","collection":"o","name":"_id_","hidden":true}`},
		"hide injection":     {"mongo.index_hide", `{"db":"shop","collection":"o","name":"a'); x(('","hidden":true}`},
		"validator where":    {"mongo.validation_set", `{"db":"shop","collection":"o","validator":"{\"$where\":\"1\"}"}`},
		"validator two keys": {"mongo.validation_set", `{"db":"shop","collection":"o","validator":"{\"$jsonSchema\":{},\"$expr\":{}}"}`},
		"validator array":    {"mongo.validation_set", `{"db":"shop","collection":"o","validator":"[1]"}`},
		"bad level":          {"mongo.validation_set", `{"db":"shop","collection":"o","level":"paranoid"}`},
		"view out":           {"mongo.view_create", `{"db":"shop","name":"v","view_on":"o","pipeline":"[{\"$out\":\"x\"}]"}`},
		"view merge":         {"mongo.view_create", `{"db":"shop","name":"v","view_on":"o","pipeline":"[{\"$merge\":{\"into\":\"x\"}}]"}`},
		"view function":      {"mongo.view_create", `{"db":"shop","name":"v","view_on":"o","pipeline":"[{\"$addFields\":{\"a\":{\"$function\":{}}}}]"}`},
		"view not array":     {"mongo.view_create", `{"db":"shop","name":"v","view_on":"o","pipeline":"{}"}`},
		"view system name":   {"mongo.view_create", `{"db":"shop","name":"system.v","view_on":"o"}`},
	} {
		if _, err := call(t, r, c[0], c[1]); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	if *seen != "" {
		t.Errorf("a refused request reached mongosh: %s", *seen)
	}
}

func TestTrimToJSONIgnoresTerminalNoiseAroundTheValue(t *testing.T) {
	noisy := "\x1b[1G\x1b[0J \x1b[1Gtest> {\"matched\":3,\"list\":[1,2]}\n\x1b[1G\x1b[0J test> \x1b[7G"
	if got := string(trimToJSON([]byte(noisy))); got != `{"matched":3,"list":[1,2]}` {
		t.Errorf("got %q", got)
	}
	if got := string(trimToJSON([]byte("banner\n[{\"a\":1}]\n"))); got != `[{"a":1}]` {
		t.Errorf("array: %q", got)
	}
	if got := string(trimToJSON([]byte("no json here"))); got != "no json here" {
		t.Errorf("plain text must come back unchanged: %q", got)
	}
}

func TestScriptErrorsPrintedByMongoshAreFailures(t *testing.T) {
	noisy := "\x1b[1G\x1b[0J test> Uncaught:\nMongoServerError: Document failed validation\n  Additional information: {...}\n"
	if got := scriptError([]byte(noisy)); !strings.Contains(got, "Document failed validation") {
		t.Errorf("error line: %q", got)
	}
	if got := scriptError([]byte("MongoServerError: E11000 duplicate key error")); !strings.Contains(got, "E11000") {
		t.Errorf("direct error: %q", got)
	}
	for _, fine := range []string{`{"inserted_id":"x"}`, "test> {\"name\":\"MongoServerError in a value\"}", ""} {
		if got := scriptError([]byte(fine)); got != "" {
			t.Errorf("%q is not an error, got %q", fine, got)
		}
	}
	// End to end: an insert whose mongosh output carries an error is reported as failed.
	h, r := setup(t)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		return "Uncaught:\nMongoServerError: Document failed validation\n", nil
	}
	if _, err := call(t, r, "mongo.document_insert", `{"db":"shop","collection":"c","document":"{\"a\":1}"}`); err == nil {
		t.Error("a rejected insert must not be reported as done")
	}
}
