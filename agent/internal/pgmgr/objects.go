package pgmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Object listing and the database overview: catalog queries only (pg_class, pg_stat_user_tables and friends), never a
// scan of the tables themselves, so a database with thousands of tables lists quickly. Like the table browser they run
// as the database's owner through its dedicated account (see queryroles.go). Everything is read-only.

// Kind labels, by pg_class.relkind.
var relkinds = map[string]string{
	"r": "table", "p": "partitioned table", "v": "view", "m": "materialized view", "f": "foreign table",
}

// kindLetters is the reverse map, for the kind filter.
func kindLetter(label string) (string, bool) {
	for l, n := range relkinds {
		if n == label {
			return l, true
		}
	}
	return "", false
}

// ObjectsIn is the listing request. Every field but the database is optional.
type ObjectsIn struct {
	tableIn
	Kind   string `json:"kind"`   // one of the labels in relkinds, or empty for all
	Search string `json:"search"` // a part of the name (case-insensitive, wildcards are literal)
	Sort   string `json:"sort"`   // name | rows | size | kind
	Dir    string `json:"dir"`    // asc | desc
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

// Object is one table, view, materialized view or foreign table with its statistics.
type Object struct {
	Schema         string `json:"schema"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Rows           int64  `json:"rows_estimate"`
	TotalBytes     int64  `json:"total_bytes"`
	DataBytes      int64  `json:"data_bytes"`
	IndexBytes     int64  `json:"index_bytes"`
	Indexes        int    `json:"indexes"`
	Columns        int    `json:"columns"`
	Constraints    int    `json:"constraints"`
	Triggers       int    `json:"triggers"`
	Partitions     int    `json:"partitions"`
	PartitionOf    string `json:"partition_of,omitempty"`
	LastVacuum     string `json:"last_vacuum,omitempty"`
	LastAnalyze    string `json:"last_analyze,omitempty"`
	Comment        string `json:"comment,omitempty"`
	HasPrimaryKey  bool   `json:"has_primary_key"`
	RowsEstimated  bool   `json:"rows_from_statistics"`
	StatisticsOnly bool   `json:"-"`
}

// escapeLike makes a search text literal inside ILIKE ... (the default escape character is a backslash).
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// objectOrder maps the public sort names to ORDER BY expressions; nothing from the request reaches the SQL text.
func objectOrder(sort, dir string) string {
	d := "ASC"
	if strings.EqualFold(dir, "desc") {
		d = "DESC"
	}
	switch sort {
	case "rows":
		return "rows_estimate " + d + ", schema, name"
	case "size":
		return "total_bytes " + d + ", schema, name"
	case "kind":
		return "kind " + d + ", schema, name"
	default:
		return "schema " + d + ", name " + d
	}
}

const objectScope = `c.relkind IN ('r','p','v','m','f') AND n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'`

func (s *session) objects(ctx context.Context, in ObjectsIn) (map[string]any, error) {
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	if in.Offset < 0 {
		in.Offset = 0
	}
	if len(in.Search) > 100 {
		return nil, actions.InvalidParams("search: at most 100 characters.")
	}
	if len(in.Schema) > 63 {
		return nil, actions.InvalidParams("schema: not a schema name.")
	}
	letter := ""
	if in.Kind != "" {
		l, ok := kindLetter(in.Kind)
		if !ok {
			return nil, actions.InvalidParams("kind: table, partitioned table, view, materialized view or foreign table.")
		}
		letter = l
	}
	// The filter is the same for the count and the page. $1 schema, $2 kind letter, $3 search pattern (all may be empty).
	where := objectScope + ` AND ($1 = '' OR n.nspname = $1) AND ($2 = '' OR c.relkind::text = $2) AND ($3 = '' OR c.relname ILIKE '%' || $3 || '%')`
	var total int64
	if err := s.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE `+where, in.Schema, letter, escapeLike(in.Search)).Scan(&total); err != nil {
		return nil, err
	}
	// Sizes and estimates of a partitioned table are the sums over its partitions (its own storage is empty).
	q := fmt.Sprintf(`
WITH base AS (
  SELECT c.oid, n.nspname AS schema, c.relname AS name, c.relkind::text AS rk,
         CASE c.relkind WHEN 'p' THEN (SELECT COALESCE(SUM(GREATEST(x.reltuples,0)),0)::bigint FROM (
                 WITH RECURSIVE t(oid) AS (SELECT c.oid UNION ALL SELECT i.inhrelid FROM pg_inherits i JOIN t ON i.inhparent = t.oid)
                 SELECT k.reltuples FROM t JOIN pg_class k ON k.oid = t.oid) x)
              ELSE CASE WHEN c.reltuples >= 0 THEN c.reltuples::bigint ELSE COALESCE(st.n_live_tup, 0) END END AS rows_estimate,
         c.reltuples < 0 AS never_analyzed,
         CASE c.relkind WHEN 'p' THEN (SELECT COALESCE(SUM(pg_table_size(t.oid)),0)::bigint FROM (
                 WITH RECURSIVE t(oid) AS (SELECT c.oid UNION ALL SELECT i.inhrelid FROM pg_inherits i JOIN t ON i.inhparent = t.oid) SELECT oid FROM t) t)
              WHEN 'r' THEN pg_table_size(c.oid) WHEN 'm' THEN pg_table_size(c.oid) ELSE 0 END AS data_bytes,
         CASE c.relkind WHEN 'p' THEN (SELECT COALESCE(SUM(pg_indexes_size(t.oid)),0)::bigint FROM (
                 WITH RECURSIVE t(oid) AS (SELECT c.oid UNION ALL SELECT i.inhrelid FROM pg_inherits i JOIN t ON i.inhparent = t.oid) SELECT oid FROM t) t)
              WHEN 'r' THEN pg_indexes_size(c.oid) WHEN 'm' THEN pg_indexes_size(c.oid) ELSE 0 END AS index_bytes,
         st.last_vacuum, st.last_autovacuum, st.last_analyze, st.last_autoanalyze
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
  LEFT JOIN pg_stat_user_tables st ON st.relid = c.oid
  WHERE %s
), sized AS (
  SELECT b.*, b.data_bytes + b.index_bytes AS total_bytes, CASE b.rk WHEN 'p' THEN 'partitioned table' WHEN 'r' THEN 'table' WHEN 'v' THEN 'view'
         WHEN 'm' THEN 'materialized view' ELSE 'foreign table' END AS kind
  FROM base b
)
SELECT schema, name, kind, rows_estimate, never_analyzed, total_bytes, data_bytes, index_bytes,
       (SELECT count(*) FROM pg_index i WHERE i.indrelid = s.oid)::int,
       (SELECT count(*) FROM pg_attribute a WHERE a.attrelid = s.oid AND a.attnum > 0 AND NOT a.attisdropped)::int,
       (SELECT count(*) FROM pg_constraint k WHERE k.conrelid = s.oid)::int,
       (SELECT count(*) FROM pg_trigger g WHERE g.tgrelid = s.oid AND NOT g.tgisinternal)::int,
       (SELECT count(*) FROM pg_inherits h WHERE h.inhparent = s.oid)::int,
       COALESCE((SELECT pc.relname FROM pg_inherits h JOIN pg_class pc ON pc.oid = h.inhparent WHERE h.inhrelid = s.oid LIMIT 1), ''),
       COALESCE(to_char(GREATEST(s.last_vacuum, s.last_autovacuum) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), ''),
       COALESCE(to_char(GREATEST(s.last_analyze, s.last_autoanalyze) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), ''),
       COALESCE(obj_description(s.oid, 'pg_class'), ''),
       EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = s.oid AND i.indisprimary)
FROM sized s ORDER BY %s LIMIT $4 OFFSET $5`, where, objectOrder(in.Sort, in.Dir))
	rows, err := s.Query(ctx, q, in.Schema, letter, escapeLike(in.Search), in.Limit, in.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Object{}
	for rows.Next() {
		var o Object
		var never bool
		if err := rows.Scan(&o.Schema, &o.Name, &o.Kind, &o.Rows, &never, &o.TotalBytes, &o.DataBytes, &o.IndexBytes, &o.Indexes, &o.Columns,
			&o.Constraints, &o.Triggers, &o.Partitions, &o.PartitionOf, &o.LastVacuum, &o.LastAnalyze, &o.Comment, &o.HasPrimaryKey); err != nil {
			return nil, err
		}
		o.RowsEstimated = !never
		items = append(items, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// How many objects of each kind the database has (the filter chips), independent of the current filter.
	kinds := map[string]int64{}
	kr, err := s.Query(ctx, `SELECT c.relkind::text, count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE `+objectScope+` GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer kr.Close()
	for kr.Next() {
		var k string
		var n int64
		if err := kr.Scan(&k, &n); err != nil {
			return nil, err
		}
		kinds[relkinds[k]] = n
	}
	schemas, err := s.userSchemas(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"total": total, "offset": in.Offset, "limit": in.Limit, "items": items, "kinds": kinds, "schemas": schemas}, kr.Err()
}

// Capabilities reports what this server can do, from its version number, so the panel shows only controls that work.
func capabilities(version int) map[string]bool {
	return map[string]bool{
		"partitioning":        version >= 100000,
		"identity_columns":    version >= 100000,
		"generated_columns":   version >= 120000,
		"reindex_concurrent":  version >= 120000,
		"detach_concurrently": version >= 140000,
		"procedures":          version >= 110000,
		"stored_generated":    version >= 120000,
	}
}

func (s *session) overview(ctx context.Context, database string) (map[string]any, error) {
	out := map[string]any{"database": database}
	var version, owner, encoding, collate, ctype string
	var size, rows, idx int64
	err := s.QueryRow(ctx, `
		SELECT current_setting('server_version'), pg_get_userbyid(d.datdba), pg_encoding_to_char(d.encoding), d.datcollate, d.datctype,
		       pg_database_size(d.oid)
		FROM pg_database d WHERE d.datname = current_database()`).Scan(&version, &owner, &encoding, &collate, &ctype, &size)
	if err != nil {
		return nil, err
	}
	if err := s.QueryRow(ctx, `
		SELECT COALESCE(SUM(GREATEST(c.reltuples,0)),0)::bigint, COALESCE(SUM(pg_indexes_size(c.oid)),0)::bigint
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r','m') AND n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'`).Scan(&rows, &idx); err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	kr, err := s.Query(ctx, `SELECT c.relkind::text, count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema' AND c.relkind IN ('r','p','v','m','f','S','i','I') GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer kr.Close()
	for kr.Next() {
		var k string
		var n int64
		if err := kr.Scan(&k, &n); err != nil {
			return nil, err
		}
		counts[k] = n
	}
	var funcs, trigs, schemaCount int64
	if err := s.QueryRow(ctx, `SELECT (SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'),
		(SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal), (SELECT count(*) FROM pg_namespace WHERE nspname NOT LIKE 'pg\_%' AND nspname <> 'information_schema')`).Scan(&funcs, &trigs, &schemaCount); err != nil {
		return nil, err
	}
	var numeric int
	if err := s.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&numeric); err != nil {
		return nil, err
	}
	exts := []string{}
	er, err := s.Query(ctx, `SELECT extname FROM pg_extension ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer er.Close()
	for er.Next() {
		var e string
		if err := er.Scan(&e); err != nil {
			return nil, err
		}
		exts = append(exts, e)
	}
	host := s.target.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := s.target.Port
	if port == 0 {
		port = 5432
	}
	out["server_version"] = version
	out["server_version_num"] = numeric
	out["owner"] = owner
	out["encoding"] = encoding
	out["collation"] = collate
	out["ctype"] = ctype
	out["size_bytes"] = size
	out["rows_estimate"] = rows
	out["index_bytes"] = idx
	out["tables"] = counts["r"] + counts["p"]
	out["partitioned_tables"] = counts["p"]
	out["views"] = counts["v"]
	out["materialized_views"] = counts["m"]
	out["foreign_tables"] = counts["f"]
	out["sequences"] = counts["S"]
	out["indexes"] = counts["i"] + counts["I"]
	out["functions"] = funcs
	out["triggers"] = trigs
	out["schemas"] = schemaCount
	out["extensions"] = exts
	out["capabilities"] = capabilities(numeric)
	// Connection details a person needs to reach this database; never a password.
	out["connection"] = map[string]any{"host": host, "port": port, "database": database, "role": owner, "tls": s.target.SSLMode, "remote": s.target.remote()}
	return out, kr.Err()
}

func registerObjects(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "pg.objects", Description: "A database's tables, views, materialized views and foreign tables with statistics (row estimate, data, index and total size, column, index, constraint, trigger and partition counts, last vacuum and analyze), filtered, sorted and paged. Catalog queries only.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in ObjectsIn
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.check(); err != nil {
			return nil, err
		}
		var out map[string]any
		err := withOwner(ctx, env, in.Target, in.Database, func(s *session) error {
			var err error
			out, err = s.objects(ctx, in)
			return err
		})
		return out, err
	}})
	r.Register(actions.Action{Name: "pg.db_overview", Description: "One database's overview: server version and capabilities, size, object counts, estimated rows, index size, owner and the connection details (never a password).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in tableIn
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.check(); err != nil {
			return nil, err
		}
		var out map[string]any
		err := withOwner(ctx, env, in.Target, in.Database, func(s *session) error {
			var err error
			out, err = s.overview(ctx, in.Database)
			return err
		})
		return out, err
	}})
}
