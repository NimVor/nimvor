package pgmgr

import (
	"context"
	"encoding/json"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

type tableIn struct {
	targetIn
	Database string `json:"database"`
	Schema   string `json:"schema"`
	Table    string `json:"table"`
}

func (in tableIn) check() error {
	return ValidName("database", in.Database)
}

func withOwner(ctx context.Context, env *host.Env, t *Target, db string, fn func(*session) error) error {
	s, err := openOwner(ctx, env, t, db)
	if err != nil {
		return err
	}
	defer s.Close(ctx)
	return fn(s)
}

func registerBrowser(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "pg.tables", Description: "The tables, views and materialized views of a database (system schemas excluded) with estimated rows, size and whether their rows can be edited.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in tableIn
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.check(); err != nil {
			return nil, err
		}
		var out map[string]any
		err := withOwner(ctx, env, in.Target, in.Database, func(s *session) error {
			schemas, err := s.schemas(ctx)
			if err != nil {
				return err
			}
			tables, err := s.tables(ctx)
			if err != nil {
				return err
			}
			out = map[string]any{"schemas": schemas, "tables": tables}
			return nil
		})
		return out, err
	}})

	r.Register(actions.Action{Name: "pg.table_structure", Description: "A table's columns (type, null, default, key), its row identity and its indexes.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in tableIn
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.check(); err != nil {
			return nil, err
		}
		var out map[string]any
		err := withOwner(ctx, env, in.Target, in.Database, func(s *session) error {
			t, err := s.resolve(ctx, in.Schema, in.Table)
			if err != nil {
				return err
			}
			rows, err := s.Query(ctx, `SELECT c.relname, pg_get_indexdef(i.indexrelid), i.indisunique, i.indisprimary FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid WHERE i.indrelid = $1 ORDER BY i.indisprimary DESC, c.relname`, t.oid)
			if err != nil {
				return err
			}
			defer rows.Close()
			type index struct {
				Name    string `json:"name"`
				Def     string `json:"definition"`
				Unique  bool   `json:"unique"`
				Primary bool   `json:"primary"`
			}
			idx := []index{}
			for rows.Next() {
				var i index
				if err := rows.Scan(&i.Name, &i.Def, &i.Unique, &i.Primary); err != nil {
					return err
				}
				idx = append(idx, i)
			}
			ed, why := t.editable()
			out = map[string]any{"columns": t.columns, "key": t.key, "indexes": idx, "editable": ed, "reason": why, "kind": t.kind}
			return rows.Err()
		})
		return out, err
	}})

	r.Register(actions.Action{Name: "pg.browse", Description: "One page of a table's rows (read-only transaction, as the database owner): sorting, text search, column filters; cells over 2048 characters are cut and marked.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			tableIn
			BrowseRequest
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.check(); err != nil {
			return nil, err
		}
		var out *Page
		err := withOwner(ctx, env, in.Target, in.Database, func(s *session) error {
			t, err := s.resolve(ctx, in.Schema, in.Table)
			if err != nil {
				return err
			}
			out, err = s.browse(ctx, t, in.BrowseRequest)
			return err
		})
		return out, err
	}})

	change := func(name, desc string, fn func(context.Context, *session, *tableInfo, RowChange) error) {
		r.Register(actions.Action{Name: name, Mutating: true, Description: desc, Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
			var in struct {
				tableIn
				RowChange
			}
			if err := host.Decode(p, &in); err != nil {
				return nil, err
			}
			if err := in.check(); err != nil {
				return nil, err
			}
			err := withOwner(ctx, env, in.Target, in.Database, func(s *session) error {
				t, err := s.resolve(ctx, in.Schema, in.Table)
				if err != nil {
					return err
				}
				return fn(ctx, s, t, in.RowChange)
			})
			return map[string]any{"ok": err == nil}, err
		}})
	}
	change("pg.row_insert", "Add a row to a table (values are text, cast by the server; omitted columns use their defaults).", func(ctx context.Context, s *session, t *tableInfo, c RowChange) error { return s.insertRow(ctx, t, c) })
	change("pg.row_update", "Change one row, addressed by every key column; refuses tables without a primary key and any change that does not touch exactly one row.", func(ctx context.Context, s *session, t *tableInfo, c RowChange) error {
		return s.changeRow(ctx, t, c, false)
	})
	change("pg.row_delete", "Delete one row, addressed by every key column; refuses tables without a primary key and any change that does not touch exactly one row.", func(ctx context.Context, s *session, t *tableInfo, c RowChange) error {
		return s.changeRow(ctx, t, c, true)
	})
}

func registerQuery(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "pg.schema_map", Description: "Table and column names of a database (user schemas, up to 3,000 columns), for the SQL editor's completion.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in tableIn
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.check(); err != nil {
			return nil, err
		}
		out := map[string][]string{}
		s, err := openQuery(ctx, env, in.Target, in.Database, true)
		if err != nil {
			return nil, err
		}
		defer s.Close(ctx)
		rows, err := s.Query(ctx, `
			SELECT n.nspname, c.relname, a.attname
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace JOIN pg_attribute a ON a.attrelid = c.oid
			WHERE c.relkind IN ('r','p','v','m','f') AND a.attnum > 0 AND NOT a.attisdropped
			  AND n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'
			ORDER BY n.nspname, c.relname, a.attnum LIMIT 3000`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var sch, tbl, col string
			if err := rows.Scan(&sch, &tbl, &col); err != nil {
				return nil, err
			}
			key := tbl
			if sch != "public" {
				key = sch + "." + tbl
			}
			out[key] = append(out[key], col)
		}
		return map[string]any{"schema": out}, rows.Err()
	}})
	r.Register(actions.Action{Name: "pg.query", Mutating: true, Description: "Run SQL in a database as its dedicated query account: read mode (SELECT only, enforced by PostgreSQL privileges) or write mode (as the database owner). One statement unless script is set; rows and cell sizes are capped, statements time out, explain shows a plan (analyze runs the statement).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			targetIn
			QueryRequest
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return runQuery(ctx, env, in.Target, in.QueryRequest)
	}})
	r.Register(actions.Action{Name: "pg.query_cancel", Mutating: true, Description: "Cancel a running statement by the id it was started with.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Database string `json:"database"`
			QueryID  string `json:"query_id"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := ValidName("database", in.Database); err != nil {
			return nil, err
		}
		if !queryIDRe.MatchString(in.QueryID) {
			return nil, actions.InvalidParams("query_id: 16 hexadecimal characters.")
		}
		return map[string]any{"cancelled": cancelQuery(in.QueryID, in.Database)}, nil
	}})
}
