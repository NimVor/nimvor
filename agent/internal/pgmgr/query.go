package pgmgr

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// The SQL editor. A statement runs in a session logged in as the database's dedicated query account (queryroles.go):
// in write mode acting as the database owner, in read-only mode as an account that can only SELECT. Read-only is
// therefore enforced by PostgreSQL's privileges, not by looking at the text. Results are read as text over the
// protocol, so NULLs, large numbers, timestamps and binary data arrive exactly as PostgreSQL prints them; the number
// of rows and the size of cells are capped, statements time out, and a running statement can be cancelled.

const (
	maxQueryBytes   = 100 * 1024
	defaultRows     = 500
	maxRows         = 1000
	defaultTimeout  = 15
	maxTimeout      = 60
	maxScriptResult = 20
	queryCellLimit  = 4096
)

var queryIDRe = regexp.MustCompile(`^[a-f0-9]{16}$`)

// QueryRequest is one execution.
type QueryRequest struct {
	Database       string `json:"database"`
	Query          string `json:"query"`
	Mode           string `json:"mode"`            // read (default) or write
	Limit          int    `json:"limit"`           // rows kept per result
	TimeoutSeconds int    `json:"timeout_seconds"` // per statement
	Script         bool   `json:"script"`          // several statements; otherwise exactly one
	Explain        string `json:"explain"`         // "", plan or analyze
	QueryID        string `json:"query_id"`
}

// QueryColumn is a result column.
type QueryColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ResultSet is the outcome of one statement.
type ResultSet struct {
	Columns      []QueryColumn `json:"columns"`
	Rows         [][]*string   `json:"rows"`
	Truncated    [][2]int      `json:"truncated"`
	RowsCapped   bool          `json:"rows_capped"`
	Command      string        `json:"command"`
	RowsAffected int64         `json:"rows_affected"`
}

// QueryError is a statement PostgreSQL refused; it is an outcome of running SQL, not a failure of the action.
type QueryError struct {
	Message  string `json:"message"`
	Detail   string `json:"detail,omitempty"`
	Hint     string `json:"hint,omitempty"`
	Code     string `json:"code,omitempty"`
	Position int    `json:"position,omitempty"` // 1-based character offset in the statement
}

// QueryResult is the answer to pg.query.
type QueryResult struct {
	Results    []ResultSet `json:"results"`
	Error      *QueryError `json:"error,omitempty"`
	DurationMs int64       `json:"duration_ms"`
	Mode       string      `json:"mode"`
}

type running struct {
	database string
	cancel   context.CancelFunc
	session  *session
}

var (
	runMu          sync.Mutex
	runningQueries = map[string]*running{}
)

func queryErr(err error) *QueryError {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		q := &QueryError{Message: pe.Message, Detail: pe.Detail, Hint: pe.Hint, Code: pe.Code, Position: int(pe.Position)}
		if pe.Code == "42501" {
			q.Hint = "Your access to this object is read-only or not granted."
		}
		return q
	}
	return &QueryError{Message: friendly(err)}
}

func (r QueryRequest) validate() error {
	if err := ValidName("database", r.Database); err != nil {
		return err
	}
	if strings.TrimSpace(r.Query) == "" {
		return actions.InvalidParams("query: write a statement.")
	}
	if len(r.Query) > maxQueryBytes || strings.ContainsRune(r.Query, 0) {
		return actions.InvalidParams("query: at most 100 KB, without NUL characters.")
	}
	switch r.Mode {
	case "", "read", "write":
	default:
		return actions.InvalidParams("mode: choose read or write.")
	}
	switch r.Explain {
	case "", "plan", "analyze":
	default:
		return actions.InvalidParams("explain: choose plan or analyze.")
	}
	if r.Explain != "" && r.Script {
		return actions.InvalidParams("explain: explains one statement; turn the script option off.")
	}
	if r.QueryID != "" && !queryIDRe.MatchString(r.QueryID) {
		return actions.InvalidParams("query_id: 16 hexadecimal characters.")
	}
	return nil
}

// readResult reads one statement's result, keeping at most limit rows. When the limit is reached the statement is
// cancelled on the server instead of being read to the end.
func readResult(pgc *pgconn.PgConn, rr *pgconn.ResultReader, limit int) (ResultSet, []uint32, error) {
	rs := ResultSet{Columns: []QueryColumn{}, Rows: [][]*string{}, Truncated: [][2]int{}}
	var oids []uint32
	for _, f := range rr.FieldDescriptions() {
		rs.Columns = append(rs.Columns, QueryColumn{Name: f.Name})
		oids = append(oids, f.DataTypeOID)
	}
	cancelled := false
	for rr.NextRow() {
		if len(rs.Rows) >= limit {
			rs.RowsCapped = true
			if !cancelled {
				cancelled = true
				c, stop := context.WithTimeout(context.Background(), 3*time.Second)
				_ = pgc.CancelRequest(c)
				stop()
			}
			continue
		}
		vals := rr.Values()
		row := make([]*string, len(vals))
		for i, v := range vals {
			if v == nil {
				continue
			}
			s := string(v)
			if r := []rune(s); len(r) > queryCellLimit {
				s = string(r[:queryCellLimit])
				rs.Truncated = append(rs.Truncated, [2]int{len(rs.Rows), i})
			}
			row[i] = &s
		}
		rs.Rows = append(rs.Rows, row)
	}
	tag, err := rr.Close()
	if err != nil {
		var pe *pgconn.PgError
		if cancelled && errors.As(err, &pe) && pe.Code == "57014" {
			err = nil // our own cancel after enough rows
		}
	}
	rs.Command = tag.String()
	rs.RowsAffected = tag.RowsAffected()
	return rs, oids, err
}

// typeNames resolves type OIDs to names through the session (a quick catalog read; the statement has finished).
func (s *session) typeNames(ctx context.Context, oids []uint32) map[uint32]string {
	out := map[uint32]string{}
	if len(oids) == 0 {
		return out
	}
	rows, err := s.Query(ctx, `SELECT oid::int8, format_type(oid, NULL) FROM pg_type WHERE oid = ANY($1)`, oids)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var o int64
		var n string
		if rows.Scan(&o, &n) == nil {
			out[uint32(o)] = n
		}
	}
	return out
}

func runQuery(ctx context.Context, env *host.Env, t *Target, req QueryRequest) (*QueryResult, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	readOnly := req.Mode != "write"
	limit := req.Limit
	if limit <= 0 {
		limit = defaultRows
	}
	if limit > maxRows {
		limit = maxRows
	}
	secs := req.TimeoutSeconds
	if secs <= 0 {
		secs = defaultTimeout
	}
	if secs > maxTimeout {
		secs = maxTimeout
	}
	sql := req.Query
	switch req.Explain {
	case "plan":
		sql = "EXPLAIN " + sql
	case "analyze":
		sql = "EXPLAIN (ANALYZE, BUFFERS) " + sql
	}

	s, err := openQuery(ctx, env, t, req.Database, readOnly)
	if err != nil {
		return nil, err
	}
	defer s.Close(context.Background())
	if _, err := s.Exec(ctx, "SET statement_timeout = "+strconv.Itoa(secs*1000)); err != nil {
		return nil, err
	}
	// The statement may run for the whole timeout (per statement in a script); the context is a backstop.
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(secs*maxScriptResult+10)*time.Second)
	defer cancel()
	if req.QueryID != "" {
		runMu.Lock()
		runningQueries[req.QueryID] = &running{database: req.Database, cancel: cancel, session: s}
		runMu.Unlock()
		defer func() {
			runMu.Lock()
			delete(runningQueries, req.QueryID)
			runMu.Unlock()
		}()
	}

	res := &QueryResult{Results: []ResultSet{}, Mode: "write"}
	if readOnly {
		res.Mode = "read"
	}
	started := time.Now()
	pgc := s.PgConn()
	var oids [][]uint32
	var runErr error
	if req.Script {
		mrr := pgc.Exec(runCtx, sql)
		for mrr.NextResult() {
			rs, o, err := readResult(pgc, mrr.ResultReader(), limit)
			if err != nil {
				runErr = err
				break
			}
			res.Results, oids = append(res.Results, rs), append(oids, o)
			if len(res.Results) >= maxScriptResult {
				break
			}
		}
		if err := mrr.Close(); err != nil && runErr == nil {
			runErr = err
		}
	} else {
		rs, o, err := readResult(pgc, pgc.ExecParams(runCtx, sql, nil, nil, nil, nil), limit)
		if err != nil {
			runErr = err
		} else {
			res.Results, oids = append(res.Results, rs), append(oids, o)
		}
	}
	res.DurationMs = time.Since(started).Milliseconds()
	if runErr != nil {
		if runCtx.Err() != nil && ctx.Err() == nil {
			res.Error = &QueryError{Message: "The statement was cancelled.", Code: "57014"}
		} else {
			res.Error = queryErr(runErr)
		}
		return res, nil
	}
	// Type names, once the statements are done and the connection is idle again.
	var all []uint32
	for _, o := range oids {
		all = append(all, o...)
	}
	names := s.typeNames(ctx, all)
	for i := range res.Results {
		for j := range res.Results[i].Columns {
			res.Results[i].Columns[j].Type = names[oids[i][j]]
		}
	}
	return res, nil
}

// cancelQuery cancels a running statement (identified by the unguessable id its run was started with).
func cancelQuery(id, database string) bool {
	runMu.Lock()
	r := runningQueries[id]
	runMu.Unlock()
	if r == nil || r.database != database {
		return false
	}
	c, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	_ = r.session.PgConn().CancelRequest(c)
	return true
}
