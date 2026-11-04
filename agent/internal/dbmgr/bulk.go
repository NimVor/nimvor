package dbmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/mysqlwire"
)

// Bulk operations on tables and views of one database. Every name is looked up in information_schema first and quoted
// with Ident; the operation comes from a fixed list. Each object runs on its own and gets its own result. REPAIR TABLE is
// offered only for the storage engines that support it (MyISAM, Aria, Archive, CSV): on InnoDB it does nothing, and
// pretending otherwise would be wrong.

const maxBulkTables = 50

var repairable = map[string]bool{"MYISAM": true, "ARIA": true, "ARCHIVE": true, "CSV": true}

type bulkReq struct {
	Database string   `json:"database"`
	Op       string   `json:"op"` // drop | truncate | optimize | analyze | check | repair
	Tables   []string `json:"tables"`
	Confirm  bool     `json:"confirm"`
	DryRun   bool     `json:"dry_run"`
}

type bulkResult struct {
	Table     string `json:"table"`
	Kind      string `json:"kind,omitempty"`
	Engine    string `json:"engine,omitempty"`
	Statement string `json:"statement,omitempty"`
	OK        bool   `json:"ok"`
	Message   string `json:"message,omitempty"` // what the server said (status/note/warning) or why this was not done
}

func bulkStatement(op, table string, view bool, engine string) (string, error) {
	q := Ident(table)
	switch op {
	case "drop":
		if view {
			return "DROP VIEW " + q, nil
		}
		return "DROP TABLE " + q, nil
	case "truncate":
		if view {
			return "", fmt.Errorf("a view holds no rows to remove")
		}
		return "TRUNCATE TABLE " + q, nil
	case "optimize", "analyze", "check":
		if view {
			return "", fmt.Errorf("a view has no stored data")
		}
		return strings.ToUpper(op) + " TABLE " + q, nil
	case "repair":
		if view {
			return "", fmt.Errorf("a view has no stored data")
		}
		if !repairable[strings.ToUpper(engine)] {
			return "", fmt.Errorf("the %s storage engine does not support REPAIR TABLE; use check, optimize or restore a backup", orUnknown(engine))
		}
		return "REPAIR TABLE " + q, nil
	}
	return "", fmt.Errorf("unknown operation %q", op)
}

func orUnknown(s string) string {
	if s == "" {
		return "table's"
	}
	return s
}

func registerBulk(r *actions.Registry, c Client) {
	r.Register(actions.Action{Name: "db.browse_bulk", Mutating: true, Description: "Database browser: drop, empty, optimize, analyze, check or repair up to 50 tables or views at once; each is checked in information_schema, run on its own and reported on its own. dry_run returns the statements only.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in bulkReq
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		switch in.Op {
		case "drop", "truncate", "optimize", "analyze", "check", "repair":
		default:
			return nil, actions.InvalidParams("op: drop, truncate, optimize, analyze, check or repair.")
		}
		if len(in.Tables) == 0 || len(in.Tables) > maxBulkTables {
			return nil, actions.InvalidParams(fmt.Sprintf("tables: choose between 1 and %d.", maxBulkTables))
		}
		for _, t := range in.Tables {
			if err := checkTable(t); err != nil {
				return nil, host.Invalid(err)
			}
		}
		if (in.Op == "drop" || in.Op == "truncate") && !in.Confirm && !in.DryRun {
			return nil, actions.InvalidParams("confirm: dropping or emptying tables must be confirmed.")
		}
		var out map[string]any
		err := c.withBrowser(ctx, in.Database, func(conn *mysqlwire.Conn) error {
			results := []bulkResult{}
			seen := map[string]bool{}
			ok := 0
			for _, name := range in.Tables {
				if seen[name] {
					continue
				}
				seen[name] = true
				res := bulkResult{Table: name}
				if ctx.Err() != nil {
					res.Message = "not run: the time limit was reached; run it again for the rest"
					results = append(results, res)
					continue
				}
				info, err := one(conn, "SELECT TABLE_TYPE, COALESCE(ENGINE,'') FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = "+Quote(name))
				if err != nil {
					return sqlFailed(err)
				}
				if len(info.Rows) == 0 {
					res.Message = "there is no such table or view"
					results = append(results, res)
					continue
				}
				view := str(info.Rows[0][0]) == "VIEW"
				res.Engine = str(info.Rows[0][1])
				res.Kind = "table"
				if view {
					res.Kind = "view"
				}
				stmt, err := bulkStatement(in.Op, name, view, res.Engine)
				if err != nil {
					res.Message = err.Error()
					results = append(results, res)
					continue
				}
				res.Statement = stmt
				if in.DryRun {
					res.OK = true
					results = append(results, res)
					ok++
					continue
				}
				rs, err := conn.Query(stmt, mysqlwire.Limits{MaxRows: 20, MaxBytes: 1 << 16})
				if err != nil {
					res.Message = sqlFailed(err).Error()
					results = append(results, res)
					continue
				}
				res.OK = true
				// OPTIMIZE / ANALYZE / CHECK / REPAIR answer with rows (Table, Op, Msg_type, Msg_text): an "error" row is a failure.
				if len(rs) > 0 && len(rs[0].Columns) >= 4 {
					var msgs []string
					for _, row := range rs[0].Rows {
						kind, text := strings.ToLower(str(row[2])), str(row[3])
						if kind == "error" {
							res.OK = false
						}
						if kind != "status" || text != "OK" {
							msgs = append(msgs, kind+": "+text)
						} else if len(msgs) == 0 {
							msgs = append(msgs, "OK")
						}
					}
					res.Message = strings.Join(msgs, "; ")
				}
				if res.OK {
					ok++
				}
				results = append(results, res)
			}
			out = map[string]any{"op": in.Op, "dry_run": in.DryRun, "results": results, "succeeded": ok, "failed": len(results) - ok}
			return nil
		})
		return out, err
	}})
}
