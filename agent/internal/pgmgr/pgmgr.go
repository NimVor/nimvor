// Package pgmgr manages PostgreSQL: detecting, installing and controlling the local server, and provisioning
// databases, users and privileges on it (or on a registered remote server).
//
// The agent talks to PostgreSQL with pgx. The local server is reached over 127.0.0.1 as the role nimvor_admin
// (created with CREATEDB and CREATEROLE only, never a superuser), whose credentials live in AdminFile, root-only.
// Customers never get that role; every customer-facing statement runs as the customer's own role. Identifiers
// are validated against a strict pattern and quoted by the server's own format(%I); values are parameters or
// format(%L). Remote servers are reached only through a Target the panel builds from an administrator-registered
// server, over TLS.
package pgmgr

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
)

// ServiceUnit starts every PostgreSQL cluster on Debian and Ubuntu and is the server unit on the RHEL family.
const ServiceUnit = "postgresql.service"

// Status is what the panel shows about the local server.
type Status struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Managed   bool   `json:"managed"` // NimVor holds admin credentials for it
	Version   string `json:"version,omitempty"`
	Port      int    `json:"port"`
}

var versionRe = regexp.MustCompile(`([0-9]+)(?:\.([0-9]+))?`)

// serverBinary reports whether the PostgreSQL server (not just the client) is installed.
func serverBinary(env *host.Env) bool {
	for _, pat := range []string{"/usr/lib/postgresql/*/bin/postgres", "/usr/bin/postgres", "/usr/pgsql-*/bin/postgres"} {
		p, err := env.Root.Path(pat)
		if err != nil {
			continue
		}
		if m, _ := filepath.Glob(p); len(m) > 0 {
			return true
		}
	}
	return false
}

func status(ctx context.Context, env *host.Env) Status {
	st := Status{Installed: serverBinary(env), Port: 5432}
	if !st.Installed {
		return st
	}
	if out, err := env.Runner.Run(ctx, "pg_isready", "-h", "127.0.0.1", "-p", "5432", "-t", "3"); err == nil && strings.Contains(string(out), "accepting") {
		st.Running = true
	}
	if p, err := env.Root.Path(AdminFile); err == nil {
		if _, err := os.Stat(p); err == nil {
			st.Managed = true
		}
	}
	if out, err := env.Runner.Run(ctx, "psql", "--version"); err == nil {
		if m := versionRe.FindStringSubmatch(string(out)); m != nil {
			st.Version = strings.TrimSuffix(m[1]+"."+m[2], ".")
		}
	}
	return st
}

// randomPassword returns a 32-character password from an alphabet that needs no quoting anywhere.
func randomPassword() (string, error) {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 32)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}

// postgresCred is the operating-system account that owns the cluster.
func postgresCred() (*execx.Credential, error) {
	u, err := user.Lookup("postgres")
	if err != nil {
		return nil, fmt.Errorf("the operating-system account postgres does not exist")
	}
	uid, _ := strconv.ParseUint(u.Uid, 10, 32)
	gid, _ := strconv.ParseUint(u.Gid, 10, 32)
	home := u.HomeDir
	if home == "" {
		home = "/var/lib/postgresql"
	}
	return &execx.Credential{UID: uint32(uid), GID: uint32(gid), Home: home}, nil
}

// bootstrapAdmin creates (or re-keys) the nimvor_admin role through the postgres account's own socket access
// and stores its credentials. It changes no PostgreSQL configuration file.
func bootstrapAdmin(ctx context.Context, env *host.Env, log io.Writer, harden bool) error {
	cred, err := postgresCred()
	if err != nil {
		return err
	}
	pw, err := randomPassword()
	if err != nil {
		return err
	}
	sql := fmt.Sprintf(`DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%[1]s') THEN
    CREATE ROLE %[1]s LOGIN CREATEDB CREATEROLE NOSUPERUSER NOREPLICATION NOBYPASSRLS;
  END IF;
END $$;
ALTER ROLE %[1]s PASSWORD '%[2]s';
GRANT pg_monitor TO %[1]s;
GRANT CONNECT ON DATABASE postgres TO %[1]s;
`, AdminRole, pw)
	if harden {
		// Database users must not be able to enter the maintenance databases (PostgreSQL lets PUBLIC connect to them).
		sql += hardenSQL
	}
	if v := serverMajor(ctx, env); v >= 16 {
		sql += fmt.Sprintf("ALTER ROLE %s SET createrole_self_grant = 'set, inherit';\n", AdminRole)
	}
	out, err := env.Runner.RunWith(ctx, "psql", execx.Opts{As: cred, Stdin: strings.NewReader(sql), Dir: "/", Timeout: 30 * time.Second},
		"-X", "-q", "-v", "ON_ERROR_STOP=1", "-d", "postgres")
	if err != nil {
		return fmt.Errorf("the provisioning role could not be created: %s", host.Output(out))
	}
	t := Target{Host: "127.0.0.1", Port: 5432, User: AdminRole, Password: pw}
	b, _ := json.Marshal(t)
	if _, err := env.Writer.MkdirAll(filepath.Dir(AdminFile), 0o700, agentfs.RootOwner); err != nil {
		return err
	}
	if _, err := env.Writer.WriteFile(AdminFile, append(b, '\n'), 0o600, agentfs.RootOwner); err != nil {
		return fmt.Errorf("the credentials could not be saved to %s: %w", AdminFile, err)
	}
	fmt.Fprintln(log, "  ✓ the provisioning role "+AdminRole+" is ready (no superuser rights)")
	return nil
}

// hardenSQL takes PUBLIC's right to connect to the maintenance databases away. template0 already refuses connections.
const hardenSQL = "REVOKE CONNECT ON DATABASE postgres FROM PUBLIC;\nREVOKE CONNECT ON DATABASE template1 FROM PUBLIC;\n"

func serverMajor(ctx context.Context, env *host.Env) int {
	out, err := env.Runner.Run(ctx, "psql", "--version")
	if err != nil {
		return 0
	}
	if m := versionRe.FindStringSubmatch(string(out)); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

type targetIn struct {
	Target *Target `json:"target,omitempty"`
}

func withSession(ctx context.Context, env *host.Env, t *Target, db string, fn func(*session) error) error {
	s, err := open(ctx, env, t, db)
	if err != nil {
		return err
	}
	defer s.Close(ctx)
	return fn(s)
}

// Register adds the PostgreSQL actions.
func Register(r *actions.Registry, env *host.Env) {
	registerBrowser(r, env)
	registerQuery(r, env)
	registerBackup(r, env)
	registerMonitor(r, env)
	r.Register(actions.Action{Name: "pg.status", Description: "PostgreSQL on this server: installed, running, version, and whether NimVor holds credentials for it.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return status(ctx, env), nil
	}})

	r.Register(actions.Action{Name: "pg.install", Mutating: true, Description: "Install PostgreSQL from the operating system's packages, start it and create NimVor's provisioning role. Refuses when a server is already installed.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		if err := host.Decode(p, &struct{}{}); err != nil {
			return nil, err
		}
		if serverBinary(env) {
			return nil, actions.Conflict("PostgreSQL is already installed. Use \"Adopt\" to let NimVor manage the existing server.")
		}
		j := env.Jobs.Start("pg.install", "server:postgresql", func(ctx context.Context, log io.Writer) (any, error) {
			names := []string{"postgresql"}
			if env.Platform.PackageManager() == "dnf" {
				names = []string{"postgresql-server"}
			}
			if err := pkgs.Install(ctx, env, log, names); err != nil {
				return nil, err
			}
			if env.Platform.PackageManager() == "dnf" {
				fmt.Fprintln(log, "Initialising the database cluster…")
				if out, err := env.Runner.Run(ctx, "postgresql-setup", "--initdb"); err != nil {
					return nil, fmt.Errorf("the cluster could not be initialised: %s", host.Output(out))
				}
			}
			fmt.Fprintln(log, "Enabling and starting PostgreSQL…")
			if err := env.Systemctl(ctx, "enable", ServiceUnit); err != nil {
				return nil, err
			}
			if err := env.Systemctl(ctx, "start", ServiceUnit); err != nil {
				return nil, err
			}
			if err := waitReady(ctx, env); err != nil {
				return nil, err
			}
			if err := bootstrapAdmin(ctx, env, log, true); err != nil {
				return nil, err
			}
			fmt.Fprintln(log, "  ✓ PostgreSQL is installed and running")
			return nil, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})

	r.Register(actions.Action{Name: "pg.adopt", Mutating: true, Description: "Let NimVor manage an existing local PostgreSQL server: creates one role (nimvor_admin, no superuser rights) through the postgres account. No configuration file is changed. Needs confirm=true.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Confirm bool `json:"confirm"`
			// Harden also removes PUBLIC's CONNECT on the postgres and template1 databases, so customers' users
			// cannot enter them. It can affect other applications that rely on it, hence the separate choice.
			Harden bool `json:"harden"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !in.Confirm {
			return nil, actions.InvalidParams("confirm: adopting an existing server creates the role " + AdminRole + " on it; confirm to continue.")
		}
		st := status(ctx, env)
		if !st.Installed {
			return nil, actions.Conflict("PostgreSQL is not installed.")
		}
		if !st.Running {
			return nil, actions.Conflict("PostgreSQL is not running. Start it first.")
		}
		var out strings.Builder
		if err := bootstrapAdmin(ctx, env, &out, in.Harden); err != nil {
			return nil, actions.Failed("The existing server could not be adopted.", err.Error())
		}
		return map[string]any{"managed": true}, nil
	}})

	r.Register(actions.Action{Name: "pg.service", Mutating: true, Description: "Start, stop, restart or reload the PostgreSQL service.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Verb string `json:"verb"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		switch in.Verb {
		case "start", "stop", "restart", "reload":
		default:
			return nil, actions.InvalidParams("verb: choose start, stop, restart or reload.")
		}
		if !serverBinary(env) {
			return nil, actions.Conflict("PostgreSQL is not installed.")
		}
		if err := env.Systemctl(ctx, in.Verb, ServiceUnit); err != nil {
			return nil, actions.Failed("PostgreSQL could not be "+in.Verb+"ed.", err.Error())
		}
		return status(ctx, env), nil
	}})

	r.Register(actions.Action{Name: "pg.test", Description: "Test a connection to the local server or, with a target, to a registered remote server (TLS settings included).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in targetIn
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		var out map[string]any
		err := withSession(ctx, env, in.Target, "postgres", func(s *session) error {
			var ver string
			if err := s.QueryRow(ctx, `SHOW server_version`).Scan(&ver); err != nil {
				return err
			}
			out = map[string]any{"ok": true, "version": ver, "superuser": s.super}
			return nil
		})
		return out, err
	}})

	r.Register(actions.Action{Name: "pg.overview", Description: "The server's databases (with sizes and connections) and database users.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in targetIn
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		var out map[string]any
		err := withSession(ctx, env, in.Target, "postgres", func(s *session) error {
			dbs, err := s.databases(ctx)
			if err != nil {
				return err
			}
			roles, err := s.roles(ctx)
			if err != nil {
				return err
			}
			var ver string
			_ = s.QueryRow(ctx, `SHOW server_version`).Scan(&ver)
			var maxc, active int
			_ = s.QueryRow(ctx, `SELECT current_setting('max_connections')::int, (SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'client backend')`).Scan(&maxc, &active)
			out = map[string]any{"databases": dbs, "roles": roles, "version": ver, "max_connections": maxc, "active_connections": active}
			return nil
		})
		return out, err
	}})

	r.Register(actions.Action{Name: "pg.role_create", Mutating: true, Description: "Create a database user: login only, no superuser, database or role creation, replication or row-security bypass.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			targetIn
			Name      string `json:"name"`
			Password  string `json:"password"`
			ConnLimit int    `json:"conn_limit"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := ValidName("name", in.Name); err != nil {
			return nil, err
		}
		if err := ValidPassword("password", in.Password); err != nil {
			return nil, err
		}
		if in.ConnLimit == 0 {
			in.ConnLimit = -1
		}
		if in.ConnLimit < -1 || in.ConnLimit > 10000 {
			return nil, actions.InvalidParams("conn_limit: -1 (no limit) to 10000.")
		}
		err := withSession(ctx, env, in.Target, "postgres", func(s *session) error {
			return s.createRole(ctx, in.Name, in.Password, in.ConnLimit)
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"name": in.Name}, nil
	}})

	r.Register(actions.Action{Name: "pg.role_password", Mutating: true, Description: "Change a database user's password.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			targetIn
			Name     string `json:"name"`
			Password string `json:"password"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := ValidName("name", in.Name); err != nil {
			return nil, err
		}
		if err := ValidPassword("password", in.Password); err != nil {
			return nil, err
		}
		return map[string]any{"name": in.Name}, withSession(ctx, env, in.Target, "postgres", func(s *session) error {
			return s.setPassword(ctx, in.Name, in.Password)
		})
	}})

	r.Register(actions.Action{Name: "pg.role_delete", Mutating: true, Description: "Delete a database user and its privileges. Refuses while the user owns a database.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			targetIn
			Name string `json:"name"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := ValidName("name", in.Name); err != nil {
			return nil, err
		}
		return map[string]any{"name": in.Name}, withSession(ctx, env, in.Target, "postgres", func(s *session) error {
			return s.dropRole(ctx, env, in.Target, in.Name)
		})
	}})

	r.Register(actions.Action{Name: "pg.database_create", Mutating: true, Description: "Create a database owned by an existing database user; PUBLIC is stripped of access, so only the owner and users granted later can connect.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			targetIn
			Name     string `json:"name"`
			Owner    string `json:"owner"`
			Encoding string `json:"encoding"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := ValidName("name", in.Name); err != nil {
			return nil, err
		}
		if err := ValidName("owner", in.Owner); err != nil {
			return nil, err
		}
		if in.Encoding != "" && in.Encoding != "UTF8" && in.Encoding != "LATIN1" && in.Encoding != "SQL_ASCII" {
			return nil, actions.InvalidParams("encoding: choose UTF8 (recommended), LATIN1 or SQL_ASCII.")
		}
		return map[string]any{"name": in.Name}, withSession(ctx, env, in.Target, "postgres", func(s *session) error {
			return s.createDatabase(ctx, env, in.Target, in.Name, in.Owner, in.Encoding)
		})
	}})

	r.Register(actions.Action{Name: "pg.database_delete", Mutating: true, Description: "Delete a database (connections to it are ended). Deleting one that is gone is not an error.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			targetIn
			Name string `json:"name"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := ValidName("name", in.Name); err != nil {
			return nil, err
		}
		return map[string]any{"name": in.Name}, withSession(ctx, env, in.Target, "postgres", func(s *session) error {
			return s.dropDatabase(ctx, in.Name)
		})
	}})

	r.Register(actions.Action{Name: "pg.grant_set", Mutating: true, Description: "Set what a database user may do in a database: none, readonly, readwrite or all (existing objects and ones created later).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			targetIn
			Database string `json:"database"`
			Role     string `json:"role"`
			Level    Level  `json:"level"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := ValidName("database", in.Database); err != nil {
			return nil, err
		}
		if err := ValidName("role", in.Role); err != nil {
			return nil, err
		}
		if err := ValidLevel("level", in.Level); err != nil {
			return nil, err
		}
		return map[string]any{"database": in.Database, "role": in.Role, "level": in.Level}, setLevel(ctx, env, in.Target, in.Database, in.Role, in.Level)
	}})

	r.Register(actions.Action{Name: "pg.grant_list", Description: "The database users that may connect to a database (besides its owner).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			targetIn
			Database string `json:"database"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := ValidName("database", in.Database); err != nil {
			return nil, err
		}
		var users []string
		err := withSession(ctx, env, in.Target, "postgres", func(s *session) error {
			var e error
			users, e = s.grantsOf(ctx, in.Database)
			return e
		})
		return map[string]any{"database": in.Database, "users": users}, err
	}})
}

func waitReady(ctx context.Context, env *host.Env) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if out, err := env.Runner.Run(ctx, "pg_isready", "-h", "127.0.0.1", "-p", "5432", "-t", "3"); err == nil && strings.Contains(string(out), "accepting") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("PostgreSQL did not become ready within a minute")
}
