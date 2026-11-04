//go:build realhost

package mongomgr

import (
	"context"
	"encoding/json"
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

// TestMongoLiveBrowser runs the MongoDB browser actions against a real mongod and mongosh (no authentication, as a
// fresh MongoDB install). It is not named RealHost on purpose: the real-host gate treats a skip as a failure, and this
// needs MongoDB installed, so it has its own CI job and runs only when NIMVOR_MONGO_LIVE=1.
func TestMongoLiveBrowser(t *testing.T) {
	if os.Getenv("NIMVOR_MONGO_LIVE") != "1" {
		t.Skip("set NIMVOR_MONGO_LIVE=1 with a local mongod and mongosh")
	}
	root := filesystem.Host()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(config.Default(), root, platform.Detect(root), execx.NewSystemRunner(60*time.Second), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)
	ctx := context.Background()
	const db = "nimvor_live"
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
		_ = json.Unmarshal(b, &m)
		return m, nil
	}
	do := func(name, params string) map[string]any {
		t.Helper()
		m, err := call(name, params)
		if err != nil {
			t.Fatalf("%s %s: %v", name, params, err)
		}
		return m
	}
	t.Cleanup(func() { _, _ = call("mongo.database_drop", `{"name":"`+db+`","confirm":"`+db+`"}`) })

	// Collections of every kind, the detailed listing and the overview.
	do("mongo.collection_create_ex", `{"db":"`+db+`","collection":"people","kind":"collection"}`)
	do("mongo.collection_create_ex", `{"db":"`+db+`","collection":"logs","kind":"capped","size_bytes":65536,"max_docs":100}`)
	if _, err := call("mongo.collection_create_ex", `{"db":"`+db+`","collection":"ts","kind":"timeseries","time_field":"at","meta_field":"sensor","granularity":"minutes"}`); err != nil {
		t.Logf("time-series collections need MongoDB 5.0+: %v", err)
	}
	for i := 1; i <= 6; i++ {
		do("mongo.document_insert", `{"db":"`+db+`","collection":"people","document":"{\"name\":\"p`+string(rune('0'+i))+`\",\"age\":`+string(rune('0'+i))+`0,\"tags\":[\"a\"],\"when\":{\"$date\":\"2026-01-0`+string(rune('0'+i))+`T00:00:00Z\"}}"}`)
	}
	det := do("mongo.collections_detail", `{"db":"`+db+`"}`)["collections"].([]any)
	seen := map[string]map[string]any{}
	for _, c := range det {
		m := c.(map[string]any)
		seen[m["name"].(string)] = m
	}
	if seen["people"]["count"].(float64) != 6 || seen["people"]["type"] != "collection" || seen["logs"]["capped"] != true {
		t.Fatalf("collections_detail = %+v", seen)
	}
	ov := do("mongo.db_overview", `{"db":"`+db+`"}`)
	if ov["version"] == "" || ov["objects"].(float64) < 6 || ov["collections"].(float64) < 2 {
		t.Fatalf("db_overview = %+v", ov)
	}

	// Documents: structured update, duplicate, delete many; BSON types survive (date stays a date).
	pre := do("mongo.documents_update_many", `{"db":"`+db+`","collection":"people","filter":"{\"age\":{\"$gte\":40}}","dry_run":true}`)
	if pre["matched"].(float64) != 3 {
		t.Fatalf("dry run matched %v, want 3", pre["matched"])
	}
	up := do("mongo.documents_update_many", `{"db":"`+db+`","collection":"people","filter":"{\"age\":{\"$gte\":40}}","ops":[{"op":"set","field":"senior","value":"true"},{"op":"inc","field":"age","value":"1"},{"op":"rename","field":"name","value":"fullname"},{"op":"unset","field":"tags"}]}`)
	if up["modified"].(float64) != 3 {
		t.Fatalf("update many = %+v", up)
	}
	docs := do("mongo.documents", `{"db":"`+db+`","collection":"people","filter":"{\"senior\":true}","limit":10}`)
	first := docs["documents"].([]any)[0].(map[string]any)
	if _, ok := first["fullname"]; !ok || first["tags"] != nil || first["age"].(float64) < 41 {
		t.Fatalf("updated document = %+v", first)
	}
	if w, ok := first["when"].(map[string]any); !ok || w["$date"] == nil {
		t.Fatalf("the date lost its BSON type: %+v", first["when"])
	}
	idJSON, _ := json.Marshal(first["_id"])
	dup := do("mongo.document_duplicate", `{"db":"`+db+`","collection":"people","id":`+jsonString(string(idJSON))+`}`)
	if dup["inserted_id"] == nil {
		t.Fatalf("duplicate = %+v", dup)
	}
	if n := do("mongo.collections_detail", `{"db":"`+db+`"}`); n == nil {
		t.Fatal("no listing")
	}
	var ids []string
	for _, d := range do("mongo.documents", `{"db":"`+db+`","collection":"people","filter":"{\"age\":{\"$lt\":30}}","limit":10}`)["documents"].([]any) {
		b, _ := json.Marshal(d.(map[string]any)["_id"])
		ids = append(ids, jsonString(string(b)))
	}
	if del := do("mongo.documents_delete_many", `{"db":"`+db+`","collection":"people","ids":[`+strings.Join(ids, ",")+`]}`); del["deleted"].(float64) != float64(len(ids)) {
		t.Fatalf("delete many = %+v (ids %v)", del, ids)
	}
	if _, err := call("mongo.documents_update_many", `{"db":"`+db+`","collection":"people","filter":"{\"$where\":\"1\"}","ops":[{"op":"set","field":"a","value":"1"}]}`); err == nil {
		t.Fatal("a $where filter was accepted")
	}
	if _, err := call("mongo.documents_update_many", `{"db":"`+db+`","collection":"people","filter":"{}","ops":[{"op":"set","field":"a","value":"1"}]}`); err == nil {
		t.Fatal("an empty filter was accepted without all:true")
	}

	// Indexes: create, usage statistics, hide/unhide.
	do("mongo.index_create", `{"db":"`+db+`","collection":"people","keys":"{\"age\":1}","options":"{\"name\":\"age_1\"}"}`)
	do("mongo.documents", `{"db":"`+db+`","collection":"people","filter":"{\"age\":{\"$gte\":0}}","limit":5}`)
	if st := do("mongo.index_stats", `{"db":"`+db+`","collection":"people"}`)["stats"].([]any); len(st) < 2 {
		t.Fatalf("index stats = %+v", st)
	}
	do("mongo.index_hide", `{"db":"`+db+`","collection":"people","name":"age_1","hidden":true}`)
	do("mongo.index_hide", `{"db":"`+db+`","collection":"people","name":"age_1","hidden":false}`)
	if _, err := call("mongo.index_hide", `{"db":"`+db+`","collection":"people","name":"_id_","hidden":true}`); err == nil {
		t.Fatal("the _id index was hidden")
	}

	// Validation: rules are enforced; clearing them lifts the rule.
	do("mongo.validation_set", `{"db":"`+db+`","collection":"people","validator":"{\"$jsonSchema\":{\"bsonType\":\"object\",\"required\":[\"email\"]}}","level":"strict","action":"error"}`)
	if _, err := call("mongo.document_insert", `{"db":"`+db+`","collection":"people","document":"{\"name\":\"no email\"}"}`); err == nil {
		t.Fatal("a document that breaks the validation rules was inserted")
	}
	do("mongo.validation_set", `{"db":"`+db+`","collection":"people","validator":"","level":"off"}`)
	do("mongo.document_insert", `{"db":"`+db+`","collection":"people","document":"{\"name\":\"fine now\"}"}`)

	// Views, copy, rename-free bulk operations.
	do("mongo.view_create", `{"db":"`+db+`","name":"seniors","view_on":"people","pipeline":"[{\"$match\":{\"senior\":true}},{\"$project\":{\"fullname\":1}}]"}`)
	if v := do("mongo.documents", `{"db":"`+db+`","collection":"seniors","limit":10}`); len(v["documents"].([]any)) != 3 {
		t.Fatalf("view documents = %+v", v)
	}
	if _, err := call("mongo.view_create", `{"db":"`+db+`","name":"bad","view_on":"people","pipeline":"[{\"$out\":\"x\"}]"}`); err == nil {
		t.Fatal("a view with $out was created")
	}
	cp := do("mongo.collection_copy", `{"db":"`+db+`","collection":"people","new_name":"people_copy"}`)
	if cp["copied"].(float64) < 4 || cp["indexes"].(float64) < 1 {
		t.Fatalf("copy = %+v", cp)
	}
	if _, err := call("mongo.collection_copy", `{"db":"`+db+`","collection":"people","new_name":"people_copy"}`); err == nil {
		t.Fatal("copying onto an existing collection succeeded")
	}
	val := do("mongo.collections_bulk", `{"db":"`+db+`","op":"validate","collections":["people","people_copy","seniors","gone"]}`)
	if val["succeeded"].(float64) != 2 || val["failed"].(float64) != 2 {
		t.Fatalf("bulk validate = %+v", val)
	}
	if _, err := call("mongo.collections_bulk", `{"db":"`+db+`","op":"drop","collections":["people"]}`); err == nil {
		t.Fatal("bulk drop without confirm was accepted")
	}
	emp := do("mongo.collections_bulk", `{"db":"`+db+`","op":"empty","collections":["people_copy","seniors"],"confirm":true}`)
	if emp["succeeded"].(float64) != 1 || emp["failed"].(float64) != 1 {
		t.Fatalf("bulk empty (a view cannot be emptied) = %+v", emp)
	}
	drop := do("mongo.collections_bulk", `{"db":"`+db+`","op":"drop","collections":["people_copy","seniors","logs","people"],"confirm":true}`)
	if drop["succeeded"].(float64) != 4 {
		t.Fatalf("bulk drop = %+v", drop)
	}
}
