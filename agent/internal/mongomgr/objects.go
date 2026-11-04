package mongomgr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// registerObjects adds the database overview and the detailed collection listing of the MongoDB browser. Both are
// read-only. Numbers are coerced with Number() so a mongosh Long decodes as a plain JSON number, and each collection is
// measured inside try/catch: a view has no collStats, and one unreadable collection must not hide the others.
func registerObjects(r *actions.Registry, env *host.Env, c Client) {
	type dbIn struct {
		DB string `json:"db"`
	}
	check := func(p json.RawMessage) (dbIn, error) {
		var in dbIn
		if err := host.Decode(p, &in); err != nil {
			return in, err
		}
		if !nameRe.MatchString(in.DB) {
			return in, actions.InvalidParams("db: invalid database name")
		}
		if !installed(env) {
			return in, actions.Conflict("MongoDB is not installed.")
		}
		return in, nil
	}

	r.Register(actions.Action{Name: "mongo.collections_detail", Description: "A database's collections, views and time-series collections with document count, sizes, index count, capped and validation flags.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		in, err := check(p)
		if err != nil {
			return nil, err
		}
		script := fmt.Sprintf("var d=db.getSiblingDB(%s);"+
			"var infos=d.getCollectionInfos({},{nameOnly:false});"+
			"var out=infos.filter(function(i){return i.name.indexOf('system.')!==0}).map(function(i){"+
			"var o=i.options||{};var s={};"+
			"if(i.type!=='view'){try{s=d.runCommand({collStats:i.name})}catch(e){s={}}}"+
			"return {name:i.name,type:o.timeseries?'timeseries':(i.type||'collection'),capped:!!o.capped,"+
			"viewOn:o.viewOn||null,"+
			"count:Number(s.count||0),size:Number(s.size||0),storageSize:Number(s.storageSize||0),avgObjSize:Number(s.avgObjSize||0),"+
			"nindexes:Number(s.nindexes||0),totalIndexSize:Number(s.totalIndexSize||0),"+
			"validated:!!o.validator&&Object.keys(o.validator).length>0}});"+
			"print(EJSON.stringify(out,null,0,{relaxed:true}))", jsString(in.DB))
		out, err := c.eval(ctx, script)
		if err != nil {
			return nil, actions.Failed("The collections could not be listed.", lastLine(out))
		}
		raw := trimToJSON(out)
		var list []map[string]any
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, actions.Failed("The collections could not be listed.", "unexpected output: "+lastLine(out))
		}
		if list == nil {
			list = []map[string]any{}
		}
		return map[string]any{"collections": list}, nil
	}})

	r.Register(actions.Action{Name: "mongo.db_overview", Description: "One database's overview: server version, size and counts, and the deployment role (standalone, replica set member or sharded router).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		in, err := check(p)
		if err != nil {
			return nil, err
		}
		script := fmt.Sprintf("var d=db.getSiblingDB(%s);var s={};try{s=d.runCommand({dbStats:1})}catch(e){}"+
			"var h={};try{h=db.runCommand({hello:1})}catch(e){try{h=db.runCommand({isMaster:1})}catch(e2){}}"+
			"var v='';try{v=db.version()}catch(e){}"+
			"var views=0;try{views=d.getCollectionInfos({type:'view'}).length}catch(e){}"+
			"var ts=0;try{ts=d.getCollectionInfos({'options.timeseries':{$exists:true}}).length}catch(e){}"+
			"print(EJSON.stringify({version:v,dataSize:Number(s.dataSize||0),storageSize:Number(s.storageSize||0),indexSize:Number(s.indexSize||0),"+
			"objects:Number(s.objects||0),collections:Number(s.collections||0),views:views,timeseries:ts,indexes:Number(s.indexes||0),"+
			"avgObjSize:Number(s.avgObjSize||0),"+
			"replicaSet:h.setName||null,primary:h.isWritablePrimary===true||h.ismaster===true,secondary:h.secondary===true,"+
			"sharded:h.msg==='isdbgrid',hosts:h.hosts||[]},null,0,{relaxed:true}))", jsString(in.DB))
		out, err := c.eval(ctx, script)
		if err != nil {
			return nil, actions.Failed("The database overview could not be read.", lastLine(out))
		}
		var m map[string]any
		if err := json.Unmarshal(trimToJSON(out), &m); err != nil {
			return nil, actions.Failed("The database overview could not be read.", "unexpected output: "+lastLine(out))
		}
		m["database"] = in.DB
		return m, nil
	}})

	// Bulk operations on collections. Names are validated one by one and written into the script as JavaScript string
	// literals; the operation comes from a fixed list. Each collection is handled in its own try/catch.
	r.Register(actions.Action{Name: "mongo.collections_bulk", Mutating: true, Description: "Drop, empty or validate up to 50 collections or views at once; each is checked, run on its own and reported on its own.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			DB          string   `json:"db"`
			Op          string   `json:"op"` // drop | empty | validate
			Collections []string `json:"collections"`
			Confirm     bool     `json:"confirm"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !nameRe.MatchString(in.DB) {
			return nil, actions.InvalidParams("db: invalid database name")
		}
		switch in.Op {
		case "drop", "empty", "validate":
		default:
			return nil, actions.InvalidParams("op: drop, empty or validate.")
		}
		if len(in.Collections) == 0 || len(in.Collections) > 50 {
			return nil, actions.InvalidParams("collections: choose between 1 and 50.")
		}
		names := make([]string, 0, len(in.Collections))
		seen := map[string]bool{}
		for _, n := range in.Collections {
			if !validColl(n) {
				return nil, actions.InvalidParams("collections: " + fmt.Sprintf("%q", n) + " is not a collection name.")
			}
			if !seen[n] {
				seen[n] = true
				names = append(names, jsString(n))
			}
		}
		if (in.Op == "drop" || in.Op == "empty") && !in.Confirm {
			return nil, actions.InvalidParams("confirm: dropping or emptying collections must be confirmed.")
		}
		if !installed(env) {
			return nil, actions.Conflict("MongoDB is not installed.")
		}
		body := map[string]string{
			"drop":     "r.ok=d.getCollection(n).drop()===true;if(!r.ok){r.error='the collection could not be dropped'}",
			"empty":    "if(infos[0].type==='view'){r.error='a view holds no documents to remove'}else{r.deleted=Number(d.getCollection(n).deleteMany({}).deletedCount);r.ok=true}",
			"validate": "if(infos[0].type==='view'){r.error='a view cannot be validated'}else{var v=d.getCollection(n).validate({full:false});r.ok=v.valid===true;r.records=Number(v.nrecords||0);r.errors=(v.errors||[]).slice(0,5).map(String);r.warnings=(v.warnings||[]).slice(0,5).map(String);if(!r.ok){r.error='validation found problems'}}",
		}[in.Op]
		script := fmt.Sprintf("var d=db.getSiblingDB(%s);var out=[%s].map(function(n){var r={name:n,ok:false};"+
			"try{var infos=d.getCollectionInfos({name:n});if(infos.length===0){r.error='there is no such collection';return r}%s}"+
			"catch(e){r.error=String(e.message||e)}return r});print(EJSON.stringify(out,null,0,{relaxed:true}))",
			jsString(in.DB), strings.Join(names, ","), body)
		out, err := c.eval(ctx, script)
		if err != nil {
			return nil, actions.Failed("The operation could not be run.", lastLine(out))
		}
		var results []map[string]any
		if err := json.Unmarshal(trimToJSON(out), &results); err != nil {
			return nil, actions.Failed("The operation could not be run.", "unexpected output: "+lastLine(out))
		}
		ok := 0
		for _, res := range results {
			if res["ok"] == true {
				ok++
			}
		}
		return map[string]any{"op": in.Op, "results": results, "succeeded": ok, "failed": len(results) - ok}, nil
	}})
}
