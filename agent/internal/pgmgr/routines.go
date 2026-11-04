package pgmgr

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Routines (functions, procedures, aggregates) and sequences. Listing is catalog-only. A routine is addressed by its
// OID; the statement to drop it is built by the server from that OID (regprocedure writes the name and argument types
// correctly quoted), so no name or signature from the request reaches a statement. Sequences are looked up in
// pg_sequences, then quoted. Creating a routine means writing code, which is what the SQL tab is for; it is not offered
// here (that would be arbitrary SQL through a management endpoint).

func (s *session) routines(ctx context.Context, schema, search string) ([]map[string]any, error) {
	rows, err := s.Query(ctx, `
		SELECT p.oid::bigint, n.nspname, p.proname, p.prokind::text, l.lanname, pg_get_function_identity_arguments(p.oid),
		       COALESCE(pg_get_function_result(p.oid), ''), p.provolatile::text, p.prosecdef, pg_get_userbyid(p.proowner),
		       COALESCE(obj_description(p.oid, 'pg_proc'), ''),
		       EXISTS (SELECT 1 FROM pg_trigger g WHERE g.tgfoid = p.oid AND NOT g.tgisinternal)
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace JOIN pg_language l ON l.oid = p.prolang
		WHERE n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'
		  AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')
		  AND ($1 = '' OR n.nspname = $1) AND ($2 = '' OR p.proname ILIKE '%' || $2 || '%')
		ORDER BY n.nspname, p.proname, p.oid LIMIT 500`, schema, escapeLike(search))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	kinds := map[string]string{"f": "function", "p": "procedure", "a": "aggregate", "w": "window function"}
	vol := map[string]string{"i": "immutable", "s": "stable", "v": "volatile"}
	out := []map[string]any{}
	for rows.Next() {
		var oid int64
		var sch, name, kind, lang, args, result, vola, owner, comment string
		var secdef, trig bool
		if err := rows.Scan(&oid, &sch, &name, &kind, &lang, &args, &result, &vola, &secdef, &owner, &comment, &trig); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"oid": oid, "schema": sch, "name": name, "kind": kinds[kind], "language": lang, "arguments": args, "returns": result,
			"volatility": vol[vola], "security_definer": secdef, "owner": owner, "comment": comment, "trigger_function": trig})
	}
	return out, rows.Err()
}

func (s *session) routineDefinition(ctx context.Context, oid int64) (string, error) {
	var def string
	err := s.QueryRow(ctx, `
		SELECT CASE WHEN p.prokind = 'a' THEN '-- aggregate ' || p.oid::regprocedure::text ELSE pg_get_functiondef(p.oid) END
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE p.oid = $1 AND n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'`, oid).Scan(&def)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", actions.NotFound("That routine no longer exists.")
	}
	return def, err
}

func (s *session) routineDrop(ctx context.Context, oid int64, cascade bool) (string, error) {
	var kind, sig string
	err := s.QueryRow(ctx, `
		SELECT p.prokind::text, p.oid::regprocedure::text FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE p.oid = $1 AND n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'
		  AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')`, oid).Scan(&kind, &sig)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", actions.NotFound("That routine no longer exists.")
	}
	if err != nil {
		return "", err
	}
	word := "FUNCTION"
	switch {
	case kind == "p":
		word = "PROCEDURE"
	case kind == "a":
		word = "AGGREGATE"
	}
	c := ""
	if cascade {
		c = " CASCADE"
	}
	stmt := "DROP " + word + " " + sig + c
	if _, err := s.Exec(ctx, stmt); err != nil {
		return stmt, userError(err)
	}
	return stmt, nil
}

// ---- sequences -----------------------------------------------------------------------------------------

func (s *session) sequences(ctx context.Context, schema string) ([]map[string]any, error) {
	rows, err := s.Query(ctx, `
		SELECT q.schemaname, q.sequencename, q.data_type::text, q.start_value, q.min_value, q.max_value, q.increment_by, q.cycle, q.cache_size, q.last_value,
		       COALESCE((SELECT t.relname || '.' || a.attname FROM pg_depend d JOIN pg_class t ON t.oid = d.refobjid JOIN pg_attribute a ON a.attrelid = d.refobjid AND a.attnum = d.refobjsubid
		                 WHERE d.objid = (quote_ident(q.schemaname) || '.' || quote_ident(q.sequencename))::regclass AND d.deptype IN ('a','i') AND d.classid = 'pg_class'::regclass LIMIT 1), '')
		FROM pg_sequences q
		WHERE q.schemaname NOT LIKE 'pg\_%' AND q.schemaname <> 'information_schema' AND ($1 = '' OR q.schemaname = $1)
		ORDER BY q.schemaname, q.sequencename LIMIT 1000`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var sch, name, typ, owned string
		var start, minv, maxv, inc, cache int64
		var last *int64
		var cycle bool
		if err := rows.Scan(&sch, &name, &typ, &start, &minv, &maxv, &inc, &cycle, &cache, &last, &owned); err != nil {
			return nil, err
		}
		m := map[string]any{"schema": sch, "name": name, "type": typ, "start": start, "min": minv, "max": maxv, "increment": inc, "cycle": cycle, "cache": cache, "owned_by": owned}
		if last != nil {
			m["last_value"] = *last
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SeqIn is the request of pg.sequence_alter.
type SeqIn struct {
	tableIn
	Op        string `json:"op"` // create | set_value | alter | sync | drop
	Name      string `json:"name"`
	Value     *int64 `json:"value"`
	Start     *int64 `json:"start"`
	Increment *int64 `json:"increment"`
	Min       *int64 `json:"min"`
	Max       *int64 `json:"max"`
	Cache     *int64 `json:"cache"`
	Cycle     *bool  `json:"cycle"`
	Cascade   bool   `json:"cascade"`
	DryRun    bool   `json:"dry_run"`
}

func (s *session) seqSQL(ctx context.Context, in SeqIn) ([]string, error) {
	opts := func(create bool) (string, error) {
		var o []string
		add := func(word string, v *int64) {
			if v != nil {
				o = append(o, word+" "+strconv.FormatInt(*v, 10))
			}
		}
		if in.Increment != nil && *in.Increment == 0 {
			return "", actions.InvalidParams("increment: cannot be 0.")
		}
		if in.Cache != nil && *in.Cache < 1 {
			return "", actions.InvalidParams("cache: at least 1.")
		}
		add("INCREMENT BY", in.Increment)
		add("MINVALUE", in.Min)
		add("MAXVALUE", in.Max)
		if create {
			add("START WITH", in.Start)
		}
		add("CACHE", in.Cache)
		if in.Cycle != nil {
			if *in.Cycle {
				o = append(o, "CYCLE")
			} else {
				o = append(o, "NO CYCLE")
			}
		}
		return strings.Join(o, " "), nil
	}
	if in.Op == "create" {
		if err := checkName("sequence name", in.Name); err != nil {
			return nil, host.Invalid(err)
		}
		if err := checkName("schema", in.Schema); err != nil {
			return nil, host.Invalid(err)
		}
		var ok bool
		if err := s.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1 AND nspname NOT LIKE 'pg\_%' AND nspname <> 'information_schema')`, in.Schema).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			return nil, actions.NotFound("There is no schema " + in.Schema + " in this database.")
		}
		o, err := opts(true)
		if err != nil {
			return nil, err
		}
		return []string{strings.TrimSpace("CREATE SEQUENCE " + qident(in.Schema, in.Name) + " " + o)}, nil
	}
	// The rest name an existing sequence.
	var owner string
	err := s.QueryRow(ctx, `SELECT q.schemaname FROM pg_sequences q WHERE q.schemaname = $1 AND q.sequencename = $2 AND q.schemaname NOT LIKE 'pg\_%' AND q.schemaname <> 'information_schema'`, in.Schema, in.Name).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, actions.NotFound("There is no sequence " + in.Schema + "." + in.Name + " in this database.")
	}
	if err != nil {
		return nil, err
	}
	seq := qident(in.Schema, in.Name)
	switch in.Op {
	case "set_value":
		if in.Value == nil {
			return nil, actions.InvalidParams("value: the number the next call should follow.")
		}
		return []string{"SELECT setval(" + quoteLit(seq) + "::regclass, " + strconv.FormatInt(*in.Value, 10) + ", true)"}, nil
	case "restart":
		if in.Value == nil {
			return nil, actions.InvalidParams("value: the number the next call should return.")
		}
		return []string{"ALTER SEQUENCE " + seq + " RESTART WITH " + strconv.FormatInt(*in.Value, 10)}, nil
	case "alter":
		o, err := opts(false)
		if err != nil {
			return nil, err
		}
		if o == "" {
			return nil, actions.InvalidParams("Nothing to change.")
		}
		return []string{"ALTER SEQUENCE " + seq + " " + o}, nil
	case "sync":
		// Next value = highest value in the column that uses the sequence, plus one.
		var tsch, tname, col string
		err := s.QueryRow(ctx, `
			SELECT n.nspname, t.relname, a.attname FROM pg_depend d JOIN pg_class t ON t.oid = d.refobjid JOIN pg_namespace n ON n.oid = t.relnamespace
			JOIN pg_attribute a ON a.attrelid = d.refobjid AND a.attnum = d.refobjsubid
			WHERE d.objid = $1::regclass AND d.deptype IN ('a','i') AND d.classid = 'pg_class'::regclass LIMIT 1`, seq).Scan(&tsch, &tname, &col)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, actions.Conflict("This sequence is not owned by a column, so there is no table to follow. Set the value by hand instead.")
		}
		if err != nil {
			return nil, err
		}
		return []string{"SELECT setval(" + quoteLit(seq) + "::regclass, COALESCE(MAX(" + qident(col) + "), 0) + 1, false) FROM " + qident(tsch, tname)}, nil
	case "drop":
		var owned bool
		if err := s.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = $1::regclass AND d.deptype IN ('a','i') AND d.classid = 'pg_class'::regclass)`, seq).Scan(&owned); err != nil {
			return nil, err
		}
		if owned {
			return nil, actions.Conflict("This sequence belongs to a column (serial or identity). Change the column instead of dropping the sequence.")
		}
		c := ""
		if in.Cascade {
			c = " CASCADE"
		}
		return []string{"DROP SEQUENCE " + seq + c}, nil
	}
	return nil, actions.InvalidParams("op: create, set_value, restart, alter, sync or drop.")
}

// quoteLit writes an already-validated identifier text as a string literal (for regclass casts).
func quoteLit(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// ---- partitions ----------------------------------------------------------------------------------------

func (s *session) partitions(ctx context.Context, t *tableInfo) (map[string]any, error) {
	if t.kind != "p" {
		return map[string]any{"partitioned": false, "partitions": []any{}}, nil
	}
	var key string
	if err := s.QueryRow(ctx, `SELECT pg_get_partkeydef($1)`, t.oid).Scan(&key); err != nil {
		return nil, err
	}
	rows, err := s.Query(ctx, `
		SELECT n.nspname, c.relname, pg_get_expr(c.relpartbound, c.oid), c.relkind::text, GREATEST(c.reltuples,0)::bigint, pg_total_relation_size(c.oid)
		FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE i.inhparent = $1 ORDER BY pg_get_expr(c.relpartbound, c.oid), c.relname`, t.oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parts := []map[string]any{}
	for rows.Next() {
		var sch, name, bound, kind string
		var est, size int64
		if err := rows.Scan(&sch, &name, &bound, &kind, &est, &size); err != nil {
			return nil, err
		}
		parts = append(parts, map[string]any{"schema": sch, "name": name, "bound": bound, "partitioned": kind == "p", "rows_estimate": est, "total_bytes": size})
	}
	return map[string]any{"partitioned": true, "key": key, "partitions": parts}, rows.Err()
}

// BoundIn describes a partition bound.
type BoundIn struct {
	Kind      string   `json:"kind"` // range | list | hash | default
	From      []string `json:"from"`
	To        []string `json:"to"`
	Values    []string `json:"values"`
	Modulus   int      `json:"modulus"`
	Remainder int      `json:"remainder"`
}

func boundSQL(b BoundIn) (string, error) {
	lit := func(v string) (string, error) {
		switch strings.ToUpper(strings.TrimSpace(v)) {
		case "MINVALUE", "MAXVALUE":
			return strings.ToUpper(strings.TrimSpace(v)), nil
		}
		return quoteLiteral(v)
	}
	list := func(vs []string) (string, error) {
		if len(vs) == 0 || len(vs) > 32 {
			return "", actions.InvalidParams("bound: 1 to 32 values.")
		}
		out := make([]string, len(vs))
		for i, v := range vs {
			l, err := lit(v)
			if err != nil {
				return "", err
			}
			out[i] = l
		}
		return strings.Join(out, ", "), nil
	}
	switch b.Kind {
	case "default":
		return "DEFAULT", nil
	case "range":
		f, err := list(b.From)
		if err != nil {
			return "", err
		}
		to, err := list(b.To)
		if err != nil {
			return "", err
		}
		return "FOR VALUES FROM (" + f + ") TO (" + to + ")", nil
	case "list":
		v, err := list(b.Values)
		if err != nil {
			return "", err
		}
		return "FOR VALUES IN (" + v + ")", nil
	case "hash":
		if b.Modulus < 1 || b.Modulus > 1024 || b.Remainder < 0 || b.Remainder >= b.Modulus {
			return "", actions.InvalidParams("hash bound: modulus from 1, remainder below the modulus.")
		}
		return "FOR VALUES WITH (MODULUS " + strconv.Itoa(b.Modulus) + ", REMAINDER " + strconv.Itoa(b.Remainder) + ")", nil
	}
	return "", actions.InvalidParams("bound kind: range, list, hash or default.")
}

func registerRoutines(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "pg.routines", Description: "A database's functions, procedures and aggregates (extensions' own excluded) with language, arguments, result, volatility and owner.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			tableIn
			Search string `json:"search"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.check(); err != nil {
			return nil, err
		}
		if len(in.Search) > 100 || len(in.Schema) > 63 {
			return nil, actions.InvalidParams("search / schema: too long.")
		}
		var out []map[string]any
		err := withOwner(ctx, env, in.Target, in.Database, func(s *session) error {
			var err error
			out, err = s.routines(ctx, in.Schema, in.Search)
			return err
		})
		return map[string]any{"routines": out}, err
	}})
	r.Register(actions.Action{Name: "pg.routine_definition", Description: "The CREATE statement of one routine, addressed by OID.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			tableIn
			OID int64 `json:"oid"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.check(); err != nil {
			return nil, err
		}
		var def string
		err := withOwner(ctx, env, in.Target, in.Database, func(s *session) error {
			var err error
			def, err = s.routineDefinition(ctx, in.OID)
			return err
		})
		return map[string]any{"definition": def}, err
	}})
	r.Register(actions.Action{Name: "pg.routine_drop", Mutating: true, Description: "Drop a function, procedure or aggregate addressed by OID (the statement is written by the server from that OID).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			tableIn
			OID     int64 `json:"oid"`
			Cascade bool  `json:"cascade"`
			Confirm bool  `json:"confirm"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.check(); err != nil {
			return nil, err
		}
		if !in.Confirm {
			return nil, actions.InvalidParams("confirm: dropping a routine must be confirmed.")
		}
		var stmt string
		err := withOwner(ctx, env, in.Target, in.Database, func(s *session) error {
			var err error
			stmt, err = s.routineDrop(ctx, in.OID, in.Cascade)
			return err
		})
		return map[string]any{"sql": stmt}, err
	}})
	r.Register(actions.Action{Name: "pg.sequences", Description: "A database's sequences with their settings, last value and the column that owns them.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in tableIn
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.check(); err != nil {
			return nil, err
		}
		var out []map[string]any
		err := withOwner(ctx, env, in.Target, in.Database, func(s *session) error {
			var err error
			out, err = s.sequences(ctx, in.Schema)
			return err
		})
		return map[string]any{"sequences": out}, err
	}})
	r.Register(actions.Action{Name: "pg.sequence_alter", Mutating: true, Description: "Create, alter, restart, set the value of, sync to its column, or drop a sequence. dry_run returns the SQL only.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in SeqIn
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.check(); err != nil {
			return nil, err
		}
		var out map[string]any
		err := withOwner(ctx, env, in.Target, in.Database, func(s *session) error {
			stmts, err := s.seqSQL(ctx, in)
			if err != nil {
				return err
			}
			out = map[string]any{"sql": strings.Join(stmts, ";\n")}
			if in.DryRun {
				return nil
			}
			for _, st := range stmts {
				if _, err := s.Exec(ctx, st); err != nil {
					return userError(err)
				}
			}
			return nil
		})
		return out, err
	}})
	r.Register(actions.Action{Name: "pg.partitions", Description: "A partitioned table's strategy, key and partitions with bounds, estimated rows and size.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
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
			out, err = s.partitions(ctx, t)
			return err
		})
		return out, err
	}})
}
