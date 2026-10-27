package dbmgr

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/mysqlwire"
)

// Structure changes, CSV export and import for the database browser. They
// run as the database's browser account like everything else there, so
// they cannot reach other databases. Structure changes are built from
// structured input (identifiers quoted, types from a fixed list, values
// quoted) rather than typed SQL; the SQL tab remains for anything else.

// ---- column specifications ------------------------------------------------

// ColumnSpec describes a column to add or change.
type ColumnSpec struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	Length        string   `json:"length"` // "255", "10,2" or "" (per type)
	Values        []string `json:"values"` // ENUM and SET
	Unsigned      bool     `json:"unsigned"`
	Nullable      bool     `json:"nullable"`
	Default       string   `json:"default_kind"` // none, null, value, current_timestamp
	DefaultValue  string   `json:"default"`
	AutoIncrement bool     `json:"auto_increment"`
	OnUpdateNow   bool     `json:"on_update_current_timestamp"`
	Comment       string   `json:"comment"`
}

// Column types and what their length means: "" none allowed, "n" optional
// size, "n!" required size, "p,s" optional precision and scale, "fsp"
// optional fractional seconds (0–6), "values" ENUM/SET values.
var columnTypes = map[string]string{
	"TINYINT": "", "SMALLINT": "", "MEDIUMINT": "", "INT": "", "BIGINT": "",
	"DECIMAL": "p,s", "FLOAT": "", "DOUBLE": "", "BIT": "n",
	"CHAR": "n", "VARCHAR": "n!", "BINARY": "n", "VARBINARY": "n!",
	"TINYTEXT": "", "TEXT": "", "MEDIUMTEXT": "", "LONGTEXT": "",
	"TINYBLOB": "", "BLOB": "", "MEDIUMBLOB": "", "LONGBLOB": "",
	"DATE": "", "DATETIME": "fsp", "TIMESTAMP": "fsp", "TIME": "fsp", "YEAR": "",
	"ENUM": "values", "SET": "values", "JSON": "",
}

var (
	numericTypes = map[string]bool{"TINYINT": true, "SMALLINT": true, "MEDIUMINT": true, "INT": true, "BIGINT": true, "DECIMAL": true, "FLOAT": true, "DOUBLE": true}
	integerTypes = map[string]bool{"TINYINT": true, "SMALLINT": true, "MEDIUMINT": true, "INT": true, "BIGINT": true}
	noDefault    = map[string]bool{"TINYTEXT": true, "TEXT": true, "MEDIUMTEXT": true, "LONGTEXT": true, "TINYBLOB": true, "BLOB": true, "MEDIUMBLOB": true, "LONGBLOB": true, "JSON": true}
)

var reN, rePS = regexp.MustCompile(`^[0-9]{1,5}$`), regexp.MustCompile(`^[0-9]{1,2}(,[0-9]{1,2})?$`)

func checkIdent(field, name string) error {
	if name == "" || len(name) > 64 || strings.ContainsRune(name, 0) || !utf8.ValidString(name) || strings.TrimRight(name, " ") != name {
		return actions.InvalidParams(field + ": 1–64 characters, not ending in a space")
	}
	return nil
}

// sql renders the column definition.
func (c ColumnSpec) sql() (string, error) {
	if err := checkIdent("column name", c.Name); err != nil {
		return "", err
	}
	t := strings.ToUpper(strings.TrimSpace(c.Type))
	kind, ok := columnTypes[t]
	if !ok {
		return "", actions.InvalidParams("column " + c.Name + ": unsupported type " + c.Type)
	}
	def := t
	l := strings.ReplaceAll(strings.TrimSpace(c.Length), " ", "")
	switch kind {
	case "":
		if l != "" {
			return "", actions.InvalidParams("column " + c.Name + ": " + t + " takes no length")
		}
	case "n", "n!":
		if l == "" && kind == "n!" {
			return "", actions.InvalidParams("column " + c.Name + ": " + t + " needs a length")
		}
		if l != "" {
			n, _ := strconv.Atoi(l)
			max := 65535
			switch t {
			case "CHAR", "BINARY":
				max = 255
			case "BIT":
				max = 64
			}
			if !reN.MatchString(l) || n < 1 && t != "CHAR" && t != "BINARY" || n > max {
				return "", actions.InvalidParams(fmt.Sprintf("column %s: length must be a number up to %d", c.Name, max))
			}
			def += "(" + l + ")"
		}
	case "p,s":
		if l != "" {
			if !rePS.MatchString(l) {
				return "", actions.InvalidParams("column " + c.Name + ": precision and scale like 10,2")
			}
			parts := strings.Split(l, ",")
			p, _ := strconv.Atoi(parts[0])
			s := 0
			if len(parts) == 2 {
				s, _ = strconv.Atoi(parts[1])
			}
			if p < 1 || p > 65 || s > 30 || s > p {
				return "", actions.InvalidParams("column " + c.Name + ": precision 1–65, scale 0–30 and not above the precision")
			}
			def += "(" + l + ")"
		}
	case "fsp":
		if l != "" {
			if len(l) != 1 || l[0] < '0' || l[0] > '6' {
				return "", actions.InvalidParams("column " + c.Name + ": fractional seconds 0–6")
			}
			def += "(" + l + ")"
		}
	case "values":
		if len(c.Values) == 0 || len(c.Values) > 1000 {
			return "", actions.InvalidParams("column " + c.Name + ": " + t + " needs 1–1000 values")
		}
		q := make([]string, len(c.Values))
		for i, v := range c.Values {
			if !utf8.ValidString(v) || len(v) > 255 {
				return "", actions.InvalidParams("column " + c.Name + ": values up to 255 characters")
			}
			q[i] = Quote(v)
		}
		def += "(" + strings.Join(q, ",") + ")"
	}
	if c.Unsigned {
		if !numericTypes[t] {
			return "", actions.InvalidParams("column " + c.Name + ": only numbers can be unsigned")
		}
		def += " UNSIGNED"
	}
	if c.Nullable {
		def += " NULL"
	} else {
		def += " NOT NULL"
	}
	switch c.Default {
	case "", "none":
	case "null":
		if !c.Nullable {
			return "", actions.InvalidParams("column " + c.Name + ": a NOT NULL column cannot default to NULL")
		}
		def += " DEFAULT NULL"
	case "value":
		if noDefault[t] {
			return "", actions.InvalidParams("column " + c.Name + ": " + t + " columns cannot have a default value here")
		}
		if !utf8.ValidString(c.DefaultValue) || len(c.DefaultValue) > 1024 {
			return "", actions.InvalidParams("column " + c.Name + ": default up to 1024 characters")
		}
		def += " DEFAULT " + Quote(c.DefaultValue)
	case "current_timestamp":
		if t != "TIMESTAMP" && t != "DATETIME" {
			return "", actions.InvalidParams("column " + c.Name + ": CURRENT_TIMESTAMP is for DATETIME and TIMESTAMP")
		}
		def += " DEFAULT CURRENT_TIMESTAMP"
		if l != "" {
			def += "(" + l + ")"
		}
	default:
		return "", actions.InvalidParams("column " + c.Name + ": unknown default")
	}
	if c.OnUpdateNow {
		if t != "TIMESTAMP" && t != "DATETIME" {
			return "", actions.InvalidParams("column " + c.Name + ": ON UPDATE CURRENT_TIMESTAMP is for DATETIME and TIMESTAMP")
		}
		def += " ON UPDATE CURRENT_TIMESTAMP"
		if l != "" {
			def += "(" + l + ")"
		}
	}
	if c.AutoIncrement {
		if !integerTypes[t] {
			return "", actions.InvalidParams("column " + c.Name + ": only integer columns can auto-increment")
		}
		def += " AUTO_INCREMENT"
	}
	if c.Comment != "" {
		if !utf8.ValidString(c.Comment) || utf8.RuneCountInString(c.Comment) > 1024 {
			return "", actions.InvalidParams("column " + c.Name + ": comment up to 1024 characters")
		}
		def += " COMMENT " + Quote(c.Comment)
	}
	return Ident(c.Name) + " " + def, nil
}

// ParseColumnType splits information_schema's COLUMN_TYPE for the edit form:
// "int(10) unsigned" → INT, "", unsigned; "enum('a','b')" → ENUM, values.
func ParseColumnType(ct string) (typ, length string, values []string, unsigned bool) {
	ct = strings.TrimSpace(ct)
	lower := strings.ToLower(ct)
	unsigned = strings.Contains(lower, " unsigned")
	base, args := ct, ""
	if i := strings.IndexByte(ct, '('); i > 0 {
		if j := strings.LastIndexByte(ct, ')'); j > i {
			base, args = ct[:i], ct[i+1:j]
		}
	} else if i := strings.IndexByte(ct, ' '); i > 0 {
		base = ct[:i]
	}
	typ = strings.ToUpper(base)
	switch typ {
	case "ENUM", "SET":
		// 'a','b''c' → a, b'c
		var cur strings.Builder
		in := false
		for i := 0; i < len(args); i++ {
			ch := args[i]
			switch {
			case ch == '\'' && in && i+1 < len(args) && args[i+1] == '\'':
				cur.WriteByte('\'')
				i++
			case ch == '\\' && in && i+1 < len(args):
				cur.WriteByte(args[i+1])
				i++
			case ch == '\'':
				if in {
					values = append(values, cur.String())
					cur.Reset()
				}
				in = !in
			case in:
				cur.WriteByte(ch)
			}
		}
	default:
		// Display widths of integers are not kept (deprecated in MySQL 8).
		if !integerTypes[typ] {
			length = args
		}
	}
	return
}

// ---- structure changes -----------------------------------------------------

type alterReq struct {
	Op        string       `json:"op"`
	NewName   string       `json:"new_name"`
	Column    *ColumnSpec  `json:"column"`
	Columns   []ColumnSpec `json:"columns"` // create_table
	Name      string       `json:"name"`    // column, index or foreign key to change or drop
	After     string       `json:"after"`   // add_column / modify_column: "" (end), "^" (first) or a column
	Kind      string       `json:"kind"`    // add_index: index, unique, fulltext, primary
	IndexCols []string     `json:"index_columns"`
	RefTable  string       `json:"ref_table"`
	RefCols   []string     `json:"ref_columns"`
	OnDelete  string       `json:"on_delete"`
	OnUpdate  string       `json:"on_update"`
	Primary   []string     `json:"primary"` // create_table
	Engine    string       `json:"engine"`
	DryRun    bool         `json:"dry_run"`
}

var fkActions = map[string]string{"": "RESTRICT", "restrict": "RESTRICT", "cascade": "CASCADE", "set null": "SET NULL", "no action": "NO ACTION"}

var tableEngines = map[string]bool{"": true, "InnoDB": true, "Aria": true, "MyISAM": true, "MEMORY": true}

func identList(field string, names []string) (string, error) {
	if len(names) == 0 || len(names) > 16 {
		return "", actions.InvalidParams(field + ": 1–16 columns")
	}
	q := make([]string, len(names))
	for i, n := range names {
		if err := checkIdent(field, n); err != nil {
			return "", err
		}
		q[i] = Ident(n)
	}
	return strings.Join(q, ", "), nil
}

func position(after string) (string, error) {
	switch after {
	case "":
		return "", nil
	case "^":
		return " FIRST", nil
	}
	if err := checkIdent("after", after); err != nil {
		return "", err
	}
	return " AFTER " + Ident(after), nil
}

// alterSQL builds the statement for a structure change on table.
func alterSQL(table string, in alterReq) (string, error) {
	alter := "ALTER TABLE " + Ident(table) + " "
	switch in.Op {
	case "create_table":
		if len(in.Columns) == 0 || len(in.Columns) > 200 {
			return "", actions.InvalidParams("a new table needs 1–200 columns")
		}
		var defs []string
		for _, c := range in.Columns {
			d, err := c.sql()
			if err != nil {
				return "", err
			}
			defs = append(defs, d)
		}
		if len(in.Primary) > 0 {
			pk, err := identList("primary key", in.Primary)
			if err != nil {
				return "", err
			}
			defs = append(defs, "PRIMARY KEY ("+pk+")")
		}
		if !tableEngines[in.Engine] {
			return "", actions.InvalidParams("engine: InnoDB, Aria, MyISAM or MEMORY")
		}
		s := "CREATE TABLE " + Ident(table) + " (\n  " + strings.Join(defs, ",\n  ") + "\n)"
		if in.Engine != "" {
			s += " ENGINE=" + in.Engine
		}
		return s, nil
	case "rename_table":
		if err := checkIdent("new name", in.NewName); err != nil {
			return "", err
		}
		return "RENAME TABLE " + Ident(table) + " TO " + Ident(in.NewName), nil
	case "drop_table":
		return "DROP TABLE " + Ident(table), nil
	case "drop_view":
		return "DROP VIEW " + Ident(table), nil
	case "truncate_table":
		return "TRUNCATE TABLE " + Ident(table), nil
	case "optimize_table":
		return "OPTIMIZE TABLE " + Ident(table), nil
	case "analyze_table":
		return "ANALYZE TABLE " + Ident(table), nil
	case "check_table":
		return "CHECK TABLE " + Ident(table), nil
	case "repair_table":
		return "REPAIR TABLE " + Ident(table), nil
	case "add_column", "modify_column":
		if in.Column == nil {
			return "", actions.InvalidParams("column is required")
		}
		d, err := in.Column.sql()
		if err != nil {
			return "", err
		}
		pos, err := position(in.After)
		if err != nil {
			return "", err
		}
		if in.Op == "add_column" {
			return alter + "ADD COLUMN " + d + pos, nil
		}
		if err := checkIdent("column", in.Name); err != nil {
			return "", err
		}
		return alter + "CHANGE COLUMN " + Ident(in.Name) + " " + d + pos, nil
	case "drop_column":
		if err := checkIdent("column", in.Name); err != nil {
			return "", err
		}
		return alter + "DROP COLUMN " + Ident(in.Name), nil
	case "add_index":
		cols, err := identList("index columns", in.IndexCols)
		if err != nil {
			return "", err
		}
		name := ""
		if in.Name != "" {
			if err := checkIdent("index name", in.Name); err != nil {
				return "", err
			}
			name = Ident(in.Name) + " "
		}
		switch in.Kind {
		case "primary":
			return alter + "ADD PRIMARY KEY (" + cols + ")", nil
		case "index", "":
			return alter + "ADD INDEX " + name + "(" + cols + ")", nil
		case "unique":
			return alter + "ADD UNIQUE INDEX " + name + "(" + cols + ")", nil
		case "fulltext":
			return alter + "ADD FULLTEXT INDEX " + name + "(" + cols + ")", nil
		}
		return "", actions.InvalidParams("kind: index, unique, fulltext or primary")
	case "drop_index":
		if strings.EqualFold(in.Name, "PRIMARY") {
			return alter + "DROP PRIMARY KEY", nil
		}
		if err := checkIdent("index", in.Name); err != nil {
			return "", err
		}
		return alter + "DROP INDEX " + Ident(in.Name), nil
	case "add_foreign_key":
		cols, err := identList("columns", in.IndexCols)
		if err != nil {
			return "", err
		}
		refs, err := identList("referenced columns", in.RefCols)
		if err != nil {
			return "", err
		}
		if len(in.IndexCols) != len(in.RefCols) {
			return "", actions.InvalidParams("as many referenced columns as columns")
		}
		if err := checkIdent("referenced table", in.RefTable); err != nil {
			return "", err
		}
		del, ok1 := fkActions[strings.ToLower(in.OnDelete)]
		upd, ok2 := fkActions[strings.ToLower(in.OnUpdate)]
		if !ok1 || !ok2 {
			return "", actions.InvalidParams("on delete / on update: restrict, cascade, set null or no action")
		}
		name := ""
		if in.Name != "" {
			if err := checkIdent("constraint name", in.Name); err != nil {
				return "", err
			}
			name = "CONSTRAINT " + Ident(in.Name) + " "
		}
		return alter + "ADD " + name + "FOREIGN KEY (" + cols + ") REFERENCES " + Ident(in.RefTable) + " (" + refs + ") ON DELETE " + del + " ON UPDATE " + upd, nil
	case "drop_foreign_key":
		if err := checkIdent("foreign key", in.Name); err != nil {
			return "", err
		}
		return alter + "DROP FOREIGN KEY " + Ident(in.Name), nil
	}
	return "", actions.InvalidParams("unknown operation " + in.Op)
}

// ---- CSV ---------------------------------------------------------------------

const (
	csvExportRows  = 200000
	csvExportBytes = 64 << 20
	csvImportBytes = 16 << 20
	csvImportRows  = 100000
	csvBatch       = 500
)

func csvCell(col mysqlwire.Column, v *[]byte) string {
	if v == nil {
		return `\N`
	}
	if col.Binary() || !utf8.Valid(*v) {
		return "0x" + fmt.Sprintf("%x", *v)
	}
	return string(*v)
}

// rowsWhere builds the WHERE clause shared by browsing and export.
func rowsWhere(t tableInfo, f *filterReq, search string) (string, error) {
	var conds []string
	if f != nil && f.Column != "" {
		op, ok := filterOps[strings.ToLower(f.Op)]
		if !ok || !t.has(f.Column) {
			return "", actions.InvalidParams("filter column or operator is invalid")
		}
		c := Ident(f.Column) + " " + op
		if !strings.HasPrefix(op, "IS") {
			c += " " + Quote(f.Value)
		}
		conds = append(conds, c)
	}
	if s := strings.TrimSpace(search); s != "" {
		if len(s) > 1000 || !utf8.ValidString(s) {
			return "", actions.InvalidParams("search up to 1000 characters")
		}
		// Any column containing the text (LIKE wildcards in it are literal).
		pat := Quote("%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s) + "%")
		var ors []string
		for _, c := range t.Columns {
			ors = append(ors, "CAST("+Ident(c)+" AS CHAR) LIKE "+pat)
		}
		conds = append(conds, "("+strings.Join(ors, " OR ")+")")
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), nil
}

// ParseCSV reads CSV text (delimiter , ; or tab) into records.
func ParseCSV(text string, delim rune) ([][]string, error) {
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, "\ufeff")))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	var out [][]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(rec) == 1 && rec[0] == "" {
			continue // blank line
		}
		out = append(out, rec)
		if len(out) > csvImportRows+1 {
			return nil, fmt.Errorf("more than %d rows", csvImportRows)
		}
	}
	return out, nil
}

// ---- actions -----------------------------------------------------------------

func registerBrowseExt(r *actions.Registry, c Client) {
	r.Register(actions.Action{Name: "db.browse_alter", Mutating: true, Description: "Database browser: change a table's structure from structured input (create, rename, truncate, drop, optimize/analyze/check/repair; add, change or drop columns, indexes and foreign keys). dry_run returns the SQL only.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			browseReq
			alterReq
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := checkTable(in.Table); err != nil {
			return nil, host.Invalid(err)
		}
		sql, err := alterSQL(in.Table, in.alterReq)
		if err != nil {
			return nil, err
		}
		if in.DryRun {
			return map[string]any{"sql": sql}, nil
		}
		var out map[string]any
		err = c.withBrowser(ctx, in.Database, func(conn *mysqlwire.Conn) error {
			rs, err := conn.Query(sql, mysqlwire.Limits{MaxRows: 100, MaxBytes: 1 << 20})
			if err != nil {
				return sqlFailed(err)
			}
			out = map[string]any{"sql": sql}
			// OPTIMIZE/ANALYZE/CHECK/REPAIR report per table.
			if len(rs) > 0 && len(rs[0].Columns) > 0 {
				out["result"] = resultOut(rs[0], 4096)
			}
			return nil
		})
		return out, err
	}})

	r.Register(actions.Action{Name: "db.browse_export_csv", Mutating: true, Description: "Database browser: a table (optionally filtered or searched) as CSV; NULL is written as \\N and binary values as 0x… hex.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in browseReq
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := checkTable(in.Table); err != nil {
			return nil, host.Invalid(err)
		}
		var out map[string]any
		err := c.withBrowser(ctx, in.Database, func(conn *mysqlwire.Conn) error {
			t, err := loadTable(conn, in.Table)
			if err != nil {
				return notFoundOr(err)
			}
			where, err := rowsWhere(t, in.Filter, in.Search)
			if err != nil {
				return err
			}
			rs, err := conn.Query("SELECT * FROM "+Ident(in.Table)+where, mysqlwire.Limits{MaxRows: csvExportRows, MaxBytes: csvExportBytes})
			if err != nil {
				return sqlFailed(err)
			}
			var buf bytes.Buffer
			w := csv.NewWriter(&buf)
			head := make([]string, len(rs[0].Columns))
			for i, col := range rs[0].Columns {
				head[i] = col.Name
			}
			w.Write(head)
			for _, row := range rs[0].Rows {
				rec := make([]string, len(row))
				for i, v := range row {
					rec[i] = csvCell(rs[0].Columns[i], v)
				}
				w.Write(rec)
			}
			w.Flush()
			out = map[string]any{"csv": buf.String(), "rows": len(rs[0].Rows), "truncated": rs[0].Truncated}
			return nil
		})
		return out, err
	}})

	r.Register(actions.Action{Name: "db.browse_import_csv", Mutating: true, Description: "Database browser: insert CSV rows into a table in one transaction (all or nothing). columns maps each CSV column to a table column (\"\" skips it); \\N is NULL.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			browseReq
			CSV       string   `json:"csv"`
			Delimiter string   `json:"delimiter"`
			Header    bool     `json:"header"`
			Columns   []string `json:"columns"`
			Mode      string   `json:"mode"` // insert, ignore (skip duplicates), replace
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := checkTable(in.Table); err != nil {
			return nil, host.Invalid(err)
		}
		if in.CSV == "" || len(in.CSV) > csvImportBytes || !utf8.ValidString(in.CSV) {
			return nil, actions.InvalidParams(fmt.Sprintf("csv must be 1 byte to %d MiB of UTF-8 text", csvImportBytes>>20))
		}
		delim := ','
		switch in.Delimiter {
		case "", ",":
		case ";":
			delim = ';'
		case "\t", "tab":
			delim = '\t'
		default:
			return nil, actions.InvalidParams("delimiter: comma, semicolon or tab")
		}
		verb := map[string]string{"": "INSERT", "insert": "INSERT", "ignore": "INSERT IGNORE", "replace": "REPLACE"}[in.Mode]
		if verb == "" {
			return nil, actions.InvalidParams("mode: insert, ignore or replace")
		}
		recs, err := ParseCSV(in.CSV, delim)
		if err != nil {
			return nil, actions.InvalidParams("csv: " + err.Error())
		}
		if in.Header && len(recs) > 0 {
			recs = recs[1:]
		}
		if len(recs) == 0 {
			return nil, actions.InvalidParams("the CSV has no rows")
		}
		var out map[string]any
		err = c.withBrowser(ctx, in.Database, func(conn *mysqlwire.Conn) error {
			t, err := loadTable(conn, in.Table)
			if err != nil {
				return notFoundOr(err)
			}
			if len(in.Columns) == 0 || len(in.Columns) > len(t.Columns)+50 {
				return actions.InvalidParams("columns: one entry per CSV column")
			}
			var idx []int
			var names []string
			seen := map[string]bool{}
			for i, col := range in.Columns {
				if col == "" {
					continue
				}
				if !t.has(col) || seen[col] {
					return actions.InvalidParams("columns: " + col + " is not a column of the table or is used twice")
				}
				seen[col] = true
				idx = append(idx, i)
				names = append(names, Ident(col))
			}
			if len(idx) == 0 {
				return actions.InvalidParams("columns: map at least one CSV column")
			}
			if _, err := conn.Query("START TRANSACTION", mysqlwire.Limits{}); err != nil {
				return sqlFailed(err)
			}
			var affected uint64
			head := verb + " INTO " + Ident(in.Table) + " (" + strings.Join(names, ", ") + ") VALUES "
			for start := 0; start < len(recs); start += csvBatch {
				end := min(start+csvBatch, len(recs))
				var tuples []string
				for n, rec := range recs[start:end] {
					if len(rec) != len(in.Columns) {
						conn.Query("ROLLBACK", mysqlwire.Limits{})
						line := start + n + 1
						if in.Header {
							line++
						}
						return actions.InvalidParams(fmt.Sprintf("row %d has %d columns, expected %d; nothing was imported", line, len(rec), len(in.Columns)))
					}
					vals := make([]string, len(idx))
					for k, i := range idx {
						if rec[i] == `\N` {
							vals[k] = "NULL"
						} else {
							vals[k] = Quote(rec[i])
						}
					}
					tuples = append(tuples, "("+strings.Join(vals, ", ")+")")
				}
				rs, err := conn.Query(head+strings.Join(tuples, ", "), mysqlwire.Limits{})
				if err != nil {
					conn.Query("ROLLBACK", mysqlwire.Limits{})
					var me *mysqlwire.Error
					msg := err.Error()
					if errors.As(err, &me) {
						msg = me.Error()
					}
					return actions.Failed(fmt.Sprintf("Rows %d–%d were refused; nothing was imported.", start+1, end), msg)
				}
				affected += rs[0].AffectedRows
			}
			if _, err := conn.Query("COMMIT", mysqlwire.Limits{}); err != nil {
				return sqlFailed(err)
			}
			out = map[string]any{"rows": len(recs), "affected_rows": affected}
			return nil
		})
		return out, err
	}})
}
