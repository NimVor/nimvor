package pgmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Bulk maintenance of tables and views: drop, truncate, vacuum, analyze, reindex, refresh a materialized view.
// Every object is looked up in the catalog first (user schemas only) and its name is quoted with pgx.Identifier; the
// operation is chosen from a fixed list and no text from the request is ever placed into a statement. The statements
// run as the database's owner through its query account, so PostgreSQL's own privilege checks apply. Each object is
// processed on its own: a failure is reported for that object and the others still run.
//
// PostgreSQL has no "repair table" statement (REPAIR is MySQL's); the nearest honest tools are VACUUM, ANALYZE and
// REINDEX, and those are what is offered.

const (
	maxBulkObjects  = 100
	maintenanceWait = "10min"
)

// BulkItem names one object.
type BulkItem struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

// BulkIn is the request of pg.objects_bulk.
type BulkIn struct {
	tableIn
	Op              string     `json:"op"` // drop | truncate | vacuum | vacuum_analyze | analyze | reindex | refresh
	Items           []BulkItem `json:"items"`
	Cascade         bool       `json:"cascade"`          // drop, truncate: also objects that depend on these
	RestartIdentity bool       `json:"restart_identity"` // truncate: reset the tables' sequences
	Confirm         bool       `json:"confirm"`          // required for drop and truncate
	DryRun          bool       `json:"dry_run"`          // return the statements without running them
}

// BulkResult is one object's outcome.
type BulkResult struct {
	Schema    string `json:"schema"`
	Name      string `json:"name"`
	Kind      string `json:"kind,omitempty"`
	Statement string `json:"statement,omitempty"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}

// bulkStatement builds the statement for op on an object of relkind kind, or explains why this does not apply.
func bulkStatement(in BulkIn, kind, quoted string) (string, error) {
	word := map[string]string{"r": "TABLE", "p": "TABLE", "v": "VIEW", "m": "MATERIALIZED VIEW", "f": "FOREIGN TABLE"}[kind]
	cascade := ""
	if in.Cascade {
		cascade = " CASCADE"
	}
	switch in.Op {
	case "drop":
		return "DROP " + word + " " + quoted + cascade, nil
	case "truncate":
		if kind != "r" && kind != "p" {
			return "", errors.New("only tables can be emptied")
		}
		id := " CONTINUE IDENTITY"
		if in.RestartIdentity {
			id = " RESTART IDENTITY"
		}
		return "TRUNCATE TABLE " + quoted + id + cascade, nil
	case "vacuum", "vacuum_analyze":
		if kind != "r" && kind != "p" && kind != "m" {
			return "", errors.New("only tables and materialized views can be vacuumed")
		}
		if in.Op == "vacuum_analyze" {
			return "VACUUM (ANALYZE) " + quoted, nil
		}
		return "VACUUM " + quoted, nil
	case "analyze":
		if kind == "v" {
			return "", errors.New("a view holds no data to analyze")
		}
		return "ANALYZE " + quoted, nil
	case "reindex":
		if kind != "r" && kind != "p" && kind != "m" {
			return "", errors.New("only tables and materialized views have indexes to rebuild")
		}
		return "REINDEX TABLE " + quoted, nil
	case "refresh":
		if kind != "m" {
			return "", errors.New("only a materialized view can be refreshed")
		}
		return "REFRESH MATERIALIZED VIEW " + quoted, nil
	}
	return "", fmt.Errorf("unknown operation %q", in.Op)
}

func (in BulkIn) validate() error {
	if err := in.check(); err != nil {
		return err
	}
	switch in.Op {
	case "drop", "truncate", "vacuum", "vacuum_analyze", "analyze", "reindex", "refresh":
	default:
		return actions.InvalidParams("op: drop, truncate, vacuum, vacuum_analyze, analyze, reindex or refresh.")
	}
	if len(in.Items) == 0 {
		return actions.InvalidParams("items: choose at least one object.")
	}
	if len(in.Items) > maxBulkObjects {
		return actions.InvalidParams(fmt.Sprintf("items: at most %d objects at a time.", maxBulkObjects))
	}
	if (in.Op == "drop" || in.Op == "truncate") && !in.Confirm && !in.DryRun {
		return actions.InvalidParams("confirm: dropping or emptying objects must be confirmed.")
	}
	return nil
}

func (s *session) bulk(ctx context.Context, in BulkIn) ([]BulkResult, error) {
	if _, err := s.Exec(ctx, "SET statement_timeout = '"+maintenanceWait+"'"); err != nil {
		return nil, err
	}
	out := make([]BulkResult, 0, len(in.Items))
	seen := map[BulkItem]bool{}
	for _, it := range in.Items {
		res := BulkResult{Schema: it.Schema, Name: it.Name}
		if seen[it] {
			continue // the same object twice in one request is processed once
		}
		seen[it] = true
		if it.Schema == "" || it.Name == "" || len(it.Schema) > 63 || len(it.Name) > 63 || strings.ContainsRune(it.Schema+it.Name, 0) {
			res.Error = "not an object name"
			out = append(out, res)
			continue
		}
		var kind string
		err := s.QueryRow(ctx, `
			SELECT c.relkind::text FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r','p','v','m','f')
			  AND n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'`, it.Schema, it.Name).Scan(&kind)
		if errors.Is(err, pgx.ErrNoRows) {
			res.Error = "there is no such table or view"
			out = append(out, res)
			continue
		}
		if err != nil {
			return nil, err
		}
		res.Kind = relkinds[kind]
		stmt, err := bulkStatement(in, kind, pgx.Identifier{it.Schema, it.Name}.Sanitize())
		if err != nil {
			res.Error = err.Error()
			out = append(out, res)
			continue
		}
		res.Statement = stmt
		if in.DryRun {
			res.OK = true
			out = append(out, res)
			continue
		}
		if _, err := s.Exec(ctx, stmt); err != nil {
			res.Error = friendlyPG(err)
		} else {
			res.OK = true
		}
		out = append(out, res)
	}
	return out, nil
}

// friendlyPG is the server's message without internal detail, for a per-object result.
func friendlyPG(err error) string {
	msg := userError(err).Error()
	const lead = "PostgreSQL refused the request: "
	if strings.HasPrefix(msg, lead) {
		msg = strings.TrimSuffix(strings.TrimPrefix(msg, lead), ".")
	}
	return msg
}

func registerMaintenance(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "pg.objects_bulk", Mutating: true, Description: "Drop, empty, vacuum, analyze, reindex or refresh up to 100 tables, views or materialized views at once; each object is checked in the catalog, run on its own and reported on its own.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in BulkIn
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		var results []BulkResult
		err := withOwner(ctx, env, in.Target, in.Database, func(s *session) error {
			var err error
			results, err = s.bulk(ctx, in)
			return err
		})
		if err != nil {
			return nil, err
		}
		done := 0
		for _, r := range results {
			if r.OK {
				done++
			}
		}
		return map[string]any{"op": in.Op, "dry_run": in.DryRun, "results": results, "succeeded": done, "failed": len(results) - done}, nil
	}})
}
