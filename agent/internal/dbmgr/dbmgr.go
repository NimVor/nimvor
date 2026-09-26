// Package dbmgr manages MySQL/MariaDB databases, users and privileges.
//
// The agent connects as the database root account over the local socket
// (unix_socket / auth_socket authentication, the Ubuntu default), or with
// /etc/whacp/mysql-admin.cnf when present. SQL is sent on stdin, never on
// the command line, so passwords do not appear in the process list.
// Identifiers are restricted to [A-Za-z0-9_] and always backtick-quoted.
package dbmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// AdminDefaults is an optional client config with admin credentials.
const AdminDefaults = "/etc/whacp/mysql-admin.cnf"

var hostRe = regexp.MustCompile(`^(localhost|%|[0-9A-Fa-f.:%/]{1,60}|[A-Za-z0-9.-]{1,60})$`)

// Privilege sets offered to users.
var privilegeSets = map[string]string{
	"all":      "ALL PRIVILEGES",
	"readonly": "SELECT, SHOW VIEW",
	"readwrite": "SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, INDEX, ALTER, CREATE TEMPORARY TABLES, LOCK TABLES, " +
		"EXECUTE, CREATE VIEW, SHOW VIEW, CREATE ROUTINE, ALTER ROUTINE, EVENT, TRIGGER, REFERENCES",
}

// Client runs SQL against the local server.
type Client struct{ Env *host.Env }

func (c Client) args(extra ...string) []string {
	args := []string{}
	if c.Env.Root.Exists(AdminDefaults) {
		args = append(args, "--defaults-extra-file="+AdminDefaults)
	} else {
		args = append(args, "--user=root")
	}
	return append(args, append([]string{"--protocol=socket", "--batch", "--skip-column-names", "--raw"}, extra...)...)
}

// guards caches the option that turns off the mysql client's own commands
// (only a positive answer: a client that lacks it is asked again).
var guards sync.Map

// clientGuard returns the mysql client's option that disables its local
// commands (shell, file reads and writes): --sandbox (MariaDB 10.5.25,
// 10.6.18, 10.11.8 and later) or --commands=OFF (recent MySQL clients;
// 8.0.46 checked), or "" when the installed client has neither.
func (c Client) clientGuard(ctx context.Context) string {
	if g, ok := guards.Load("mysql"); ok {
		return g.(string)
	}
	out, _ := c.Env.Runner.Run(ctx, "mysql", "--help")
	g := ""
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) > 0 && f[0] == "--sandbox":
			g = "--sandbox"
		case len(f) > 0 && f[0] == "--commands" && g == "":
			g = "--commands=OFF"
		}
	}
	if g != "" {
		guards.Store("mysql", g)
	}
	return g
}

// Query runs SQL and returns tab-separated rows. The SQL is WhaCP's own
// (identifiers validated, values quoted); the client's commands are still
// turned off where the client supports it.
func (c Client) Query(ctx context.Context, sql string) ([][]string, error) {
	args := c.args()
	if g := c.clientGuard(ctx); g != "" {
		args = c.args(g)
	}
	out, err := c.Env.Runner.RunWith(ctx, "mysql", execx.Opts{Stdin: strings.NewReader(sql)}, args...)
	if err != nil {
		return nil, fmt.Errorf("%s", host.Output(out))
	}
	var rows [][]string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	return rows, nil
}

// Exec runs SQL statements, returning a user-facing error on failure.
func (c Client) Exec(ctx context.Context, what, sql string) error {
	if _, err := c.Query(ctx, sql); err != nil {
		return actions.Failed(what+" failed.", err.Error())
	}
	return nil
}

// Quote escapes a string literal for MySQL.
func Quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\x00", `\0`, "\n", `\n`, "\r", `\r`, "\x1a", `\Z`)
	return "'" + r.Replace(s) + "'"
}

// Ident backtick-quotes an identifier that was validated.
func Ident(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }

func account(user, host string) string { return Quote(user) + "@" + Quote(host) }

type dbReq struct {
	Name    string `json:"name"`
	Charset string `json:"charset"`
	Confirm string `json:"confirm"`
}

type userReq struct {
	User     string `json:"user"`
	Host     string `json:"host"`
	Password string `json:"password"`
}

type grantReq struct {
	User       string `json:"user"`
	Host       string `json:"host"`
	Database   string `json:"database"`
	Privileges string `json:"privileges"`
	Revoke     bool   `json:"revoke"`
}

func checkUser(u, h string) error {
	if err := validate.DBIdentifier("user", u); err != nil {
		return err
	}
	if len(u) > 32 {
		return &validate.Error{Field: "user", Message: "must be at most 32 characters"}
	}
	if strings.HasPrefix(strings.ToLower(u), BrowserPrefix) {
		return &validate.Error{Field: "user", Message: "names starting with " + BrowserPrefix + " are reserved for the database browser"}
	}
	if !hostRe.MatchString(h) {
		return &validate.Error{Field: "host", Message: "is not a valid host"}
	}
	return nil
}

// Register adds database actions.
func Register(r *actions.Registry, env *host.Env) {
	c := Client{Env: env}
	registerBrowse(r, c)
	r.Register(actions.Action{Name: "db.overview", Description: "Databases, sizes, users and grants (live from the server).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return c.Overview(ctx)
	}})
	r.Register(actions.Action{Name: "db.create", Mutating: true, Description: "Create a database (utf8mb4).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in dbReq
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.DBIdentifier("name", in.Name); err != nil {
			return nil, host.Invalid(err)
		}
		exists, err := c.databaseExists(ctx, in.Name)
		if err != nil {
			return nil, actions.Failed("The database server is not reachable.", err.Error())
		}
		if exists {
			return nil, actions.Conflict("A database named " + in.Name + " already exists.")
		}
		sql := fmt.Sprintf("CREATE DATABASE %s CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;", Ident(in.Name))
		return map[string]any{"name": in.Name}, c.Exec(ctx, "Creating the database", sql)
	}})
	r.Register(actions.Action{Name: "db.drop", Mutating: true, Description: "Drop a database (requires confirm = name).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in dbReq
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.DBIdentifier("name", in.Name); err != nil {
			return nil, host.Invalid(err)
		}
		if in.Confirm != in.Name {
			return nil, actions.InvalidParams("confirm must repeat the database name")
		}
		return map[string]any{"name": in.Name}, c.Exec(ctx, "Dropping the database", fmt.Sprintf("DROP DATABASE IF EXISTS %s;\nDROP USER IF EXISTS %s;", Ident(in.Name), account(BrowserAccount(in.Name), "localhost")))
	}})
	r.Register(actions.Action{Name: "db.user_create", Mutating: true, Description: "Create a database user.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in userReq
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.Host == "" {
			in.Host = "localhost"
		}
		if err := checkUser(in.User, in.Host); err != nil {
			return nil, host.Invalid(err)
		}
		if err := validate.Password("password", in.Password, 12); err != nil {
			return nil, host.Invalid(err)
		}
		exists, err := c.userExists(ctx, in.User, in.Host)
		if err != nil {
			return nil, actions.Failed("The database server is not reachable.", err.Error())
		}
		if exists {
			return nil, actions.Conflict("The database user " + in.User + "@" + in.Host + " already exists.")
		}
		sql := fmt.Sprintf("CREATE USER %s IDENTIFIED BY %s;", account(in.User, in.Host), Quote(in.Password))
		return map[string]any{"user": in.User, "host": in.Host}, c.Exec(ctx, "Creating the database user", sql)
	}})
	r.Register(actions.Action{Name: "db.user_password", Mutating: true, Description: "Change a database user's password.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in userReq
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := checkUser(in.User, in.Host); err != nil {
			return nil, host.Invalid(err)
		}
		if err := validate.Password("password", in.Password, 12); err != nil {
			return nil, host.Invalid(err)
		}
		sql := fmt.Sprintf("ALTER USER %s IDENTIFIED BY %s;", account(in.User, in.Host), Quote(in.Password))
		return map[string]any{"user": in.User}, c.Exec(ctx, "Changing the password", sql)
	}})
	r.Register(actions.Action{Name: "db.user_drop", Mutating: true, Description: "Drop a database user.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in userReq
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := checkUser(in.User, in.Host); err != nil {
			return nil, host.Invalid(err)
		}
		return map[string]any{"user": in.User}, c.Exec(ctx, "Removing the database user", fmt.Sprintf("DROP USER IF EXISTS %s;", account(in.User, in.Host)))
	}})
	r.Register(actions.Action{Name: "db.grant", Mutating: true, Description: "Grant or revoke a privilege set on one database.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in grantReq
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.Host == "" {
			in.Host = "localhost"
		}
		if err := checkUser(in.User, in.Host); err != nil {
			return nil, host.Invalid(err)
		}
		if err := validate.DBIdentifier("database", in.Database); err != nil {
			return nil, host.Invalid(err)
		}
		var sql string
		if in.Revoke {
			sql = fmt.Sprintf("REVOKE ALL PRIVILEGES ON %s.* FROM %s;", Ident(in.Database), account(in.User, in.Host))
		} else {
			priv, ok := privilegeSets[in.Privileges]
			if !ok {
				return nil, actions.InvalidParams("privileges must be all, readwrite or readonly")
			}
			// Replace any previous grant on this database with the new set.
			sql = fmt.Sprintf("REVOKE ALL PRIVILEGES ON %s.* FROM %s;\nGRANT %s ON %s.* TO %s;",
				Ident(in.Database), account(in.User, in.Host), priv, Ident(in.Database), account(in.User, in.Host))
			if exists, _ := c.hasGrant(ctx, in.User, in.Host, in.Database); !exists {
				sql = fmt.Sprintf("GRANT %s ON %s.* TO %s;", priv, Ident(in.Database), account(in.User, in.Host))
			}
		}
		return map[string]any{"user": in.User, "database": in.Database}, c.Exec(ctx, "Changing privileges", sql)
	}})
	r.Register(actions.Action{Name: "db.dump", Mutating: true, Description: "Dump a database to a gzip file in the backup directory (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Name string `json:"name"`
			Dest string `json:"dest"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.DBIdentifier("name", in.Name); err != nil {
			return nil, host.Invalid(err)
		}
		dest := in.Dest
		if dest == "" {
			dest = fmt.Sprintf("%s/databases/%s-%s.sql.gz", env.Cfg.BackupDir, in.Name, env.Now().Format("20060102T150405Z"))
		}
		if err := validate.Within("dest", env.Cfg.BackupDir, dest); err != nil {
			return nil, host.Invalid(err)
		}
		j := env.Jobs.Start("db.dump", "db:"+in.Name, func(ctx context.Context, log io.Writer) (any, error) {
			size, err := c.Dump(ctx, in.Name, dest, log)
			return map[string]any{"file": dest, "size": size}, err
		})
		return map[string]any{"job_id": j.ID, "file": dest}, nil
	}})
	r.Register(actions.Action{Name: "db.import", Mutating: true, Description: "Import a .sql or .sql.gz file into a database (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.DBIdentifier("name", in.Name); err != nil {
			return nil, host.Invalid(err)
		}
		if validate.Within("source", env.Cfg.BackupDir, in.Source) != nil && validate.Within("source", env.Cfg.SitesRoot, in.Source) != nil {
			return nil, actions.InvalidParams("source must be a file inside the backup directory or a website")
		}
		if !strings.HasSuffix(in.Source, ".sql") && !strings.HasSuffix(in.Source, ".sql.gz") {
			return nil, actions.InvalidParams("source must be a .sql or .sql.gz file")
		}
		j := env.Jobs.Start("db.import", "db:"+in.Name, func(ctx context.Context, log io.Writer) (any, error) {
			return nil, c.Import(ctx, in.Name, in.Source, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
}

// Database is one schema with its size.
type Database struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"size_bytes"`
	Tables int    `json:"tables"`
}

// User is one account with the databases it may access.
type User struct {
	User   string            `json:"user"`
	Host   string            `json:"host"`
	Grants map[string]string `json:"grants"`
}

// Overview returns databases, users and grants.
func (c Client) Overview(ctx context.Context) (map[string]any, error) {
	ver, err := c.Query(ctx, "SELECT VERSION();")
	if err != nil {
		return nil, actions.Failed("The database server is not reachable as root over its local socket. Configure "+AdminDefaults+" if root uses a password.", err.Error())
	}
	rows, err := c.Query(ctx, `SELECT s.schema_name, COALESCE(SUM(t.data_length + t.index_length), 0), COUNT(t.table_name)
FROM information_schema.schemata s LEFT JOIN information_schema.tables t ON t.table_schema = s.schema_name
WHERE s.schema_name NOT IN ('mysql','information_schema','performance_schema','sys')
GROUP BY s.schema_name ORDER BY s.schema_name;`)
	if err != nil {
		return nil, actions.Failed("Could not list databases.", err.Error())
	}
	dbs := []Database{}
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		b, _ := strconv.ParseInt(r[1], 10, 64)
		n, _ := strconv.Atoi(r[2])
		dbs = append(dbs, Database{Name: r[0], Bytes: b, Tables: n})
	}
	urows, err := c.Query(ctx, `SELECT user, host FROM mysql.user WHERE user NOT IN ('root','mysql','mariadb.sys','debian-sys-maint','mysql.sys','mysql.session','mysql.infoschema','') AND user NOT LIKE 'whacp\\_b\\_%' ORDER BY user, host;`)
	if err != nil {
		return nil, actions.Failed("Could not list database users.", err.Error())
	}
	users := []User{}
	for _, r := range urows {
		if len(r) >= 2 {
			users = append(users, User{User: r[0], Host: r[1], Grants: map[string]string{}})
		}
	}
	grows, _ := c.Query(ctx, `SELECT grantee, table_schema, GROUP_CONCAT(privilege_type ORDER BY privilege_type) FROM information_schema.schema_privileges GROUP BY grantee, table_schema;`)
	for _, g := range grows {
		if len(g) < 3 {
			continue
		}
		for i := range users {
			if g[0] == "'"+users[i].User+"'@'"+users[i].Host+"'" {
				users[i].Grants[g[1]] = summarize(g[2])
			}
		}
	}
	return map[string]any{"version": ver[0][0], "databases": dbs, "users": users, "checked_at": time.Now().UTC()}, nil
}

func summarize(privs string) string {
	switch {
	case strings.Count(privs, ",") >= 16:
		return "all"
	case privs == "SELECT,SHOW VIEW" || privs == "SELECT":
		return "readonly"
	default:
		return "readwrite"
	}
}

func (c Client) databaseExists(ctx context.Context, name string) (bool, error) {
	rows, err := c.Query(ctx, fmt.Sprintf("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = %s;", Quote(name)))
	return err == nil && len(rows) == 1 && rows[0][0] != "0", err
}

func (c Client) userExists(ctx context.Context, user, h string) (bool, error) {
	rows, err := c.Query(ctx, fmt.Sprintf("SELECT COUNT(*) FROM mysql.user WHERE user = %s AND host = %s;", Quote(user), Quote(h)))
	return err == nil && len(rows) == 1 && rows[0][0] != "0", err
}

func (c Client) hasGrant(ctx context.Context, user, h, db string) (bool, error) {
	rows, err := c.Query(ctx, fmt.Sprintf("SELECT COUNT(*) FROM information_schema.schema_privileges WHERE grantee = %s AND table_schema = %s;", Quote("'"+user+"'@'"+h+"'"), Quote(db)))
	return err == nil && len(rows) == 1 && rows[0][0] != "0", err
}
