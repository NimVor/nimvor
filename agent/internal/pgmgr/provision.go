package pgmgr

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Database is one database of the server.
type Database struct {
	Name        string `json:"name"`
	Owner       string `json:"owner"`
	Encoding    string `json:"encoding"`
	SizeBytes   int64  `json:"size_bytes"`
	Connections int    `json:"connections"`
	System      bool   `json:"system"`
}

// Role is one database user (a PostgreSQL role that can log in) or group.
type Role struct {
	Name      string `json:"name"`
	CanLogin  bool   `json:"can_login"`
	Super     bool   `json:"superuser"`
	ConnLimit int    `json:"conn_limit"`
	System    bool   `json:"system"`
}

func isSystemRole(n string) bool {
	return n == "postgres" || n == AdminRole || strings.HasPrefix(n, "pg_") || strings.HasPrefix(n, "nimvor_")
}

func (s *session) databases(ctx context.Context) ([]Database, error) {
	rows, err := s.Query(ctx, `
		SELECT d.datname, pg_get_userbyid(d.datdba), pg_encoding_to_char(d.encoding),
		       CASE WHEN has_database_privilege(d.datname, 'CONNECT') THEN pg_database_size(d.datname) ELSE 0 END,
		       (SELECT count(*) FROM pg_stat_activity a WHERE a.datname = d.datname)
		FROM pg_database d WHERE NOT d.datistemplate ORDER BY d.datname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Database{}
	for rows.Next() {
		var d Database
		if err := rows.Scan(&d.Name, &d.Owner, &d.Encoding, &d.SizeBytes, &d.Connections); err != nil {
			return nil, err
		}
		d.System = d.Name == "postgres" || isSystemRole(d.Owner) && d.Owner != AdminRole
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *session) roles(ctx context.Context) ([]Role, error) {
	rows, err := s.Query(ctx, `SELECT rolname, rolcanlogin, rolsuper, rolconnlimit FROM pg_roles ORDER BY rolname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.Name, &r.CanLogin, &r.Super, &r.ConnLimit); err != nil {
			return nil, err
		}
		r.System = isSystemRole(r.Name)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *session) roleExists(ctx context.Context, name string) (bool, error) {
	var n int
	err := s.QueryRow(ctx, `SELECT count(*) FROM pg_roles WHERE rolname = $1`, name).Scan(&n)
	return n > 0, err
}

func (s *session) databaseExists(ctx context.Context, name string) (bool, error) {
	var n int
	err := s.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname = $1`, name).Scan(&n)
	return n > 0, err
}

// createRole makes a login role that can do nothing but connect where it is granted: no superuser, database
// creation, role creation, replication or row-security bypass.
func (s *session) createRole(ctx context.Context, name, password string, connLimit int) error {
	if ok, err := s.roleExists(ctx, name); err != nil {
		return err
	} else if ok {
		return actions.Conflict("A database user named " + name + " already exists on this server.")
	}
	if err := s.execf(ctx, "CREATE ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS INHERIT CONNECTION LIMIT %s PASSWORD %L",
		name, fmt.Sprint(connLimit), password); err != nil {
		return err
	}
	return s.adminMembership(ctx, name)
}

// adminMembership lets a provisioning account that is not a superuser act as the new role (SET ROLE), which it
// needs to grant on objects the role owns. A superuser needs nothing.
func (s *session) adminMembership(ctx context.Context, role string) error {
	if s.super {
		return nil
	}
	if s.version >= 160000 {
		return s.execf(ctx, "GRANT %I TO CURRENT_USER WITH SET TRUE, INHERIT TRUE", role)
	}
	return s.execf(ctx, "GRANT %I TO CURRENT_USER", role)
}

func (s *session) setPassword(ctx context.Context, name, password string) error {
	if ok, err := s.roleExists(ctx, name); err != nil {
		return err
	} else if !ok {
		return actions.NotFound("There is no database user " + name + ".")
	}
	return s.execf(ctx, "ALTER ROLE %I PASSWORD %L", name, password)
}

// createDatabase makes a database owned by owner that no one else can enter: PUBLIC loses CONNECT and the
// public schema, so isolation does not depend on pg_hba.conf.
func (s *session) createDatabase(ctx context.Context, env *host.Env, t *Target, name, owner, encoding string) error {
	if ok, err := s.roleExists(ctx, owner); err != nil {
		return err
	} else if !ok {
		return actions.NotFound("There is no database user " + owner + " to own the database.")
	}
	if ok, err := s.databaseExists(ctx, name); err != nil {
		return err
	} else if ok {
		return actions.Conflict("A database named " + name + " already exists on this server.")
	}
	if encoding == "" {
		encoding = "UTF8"
	}
	if err := s.execf(ctx, "CREATE DATABASE %I OWNER %I TEMPLATE template0 ENCODING %L", name, owner, encoding); err != nil {
		return err
	}
	if err := s.execf(ctx, "REVOKE ALL ON DATABASE %I FROM PUBLIC", name); err != nil {
		return err
	}
	// The public schema of the new database: only the owner uses it.
	d, err := open(ctx, env, t, name)
	if err != nil {
		return err
	}
	defer d.Close(ctx)
	if err := d.execf(ctx, "REVOKE ALL ON SCHEMA public FROM PUBLIC"); err != nil {
		return err
	}
	return d.execf(ctx, "ALTER SCHEMA public OWNER TO %I", owner)
}

func (s *session) dropDatabase(ctx context.Context, name string) error {
	if ok, err := s.databaseExists(ctx, name); err != nil {
		return err
	} else if !ok {
		return nil // already gone: deleting twice is not an error
	}
	// Connections to it end first; FORCE (PostgreSQL 13+) does that too, this also covers older servers.
	if _, err := s.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name); err != nil {
		return err
	}
	var err error
	if s.version >= 130000 {
		err = s.execf(ctx, "DROP DATABASE IF EXISTS %I WITH (FORCE)", name)
	} else {
		err = s.execf(ctx, "DROP DATABASE IF EXISTS %I", name)
	}
	if err != nil {
		return err
	}
	s.dropQueryAccounts(ctx, name)
	return nil
}

// dropRole removes a role everywhere. It refuses while the role still owns a database, so a customer's data is
// never dropped as a side effect of removing a user.
func (s *session) dropRole(ctx context.Context, env *host.Env, t *Target, name string) error {
	if ok, err := s.roleExists(ctx, name); err != nil {
		return err
	} else if !ok {
		return nil
	}
	var owned []string
	rows, err := s.Query(ctx, `SELECT datname FROM pg_database WHERE datdba = (SELECT oid FROM pg_roles WHERE rolname = $1)`, name)
	if err != nil {
		return err
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		owned = append(owned, n)
	}
	rows.Close()
	if len(owned) > 0 {
		return actions.Conflict("The database user " + name + " still owns the database " + strings.Join(owned, ", ") + ". Delete the database or give it to another user first.")
	}
	dbs, err := s.databases(ctx)
	if err != nil {
		return err
	}
	for _, d := range dbs {
		c, err := open(ctx, env, t, d.Name)
		if err != nil {
			continue // a database the account cannot enter has no grants of this role to remove
		}
		_ = c.execf(ctx, "DROP OWNED BY %I", name)
		c.Close(ctx)
	}
	return s.execf(ctx, "DROP ROLE IF EXISTS %I", name)
}

// userSchemas lists the schemas of the connected database a role can be granted on.
func (s *session) userSchemas(ctx context.Context) ([]string, error) {
	rows, err := s.Query(ctx, `SELECT nspname FROM pg_namespace WHERE nspname NOT LIKE 'pg\_%' AND nspname <> 'information_schema' ORDER BY nspname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// setLevel gives role the privileges of level on the database db (connected as d): existing objects and, through
// default privileges of the database owner, the ones created later.
func setLevel(ctx context.Context, env *host.Env, t *Target, db, role string, level Level) error {
	d, err := open(ctx, env, t, db)
	if err != nil {
		return err
	}
	defer d.Close(ctx)
	var owner string
	if err := d.QueryRow(ctx, `SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = $1`, db).Scan(&owner); err != nil {
		if err == pgx.ErrNoRows {
			return actions.NotFound("There is no database " + db + ".")
		}
		return err
	}
	if role == owner {
		// The owner always has everything; nothing to grant or take away (revoking would remove its own rights).
		if level != LevelAll {
			return actions.Conflict("The owner of a database always has full access to it.")
		}
		return nil
	}
	if ok, err := d.roleExists(ctx, role); err != nil {
		return err
	} else if !ok {
		return actions.NotFound("There is no database user " + role + ".")
	}
	// A provisioning account that is not a superuser acts as the owner for the grants (it must be allowed to).
	if !d.super {
		if err := d.execf(ctx, "SET ROLE %I", owner); err != nil {
			return actions.Failed("The provisioning account cannot act for the database owner.", err.Error())
		}
		defer func() { _, _ = d.Exec(ctx, "RESET ROLE") }()
	}
	schemas, err := d.userSchemas(ctx)
	if err != nil {
		return err
	}
	ownerQ := strings.ReplaceAll(Quote(owner), "%", "%%") // goes into a format() template
	// Reset first, so lowering a level removes what the higher one gave.
	for _, sc := range schemas {
		for _, stmt := range []string{
			"REVOKE ALL ON ALL TABLES IN SCHEMA %I FROM %I",
			"REVOKE ALL ON ALL SEQUENCES IN SCHEMA %I FROM %I",
			"REVOKE ALL ON ALL FUNCTIONS IN SCHEMA %I FROM %I",
			"REVOKE ALL ON SCHEMA %I FROM %I",
			"ALTER DEFAULT PRIVILEGES FOR ROLE " + ownerQ + " IN SCHEMA %I REVOKE ALL ON TABLES FROM %I",
			"ALTER DEFAULT PRIVILEGES FOR ROLE " + ownerQ + " IN SCHEMA %I REVOKE ALL ON SEQUENCES FROM %I",
			"ALTER DEFAULT PRIVILEGES FOR ROLE " + ownerQ + " IN SCHEMA %I REVOKE ALL ON FUNCTIONS FROM %I",
		} {
			if err := d.execf(ctx, stmt, sc, role); err != nil {
				return err
			}
		}
	}
	if err := d.execf(ctx, "REVOKE ALL ON DATABASE %I FROM %I", db, role); err != nil {
		return err
	}
	if level == LevelNone {
		return nil
	}
	if err := d.execf(ctx, "GRANT CONNECT, TEMPORARY ON DATABASE %I TO %I", db, role); err != nil {
		return err
	}
	for _, sc := range schemas {
		var schemaPriv, tablePriv, seqPriv string
		switch level {
		case LevelReadOnly:
			schemaPriv, tablePriv, seqPriv = "USAGE", "SELECT", "SELECT"
		case LevelReadWrite:
			schemaPriv, tablePriv, seqPriv = "USAGE", "SELECT, INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER", "USAGE, SELECT, UPDATE"
		case LevelAll:
			schemaPriv, tablePriv, seqPriv = "USAGE, CREATE", "ALL", "ALL"
		}
		for _, stmt := range []string{
			"GRANT " + schemaPriv + " ON SCHEMA %I TO %I",
			"GRANT " + tablePriv + " ON ALL TABLES IN SCHEMA %I TO %I",
			"GRANT " + seqPriv + " ON ALL SEQUENCES IN SCHEMA %I TO %I",
			"ALTER DEFAULT PRIVILEGES FOR ROLE " + ownerQ + " IN SCHEMA %I GRANT " + tablePriv + " ON TABLES TO %I",
			"ALTER DEFAULT PRIVILEGES FOR ROLE " + ownerQ + " IN SCHEMA %I GRANT " + seqPriv + " ON SEQUENCES TO %I",
		} {
			if err := d.execf(ctx, stmt, sc, role); err != nil {
				return err
			}
		}
	}
	return nil
}

// grantsOf lists who may connect to a database, besides its owner: the roles with CONNECT in its ACL.
func (s *session) grantsOf(ctx context.Context, db string) ([]string, error) {
	rows, err := s.Query(ctx, `
		SELECT DISTINCT pg_get_userbyid(a.grantee) FROM pg_database d, aclexplode(d.datacl) a
		WHERE d.datname = $1 AND a.privilege_type = 'CONNECT' AND a.grantee <> 0 AND a.grantee <> d.datdba
		ORDER BY 1`, db)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
