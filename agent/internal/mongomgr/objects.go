package mongomgr

import (
	"context"
	"encoding/json"
	"fmt"

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
}
