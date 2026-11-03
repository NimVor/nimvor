package pgmgr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// The table browser reads and edits one database as that database's OWNER role (SET ROLE), never as the
// provisioning role, so it can do no more than the owner could do by connecting. Table and column names are not
// restricted to NimVor's naming rules (a customer's own tables may be called anything): they are looked up in the
// catalog first, so only names that exist are used, and are quoted with pgx's identifier quoting. Values are always
// parameters. Reads run in a read-only transaction. A table without a usable row identity is browse-only.

const (
	maxPage       = 500
	cellLimit     = 2048
	searchLimit   = 200
	countCap      = 100000
	maxFilters    = 8
	browseTimeout = "15s"
)

// ColumnInfo describes one column.
type ColumnInfo struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	NotNull   bool   `json:"not_null"`
	Default   string `json:"default,omitempty"`
	Key       bool   `json:"key"`
	Generated bool   `json:"generated"`
	Identity  bool   `json:"identity"`
}

type tableInfo struct {
	oid     uint32
	kind    string
	quoted  string
	columns []ColumnInfo
	key     []string
}

// openOwner connects to database and acts as its owner.
func openOwner(ctx context.Context, env *host.Env, t *Target, database string) (*session, error) {
	s, err := open(ctx, env, t, database)
	if err != nil {
		return nil, err
	}
	var owner string
	if err := s.QueryRow(ctx, `SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = current_database()`).Scan(&owner); err != nil {
		s.Close(ctx)
		return nil, err
	}
	if err := s.execf(ctx, "SET ROLE %I", owner); err != nil {
		s.Close(ctx)
		return nil, actions.Failed("The database could not be opened for browsing.", friendly(err))
	}
	for _, stmt := range []string{"SET statement_timeout = '" + browseTimeout + "'", "SET lock_timeout = '3s'", "SET idle_in_transaction_session_timeout = '30s'"} {
		if _, err := s.Exec(ctx, stmt); err != nil {
			s.Close(ctx)
			return nil, err
		}
	}
	return s, nil
}

func userError(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		msg := pe.Message
		if pe.Code == "42501" {
			msg = "permission denied for this table"
		}
		return actions.Failed("PostgreSQL refused the request: "+msg+".", "")
	}
	return err
}

func (s *session) schemas(ctx context.Context) ([]string, error) { return s.userSchemas(ctx) }

// TableSummary is one entry of a database's table list.
type TableSummary struct {
	Schema   string `json:"schema"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // table, view, materialized view, foreign table
	Rows     int64  `json:"rows_estimate"`
	Size     int64  `json:"size_bytes"`
	Editable bool   `json:"editable"`
}

func (s *session) tables(ctx context.Context) ([]TableSummary, error) {
	rows, err := s.Query(ctx, `
		SELECT n.nspname, c.relname,
		       CASE c.relkind WHEN 'r' THEN 'table' WHEN 'p' THEN 'table' WHEN 'v' THEN 'view' WHEN 'm' THEN 'materialized view' ELSE 'foreign table' END,
		       GREATEST(c.reltuples, 0)::bigint,
		       CASE WHEN c.relkind IN ('r','p','m') THEN pg_table_size(c.oid) ELSE 0 END,
		       c.relkind IN ('r','p') AND EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.oid AND i.indisprimary)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r','p','v','m','f') AND n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'
		ORDER BY n.nspname, c.relname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TableSummary{}
	for rows.Next() {
		var t TableSummary
		if err := rows.Scan(&t.Schema, &t.Name, &t.Kind, &t.Rows, &t.Size, &t.Editable); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// resolve finds a table in the catalog (user schemas only) and reads its columns and row identity.
func (s *session) resolve(ctx context.Context, schema, table string) (*tableInfo, error) {
	if schema == "" || table == "" || len(schema) > 63 || len(table) > 63 || strings.ContainsRune(schema+table, 0) {
		return nil, actions.InvalidParams("schema and table: name a table of the database.")
	}
	t := &tableInfo{}
	err := s.QueryRow(ctx, `
		SELECT c.oid, c.relkind::text FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r','p','v','m','f')
		  AND n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'`, schema, table).Scan(&t.oid, &t.kind)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, actions.NotFound("There is no table " + schema + "." + table + " in this database.")
		}
		return nil, err
	}
	t.quoted = pgx.Identifier{schema, table}.Sanitize()
	rows, err := s.Query(ctx, `
		SELECT a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull, coalesce(pg_get_expr(d.adbin, d.adrelid), ''),
		       a.attgenerated <> '', a.attidentity <> ''
		FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum`, t.oid)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c ColumnInfo
		if err := rows.Scan(&c.Name, &c.Type, &c.NotNull, &c.Default, &c.Generated, &c.Identity); err != nil {
			rows.Close()
			return nil, err
		}
		t.columns = append(t.columns, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if t.kind == "r" || t.kind == "p" {
		t.key, err = s.keyOf(ctx, t.oid)
		if err != nil {
			return nil, err
		}
		for i := range t.columns {
			for _, k := range t.key {
				if t.columns[i].Name == k {
					t.columns[i].Key = true
				}
			}
		}
	}
	return t, nil
}

// keyOf is the row identity: the primary key, or else a unique index without a predicate whose columns are all
// NOT NULL. Without either, rows cannot be addressed unambiguously and the table is browse-only.
func (s *session) keyOf(ctx context.Context, oid uint32) ([]string, error) {
	rows, err := s.Query(ctx, `
		SELECT i.indexrelid, i.indisprimary, array_agg(a.attname ORDER BY k.ord)
		FROM pg_index i
		JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
		WHERE i.indrelid = $1 AND i.indisunique AND i.indisvalid AND i.indpred IS NULL AND i.indexprs IS NULL
		  AND (i.indisprimary OR NOT EXISTS (SELECT 1 FROM unnest(i.indkey) x JOIN pg_attribute b ON b.attrelid = i.indrelid AND b.attnum = x WHERE NOT b.attnotnull))
		GROUP BY i.indexrelid, i.indisprimary ORDER BY i.indisprimary DESC, i.indexrelid LIMIT 1`, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if rows.Next() {
		var id uint32
		var primary bool
		var cols []string
		if err := rows.Scan(&id, &primary, &cols); err != nil {
			return nil, err
		}
		return cols, nil
	}
	return nil, rows.Err()
}

func (t *tableInfo) column(name string) *ColumnInfo {
	for i := range t.columns {
		if t.columns[i].Name == name {
			return &t.columns[i]
		}
	}
	return nil
}

func (t *tableInfo) editable() (bool, string) {
	switch {
	case t.kind != "r" && t.kind != "p":
		return false, "Views, materialized views and foreign tables are browse-only."
	case len(t.key) == 0:
		return false, "This table has no primary key (or unique key of non-null columns), so its rows cannot be changed safely."
	}
	return true, ""
}

// Filter narrows a browse.
type Filter struct {
	Column string `json:"column"`
	Op     string `json:"op"` // eq, ne, contains, null, notnull
	Value  string `json:"value"`
}

// BrowseRequest is one page of a table.
type BrowseRequest struct {
	Offset  int      `json:"offset"`
	Limit   int      `json:"limit"`
	Sort    string   `json:"sort"`
	Desc    bool     `json:"desc"`
	Search  string   `json:"search"`
	Filters []Filter `json:"filters"`
}

func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

func (t *tableInfo) where(req BrowseRequest, args *[]any) (string, error) {
	var conds []string
	add := func(v any) string {
		*args = append(*args, v)
		return fmt.Sprintf("$%d", len(*args))
	}
	if req.Search != "" {
		if len(req.Search) > searchLimit {
			return "", actions.InvalidParams("search: at most 200 characters.")
		}
		p := add(likePattern(req.Search))
		var any []string
		for _, c := range t.columns {
			any = append(any, pgx.Identifier{c.Name}.Sanitize()+"::text ILIKE "+p)
		}
		conds = append(conds, "("+strings.Join(any, " OR ")+")")
	}
	if len(req.Filters) > maxFilters {
		return "", actions.InvalidParams("filters: at most 8.")
	}
	for _, f := range req.Filters {
		c := t.column(f.Column)
		if c == nil {
			return "", actions.InvalidParams("filters: there is no column " + f.Column + ".")
		}
		q := pgx.Identifier{c.Name}.Sanitize()
		switch f.Op {
		case "eq":
			conds = append(conds, q+"::text = "+add(f.Value))
		case "ne":
			conds = append(conds, q+"::text IS DISTINCT FROM "+add(f.Value))
		case "contains":
			conds = append(conds, q+"::text ILIKE "+add(likePattern(f.Value)))
		case "null":
			conds = append(conds, q+" IS NULL")
		case "notnull":
			conds = append(conds, q+" IS NOT NULL")
		default:
			return "", actions.InvalidParams("filters: choose eq, ne, contains, null or notnull.")
		}
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), nil
}

// Page is the answer to a browse.
type Page struct {
	Columns     []ColumnInfo `json:"columns"`
	Rows        [][]*string  `json:"rows"`
	Truncated   [][2]int     `json:"truncated"`
	Total       int64        `json:"total"`
	TotalCapped bool         `json:"total_capped"`
	Offset      int          `json:"offset"`
	Limit       int          `json:"limit"`
	Key         []string     `json:"key"`
	Editable    bool         `json:"editable"`
	Reason      string       `json:"reason,omitempty"`
	Kind        string       `json:"kind"`
}

func (s *session) browse(ctx context.Context, t *tableInfo, req BrowseRequest) (*Page, error) {
	if req.Limit <= 0 {
		req.Limit = 50
	}
	if req.Limit > maxPage {
		req.Limit = maxPage
	}
	if req.Offset < 0 || req.Offset > 10_000_000 {
		return nil, actions.InvalidParams("offset: out of range.")
	}
	var args []any
	where, err := t.where(req, &args)
	if err != nil {
		return nil, err
	}
	order := ""
	if req.Sort != "" {
		c := t.column(req.Sort)
		if c == nil {
			return nil, actions.InvalidParams("sort: there is no column " + req.Sort + ".")
		}
		order = " ORDER BY " + pgx.Identifier{c.Name}.Sanitize()
		if req.Desc {
			order += " DESC"
		}
		order += " NULLS LAST"
	} else if len(t.key) > 0 {
		qs := make([]string, len(t.key))
		for i, k := range t.key {
			qs[i] = pgx.Identifier{k}.Sanitize()
		}
		order = " ORDER BY " + strings.Join(qs, ", ")
	}
	sel := make([]string, len(t.columns))
	for i, c := range t.columns {
		sel[i] = fmt.Sprintf("left(%s::text, %d)", pgx.Identifier{c.Name}.Sanitize(), cellLimit+1)
	}
	tx, err := s.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	p := &Page{Columns: t.columns, Rows: [][]*string{}, Truncated: [][2]int{}, Offset: req.Offset, Limit: req.Limit, Key: t.key, Kind: t.kind}
	p.Editable, p.Reason = t.editable()
	if p.Key == nil {
		p.Key = []string{}
	}
	if err := tx.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM (SELECT 1 FROM %s%s LIMIT %d) s", t.quoted, where, countCap+1), args...).Scan(&p.Total); err != nil {
		return nil, userError(err)
	}
	if p.Total > countCap {
		p.Total, p.TotalCapped = countCap, true
	}
	q := fmt.Sprintf("SELECT %s FROM %s%s%s LIMIT %d OFFSET %d", strings.Join(sel, ", "), t.quoted, where, order, req.Limit, req.Offset)
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, userError(err)
	}
	defer rows.Close()
	for rows.Next() {
		cells := make([]*string, len(t.columns))
		ptrs := make([]any, len(cells))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		ri := len(p.Rows)
		for ci, c := range cells {
			if c != nil && len([]rune(*c)) > cellLimit {
				v := string([]rune(*c)[:cellLimit])
				cells[ci] = &v
				p.Truncated = append(p.Truncated, [2]int{ri, ci})
			}
		}
		p.Rows = append(p.Rows, cells)
	}
	return p, rows.Err()
}

// RowChange is an insert, update or delete of one row. Values are text (nil is NULL) and are cast by the server to
// the column's type.
type RowChange struct {
	Values map[string]*string `json:"values"`
	Key    map[string]*string `json:"key"`
}

func (s *session) insertRow(ctx context.Context, t *tableInfo, ch RowChange) error {
	if t.kind != "r" && t.kind != "p" {
		return actions.Conflict("Rows can only be added to tables.")
	}
	var cols, ph []string
	var args []any
	for name, v := range ch.Values {
		c := t.column(name)
		if c == nil {
			return actions.InvalidParams("values: there is no column " + name + ".")
		}
		if c.Generated {
			return actions.InvalidParams("values: " + name + " is generated and cannot be set.")
		}
		cols = append(cols, pgx.Identifier{name}.Sanitize())
		args = append(args, v)
		ph = append(ph, fmt.Sprintf("$%d", len(args)))
	}
	stmt := "INSERT INTO " + t.quoted + " DEFAULT VALUES"
	if len(cols) > 0 {
		stmt = "INSERT INTO " + t.quoted + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(ph, ", ") + ")"
	}
	_, err := s.Exec(ctx, stmt, args...)
	return userError(err)
}

func (t *tableInfo) keyWhere(key map[string]*string, args *[]any) (string, error) {
	if ok, why := t.editable(); !ok {
		return "", actions.Conflict(why)
	}
	if len(key) != len(t.key) {
		return "", actions.InvalidParams("key: give every key column (" + strings.Join(t.key, ", ") + ").")
	}
	var conds []string
	for _, k := range t.key {
		v, ok := key[k]
		if !ok || v == nil {
			return "", actions.InvalidParams("key: give a value for " + k + ".")
		}
		*args = append(*args, *v)
		conds = append(conds, fmt.Sprintf("%s = $%d", pgx.Identifier{k}.Sanitize(), len(*args)))
	}
	return " WHERE " + strings.Join(conds, " AND "), nil
}

func (s *session) changeRow(ctx context.Context, t *tableInfo, ch RowChange, del bool) error {
	var args []any
	where, err := t.keyWhere(ch.Key, &args)
	if err != nil {
		return err
	}
	var stmt string
	if del {
		stmt = "DELETE FROM " + t.quoted + where
	} else {
		if len(ch.Values) == 0 {
			return actions.InvalidParams("values: nothing to change.")
		}
		var sets []string
		for name, v := range ch.Values {
			c := t.column(name)
			if c == nil {
				return actions.InvalidParams("values: there is no column " + name + ".")
			}
			if c.Generated {
				return actions.InvalidParams("values: " + name + " is generated and cannot be set.")
			}
			args = append(args, v)
			sets = append(sets, fmt.Sprintf("%s = $%d", pgx.Identifier{name}.Sanitize(), len(args)))
		}
		// The key placeholders were numbered first; the SET list follows.
		stmt = "UPDATE " + t.quoted + " SET " + strings.Join(sets, ", ") + where
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, stmt, args...)
	if err != nil {
		return userError(err)
	}
	if tag.RowsAffected() != 1 {
		return actions.Conflict("The row no longer exists or changed; nothing was modified. Reload the table.")
	}
	return tx.Commit(ctx)
}

var _ = time.Second
