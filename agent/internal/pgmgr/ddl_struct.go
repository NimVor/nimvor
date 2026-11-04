package pgmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Indexes, constraints and triggers (pg.table_alter, continued). The same rules as ddl.go: names are looked up in
// the catalog or checked against the table's own columns, then quoted; options come from fixed lists; a condition is a
// column, an operator and a quoted literal, never text from the request.

// IndexCol is one column of an index.
type IndexCol struct {
	Name  string `json:"name"`
	Order string `json:"order"` // asc | desc | ""
	Nulls string `json:"nulls"` // first | last | ""
}

// Predicate is a simple condition on one column: for a partial index or a CHECK constraint.
type Predicate struct {
	Column string `json:"column"`
	Op     string `json:"op"` // eq ne lt le gt ge null not_null in like between
	Value  string `json:"value"`
	Value2 string `json:"value2"`
}

var predicateOps = map[string]string{"eq": "=", "ne": "<>", "lt": "<", "le": "<=", "gt": ">", "ge": ">="}

var indexMethods = map[string]bool{"btree": true, "hash": true, "gist": true, "gin": true, "brin": true, "spgist": true}

var fkActionsPG = map[string]string{"": "NO ACTION", "no action": "NO ACTION", "restrict": "RESTRICT", "cascade": "CASCADE", "set null": "SET NULL", "set default": "SET DEFAULT"}

func (p Predicate) sql(cols map[string]bool) (string, error) {
	if !cols[p.Column] {
		return "", actions.InvalidParams("condition: " + p.Column + " is not a column of this table.")
	}
	col := qident(p.Column)
	if op, ok := predicateOps[p.Op]; ok {
		v, err := quoteLiteral(p.Value)
		if err != nil {
			return "", err
		}
		return col + " " + op + " " + v, nil
	}
	switch p.Op {
	case "null":
		return col + " IS NULL", nil
	case "not_null":
		return col + " IS NOT NULL", nil
	case "like":
		v, err := quoteLiteral(p.Value)
		if err != nil {
			return "", err
		}
		return col + " LIKE " + v, nil
	case "between":
		a, err := quoteLiteral(p.Value)
		if err != nil {
			return "", err
		}
		b, err := quoteLiteral(p.Value2)
		if err != nil {
			return "", err
		}
		return col + " BETWEEN " + a + " AND " + b, nil
	case "in":
		items := strings.Split(p.Value, ",")
		if len(items) > 50 {
			return "", actions.InvalidParams("condition: at most 50 values")
		}
		out := make([]string, 0, len(items))
		for _, it := range items {
			v, err := quoteLiteral(strings.TrimSpace(it))
			if err != nil {
				return "", err
			}
			out = append(out, v)
		}
		return col + " IN (" + strings.Join(out, ", ") + ")", nil
	}
	return "", actions.InvalidParams("condition: eq, ne, lt, le, gt, ge, null, not_null, like, between or in.")
}

func (s *session) tableColumns(t *tableInfo) map[string]bool {
	m := map[string]bool{}
	for _, c := range t.columns {
		m[c.Name] = true
	}
	return m
}

// namedOnTable checks that an index / constraint / trigger exists on this very table, using kind-specific catalog SQL.
func (s *session) namedOnTable(ctx context.Context, t *tableInfo, kind, name string) error {
	var q string
	switch kind {
	case "index":
		q = `SELECT EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid WHERE i.indrelid = $1 AND c.relname = $2)`
	case "constraint":
		q = `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = $1 AND conname = $2)`
	case "trigger":
		q = `SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = $1 AND tgname = $2 AND NOT tgisinternal)`
	default:
		return fmt.Errorf("unknown kind %s", kind)
	}
	var ok bool
	if err := s.QueryRow(ctx, q, t.oid, name).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return actions.NotFound("There is no " + kind + " named " + name + " on this table.")
	}
	return nil
}

func (s *session) structureSQL(ctx context.Context, in AlterIn, t *tableInfo, tbl, word string, isTable bool) (string, error) {
	cols := s.tableColumns(t)
	x := in.Extra
	switch in.Op {
	case "create_index":
		if t.kind == "v" || t.kind == "f" {
			return "", actions.InvalidParams("Indexes can be created on tables and materialized views.")
		}
		if len(x.IndexCols) == 0 || len(x.IndexCols) > 32 {
			return "", actions.InvalidParams("index_columns: 1 to 32 columns.")
		}
		method := strings.ToLower(x.Method)
		if method == "" {
			method = "btree"
		}
		if !indexMethods[method] {
			return "", actions.InvalidParams("method: btree, hash, gist, gin, brin or spgist.")
		}
		if x.Unique && method != "btree" {
			return "", actions.InvalidParams("Only btree indexes can be unique.")
		}
		if x.Concurrent && t.kind == "p" {
			return "", actions.InvalidParams("An index on a partitioned table cannot be built concurrently.")
		}
		parts := make([]string, 0, len(x.IndexCols))
		for _, c := range x.IndexCols {
			if !cols[c.Name] {
				return "", actions.InvalidParams("index column " + c.Name + " is not a column of this table.")
			}
			p := qident(c.Name)
			switch strings.ToLower(c.Order) {
			case "", "asc":
			case "desc":
				p += " DESC"
			default:
				return "", actions.InvalidParams("order: asc or desc.")
			}
			switch strings.ToLower(c.Nulls) {
			case "":
			case "first":
				p += " NULLS FIRST"
			case "last":
				p += " NULLS LAST"
			default:
				return "", actions.InvalidParams("nulls: first or last.")
			}
			parts = append(parts, p)
		}
		sql := "CREATE "
		if x.Unique {
			sql += "UNIQUE "
		}
		sql += "INDEX "
		if x.Concurrent {
			sql += "CONCURRENTLY "
		}
		if in.IfNotExist {
			sql += "IF NOT EXISTS "
		}
		if in.Name != "" {
			if err := checkName("index name", in.Name); err != nil {
				return "", host.Invalid(err)
			}
			sql += qident(in.Name) + " "
		}
		sql += "ON " + tbl + " USING " + method + " (" + strings.Join(parts, ", ") + ")"
		if len(x.Include) > 0 {
			if method != "btree" && method != "gist" {
				return "", actions.InvalidParams("INCLUDE columns are only available for btree and gist indexes.")
			}
			inc := make([]string, 0, len(x.Include))
			for _, c := range x.Include {
				if !cols[c] {
					return "", actions.InvalidParams("included column " + c + " is not a column of this table.")
				}
				inc = append(inc, qident(c))
			}
			sql += " INCLUDE (" + strings.Join(inc, ", ") + ")"
		}
		if x.Predicate != nil {
			cond, err := x.Predicate.sql(cols)
			if err != nil {
				return "", err
			}
			sql += " WHERE " + cond
		}
		return sql, nil
	case "drop_index", "rename_index", "reindex_index":
		if err := s.namedOnTable(ctx, t, "index", in.Name); err != nil {
			return "", err
		}
		idx := qident(t.schema(), in.Name)
		switch in.Op {
		case "drop_index":
			var owned bool
			if err := s.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conindid = (SELECT i.indexrelid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid WHERE i.indrelid = $1 AND c.relname = $2) AND contype IN ('p','u','x'))`, t.oid, in.Name).Scan(&owned); err != nil {
				return "", err
			}
			if owned {
				return "", actions.Conflict("This index backs a primary key or unique constraint; drop the constraint instead.")
			}
			c := ""
			if x.Concurrent {
				c = "CONCURRENTLY "
			}
			return "DROP INDEX " + c + idx, nil
		case "rename_index":
			if err := checkName("new name", in.NewName); err != nil {
				return "", host.Invalid(err)
			}
			return "ALTER INDEX " + idx + " RENAME TO " + qident(in.NewName), nil
		default:
			if x.Concurrent {
				if s.version < 120000 {
					return "", actions.InvalidParams("REINDEX CONCURRENTLY needs PostgreSQL 12 or newer.")
				}
				return "REINDEX INDEX CONCURRENTLY " + idx, nil
			}
			return "REINDEX INDEX " + idx, nil
		}
	case "add_constraint":
		if !isTable {
			return "", actions.InvalidParams("Constraints can only be added to tables.")
		}
		name := ""
		if in.Name != "" {
			if err := checkName("constraint name", in.Name); err != nil {
				return "", host.Invalid(err)
			}
			name = "CONSTRAINT " + qident(in.Name) + " "
		}
		colList := func(names []string) (string, error) {
			if len(names) == 0 || len(names) > 32 {
				return "", actions.InvalidParams("columns: 1 to 32 columns.")
			}
			q := make([]string, len(names))
			for i, n := range names {
				if !cols[n] {
					return "", actions.InvalidParams("column " + n + " is not a column of this table.")
				}
				q[i] = qident(n)
			}
			return strings.Join(q, ", "), nil
		}
		switch x.Kind {
		case "primary", "unique":
			cl, err := colList(x.Columns)
			if err != nil {
				return "", err
			}
			kw := "UNIQUE"
			if x.Kind == "primary" {
				kw = "PRIMARY KEY"
			}
			return "ALTER TABLE " + tbl + " ADD " + name + kw + " (" + cl + ")", nil
		case "check":
			if x.Predicate == nil {
				return "", actions.InvalidParams("predicate: describe the condition.")
			}
			cond, err := x.Predicate.sql(cols)
			if err != nil {
				return "", err
			}
			nv := ""
			if x.NotValid {
				nv = " NOT VALID"
			}
			return "ALTER TABLE " + tbl + " ADD " + name + "CHECK (" + cond + ")" + nv, nil
		case "foreign":
			cl, err := colList(x.Columns)
			if err != nil {
				return "", err
			}
			ref, err := s.resolve(ctx, x.RefSchema, x.RefTable)
			if err != nil {
				return "", err
			}
			if len(x.RefColumns) != len(x.Columns) {
				return "", actions.InvalidParams("ref_columns: as many referenced columns as columns.")
			}
			rc := make([]string, len(x.RefColumns))
			refCols := s.tableColumns(ref)
			for i, n := range x.RefColumns {
				if !refCols[n] {
					return "", actions.InvalidParams("referenced column " + n + " is not a column of " + x.RefTable + ".")
				}
				rc[i] = qident(n)
			}
			del, ok1 := fkActionsPG[strings.ToLower(x.OnDelete)]
			upd, ok2 := fkActionsPG[strings.ToLower(x.OnUpdate)]
			if !ok1 || !ok2 {
				return "", actions.InvalidParams("on delete / on update: no action, restrict, cascade, set null or set default.")
			}
			sql := "ALTER TABLE " + tbl + " ADD " + name + "FOREIGN KEY (" + cl + ") REFERENCES " + ref.quoted + " (" + strings.Join(rc, ", ") + ") ON DELETE " + del + " ON UPDATE " + upd
			if x.Deferrable {
				sql += " DEFERRABLE INITIALLY IMMEDIATE"
			}
			if x.NotValid {
				sql += " NOT VALID"
			}
			return sql, nil
		}
		return "", actions.InvalidParams("kind: primary, unique, check or foreign.")
	case "drop_constraint", "validate_constraint", "rename_constraint":
		if !isTable {
			return "", actions.InvalidParams("Constraints belong to tables.")
		}
		if err := s.namedOnTable(ctx, t, "constraint", in.Name); err != nil {
			return "", err
		}
		con := qident(in.Name)
		switch in.Op {
		case "drop_constraint":
			c := ""
			if in.Cascade {
				c = " CASCADE"
			}
			return "ALTER TABLE " + tbl + " DROP CONSTRAINT " + con + c, nil
		case "validate_constraint":
			return "ALTER TABLE " + tbl + " VALIDATE CONSTRAINT " + con, nil
		default:
			if err := checkName("new name", in.NewName); err != nil {
				return "", host.Invalid(err)
			}
			return "ALTER TABLE " + tbl + " RENAME CONSTRAINT " + con + " TO " + qident(in.NewName), nil
		}
	case "create_partition", "attach_partition", "detach_partition":
		if t.kind != "p" {
			return "", actions.InvalidParams("This table is not partitioned.")
		}
		switch in.Op {
		case "create_partition":
			if err := checkName("partition name", in.NewName); err != nil {
				return "", host.Invalid(err)
			}
			b, err := boundSQL(in.Extra.Bound)
			if err != nil {
				return "", err
			}
			return "CREATE TABLE " + qident(t.schema(), in.NewName) + " PARTITION OF " + tbl + " " + b, nil
		case "attach_partition":
			child, err := s.resolve(ctx, in.Schema, in.Name)
			if err != nil {
				return "", err
			}
			if child.kind != "r" && child.kind != "p" {
				return "", actions.InvalidParams("Only a table can be attached as a partition.")
			}
			b, err := boundSQL(in.Extra.Bound)
			if err != nil {
				return "", err
			}
			return "ALTER TABLE " + tbl + " ATTACH PARTITION " + child.quoted + " " + b, nil
		default:
			var ok bool
			if err := s.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid JOIN pg_namespace n ON n.oid = c.relnamespace WHERE i.inhparent = $1 AND n.nspname = $2 AND c.relname = $3)`, t.oid, in.Schema, in.Name).Scan(&ok); err != nil {
				return "", err
			}
			if !ok {
				return "", actions.NotFound("There is no partition " + in.Name + " on this table.")
			}
			c := ""
			if in.Extra.Concurrent {
				if s.version < 140000 {
					return "", actions.InvalidParams("DETACH PARTITION CONCURRENTLY needs PostgreSQL 14 or newer.")
				}
				c = " CONCURRENTLY"
			}
			return "ALTER TABLE " + tbl + " DETACH PARTITION " + qident(in.Schema, in.Name) + c, nil
		}
	case "enable_trigger", "disable_trigger", "drop_trigger":
		if !isTable {
			return "", actions.InvalidParams("Triggers on views are managed from the SQL tab.")
		}
		if err := s.namedOnTable(ctx, t, "trigger", in.Name); err != nil {
			return "", err
		}
		trg := qident(in.Name)
		switch in.Op {
		case "enable_trigger":
			return "ALTER TABLE " + tbl + " ENABLE TRIGGER " + trg, nil
		case "disable_trigger":
			return "ALTER TABLE " + tbl + " DISABLE TRIGGER " + trg, nil
		default:
			return "DROP TRIGGER " + trg + " ON " + tbl, nil
		}
	}
	return "", actions.InvalidParams("op: unknown operation.")
}

// details lists a table's indexes, constraints and triggers for the Structure tab.
func (s *session) details(ctx context.Context, t *tableInfo) (map[string]any, error) {
	type idx struct {
		Name      string `json:"name"`
		Def       string `json:"definition"`
		Method    string `json:"method"`
		Unique    bool   `json:"unique"`
		Primary   bool   `json:"primary"`
		Valid     bool   `json:"valid"`
		Backs     string `json:"backs_constraint,omitempty"`
		Predicate string `json:"predicate,omitempty"`
		Bytes     int64  `json:"bytes"`
		Scans     int64  `json:"scans"`
		Columns   string `json:"columns"`
	}
	indexes := []idx{}
	rows, err := s.Query(ctx, `
		SELECT c.relname, pg_get_indexdef(i.indexrelid), am.amname, i.indisunique, i.indisprimary, i.indisvalid,
		       COALESCE((SELECT conname FROM pg_constraint k WHERE k.conindid = i.indexrelid AND k.contype IN ('p','u','x') LIMIT 1), ''),
		       COALESCE(pg_get_expr(i.indpred, i.indrelid), ''), pg_relation_size(i.indexrelid), COALESCE(st.idx_scan, 0),
		       COALESCE((SELECT string_agg(pg_get_indexdef(i.indexrelid, n, true), ', ' ORDER BY n) FROM generate_series(1, i.indnkeyatts) n), '')
		FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid JOIN pg_am am ON am.oid = c.relam
		LEFT JOIN pg_stat_user_indexes st ON st.indexrelid = i.indexrelid
		WHERE i.indrelid = $1 ORDER BY i.indisprimary DESC, c.relname`, t.oid)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x idx
		if err := rows.Scan(&x.Name, &x.Def, &x.Method, &x.Unique, &x.Primary, &x.Valid, &x.Backs, &x.Predicate, &x.Bytes, &x.Scans, &x.Columns); err != nil {
			rows.Close()
			return nil, err
		}
		indexes = append(indexes, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	type con struct {
		Name       string `json:"name"`
		Type       string `json:"type"` // primary key | unique | foreign key | check | exclusion
		Def        string `json:"definition"`
		Validated  bool   `json:"validated"`
		Deferrable bool   `json:"deferrable"`
	}
	cons := []con{}
	rows, err = s.Query(ctx, `
		SELECT conname, CASE contype WHEN 'p' THEN 'primary key' WHEN 'u' THEN 'unique' WHEN 'f' THEN 'foreign key' WHEN 'c' THEN 'check' WHEN 'x' THEN 'exclusion' ELSE contype::text END,
		       pg_get_constraintdef(oid), convalidated, condeferrable
		FROM pg_constraint WHERE conrelid = $1 AND contype IN ('p','u','f','c','x') ORDER BY contype <> 'p', conname`, t.oid)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x con
		if err := rows.Scan(&x.Name, &x.Type, &x.Def, &x.Validated, &x.Deferrable); err != nil {
			rows.Close()
			return nil, err
		}
		cons = append(cons, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	type trg struct {
		Name    string `json:"name"`
		Enabled string `json:"enabled"` // enabled | disabled | replica | always
		Def     string `json:"definition"`
		Func    string `json:"function"`
	}
	trgs := []trg{}
	rows, err = s.Query(ctx, `
		SELECT tgname, CASE tgenabled WHEN 'D' THEN 'disabled' WHEN 'R' THEN 'replica' WHEN 'A' THEN 'always' ELSE 'enabled' END, pg_get_triggerdef(oid), tgfoid::regproc::text
		FROM pg_trigger WHERE tgrelid = $1 AND NOT tgisinternal ORDER BY tgname`, t.oid)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x trg
		if err := rows.Scan(&x.Name, &x.Enabled, &x.Def, &x.Func); err != nil {
			rows.Close()
			return nil, err
		}
		trgs = append(trgs, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"indexes": indexes, "constraints": cons, "triggers": trgs, "kind": relkinds[t.kind], "capabilities": capabilities(s.version)}, nil
}

func registerDetails(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "pg.table_details", Description: "One table's indexes (method, columns, predicate, size, scans, validity), constraints and triggers.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
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
			out, err = s.details(ctx, t)
			return err
		})
		return out, err
	}})
}

func (t *tableInfo) schema() string { return t.nspname }
