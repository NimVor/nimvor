package dbmgr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/changeset"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// The MariaDB/MySQL server manager: status, connections, a fixed list of
// settings in NimVor's own drop-in (zz-nimvor.cnf, read after the
// distribution's files), configuration validation and a restart that
// checks the configuration first and waits for the server to answer.
// Of networking, the listening address can only be narrowed to this
// server (bind_address 127.0.0.1) or left to the distribution; the port is
// shown, not changed.

// DropInName is NimVor's option file in the server's include directory.
const DropInName = "zz-nimvor.cnf"

var dropInDirs = []string{"/etc/mysql/mariadb.conf.d", "/etc/mysql/mysql.conf.d", "/etc/my.cnf.d"}

// Setting kinds.
const (
	kindInt    = "int"
	kindSize   = "size" // bytes, written with K/M/G suffixes
	kindBool   = "bool"
	kindFloat  = "float"
	kindChoice = "choice"
)

// ServerSetting describes one managed option.
type ServerSetting struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Help    string   `json:"help"`
	Kind    string   `json:"kind"`
	Min     float64  `json:"min,omitempty"`
	Max     float64  `json:"max,omitempty"`
	Choices []string `json:"choices,omitempty"`
	Dynamic bool     `json:"dynamic"` // applied at once with SET GLOBAL
	Group   string   `json:"group"`
}

const gib = 1 << 30

// ServerSettings are the options NimVor manages.
var ServerSettings = []ServerSetting{
	{Key: "max_connections", Label: "Maximum connections", Help: "Connections the server accepts at once (one more is kept for administrators).", Kind: kindInt, Min: 10, Max: 100000, Dynamic: true, Group: "Connections"},
	{Key: "wait_timeout", Label: "Idle connection timeout (s)", Help: "Seconds before an idle connection from an application is closed.", Kind: kindInt, Min: 10, Max: 31536000, Dynamic: true, Group: "Connections"},
	{Key: "interactive_timeout", Label: "Idle interactive timeout (s)", Help: "The same for interactive clients (the mysql command line).", Kind: kindInt, Min: 10, Max: 31536000, Dynamic: true, Group: "Connections"},
	{Key: "max_allowed_packet", Label: "Largest packet", Help: "The largest query or row that can be sent, e.g. 64M. Imports of large rows need more.", Kind: kindSize, Min: 1 << 20, Max: gib, Dynamic: true, Group: "Connections"},
	{Key: "thread_cache_size", Label: "Thread cache", Help: "Threads kept for reuse when connections close.", Kind: kindInt, Min: 0, Max: 16384, Dynamic: true, Group: "Connections"},
	// Only ever narrowed here: opening the server to the network is not a
	// setting the panel offers (empty: the distribution's own value).
	{Key: "bind_address", Label: "Listen on", Help: "127.0.0.1: only programs on this server can connect (recommended unless remote database access is needed). Empty: the distribution's setting. Needs a restart.", Kind: kindChoice, Choices: []string{"127.0.0.1"}, Group: "Connections"},
	{Key: "skip_name_resolve", Label: "Skip host name lookups", Help: "Match remote users by IP address only (faster, no DNS). Users defined with host names then stop matching. Needs a restart.", Kind: kindBool, Group: "Connections"},
	{Key: "innodb_buffer_pool_size", Label: "InnoDB buffer pool", Help: "Memory for cached InnoDB data and indexes, e.g. 1G; often 50–70% of the memory on a server used mainly for databases.", Kind: kindSize, Min: 32 << 20, Max: 1024 * gib, Dynamic: true, Group: "Memory and storage"},
	{Key: "innodb_log_file_size", Label: "InnoDB redo log size", Help: "Size of the redo log, e.g. 256M. Needs a restart.", Kind: kindSize, Min: 8 << 20, Max: 512 * gib, Group: "Memory and storage"},
	{Key: "innodb_flush_log_at_trx_commit", Label: "InnoDB flush at commit", Help: "1: every commit is written to disk (safest). 2: written at commit, flushed each second. 0: both each second (fastest; a crash can lose a second of commits).", Kind: kindChoice, Choices: []string{"1", "2", "0"}, Dynamic: true, Group: "Memory and storage"},
	{Key: "tmp_table_size", Label: "In-memory temporary tables", Help: "Largest temporary table kept in memory, e.g. 64M (together with the heap table size).", Kind: kindSize, Min: 1 << 20, Max: 64 * gib, Dynamic: true, Group: "Memory and storage"},
	{Key: "max_heap_table_size", Label: "Heap table size", Help: "Largest MEMORY table; also caps in-memory temporary tables.", Kind: kindSize, Min: 1 << 20, Max: 64 * gib, Dynamic: true, Group: "Memory and storage"},
	{Key: "table_open_cache", Label: "Open tables cache", Help: "Tables kept open for all connections.", Kind: kindInt, Min: 64, Max: 1000000, Dynamic: true, Group: "Memory and storage"},
	{Key: "slow_query_log", Label: "Slow query log", Help: "Log queries that take longer than the time below (to the server's slow query log file).", Kind: kindBool, Dynamic: true, Group: "Logging"},
	{Key: "long_query_time", Label: "Slow query time (s)", Help: "Queries slower than this are logged when the slow query log is on, e.g. 2 or 0.5.", Kind: kindFloat, Min: 0, Max: 3600, Dynamic: true, Group: "Logging"},
	{Key: "character_set_server", Label: "Default character set", Help: "For new databases created without one.", Kind: kindChoice, Choices: []string{"utf8mb4", "utf8mb3", "latin1"}, Dynamic: true, Group: "Character set"},
	{Key: "collation_server", Label: "Default collation", Help: "Must belong to the character set, e.g. utf8mb4_unicode_ci.", Kind: kindChoice, Dynamic: true, Group: "Character set"},
}

func settingByKey(k string) (ServerSetting, bool) {
	for _, s := range ServerSettings {
		if s.Key == k {
			return s, true
		}
	}
	return ServerSetting{}, false
}

var (
	sizeRe      = regexp.MustCompile(`^([0-9]{1,13})([KMGT]?)$`)
	collationRe = regexp.MustCompile(`^[a-z0-9]+_[a-z0-9_]+$`)
)

// ParseSize reads "256M", "1g" or "1048576" as bytes.
func ParseSize(v string) (int64, bool) {
	m := sizeRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(v)))
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	shift := map[string]uint{"": 0, "K": 10, "M": 20, "G": 30, "T": 40}[m[2]]
	if n > math.MaxInt64>>shift {
		return 0, false
	}
	return n << shift, true
}

// normalize validates a value and returns what is written to the file and
// what SET GLOBAL receives.
func (s ServerSetting) normalize(v string) (file, sql string, err error) {
	v = strings.TrimSpace(v)
	bad := func(msg string) (string, string, error) {
		return "", "", actions.InvalidParams(s.Key + ": " + msg)
	}
	switch s.Kind {
	case kindInt:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || float64(n) < s.Min || float64(n) > s.Max {
			return bad(fmt.Sprintf("a whole number from %.0f to %.0f", s.Min, s.Max))
		}
		return strconv.FormatInt(n, 10), strconv.FormatInt(n, 10), nil
	case kindSize:
		n, ok := ParseSize(v)
		if !ok || float64(n) < s.Min || float64(n) > s.Max {
			return bad(fmt.Sprintf("a size from %s to %s, e.g. 256M or 1G", human(int64(s.Min)), human(int64(s.Max))))
		}
		return strings.ToUpper(v), strconv.FormatInt(n, 10), nil
	case kindBool:
		switch strings.ToLower(v) {
		case "1", "on", "true", "yes":
			return "ON", "ON", nil
		case "0", "off", "false", "no":
			return "OFF", "OFF", nil
		}
		return bad("on or off")
	case kindFloat:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < s.Min || f > s.Max || math.IsNaN(f) {
			return bad(fmt.Sprintf("a number from %g to %g", s.Min, s.Max))
		}
		t := strconv.FormatFloat(f, 'f', -1, 64)
		return t, t, nil
	case kindChoice:
		if len(s.Choices) == 0 { // collation: checked against the server
			if !collationRe.MatchString(v) || len(v) > 64 {
				return bad("a collation name such as utf8mb4_unicode_ci")
			}
			return v, Quote(v), nil
		}
		for _, c := range s.Choices {
			if v == c {
				if _, err := strconv.Atoi(c); err == nil {
					return c, c, nil
				}
				return c, Quote(c), nil
			}
		}
		return bad("one of " + strings.Join(s.Choices, ", "))
	}
	return bad("unsupported")
}

func human(n int64) string {
	for _, u := range []struct {
		s string
		b int64
	}{{"T", 1 << 40}, {"G", gib}, {"M", 1 << 20}, {"K", 1 << 10}} {
		if n >= u.b && n%u.b == 0 {
			return strconv.FormatInt(n/u.b, 10) + u.s
		}
	}
	return strconv.FormatInt(n, 10)
}

// RenderDropIn writes the managed settings.
func RenderDropIn(values map[string]string) string {
	var b strings.Builder
	b.WriteString("# Managed by NimVor (Databases → Database server). Changes here are overwritten.\n[mysqld]\n")
	for _, s := range ServerSettings { // fixed order
		if v, ok := values[s.Key]; ok {
			fmt.Fprintf(&b, "%s = %s\n", s.Key, v)
		}
	}
	return b.String()
}

// ParseDropIn reads NimVor's drop-in back (only known keys).
func ParseDropIn(text string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		k = strings.ReplaceAll(strings.TrimSpace(k), "-", "_")
		if _, known := settingByKey(k); known {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out
}

// ---- server access -----------------------------------------------------------

func (c Client) dropInPath() (string, bool) {
	for _, d := range dropInDirs {
		if c.Env.Root.Exists(d) {
			return filepath.Join(d, DropInName), true
		}
	}
	return "", false
}

func (c Client) serverBinary() string {
	for _, b := range []string{"mariadbd", "mysqld"} {
		if c.Env.Runner.Available(b) {
			return b
		}
	}
	return ""
}

// ValidateConfig asks the server binary to read the option files; unknown
// or invalid options make it fail.
func (c Client) ValidateConfig(ctx context.Context) error {
	bin := c.serverBinary()
	if bin == "" {
		return errors.New("the database server program was not found")
	}
	out, err := c.Env.Runner.Run(ctx, bin, "--help", "--verbose")
	if err != nil {
		var lines []string
		for _, l := range strings.Split(string(out), "\n") {
			if strings.Contains(l, "[ERROR]") {
				lines = append(lines, strings.TrimSpace(l))
			}
		}
		if len(lines) == 0 {
			lines = []string{host.Output(out)}
		}
		return errors.New(strings.Join(lines, "\n"))
	}
	return nil
}

func (c Client) unit(ctx context.Context) string {
	for _, u := range c.Env.M.MySQLUnit {
		if c.Env.UnitActive(ctx, u) {
			return u
		}
	}
	if len(c.Env.M.MySQLUnit) > 0 {
		return c.Env.M.MySQLUnit[0]
	}
	return ""
}

func (c Client) variables(ctx context.Context, names []string) (map[string]string, error) {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = Quote(n)
	}
	rows, err := c.Query(ctx, "SHOW GLOBAL VARIABLES WHERE Variable_name IN ("+strings.Join(q, ",")+");")
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, r := range rows {
		if len(r) >= 2 {
			out[r[0]] = r[1]
		}
	}
	return out, nil
}

// waitReady waits until the server answers.
func (c Client) waitReady(ctx context.Context, d time.Duration) error {
	deadline := time.Now().Add(d)
	var err error
	for time.Now().Before(deadline) {
		if _, err = c.Query(ctx, "SELECT 1;"); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return err
}

// safeRestart validates, restarts and waits for the server.
func (c Client) safeRestart(ctx context.Context) error {
	if err := c.ValidateConfig(ctx); err != nil {
		return actions.Conflict("The configuration is not valid, so the server was not restarted: " + err.Error())
	}
	u := c.unit(ctx)
	if u == "" {
		return actions.Failed("The database server's service was not found.", "")
	}
	if err := c.Env.Systemctl(ctx, "restart", u); err != nil {
		return actions.Failed("The database server did not restart.", err.Error())
	}
	if err := c.waitReady(ctx, 90*time.Second); err != nil {
		return actions.Failed("The database server did not answer after the restart.", fmt.Sprint(err))
	}
	return nil
}

var statusNames = []string{"Uptime", "Threads_connected", "Threads_running", "Max_used_connections", "Connections", "Aborted_connects", "Aborted_clients",
	"Questions", "Slow_queries", "Bytes_received", "Bytes_sent", "Open_tables", "Opened_tables", "Created_tmp_tables", "Created_tmp_disk_tables",
	"Innodb_buffer_pool_pages_total", "Innodb_buffer_pool_pages_free", "Innodb_buffer_pool_pages_data", "Innodb_buffer_pool_read_requests", "Innodb_buffer_pool_reads",
	"Com_select", "Com_insert", "Com_update", "Com_delete", "Table_locks_waited"}

var infoVariables = []string{"version", "version_comment", "datadir", "socket", "port", "bind_address", "skip_networking", "log_error", "slow_query_log_file", "innodb_page_size", "have_ssl"}

func registerServer(r *actions.Registry, c Client) {
	r.Register(actions.Action{Name: "db.server_status", Description: "Database server: version, uptime, status counters, connection details, managed settings (effective and NimVor's) and whether the configuration is valid.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		out := map[string]any{"running": false, "unit": c.unit(ctx), "settings": ServerSettings}
		path, ok := c.dropInPath()
		out["drop_in"] = path
		managed := map[string]string{}
		if ok {
			if b, err := c.Env.Root.ReadFile(path); err == nil {
				managed = ParseDropIn(string(b))
			}
		}
		out["managed"] = managed
		if err := c.ValidateConfig(ctx); err != nil {
			out["config_error"] = err.Error()
		}
		keys := append([]string{}, infoVariables...)
		for _, s := range ServerSettings {
			keys = append(keys, s.Key)
		}
		vars, err := c.variables(ctx, keys)
		if err != nil {
			out["error"] = err.Error()
			return out, nil
		}
		out["running"] = true
		out["variables"] = vars
		q := make([]string, len(statusNames))
		for i, n := range statusNames {
			q[i] = Quote(n)
		}
		st := map[string]string{}
		if rows, err := c.Query(ctx, "SHOW GLOBAL STATUS WHERE Variable_name IN ("+strings.Join(q, ",")+");"); err == nil {
			for _, r := range rows {
				if len(r) >= 2 {
					st[r[0]] = r[1]
				}
			}
		}
		out["status"] = st
		if rows, err := c.Query(ctx, "SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME NOT IN ('mysql','information_schema','performance_schema','sys')), (SELECT COUNT(*) FROM mysql.user WHERE User <> '' AND User NOT LIKE '"+BrowserPrefix+"%'), (SELECT COALESCE(SUM(DATA_LENGTH + INDEX_LENGTH),0) FROM information_schema.TABLES WHERE TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys'));"); err == nil && len(rows) == 1 && len(rows[0]) == 3 {
			dbs, _ := strconv.Atoi(rows[0][0])
			users, _ := strconv.Atoi(rows[0][1])
			size, _ := strconv.ParseInt(rows[0][2], 10, 64)
			out["databases"], out["users"], out["bytes"] = dbs, users, size
		}
		if rows, err := c.Query(ctx, "SELECT COLLATION_NAME, CHARACTER_SET_NAME FROM information_schema.COLLATIONS WHERE CHARACTER_SET_NAME IN ('utf8mb4','utf8mb3','latin1') ORDER BY COLLATION_NAME;"); err == nil {
			coll := map[string][]string{}
			for _, r := range rows {
				if len(r) == 2 {
					coll[r[1]] = append(coll[r[1]], r[0])
				}
			}
			out["collations"] = coll
		}
		return out, nil
	}})

	r.Register(actions.Action{Name: "db.processlist", Description: "Database server: the current connections and what they run (statements cut to 2,000 characters).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		rows, err := c.Query(ctx, "SELECT ID, USER, HOST, COALESCE(DB,''), COMMAND, TIME, COALESCE(STATE,''), REPLACE(REPLACE(COALESCE(LEFT(INFO, 2000),''), '\\t', ' '), '\\n', ' ') FROM information_schema.PROCESSLIST WHERE ID <> CONNECTION_ID() ORDER BY TIME DESC;")
		if err != nil {
			return nil, actions.Failed("The database server is not reachable.", err.Error())
		}
		list := []map[string]any{}
		for _, r := range rows {
			if len(r) < 8 {
				continue
			}
			id, _ := strconv.ParseInt(r[0], 10, 64)
			t, _ := strconv.ParseInt(r[5], 10, 64)
			list = append(list, map[string]any{"id": id, "user": r[1], "host": r[2], "db": r[3], "command": r[4], "time": t, "state": r[6], "info": r[7], "system": r[1] == "system user" || r[1] == "event_scheduler" || r[4] == "Daemon"})
		}
		return map[string]any{"connections": list}, nil
	}})

	r.Register(actions.Action{Name: "db.kill", Mutating: true, Description: "Database server: end a connection (or only its running statement with query_only). Server threads cannot be ended.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			ID        int64 `json:"id"`
			QueryOnly bool  `json:"query_only"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if in.ID <= 0 {
			return nil, actions.InvalidParams("id: a connection number")
		}
		rows, err := c.Query(ctx, fmt.Sprintf("SELECT USER, COMMAND FROM information_schema.PROCESSLIST WHERE ID = %d AND ID <> CONNECTION_ID();", in.ID))
		if err != nil {
			return nil, actions.Failed("The database server is not reachable.", err.Error())
		}
		if len(rows) == 0 {
			return nil, actions.NotFound("That connection has already ended.")
		}
		if u := rows[0][0]; u == "system user" || u == "event_scheduler" || (len(rows[0]) > 1 && rows[0][1] == "Daemon") {
			return nil, actions.Conflict("That is one of the server's own threads.")
		}
		stmt := "KILL CONNECTION"
		if in.QueryOnly {
			stmt = "KILL QUERY"
		}
		if err := c.Exec(ctx, "Ending the connection", fmt.Sprintf("%s %d;", stmt, in.ID)); err != nil {
			return nil, err
		}
		return map[string]any{"killed": in.ID, "query_only": in.QueryOnly}, nil
	}})

	r.Register(actions.Action{Name: "db.server_flush", Mutating: true, Description: "Database server: FLUSH PRIVILEGES, LOGS (reopen log files) or HOSTS (unblock hosts blocked after connection errors).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			What string `json:"what"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		stmt := map[string]string{"privileges": "FLUSH PRIVILEGES;", "logs": "FLUSH LOGS;", "hosts": "FLUSH HOSTS;"}[in.What]
		if stmt == "" {
			return nil, actions.InvalidParams("what: privileges, logs or hosts")
		}
		if err := c.Exec(ctx, "FLUSH "+strings.ToUpper(in.What), stmt); err != nil {
			return nil, err
		}
		return map[string]any{"flushed": in.What}, nil
	}})

	r.Register(actions.Action{Name: "db.server_restart", Mutating: true, Timeout: 3 * time.Minute, Description: "Database server: check the configuration, restart the service and wait until it answers again. Refused when the configuration is invalid.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		if c.Env.Cfg.SimulateCommands {
			return map[string]any{"restarted": true, "simulated": true}, nil
		}
		start := time.Now()
		if err := c.safeRestart(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"restarted": true, "seconds": int(time.Since(start).Seconds())}, nil
	}})

	r.Register(actions.Action{Name: "db.server_settings", Mutating: true, Timeout: 3 * time.Minute, Description: "Database server: set NimVor's managed options (\"\" removes one, leaving the distribution's value). Values are validated, the option files are checked by the server program, dynamic options are applied at once; others (or all, with restart) take effect after a checked restart. Anything that fails is rolled back.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Settings map[string]string `json:"settings"`
			Restart  bool              `json:"restart"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		path, ok := c.dropInPath()
		if !ok {
			return nil, actions.Conflict("No option file directory of MariaDB or MySQL was found (" + strings.Join(dropInDirs, ", ") + ").")
		}
		old := map[string]string{}
		if b, err := c.Env.Root.ReadFile(path); err == nil {
			old = ParseDropIn(string(b))
		}
		next := map[string]string{}
		for k, v := range old {
			next[k] = v
		}
		setSQL := map[string]string{}
		var changed []string
		for k, v := range in.Settings {
			s, known := settingByKey(k)
			if !known {
				return nil, actions.InvalidParams(k + " is not a setting NimVor manages")
			}
			if strings.TrimSpace(v) == "" {
				if _, had := next[k]; had {
					delete(next, k)
					changed = append(changed, k)
				}
				continue
			}
			file, sql, err := s.normalize(v)
			if err != nil {
				return nil, err
			}
			if old[k] != file {
				changed = append(changed, k)
			}
			next[k] = file
			setSQL[k] = sql
		}
		sort.Strings(changed)
		// Character set and collation must match (checked by the server).
		cs, coll := next["character_set_server"], next["collation_server"]
		if coll != "" {
			if cs == "" {
				if v, err := c.variables(ctx, []string{"character_set_server"}); err == nil {
					cs = v["character_set_server"]
				}
			}
			rows, err := c.Query(ctx, "SELECT COUNT(*) FROM information_schema.COLLATIONS WHERE COLLATION_NAME = "+Quote(coll)+" AND CHARACTER_SET_NAME = "+Quote(cs)+";")
			if err != nil {
				return nil, actions.Failed("The database server is not reachable.", err.Error())
			}
			if len(rows) != 1 || rows[0][0] != "1" {
				return nil, actions.InvalidParams("collation_server: " + coll + " is not a collation of " + cs)
			}
		}
		if len(changed) == 0 && !in.Restart {
			return map[string]any{"changed": []string{}, "applied": []string{}, "restart_needed": []string{}}, nil
		}
		// Values in effect now, to put back if applying fails.
		keys := make([]string, 0, len(ServerSettings))
		for _, s := range ServerSettings {
			keys = append(keys, s.Key)
		}
		before, err := c.variables(ctx, keys)
		if err != nil {
			return nil, actions.Failed("The database server is not reachable.", err.Error())
		}
		set := c.Env.Changes("mysql-settings")
		set.Write(path, []byte(RenderDropIn(next)), 0o644, agentfs.RootOwner)
		applied, err := set.Commit(func() error {
			if err := c.ValidateConfig(ctx); err != nil {
				return &changeset.ValidationError{Step: c.serverBinary() + " --help --verbose", Output: err.Error(), Err: err}
			}
			return nil
		})
		if err != nil {
			return nil, host.FailWithValidation("The database server settings were not saved", err)
		}
		var done, restartNeeded []string
		undo := func() {
			for _, k := range done {
				s, _ := settingByKey(k)
				v := before[k]
				if s.Kind == kindChoice && len(s.Choices) == 0 || k == "character_set_server" {
					v = Quote(v)
				}
				c.Query(ctx, fmt.Sprintf("SET GLOBAL %s = %s;", k, v))
			}
			applied.Rollback()
		}
		for _, k := range changed {
			s, _ := settingByKey(k)
			sql, has := setSQL[k]
			if !s.Dynamic || !has {
				restartNeeded = append(restartNeeded, k) // new static value, or a removed one
				continue
			}
			if in.Restart {
				continue
			}
			// Character set before collation (fixed order of changed is alphabetical).
			if _, err := c.Query(ctx, fmt.Sprintf("SET GLOBAL %s = %s;", k, sql)); err != nil {
				undo()
				return nil, actions.Failed("The server refused "+k+"; the previous settings were restored.", err.Error())
			}
			done = append(done, k)
		}
		out := map[string]any{"changed": nonNil(changed), "applied": nonNil(done), "restart_needed": nonNil(restartNeeded), "backup": applied.Backup}
		if in.Restart {
			if c.Env.Cfg.SimulateCommands {
				out["restarted"] = true
				return out, nil
			}
			if err := c.safeRestart(ctx); err != nil {
				// Back to the previous file and a server that runs.
				applied.Rollback()
				if rerr := c.safeRestart(ctx); rerr != nil {
					return nil, actions.Failed("The database server did not come back with the new settings, and not with the previous ones either. Check the server's error log.", err.Error()+"\n"+rerr.Error())
				}
				return nil, actions.Failed("The database server did not come back with the new settings; the previous settings were restored and it was restarted with them.", err.Error())
			}
			out["restarted"] = true
			out["restart_needed"] = []string{}
		}
		return out, nil
	}})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
