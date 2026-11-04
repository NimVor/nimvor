package dbmgr

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/mysqlwire"
)

// Stored routines, triggers, events, sequences (MariaDB) and partitions in the MySQL / MariaDB browser. Listing comes
// from information_schema and SHOW CREATE. Changes are limited to what needs no code: drop a routine / trigger / event,
// enable or disable an event, manage sequences and partitions. Writing a routine, trigger or event body is code and
// belongs to the SQL tab (arbitrary SQL is not accepted through a management action). Every name is checked against
// information_schema first and quoted with Ident.

func (c Client) objectsWithConn(ctx context.Context, p json.RawMessage, fn func(conn *mysqlwire.Conn, in objReq) (any, error)) (any, error) {
	var in objReq
	if err := host.Decode(p, &in); err != nil {
		return nil, err
	}
	var out any
	err := c.withBrowser(ctx, in.Database, func(conn *mysqlwire.Conn) error {
		var err error
		out, err = fn(conn, in)
		return err
	})
	return out, err
}

type objReq struct {
	Database  string `json:"database"`
	Kind      string `json:"kind"` // routine | trigger | event
	Type      string `json:"type"` // routine: function | procedure
	Name      string `json:"name"`
	Op        string `json:"op"`
	Confirm   bool   `json:"confirm"`
	DryRun    bool   `json:"dry_run"`
	Value     *int64 `json:"value"`
	Start     *int64 `json:"start"`
	Increment *int64 `json:"increment"`
	Min       *int64 `json:"min"`
	Max       *int64 `json:"max"`
	Cache     *int64 `json:"cache"`
	Cycle     *bool  `json:"cycle"`
	Table     string `json:"table"`
}

func rowsOf(conn *mysqlwire.Conn, sql string) ([][]*[]byte, error) {
	r, err := one(conn, sql)
	if err != nil {
		return nil, err
	}
	return r.Rows, nil
}

func isMariaDB(conn *mysqlwire.Conn) bool {
	r, err := one(conn, "SELECT VERSION()")
	return err == nil && len(r.Rows) == 1 && strings.Contains(strings.ToLower(str(r.Rows[0][0])), "mariadb")
}

func registerObjectsExt(r *actions.Registry, c Client) {
	r.Register(actions.Action{Name: "db.browse_routines", Mutating: true, Description: "Database browser: stored functions and procedures, triggers and events of the database.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		return c.objectsWithConn(ctx, p, func(conn *mysqlwire.Conn, in objReq) (any, error) {
			routines := []map[string]any{}
			rr, err := rowsOf(conn, "SELECT ROUTINE_NAME, ROUTINE_TYPE, COALESCE(DTD_IDENTIFIER,''), SECURITY_TYPE, IS_DETERMINISTIC, DEFINER, CREATED, LAST_ALTERED, ROUTINE_COMMENT FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() ORDER BY ROUTINE_TYPE, ROUTINE_NAME")
			if err != nil {
				return nil, sqlFailed(err)
			}
			for _, row := range rr {
				routines = append(routines, map[string]any{"name": str(row[0]), "type": strings.ToLower(str(row[1])), "returns": str(row[2]), "security": str(row[3]), "deterministic": str(row[4]) == "YES", "definer": str(row[5]), "created": str(row[6]), "modified": str(row[7]), "comment": str(row[8])})
			}
			triggers := []map[string]any{}
			tr, err := rowsOf(conn, "SELECT TRIGGER_NAME, EVENT_MANIPULATION, EVENT_OBJECT_TABLE, ACTION_TIMING, DEFINER, CREATED FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE() ORDER BY EVENT_OBJECT_TABLE, ACTION_TIMING, EVENT_MANIPULATION, TRIGGER_NAME")
			if err != nil {
				return nil, sqlFailed(err)
			}
			for _, row := range tr {
				triggers = append(triggers, map[string]any{"name": str(row[0]), "event": str(row[1]), "table": str(row[2]), "timing": str(row[3]), "definer": str(row[4]), "created": str(row[5])})
			}
			events := []map[string]any{}
			if er, err := rowsOf(conn, "SELECT EVENT_NAME, STATUS, EVENT_TYPE, COALESCE(EXECUTE_AT,''), COALESCE(INTERVAL_VALUE,''), COALESCE(INTERVAL_FIELD,''), COALESCE(LAST_EXECUTED,''), DEFINER, EVENT_COMMENT FROM information_schema.EVENTS WHERE EVENT_SCHEMA = DATABASE() ORDER BY EVENT_NAME"); err == nil {
				for _, row := range er {
					events = append(events, map[string]any{"name": str(row[0]), "status": str(row[1]), "type": str(row[2]), "execute_at": str(row[3]), "every": strings.TrimSpace(str(row[4]) + " " + strings.ToLower(str(row[5]))), "last_executed": str(row[6]), "definer": str(row[7]), "comment": str(row[8])})
				}
			}
			sched := ""
			if sr, err := rowsOf(conn, "SELECT @@event_scheduler"); err == nil && len(sr) == 1 {
				sched = str(sr[0][0])
			}
			return map[string]any{"routines": routines, "triggers": triggers, "events": events, "event_scheduler": sched}, nil
		})
	}})

	// definition of one object, read with SHOW CREATE after the name is confirmed to exist in this database
	r.Register(actions.Action{Name: "db.browse_object_definition", Mutating: true, Description: "Database browser: the CREATE statement of a routine, trigger, event or view.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		return c.objectsWithConn(ctx, p, func(conn *mysqlwire.Conn, in objReq) (any, error) {
			if err := checkIdent("name", in.Name); err != nil {
				return nil, err
			}
			var exists, show string
			col := 1
			switch in.Kind {
			case "routine":
				if in.Type != "function" && in.Type != "procedure" {
					return nil, actions.InvalidParams("type: function or procedure")
				}
				exists = "SELECT 1 FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() AND ROUTINE_TYPE = " + Quote(strings.ToUpper(in.Type)) + " AND ROUTINE_NAME = " + Quote(in.Name)
				show = "SHOW CREATE " + strings.ToUpper(in.Type) + " " + Ident(in.Name)
				col = 2
			case "trigger":
				exists = "SELECT 1 FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE() AND TRIGGER_NAME = " + Quote(in.Name)
				show = "SHOW CREATE TRIGGER " + Ident(in.Name)
				col = 2
			case "event":
				exists = "SELECT 1 FROM information_schema.EVENTS WHERE EVENT_SCHEMA = DATABASE() AND EVENT_NAME = " + Quote(in.Name)
				show = "SHOW CREATE EVENT " + Ident(in.Name)
				col = 3
			case "view":
				exists = "SELECT 1 FROM information_schema.VIEWS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = " + Quote(in.Name)
				show = "SHOW CREATE VIEW " + Ident(in.Name)
			default:
				return nil, actions.InvalidParams("kind: routine, trigger, event or view")
			}
			ex, err := rowsOf(conn, exists)
			if err != nil {
				return nil, sqlFailed(err)
			}
			if len(ex) == 0 {
				return nil, actions.NotFound(in.Kind + " " + in.Name + " does not exist")
			}
			d, err := rowsOf(conn, show)
			if err != nil || len(d) == 0 || len(d[0]) <= col {
				// SHOW CREATE needs privileges the browser account might lack for objects of another definer.
				return map[string]any{"definition": "", "note": "The definition cannot be shown to this account."}, nil
			}
			return map[string]any{"definition": str(d[0][col])}, nil
		})
	}})

	r.Register(actions.Action{Name: "db.browse_object_op", Mutating: true, Description: "Database browser: drop a routine, trigger or event, or enable / disable an event.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		return c.objectsWithConn(ctx, p, func(conn *mysqlwire.Conn, in objReq) (any, error) {
			if err := checkIdent("name", in.Name); err != nil {
				return nil, err
			}
			var exists, stmt string
			switch {
			case in.Kind == "routine" && in.Op == "drop" && (in.Type == "function" || in.Type == "procedure"):
				exists = "SELECT 1 FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() AND ROUTINE_TYPE = " + Quote(strings.ToUpper(in.Type)) + " AND ROUTINE_NAME = " + Quote(in.Name)
				stmt = "DROP " + strings.ToUpper(in.Type) + " " + Ident(in.Name)
			case in.Kind == "trigger" && in.Op == "drop":
				exists = "SELECT 1 FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE() AND TRIGGER_NAME = " + Quote(in.Name)
				stmt = "DROP TRIGGER " + Ident(in.Name)
			case in.Kind == "event" && (in.Op == "drop" || in.Op == "enable" || in.Op == "disable"):
				exists = "SELECT 1 FROM information_schema.EVENTS WHERE EVENT_SCHEMA = DATABASE() AND EVENT_NAME = " + Quote(in.Name)
				switch in.Op {
				case "drop":
					stmt = "DROP EVENT " + Ident(in.Name)
				case "enable":
					stmt = "ALTER EVENT " + Ident(in.Name) + " ENABLE"
				default:
					stmt = "ALTER EVENT " + Ident(in.Name) + " DISABLE"
				}
			default:
				return nil, actions.InvalidParams("kind/op: drop a routine or trigger; drop, enable or disable an event")
			}
			if in.Op == "drop" && !in.Confirm && !in.DryRun {
				return nil, actions.InvalidParams("confirm: dropping must be confirmed")
			}
			ex, err := rowsOf(conn, exists)
			if err != nil {
				return nil, sqlFailed(err)
			}
			if len(ex) == 0 {
				return nil, actions.NotFound(in.Kind + " " + in.Name + " does not exist")
			}
			if in.DryRun {
				return map[string]any{"sql": stmt}, nil
			}
			if _, err := conn.Query(stmt, mysqlwire.Limits{MaxRows: 1}); err != nil {
				return nil, sqlFailed(err)
			}
			return map[string]any{"sql": stmt}, nil
		})
	}})

	// ---- sequences (MariaDB 10.3+) -----------------------------------------------------------------------
	r.Register(actions.Action{Name: "db.browse_sequences", Mutating: true, Description: "Database browser (MariaDB): the database's sequences with their settings.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		return c.objectsWithConn(ctx, p, func(conn *mysqlwire.Conn, in objReq) (any, error) {
			if !isMariaDB(conn) {
				return map[string]any{"supported": false, "sequences": []any{}, "note": "Sequences are a MariaDB feature (10.3 or newer). MySQL uses AUTO_INCREMENT columns instead."}, nil
			}
			names, err := rowsOf(conn, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'SEQUENCE' ORDER BY TABLE_NAME")
			if err != nil {
				return nil, sqlFailed(err)
			}
			out := []map[string]any{}
			for _, n := range names {
				name := str(n[0])
				sr, err := rowsOf(conn, "SELECT next_not_cached_value, minimum_value, maximum_value, start_value, increment, cache_size, cycle_option, cycle_count FROM "+Ident(name))
				if err != nil || len(sr) != 1 {
					continue
				}
				i := func(k int) int64 { v, _ := strconv.ParseInt(str(sr[0][k]), 10, 64); return v }
				out = append(out, map[string]any{"name": name, "next": i(0), "min": i(1), "max": i(2), "start": i(3), "increment": i(4), "cache": i(5), "cycle": i(6) == 1, "cycles": i(7)})
			}
			return map[string]any{"supported": true, "sequences": out}, nil
		})
	}})
	r.Register(actions.Action{Name: "db.browse_sequence_op", Mutating: true, Description: "Database browser (MariaDB): create, alter, set the value of, or drop a sequence. dry_run returns the SQL only.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		return c.objectsWithConn(ctx, p, func(conn *mysqlwire.Conn, in objReq) (any, error) {
			if !isMariaDB(conn) {
				return nil, actions.Conflict("Sequences are a MariaDB feature; this server is MySQL.")
			}
			if err := checkIdent("sequence", in.Name); err != nil {
				return nil, err
			}
			opts := func(create bool) (string, error) {
				var o []string
				add := func(w string, v *int64) {
					if v != nil {
						o = append(o, w+" "+strconv.FormatInt(*v, 10))
					}
				}
				if in.Increment != nil && *in.Increment == 0 {
					return "", actions.InvalidParams("increment: cannot be 0")
				}
				if in.Cache != nil && *in.Cache < 1 {
					return "", actions.InvalidParams("cache: at least 1")
				}
				if create {
					add("START WITH", in.Start)
				}
				add("INCREMENT BY", in.Increment)
				add("MINVALUE", in.Min)
				add("MAXVALUE", in.Max)
				add("CACHE", in.Cache)
				if in.Cycle != nil {
					if *in.Cycle {
						o = append(o, "CYCLE")
					} else {
						o = append(o, "NOCYCLE")
					}
				}
				return strings.Join(o, " "), nil
			}
			var stmt string
			if in.Op == "create" {
				o, err := opts(true)
				if err != nil {
					return nil, err
				}
				stmt = strings.TrimSpace("CREATE SEQUENCE " + Ident(in.Name) + " " + o)
			} else {
				ex, err := rowsOf(conn, "SELECT 1 FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'SEQUENCE' AND TABLE_NAME = "+Quote(in.Name))
				if err != nil {
					return nil, sqlFailed(err)
				}
				if len(ex) == 0 {
					return nil, actions.NotFound("sequence " + in.Name + " does not exist")
				}
				switch in.Op {
				case "set_value":
					if in.Value == nil {
						return nil, actions.InvalidParams("value: the number the next call should follow")
					}
					stmt = "SELECT SETVAL(" + Ident(in.Name) + ", " + strconv.FormatInt(*in.Value, 10) + ")"
				case "restart":
					if in.Value == nil {
						return nil, actions.InvalidParams("value: the number the next call should return")
					}
					stmt = "ALTER SEQUENCE " + Ident(in.Name) + " RESTART WITH " + strconv.FormatInt(*in.Value, 10)
				case "alter":
					o, err := opts(false)
					if err != nil {
						return nil, err
					}
					if o == "" {
						return nil, actions.InvalidParams("Nothing to change")
					}
					stmt = "ALTER SEQUENCE " + Ident(in.Name) + " " + o
				case "drop":
					if !in.Confirm && !in.DryRun {
						return nil, actions.InvalidParams("confirm: dropping must be confirmed")
					}
					stmt = "DROP SEQUENCE " + Ident(in.Name)
				default:
					return nil, actions.InvalidParams("op: create, set_value, restart, alter or drop")
				}
			}
			if in.DryRun {
				return map[string]any{"sql": stmt}, nil
			}
			if _, err := conn.Query(stmt, mysqlwire.Limits{MaxRows: 1}); err != nil {
				return nil, sqlFailed(err)
			}
			return map[string]any{"sql": stmt}, nil
		})
	}})

	// ---- partitions --------------------------------------------------------------------------------------
	r.Register(actions.Action{Name: "db.browse_partitions", Mutating: true, Description: "Database browser: a table's partitioning method, expression and partitions.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		return c.objectsWithConn(ctx, p, func(conn *mysqlwire.Conn, in objReq) (any, error) {
			if err := checkTable(in.Table); err != nil {
				return nil, host.Invalid(err)
			}
			rr, err := rowsOf(conn, "SELECT PARTITION_NAME, COALESCE(PARTITION_METHOD,''), COALESCE(PARTITION_EXPRESSION,''), COALESCE(PARTITION_DESCRIPTION,''), COALESCE(TABLE_ROWS,0), COALESCE(DATA_LENGTH,0), COALESCE(INDEX_LENGTH,0), COALESCE(SUBPARTITION_NAME,'') FROM information_schema.PARTITIONS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = "+Quote(in.Table)+" ORDER BY PARTITION_ORDINAL_POSITION, SUBPARTITION_ORDINAL_POSITION")
			if err != nil {
				return nil, sqlFailed(err)
			}
			parts := []map[string]any{}
			method, expr := "", ""
			for _, row := range rr {
				if str(row[0]) == "" {
					continue // an unpartitioned table has one row with no partition name
				}
				method, expr = str(row[1]), str(row[2])
				i := func(k int) int64 { v, _ := strconv.ParseInt(str(row[k]), 10, 64); return v }
				parts = append(parts, map[string]any{"name": str(row[0]), "description": str(row[3]), "rows": i(4), "data_bytes": i(5), "index_bytes": i(6), "subpartition": str(row[7])})
			}
			return map[string]any{"partitioned": len(parts) > 0, "method": method, "expression": expr, "partitions": parts}, nil
		})
	}})
}
