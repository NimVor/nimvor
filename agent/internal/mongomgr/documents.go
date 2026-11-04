package mongomgr

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// More document, index, validation and view management for the MongoDB browser. Everything user-supplied that is
// not a name travels as EJSON text inside a JSON string literal (json.Marshal output is also a valid JavaScript
// string) handed to EJSON.parse, and the script is fed on stdin, so it never appears in the process list. Field names
// in structured updates are checked against a strict pattern; operators come from a fixed list. No operator expression,
// $where or JavaScript function is ever accepted.

var fieldPathRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z0-9_]+){0,7}$`)

const maxEJSON = 1 << 20

func ejsonLit(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func checkEJSON(field, v string, wantObject bool) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > maxEJSON || !json.Valid([]byte(v)) {
		return "", actions.InvalidParams(field + ": valid JSON up to 1 MB")
	}
	if wantObject && v[0] != '{' {
		return "", actions.InvalidParams(field + ": a JSON object")
	}
	return v, nil
}

func registerDocuments(r *actions.Registry, env *host.Env, c Client) {
	type target struct {
		DB   string `json:"db"`
		Coll string `json:"collection"`
	}
	check := func(t target) error {
		if !nameRe.MatchString(t.DB) || !validColl(t.Coll) {
			return actions.InvalidParams("db/collection: invalid")
		}
		if !installed(env) {
			return actions.Conflict("MongoDB is not installed.")
		}
		return nil
	}
	run := func(ctx context.Context, script, what string) (map[string]any, error) {
		out, err := c.run(ctx, script, true)
		if err != nil {
			return nil, actions.Failed(what, lastLine(out))
		}
		var res map[string]any
		if err := json.Unmarshal(trimToJSON(out), &res); err != nil {
			return nil, actions.Failed(what, "unexpected output: "+lastLine(out))
		}
		if msg, ok := res["error"].(string); ok {
			return nil, actions.Conflict(what + " " + msg)
		}
		return res, nil
	}

	r.Register(actions.Action{Name: "mongo.documents_delete_many", Mutating: true, Description: "Delete up to 500 documents addressed by their _id values (EJSON).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			target
			IDs []string `json:"ids"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := check(in.target); err != nil {
			return nil, err
		}
		if len(in.IDs) == 0 || len(in.IDs) > 500 {
			return nil, actions.InvalidParams("ids: between 1 and 500 document ids")
		}
		parts := make([]string, 0, len(in.IDs))
		for _, id := range in.IDs {
			id = strings.TrimSpace(id)
			if id == "" || len(id) > 4096 || !json.Valid([]byte(id)) {
				return nil, actions.InvalidParams("ids: every id is the document _id as EJSON")
			}
			parts = append(parts, ejsonLit(id))
		}
		script := fmt.Sprintf("var ids=[%s].map(function(x){return EJSON.parse(x)});"+
			"var r=db.getSiblingDB(%s).getCollection(%s).deleteMany({_id:{$in:ids}});print(JSON.stringify({deleted:Number(r.deletedCount)}))",
			strings.Join(parts, ","), jsString(in.DB), jsString(in.Coll))
		return run(ctx, script, "The documents could not be deleted.")
	}})

	r.Register(actions.Action{Name: "mongo.document_duplicate", Mutating: true, Description: "Insert a copy of one document (found by _id) with a new _id.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			target
			ID string `json:"id"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := check(in.target); err != nil {
			return nil, err
		}
		id := strings.TrimSpace(in.ID)
		if id == "" || len(id) > 4096 || !json.Valid([]byte(id)) {
			return nil, actions.InvalidParams("id: the document _id as EJSON")
		}
		script := fmt.Sprintf("var c=db.getSiblingDB(%s).getCollection(%s);var d=c.findOne({_id:EJSON.parse(%s)});"+
			"if(!d){print(JSON.stringify({error:'there is no such document.'}))}else{delete d._id;var r=c.insertOne(d);print(EJSON.stringify({inserted_id:r.insertedId},null,0,{relaxed:true}))}",
			jsString(in.DB), jsString(in.Coll), ejsonLit(id))
		return run(ctx, script, "The document could not be duplicated.")
	}})

	// Structured update of many documents: $set / $unset / $inc / $rename on named fields, for documents that match a
	// filter (EJSON). An empty filter means every document and must be asked for explicitly.
	r.Register(actions.Action{Name: "mongo.documents_update_many", Mutating: true, Description: "Update the documents matching a filter with structured $set, $unset, $inc and $rename operations on named fields.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			target
			Filter string `json:"filter"`
			All    bool   `json:"all"` // the filter is empty on purpose: every document
			Ops    []struct {
				Op    string `json:"op"` // set | unset | inc | rename
				Field string `json:"field"`
				Value string `json:"value"` // EJSON (set, inc); the new field name for rename
			} `json:"ops"`
			DryRun bool `json:"dry_run"` // count the matching documents only
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := check(in.target); err != nil {
			return nil, err
		}
		filter := strings.TrimSpace(in.Filter)
		if filter == "" || filter == "{}" {
			if !in.All {
				return nil, actions.InvalidParams("filter: empty; confirm that every document should be changed")
			}
			filter = "{}"
		} else if f, err := checkEJSON("filter", filter, true); err != nil {
			return nil, err
		} else {
			filter = f
			if strings.Contains(filter, "$where") || strings.Contains(filter, "$function") || strings.Contains(filter, "$accumulator") {
				return nil, actions.InvalidParams("filter: JavaScript operators are not accepted")
			}
		}
		upd := map[string]map[string]json.RawMessage{}
		if !in.DryRun {
			if len(in.Ops) == 0 || len(in.Ops) > 50 {
				return nil, actions.InvalidParams("ops: between 1 and 50 operations")
			}
			seen := map[string]bool{}
			for _, o := range in.Ops {
				if !fieldPathRe.MatchString(o.Field) || o.Field == "_id" || strings.HasPrefix(o.Field, "_id.") {
					return nil, actions.InvalidParams("field " + fmt.Sprintf("%q", o.Field) + ": letters, digits, underscore and dots; _id cannot be changed")
				}
				if seen[o.Field] {
					return nil, actions.InvalidParams("field " + o.Field + " appears twice")
				}
				seen[o.Field] = true
				switch o.Op {
				case "set", "inc":
					v, err := checkEJSON("value of "+o.Field, o.Value, false)
					if err != nil {
						return nil, err
					}
					key := "$" + o.Op
					if upd[key] == nil {
						upd[key] = map[string]json.RawMessage{}
					}
					upd[key][o.Field] = json.RawMessage(v)
				case "unset":
					if upd["$unset"] == nil {
						upd["$unset"] = map[string]json.RawMessage{}
					}
					upd["$unset"][o.Field] = json.RawMessage(`""`)
				case "rename":
					if !fieldPathRe.MatchString(o.Value) || o.Value == "_id" {
						return nil, actions.InvalidParams("new name of " + o.Field + ": letters, digits, underscore and dots")
					}
					if upd["$rename"] == nil {
						upd["$rename"] = map[string]json.RawMessage{}
					}
					nb, _ := json.Marshal(o.Value)
					upd["$rename"][o.Field] = nb
				default:
					return nil, actions.InvalidParams("op: set, unset, inc or rename")
				}
			}
		}
		coll := fmt.Sprintf("db.getSiblingDB(%s).getCollection(%s)", jsString(in.DB), jsString(in.Coll))
		var script string
		if in.DryRun {
			script = fmt.Sprintf("print(JSON.stringify({matched:Number(%s.countDocuments(EJSON.parse(%s)))}))", coll, ejsonLit(filter))
		} else {
			ub, err := json.Marshal(upd)
			if err != nil {
				return nil, err
			}
			script = fmt.Sprintf("var r=%s.updateMany(EJSON.parse(%s),EJSON.parse(%s));print(JSON.stringify({matched:Number(r.matchedCount),modified:Number(r.modifiedCount)}))",
				coll, ejsonLit(filter), ejsonLit(string(ub)))
		}
		return run(ctx, script, "The documents could not be updated.")
	}})

	r.Register(actions.Action{Name: "mongo.index_stats", Description: "How often each index of a collection was used since the server started ($indexStats).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in target
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := check(in); err != nil {
			return nil, err
		}
		script := fmt.Sprintf("var out=[];try{out=db.getSiblingDB(%s).getCollection(%s).aggregate([{$indexStats:{}}]).toArray().map(function(s){return {name:s.name,ops:Number(s.accesses.ops),since:s.accesses.since}})}catch(e){}"+
			"print(EJSON.stringify({stats:out},null,0,{relaxed:true}))", jsString(in.DB), jsString(in.Coll))
		return run(ctx, script, "The index statistics could not be read.")
	}})

	r.Register(actions.Action{Name: "mongo.index_hide", Mutating: true, Description: "Hide or unhide an index from the query planner (MongoDB 4.4 or newer) without dropping it.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			target
			Name   string `json:"name"`
			Hidden bool   `json:"hidden"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := check(in.target); err != nil {
			return nil, err
		}
		if !indexNameRe.MatchString(in.Name) || in.Name == "_id_" {
			return nil, actions.InvalidParams("name: the index name (the _id index cannot be hidden)")
		}
		script := fmt.Sprintf("var r=db.getSiblingDB(%s).runCommand({collMod:%s,index:{name:%s,hidden:%t}});"+
			"print(JSON.stringify(r.ok===1?{hidden:%t}:{error:String(r.errmsg||'the server refused the change.')}))",
			jsString(in.DB), jsString(in.Coll), jsString(in.Name), in.Hidden, in.Hidden)
		return run(ctx, script, "The index could not be changed.")
	}})

	r.Register(actions.Action{Name: "mongo.validation_set", Mutating: true, Description: "Set (or clear) a collection's validation rules: a $jsonSchema validator object, the level and the action.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			target
			Validator string `json:"validator"` // EJSON object; empty clears the rules
			Level     string `json:"level"`     // off | moderate | strict
			Action    string `json:"action"`    // warn | error
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := check(in.target); err != nil {
			return nil, err
		}
		level, act := in.Level, in.Action
		if level == "" {
			level = "strict"
		}
		if act == "" {
			act = "error"
		}
		if (level != "off" && level != "moderate" && level != "strict") || (act != "warn" && act != "error") {
			return nil, actions.InvalidParams("level: off, moderate or strict; action: warn or error")
		}
		validator := "{}"
		if strings.TrimSpace(in.Validator) != "" {
			v, err := checkEJSON("validator", in.Validator, true)
			if err != nil {
				return nil, err
			}
			// Only a $jsonSchema validator is accepted: other validators can carry $where-style expressions.
			var top map[string]json.RawMessage
			if json.Unmarshal([]byte(v), &top) != nil || len(top) != 1 || top["$jsonSchema"] == nil {
				return nil, actions.InvalidParams("validator: an object with a single $jsonSchema key")
			}
			validator = v
		}
		script := fmt.Sprintf("var r=db.getSiblingDB(%s).runCommand({collMod:%s,validator:EJSON.parse(%s),validationLevel:%s,validationAction:%s});"+
			"print(JSON.stringify(r.ok===1?{ok:true}:{error:String(r.errmsg||'the server refused the change.')}))",
			jsString(in.DB), jsString(in.Coll), ejsonLit(validator), jsString(level), jsString(act))
		return run(ctx, script, "The validation rules could not be saved.")
	}})

	r.Register(actions.Action{Name: "mongo.view_create", Mutating: true, Description: "Create a read-only view over a collection from an aggregation pipeline (EJSON array of stages).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			DB       string `json:"db"`
			Name     string `json:"name"`
			ViewOn   string `json:"view_on"`
			Pipeline string `json:"pipeline"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !nameRe.MatchString(in.DB) || !validColl(in.Name) || !validColl(in.ViewOn) {
			return nil, actions.InvalidParams("db/name/view_on: invalid")
		}
		if !installed(env) {
			return nil, actions.Conflict("MongoDB is not installed.")
		}
		pl := strings.TrimSpace(in.Pipeline)
		if pl == "" {
			pl = "[]"
		}
		pl, err := checkEJSON("pipeline", pl, false)
		if err != nil {
			return nil, err
		}
		if pl[0] != '[' {
			return nil, actions.InvalidParams("pipeline: a JSON array of stages")
		}
		// A view cannot write ($out, $merge) and must not run JavaScript.
		for _, bad := range []string{"$out", "$merge", "$where", "$function", "$accumulator"} {
			if strings.Contains(pl, `"`+bad+`"`) {
				return nil, actions.InvalidParams("pipeline: " + bad + " is not allowed in a view")
			}
		}
		script := fmt.Sprintf("var d=db.getSiblingDB(%s);"+
			"if(d.getCollectionInfos({name:%s}).length===0){print(JSON.stringify({error:'the source collection does not exist.'}))}"+
			"else if(d.getCollectionInfos({name:%s}).length>0){print(JSON.stringify({error:'a collection or view with that name already exists.'}))}"+
			"else{d.createView(%s,%s,EJSON.parse(%s));print(JSON.stringify({created:%s}))}",
			jsString(in.DB), jsString(in.ViewOn), jsString(in.Name), jsString(in.Name), jsString(in.ViewOn), ejsonLit(pl), jsString(in.Name))
		return run(ctx, script, "The view could not be created.")
	}})
}
