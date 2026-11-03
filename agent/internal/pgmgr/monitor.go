package pgmgr

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// registerMonitor adds the read-only health view for administrators. It reads statistics only; the statement text of
// running queries is never returned (just the kind of statement), because it can hold customers' data.
func registerMonitor(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "pg.monitor", Description: "Server health: version, uptime, connections, per-database size and activity, cache hit ratio, lock waits and long-running statements.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Target *Target `json:"target"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		s, err := open(ctx, env, in.Target, "postgres")
		if err != nil {
			return nil, err
		}
		defer s.Close(ctx)
		out := map[string]any{}
		var version string
		var started time.Time
		var maxConn int
		if err := s.QueryRow(ctx, `SELECT version(), pg_postmaster_start_time(), current_setting('max_connections')::int`).Scan(&version, &started, &maxConn); err != nil {
			return nil, actions.Failed("The server could not be read.", friendly(err))
		}
		out["version"], out["started_at"], out["uptime_seconds"], out["max_connections"] = version, started.UTC().Format(time.RFC3339), int64(time.Since(started).Seconds()), maxConn

		states := map[string]int{}
		rows, err := s.Query(ctx, `SELECT COALESCE(state, 'unknown'), count(*)::int FROM pg_stat_activity WHERE backend_type = 'client backend' GROUP BY 1`)
		if err != nil {
			return nil, actions.Failed("The connections could not be read.", friendly(err))
		}
		total := 0
		for rows.Next() {
			var st string
			var n int
			if err := rows.Scan(&st, &n); err != nil {
				rows.Close()
				return nil, err
			}
			states[st] = n
			total += n
		}
		rows.Close()
		out["connections"] = map[string]any{"total": total, "by_state": states}

		dbs := []map[string]any{}
		rows, err = s.Query(ctx, `SELECT d.datname, pg_database_size(d.datname)::bigint, st.numbackends, st.xact_commit, st.xact_rollback, st.blks_read, st.blks_hit, st.deadlocks, st.temp_bytes
			FROM pg_database d JOIN pg_stat_database st ON st.datid = d.oid
			WHERE NOT d.datistemplate AND d.datname <> 'postgres' ORDER BY 2 DESC LIMIT 200`)
		if err != nil {
			return nil, actions.Failed("The databases could not be read.", friendly(err))
		}
		for rows.Next() {
			var name string
			var size, commit, rollback, read, hit, deadlocks, temp int64
			var backends int
			if err := rows.Scan(&name, &size, &backends, &commit, &rollback, &read, &hit, &deadlocks, &temp); err != nil {
				rows.Close()
				return nil, err
			}
			ratio := 100.0
			if read+hit > 0 {
				ratio = float64(hit) * 100 / float64(read+hit)
			}
			dbs = append(dbs, map[string]any{"name": name, "size_bytes": size, "connections": backends, "commits": commit, "rollbacks": rollback, "cache_hit_percent": ratio, "deadlocks": deadlocks, "temp_bytes": temp})
		}
		rows.Close()
		out["databases"] = dbs

		var waiting int
		_ = s.QueryRow(ctx, `SELECT count(*)::int FROM pg_stat_activity WHERE wait_event_type = 'Lock'`).Scan(&waiting)
		out["lock_waits"] = waiting

		long := []map[string]any{}
		rows, err = s.Query(ctx, `SELECT pid, COALESCE(datname, ''), COALESCE(usename, ''), COALESCE(state, ''), EXTRACT(EPOCH FROM now() - query_start)::bigint, upper(split_part(trim(query), ' ', 1))
			FROM pg_stat_activity WHERE backend_type = 'client backend' AND pid <> pg_backend_pid() AND state <> 'idle' AND query_start < now() - interval '30 seconds'
			ORDER BY query_start LIMIT 20`)
		if err == nil {
			for rows.Next() {
				var pid int32
				var db, user, state, kind string
				var secs int64
				if err := rows.Scan(&pid, &db, &user, &state, &secs, &kind); err == nil {
					long = append(long, map[string]any{"pid": pid, "database": db, "user": user, "state": state, "seconds": secs, "statement": kind})
				}
			}
			rows.Close()
		}
		out["long_running"] = long
		return out, nil
	}})
}
