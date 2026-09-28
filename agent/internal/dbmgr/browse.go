package dbmgr

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/mysqlwire"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// The database browser never runs SQL as root. Each database has its own
// browser account (whacp_b_<hash>@localhost) with privileges on that
// database only. The account is locked between uses; for each request it
// gets a fresh random password, is unlocked, used over the local socket
// and locked again with another random password. Anything typed in the SQL
// console therefore cannot reach other databases or server settings.
// (The account is kept rather than dropped so views and routines created
// from the browser keep a valid definer.)

// BrowserPrefix marks browser accounts; users cannot create such names.
const BrowserPrefix = "whacp_b_"

var browseMu sync.Mutex

// BrowserAccount returns the browser account name for a database.
func BrowserAccount(db string) string {
	sum := sha256.Sum256([]byte(db))
	return BrowserPrefix + hex.EncodeToString(sum[:8])
}

func randomPassword() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Browse limits.
const (
	maxQueryBytes   = 1 << 20
	maxResultRows   = 1000
	maxResultBytes  = 16 << 20
	gridCellBytes   = 512
	queryCellBytes  = 64 << 10
	maxRowsPage     = 500
	statementSecs   = 30
	browseTimeout   = 60 * time.Second
	maxDeleteAtOnce = 500
)

// withBrowser runs fn connected as the database's browser account.
func (c Client) withBrowser(ctx context.Context, db string, fn func(*mysqlwire.Conn) error) error {
	if err := validate.DBIdentifier("database", db); err != nil {
		return host.Invalid(err)
	}
	browseMu.Lock()
	defer browseMu.Unlock()
	ok, err := c.databaseExists(ctx, db)
	if err != nil {
		return actions.Failed("The database server is not reachable.", err.Error())
	}
	if !ok {
		return actions.NotFound("database " + db + " does not exist")
	}
	sock, err := c.Query(ctx, "SELECT @@socket;")
	if err != nil || len(sock) != 1 || !strings.HasPrefix(sock[0][0], "/") {
		return actions.Failed("Could not find the database server's socket.", fmt.Sprint(err))
	}
	acct := account(BrowserAccount(db), "localhost")
	pw := randomPassword()
	setup := fmt.Sprintf("CREATE USER IF NOT EXISTS %s IDENTIFIED BY %s WITH MAX_USER_CONNECTIONS 2;\nALTER USER %s IDENTIFIED BY %s ACCOUNT UNLOCK;\nGRANT ALL PRIVILEGES ON %s.* TO %s;\n",
		acct, Quote(pw), acct, Quote(pw), Ident(db), acct)
	if _, err := c.Query(ctx, setup); err != nil {
		return actions.Failed("Could not prepare the database browser account.", err.Error())
	}
	defer func() {
		// Lock again with a password nobody knows, whatever happened.
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		c.Query(lctx, fmt.Sprintf("ALTER USER %s IDENTIFIED BY %s ACCOUNT LOCK;", acct, Quote(randomPassword())))
	}()
	ctx, cancel := context.WithTimeout(ctx, browseTimeout)
	defer cancel()
	conn, err := mysqlwire.Dial(ctx, mysqlwire.Options{Socket: sock[0][0], User: BrowserAccount(db), Password: pw, Database: db, Timeout: 10 * time.Second})
	if err != nil {
		return actions.Failed("Could not connect to the database.", err.Error())
	}
	defer conn.Close()
	// Bound each statement (MariaDB and MySQL spell it differently).
	conn.Query(fmt.Sprintf("SET SESSION max_statement_time = %d", statementSecs), mysqlwire.Limits{})
	conn.Query(fmt.Sprintf("SET SESSION max_execution_time = %d", statementSecs*1000), mysqlwire.Limits{})
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			// Stop the server-side work too, not just our read.
			kctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c.Query(kctx, fmt.Sprintf("KILL CONNECTION %d;", conn.ThreadID))
		case <-done:
		}
	}()
	return fn(conn)
}

// ---- cells ---------------------------------------------------------------

// Cell values in results: null, a string, or for binary data or text cut
// short an object {"hex"|"text", "size", "truncated"}.
func cell(col mysqlwire.Column, v *[]byte, limit int) any {
	if v == nil {
		return nil
	}
	b := *v
	if col.Binary() || !utf8.Valid(b) {
		n := min(len(b), limit/2)
		return map[string]any{"hex": hex.EncodeToString(b[:n]), "size": len(b), "truncated": n < len(b)}
	}
	if len(b) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(b[cut]) {
			cut--
		}
		return map[string]any{"text": string(b[:cut]), "size": len(b), "truncated": true}
	}
	return string(b)
}

type colOut struct {
	Name    string `json:"name"`
	Numeric bool   `json:"numeric,omitempty"`
	Binary  bool   `json:"binary,omitempty"`
}

func resultOut(r mysqlwire.Result, limit int) map[string]any {
	cols := make([]colOut, len(r.Columns))
	for i, c := range r.Columns {
		cols[i] = colOut{Name: c.Name, Numeric: c.Numeric(), Binary: c.Binary()}
	}
	rows := make([][]any, len(r.Rows))
	for i, row := range r.Rows {
		out := make([]any, len(row))
		for j, v := range row {
			out[j] = cell(r.Columns[j], v, limit)
		}
		rows[i] = out
	}
	m := map[string]any{"columns": cols, "rows": rows, "truncated": r.Truncated}
	if len(r.Columns) == 0 {
		m = map[string]any{"affected_rows": r.AffectedRows, "insert_id": r.InsertID, "info": r.Info}
	}
	m["warnings"] = r.Warnings
	return m
}

// literal turns an input value into SQL: null, a string, {"hex": …} or
// {"default": true}.
func literal(v json.RawMessage) (string, error) {
	s := strings.TrimSpace(string(v))
	if s == "" || s == "null" {
		return "NULL", nil
	}
	var str string
	if json.Unmarshal(v, &str) == nil {
		return Quote(str), nil
	}
	var obj struct {
		Hex     *string `json:"hex"`
		Default bool    `json:"default"`
	}
	if err := json.Unmarshal(v, &obj); err != nil {
		return "", errors.New("values must be null, a string or {\"hex\": …}")
	}
	switch {
	case obj.Default:
		return "DEFAULT", nil
	case obj.Hex != nil:
		if _, err := hex.DecodeString(*obj.Hex); err != nil {
			return "", errors.New("hex value is invalid")
		}
		if *obj.Hex == "" {
			return "''", nil
		}
		return "X" + Quote(*obj.Hex), nil
	}
	return "", errors.New("values must be null, a string or {\"hex\": …}")
}

// ---- table metadata ------------------------------------------------------

type tableInfo struct {
	Columns []string
	Key     []string // primary key, else a unique key without NULLs
}

func one(conn *mysqlwire.Conn, sql string) (mysqlwire.Result, error) {
	rs, err := conn.Query(sql, mysqlwire.Limits{MaxRows: 100000, MaxBytes: maxResultBytes})
	if err != nil {
		return mysqlwire.Result{}, err
	}
	if len(rs) == 0 {
		return mysqlwire.Result{}, errors.New("no result")
	}
	return rs[0], nil
}

func str(v *[]byte) string {
	if v == nil {
		return ""
	}
	return string(*v)
}

func checkTable(name string) error {
	if name == "" || len(name) > 64 || strings.ContainsAny(name, "\x00") || !utf8.ValidString(name) {
		return &validate.Error{Field: "table", Message: "is not a valid table name"}
	}
	return nil
}

func loadTable(conn *mysqlwire.Conn, table string) (tableInfo, error) {
	var t tableInfo
	r, err := one(conn, "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = "+Quote(table)+" ORDER BY ORDINAL_POSITION")
	if err != nil {
		return t, err
	}
	for _, row := range r.Rows {
		t.Columns = append(t.Columns, str(row[0]))
	}
	if len(t.Columns) == 0 {
		return t, actions.NotFound("table " + table + " does not exist")
	}
	// Primary key first, then the first unique key whose columns are all
	// NOT NULL.
	r, err = one(conn, `SELECT s.INDEX_NAME, s.COLUMN_NAME, c.IS_NULLABLE FROM information_schema.STATISTICS s
JOIN information_schema.COLUMNS c ON c.TABLE_SCHEMA = s.TABLE_SCHEMA AND c.TABLE_NAME = s.TABLE_NAME AND c.COLUMN_NAME = s.COLUMN_NAME
WHERE s.TABLE_SCHEMA = DATABASE() AND s.TABLE_NAME = `+Quote(table)+` AND s.NON_UNIQUE = 0
ORDER BY s.INDEX_NAME <> 'PRIMARY', s.INDEX_NAME, s.SEQ_IN_INDEX`)
	if err != nil {
		return t, err
	}
	keys := map[string][]string{}
	nullable := map[string]bool{}
	var order []string
	for _, row := range r.Rows {
		idx := str(row[0])
		if _, ok := keys[idx]; !ok {
			order = append(order, idx)
		}
		keys[idx] = append(keys[idx], str(row[1]))
		if str(row[2]) == "YES" {
			nullable[idx] = true
		}
	}
	for _, idx := range order {
		if !nullable[idx] {
			t.Key = keys[idx]
			break
		}
	}
	return t, nil
}

func (t tableInfo) has(col string) bool {
	for _, c := range t.Columns {
		if c == col {
			return true
		}
	}
	return false
}

// where builds "WHERE k1 = v1 AND …" for one row's key.
func (t tableInfo) where(key map[string]json.RawMessage) (string, error) {
	if len(t.Key) == 0 {
		return "", actions.InvalidParams("this table has no primary or unique key, so single rows cannot be edited here; use the SQL tab")
	}
	var parts []string
	for _, k := range t.Key {
		v, ok := key[k]
		if !ok {
			return "", actions.InvalidParams("key column " + k + " is missing")
		}
		lit, err := literal(v)
		if err != nil || lit == "NULL" || lit == "DEFAULT" {
			return "", actions.InvalidParams("key value for " + k + " is invalid")
		}
		parts = append(parts, Ident(k)+" = "+lit)
	}
	if len(key) != len(t.Key) {
		return "", actions.InvalidParams("key must contain exactly the key columns")
	}
	return " WHERE " + strings.Join(parts, " AND "), nil
}

// ---- actions -------------------------------------------------------------

type browseReq struct {
	Database string                       `json:"database"`
	Table    string                       `json:"table"`
	Offset   int                          `json:"offset"`
	Limit    int                          `json:"limit"`
	Order    string                       `json:"order"`
	Desc     bool                         `json:"desc"`
	Filter   *filterReq                   `json:"filter"`
	SQL      string                       `json:"sql"`
	Key      map[string]json.RawMessage   `json:"key"`
	Keys     []map[string]json.RawMessage `json:"keys"`
	Values   map[string]json.RawMessage   `json:"values"`
	Insert   bool                         `json:"insert"`
	Search   string                       `json:"search"`
}

type filterReq struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	Value  string `json:"value"`
}

var filterOps = map[string]string{"=": "=", "!=": "<>", "<": "<", ">": ">", "<=": "<=", ">=": ">=", "like": "LIKE", "not like": "NOT LIKE", "null": "IS NULL", "not null": "IS NOT NULL"}

func sqlFailed(err error) error {
	var me *mysqlwire.Error
	if errors.As(err, &me) {
		return actions.Failed("The database returned an error.", me.Error())
	}
	return actions.Failed("The query did not complete.", err.Error())
}

func registerBrowse(r *actions.Registry, c Client) {
	decode := func(p json.RawMessage, needTable bool) (browseReq, error) {
		var in browseReq
		if err := host.Decode(p, &in); err != nil {
			return in, err
		}
		if needTable {
			if err := checkTable(in.Table); err != nil {
				return in, host.Invalid(err)
			}
		}
		return in, nil
	}

	r.Register(actions.Action{Name: "db.browse_tables", Mutating: true, Description: "Database browser: tables and views of a database with sizes.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		in, err := decode(p, false)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		err = c.withBrowser(ctx, in.Database, func(conn *mysqlwire.Conn) error {
			r, err := one(conn, "SELECT TABLE_NAME, TABLE_TYPE, ENGINE, TABLE_ROWS, COALESCE(DATA_LENGTH,0) + COALESCE(INDEX_LENGTH,0), TABLE_COLLATION, TABLE_COMMENT FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME")
			if err != nil {
				return sqlFailed(err)
			}
			tables := []map[string]any{}
			for _, row := range r.Rows {
				rows, _ := strconv.ParseInt(str(row[3]), 10, 64)
				size, _ := strconv.ParseInt(str(row[4]), 10, 64)
				tables = append(tables, map[string]any{"name": str(row[0]), "view": str(row[1]) == "VIEW", "engine": str(row[2]), "rows": rows, "bytes": size, "collation": str(row[5]), "comment": str(row[6])})
			}
			v, _ := one(conn, "SELECT VERSION(), @@character_set_database, @@collation_database")
			out = map[string]any{"database": in.Database, "tables": tables, "server": str(v.Rows[0][0]), "charset": str(v.Rows[0][1]), "collation": str(v.Rows[0][2])}
			return nil
		})
		return out, err
	}})

	r.Register(actions.Action{Name: "db.browse_structure", Mutating: true, Description: "Database browser: columns, indexes and CREATE statement of a table.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		in, err := decode(p, true)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		err = c.withBrowser(ctx, in.Database, func(conn *mysqlwire.Conn) error {
			t, err := loadTable(conn, in.Table)
			if err != nil {
				return notFoundOr(err)
			}
			cr, err := one(conn, "SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY, COLUMN_DEFAULT, EXTRA, COLUMN_COMMENT, COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = "+Quote(in.Table)+" ORDER BY ORDINAL_POSITION")
			if err != nil {
				return sqlFailed(err)
			}
			cols := []map[string]any{}
			for _, row := range cr.Rows {
				var def any
				if row[4] != nil {
					def = str(row[4])
				}
				base, length, values, unsigned := ParseColumnType(str(row[1]))
				if values == nil {
					values = []string{}
				}
				cols = append(cols, map[string]any{"name": str(row[0]), "type": str(row[1]), "nullable": str(row[2]) == "YES", "key": str(row[3]), "default": def, "extra": str(row[5]), "comment": str(row[6]), "collation": str(row[7]),
					"base_type": base, "length": length, "values": values, "unsigned": unsigned})
			}
			ir, err := one(conn, "SELECT INDEX_NAME, NON_UNIQUE, GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX SEPARATOR ', '), INDEX_TYPE FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = "+Quote(in.Table)+" GROUP BY INDEX_NAME, NON_UNIQUE, INDEX_TYPE ORDER BY INDEX_NAME <> 'PRIMARY', INDEX_NAME")
			if err != nil {
				return sqlFailed(err)
			}
			idx := []map[string]any{}
			for _, row := range ir.Rows {
				idx = append(idx, map[string]any{"name": str(row[0]), "unique": str(row[1]) == "0", "columns": str(row[2]), "type": str(row[3])})
			}
			create := ""
			if sr, err := one(conn, "SHOW CREATE TABLE "+Ident(in.Table)); err == nil && len(sr.Rows) == 1 && len(sr.Rows[0]) >= 2 {
				create = str(sr.Rows[0][1])
			}
			fks := []map[string]any{}
			if fr, err := one(conn, `SELECT k.CONSTRAINT_NAME, GROUP_CONCAT(k.COLUMN_NAME ORDER BY k.ORDINAL_POSITION SEPARATOR ', '), k.REFERENCED_TABLE_NAME,
GROUP_CONCAT(k.REFERENCED_COLUMN_NAME ORDER BY k.ORDINAL_POSITION SEPARATOR ', '), r.DELETE_RULE, r.UPDATE_RULE
FROM information_schema.KEY_COLUMN_USAGE k JOIN information_schema.REFERENTIAL_CONSTRAINTS r ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME AND r.TABLE_NAME = k.TABLE_NAME
WHERE k.TABLE_SCHEMA = DATABASE() AND k.TABLE_NAME = `+Quote(in.Table)+` AND k.REFERENCED_TABLE_NAME IS NOT NULL
GROUP BY k.CONSTRAINT_NAME, k.REFERENCED_TABLE_NAME, r.DELETE_RULE, r.UPDATE_RULE ORDER BY k.CONSTRAINT_NAME`); err == nil {
				for _, row := range fr.Rows {
					fks = append(fks, map[string]any{"name": str(row[0]), "columns": str(row[1]), "ref_table": str(row[2]), "ref_columns": str(row[3]), "on_delete": str(row[4]), "on_update": str(row[5])})
				}
			}
			status := map[string]any{}
			if sr, err := one(conn, "SELECT TABLE_TYPE, ENGINE, ROW_FORMAT, TABLE_ROWS, AVG_ROW_LENGTH, DATA_LENGTH, INDEX_LENGTH, DATA_FREE, AUTO_INCREMENT, CREATE_TIME, UPDATE_TIME, TABLE_COLLATION, TABLE_COMMENT FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = "+Quote(in.Table)); err == nil && len(sr.Rows) == 1 {
				for i, k := range []string{"type", "engine", "row_format", "rows", "avg_row_length", "data_bytes", "index_bytes", "free_bytes", "auto_increment", "created", "updated", "collation", "comment"} {
					status[k] = str(sr.Rows[0][i])
				}
			}
			out = map[string]any{"table": in.Table, "columns": cols, "indexes": idx, "foreign_keys": fks, "status": status, "key": t.Key, "create": create}
			return nil
		})
		return out, err
	}})

	r.Register(actions.Action{Name: "db.browse_rows", Mutating: true, Description: "Database browser: a page of rows (sort, filter) with the row key.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		in, err := decode(p, true)
		if err != nil {
			return nil, err
		}
		if in.Limit <= 0 || in.Limit > maxRowsPage {
			in.Limit = 50
		}
		if in.Offset < 0 {
			in.Offset = 0
		}
		var out map[string]any
		err = c.withBrowser(ctx, in.Database, func(conn *mysqlwire.Conn) error {
			t, err := loadTable(conn, in.Table)
			if err != nil {
				return notFoundOr(err)
			}
			where, err := rowsWhere(t, in.Filter, in.Search)
			if err != nil {
				return err
			}
			order := ""
			if in.Order != "" {
				if !t.has(in.Order) {
					return actions.InvalidParams("order column does not exist")
				}
				order = " ORDER BY " + Ident(in.Order)
				if in.Desc {
					order += " DESC"
				}
			} else if len(t.Key) > 0 {
				var ks []string
				for _, k := range t.Key {
					ks = append(ks, Ident(k))
				}
				order = " ORDER BY " + strings.Join(ks, ", ")
			}
			cr, err := one(conn, "SELECT COUNT(*) FROM "+Ident(in.Table)+where)
			if err != nil {
				return sqlFailed(err)
			}
			total, _ := strconv.ParseInt(str(cr.Rows[0][0]), 10, 64)
			rs, err := conn.Query(fmt.Sprintf("SELECT * FROM %s%s%s LIMIT %d OFFSET %d", Ident(in.Table), where, order, in.Limit, in.Offset), mysqlwire.Limits{MaxRows: in.Limit, MaxBytes: maxResultBytes})
			if err != nil {
				return sqlFailed(err)
			}
			res := resultOut(rs[0], gridCellBytes)
			// Full key values, so rows whose cells were shortened can
			// still be addressed.
			var keys []map[string]any
			if len(t.Key) > 0 {
				for _, row := range rs[0].Rows {
					k := map[string]any{}
					for i, col := range rs[0].Columns {
						for _, kc := range t.Key {
							if col.Name == kc {
								k[kc] = cell(col, row[i], 1<<20)
							}
						}
					}
					keys = append(keys, k)
				}
			}
			res["keys"] = keys
			res["key"] = t.Key
			res["total"] = total
			res["offset"] = in.Offset
			res["limit"] = in.Limit
			out = res
			return nil
		})
		return out, err
	}})

	r.Register(actions.Action{Name: "db.browse_row", Mutating: true, Description: "Database browser: one full row by its key (for editing).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		in, err := decode(p, true)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		err = c.withBrowser(ctx, in.Database, func(conn *mysqlwire.Conn) error {
			t, err := loadTable(conn, in.Table)
			if err != nil {
				return notFoundOr(err)
			}
			w, err := t.where(in.Key)
			if err != nil {
				return err
			}
			rs, err := conn.Query("SELECT * FROM "+Ident(in.Table)+w+" LIMIT 1", mysqlwire.Limits{MaxRows: 1, MaxBytes: maxResultBytes})
			if err != nil {
				return sqlFailed(err)
			}
			if len(rs[0].Rows) == 0 {
				return actions.NotFound("the row no longer exists")
			}
			out = resultOut(rs[0], 4<<20)
			return nil
		})
		return out, err
	}})

	r.Register(actions.Action{Name: "db.browse_save", Mutating: true, Description: "Database browser: insert a row, or update one row by its key.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		in, err := decode(p, true)
		if err != nil {
			return nil, err
		}
		if len(in.Values) == 0 && !in.Insert {
			return nil, actions.InvalidParams("nothing to save")
		}
		var out map[string]any
		err = c.withBrowser(ctx, in.Database, func(conn *mysqlwire.Conn) error {
			t, err := loadTable(conn, in.Table)
			if err != nil {
				return notFoundOr(err)
			}
			var cols, vals, sets []string
			for _, col := range t.Columns { // table order, deterministic SQL
				v, ok := in.Values[col]
				if !ok {
					continue
				}
				lit, err := literal(v)
				if err != nil {
					return actions.InvalidParams(col + ": " + err.Error())
				}
				cols = append(cols, Ident(col))
				vals = append(vals, lit)
				sets = append(sets, Ident(col)+" = "+lit)
			}
			if len(cols) != len(in.Values) {
				return actions.InvalidParams("values name columns that do not exist")
			}
			var sql string
			if in.Insert {
				sql = "INSERT INTO " + Ident(in.Table) + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(vals, ", ") + ")"
			} else {
				w, err := t.where(in.Key)
				if err != nil {
					return err
				}
				sql = "UPDATE " + Ident(in.Table) + " SET " + strings.Join(sets, ", ") + w + " LIMIT 1"
			}
			rs, err := conn.Query(sql, mysqlwire.Limits{})
			if err != nil {
				return sqlFailed(err)
			}
			out = map[string]any{"affected_rows": rs[0].AffectedRows, "insert_id": rs[0].InsertID, "sql": sql}
			return nil
		})
		return out, err
	}})

	r.Register(actions.Action{Name: "db.browse_delete", Mutating: true, Description: "Database browser: delete rows by their keys (in one transaction).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		in, err := decode(p, true)
		if err != nil {
			return nil, err
		}
		if len(in.Keys) == 0 || len(in.Keys) > maxDeleteAtOnce {
			return nil, actions.InvalidParams(fmt.Sprintf("choose 1–%d rows", maxDeleteAtOnce))
		}
		var out map[string]any
		err = c.withBrowser(ctx, in.Database, func(conn *mysqlwire.Conn) error {
			t, err := loadTable(conn, in.Table)
			if err != nil {
				return notFoundOr(err)
			}
			stmts := []string{"START TRANSACTION"}
			for _, k := range in.Keys {
				w, err := t.where(k)
				if err != nil {
					return err
				}
				stmts = append(stmts, "DELETE FROM "+Ident(in.Table)+w+" LIMIT 1")
			}
			rs, err := conn.Query(strings.Join(stmts, ";\n"), mysqlwire.Limits{})
			if err != nil {
				conn.Query("ROLLBACK", mysqlwire.Limits{})
				return sqlFailed(err)
			}
			if _, err := conn.Query("COMMIT", mysqlwire.Limits{}); err != nil {
				return sqlFailed(err)
			}
			var n uint64
			for _, r := range rs {
				n += r.AffectedRows
			}
			out = map[string]any{"deleted": n}
			return nil
		})
		return out, err
	}})

	r.Register(actions.Action{Name: "db.query", Mutating: true, Description: "Database browser: run SQL (one or more statements) as the database's browser account, which can reach this database only.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		in, err := decode(p, false)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.SQL) == "" || len(in.SQL) > maxQueryBytes || !utf8.ValidString(in.SQL) {
			return nil, actions.InvalidParams("sql must be 1 byte to 1 MiB of UTF-8 text")
		}
		var out map[string]any
		err = c.withBrowser(ctx, in.Database, func(conn *mysqlwire.Conn) error {
			start := time.Now()
			rs, qerr := conn.Query(in.SQL, mysqlwire.Limits{MaxRows: maxResultRows, MaxBytes: maxResultBytes})
			results := []map[string]any{}
			for _, r := range rs {
				results = append(results, resultOut(r, queryCellBytes))
			}
			out = map[string]any{"results": results, "ms": time.Since(start).Milliseconds()}
			if qerr != nil {
				var me *mysqlwire.Error
				if !errors.As(qerr, &me) {
					return sqlFailed(qerr)
				}
				// Statements before the error ran; report both.
				out["error"] = me.Error()
				out["error_statement"] = len(rs) + 1
			}
			return nil
		})
		return out, err
	}})
}

func notFoundOr(err error) error {
	var ae *actions.Error
	if errors.As(err, &ae) {
		return err
	}
	return sqlFailed(err)
}
