package pgmgr

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Customer SQL (the SQL editor and the table browser's edits) never runs in a session whose login is the
// provisioning role: a session can always RESET ROLE back to its login, and a customer's trigger, function or script
// would then act as nimvor_admin, which may alter every customer's users. Each database therefore gets its own
// dedicated login accounts, created on first use, with nothing but what the owner (or a reader) may do:
//
//	nimvor_q_<h>   LOGIN, member of the database owner: runs the editor in write mode and the browser's edits
//	nimvor_qr_<h>  LOGIN, member of nimvor_ro_<h>: runs read-only queries
//	nimvor_ro_<h>  NOLOGIN group holding only SELECT on the database's objects
//
// <h> is a hash of the database name (names can be 63 characters). The passwords are derived from the provisioning
// password with HMAC, so nothing more is stored and rotating the provisioning password rotates them all.

func dbHash(database string) string {
	h := sha256.Sum256([]byte("nimvor-pg-db:" + database))
	return hex.EncodeToString(h[:])[:12]
}

type queryNames struct{ writer, reader, group string }

func namesFor(database string) queryNames {
	h := dbHash(database)
	return queryNames{writer: "nimvor_q_" + h, reader: "nimvor_qr_" + h, group: "nimvor_ro_" + h}
}

func derivedPassword(secret, role string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("nimvor-pg-query:" + role))
	return hex.EncodeToString(m.Sum(nil))[:40]
}

// ensureRole creates a login role (or re-keys it with its derived password) with no privileges of its own.
func (s *session) ensureLogin(ctx context.Context, role string) error {
	pw := derivedPassword(s.target.Password, role)
	ok, err := s.roleExists(ctx, role)
	if err != nil {
		return err
	}
	if !ok {
		if err := s.execf(ctx, "CREATE ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 5 PASSWORD %L", role, pw); err != nil {
			return err
		}
		return s.adminMembership(ctx, role)
	}
	return s.execf(ctx, "ALTER ROLE %I PASSWORD %L", role, pw)
}

func (s *session) isMember(ctx context.Context, role, of string) (bool, error) {
	var n int
	err := s.QueryRow(ctx, `SELECT count(*) FROM pg_auth_members m WHERE m.roleid = (SELECT oid FROM pg_roles WHERE rolname = $1) AND m.member = (SELECT oid FROM pg_roles WHERE rolname = $2)`, of, role).Scan(&n)
	return n > 0, err
}

// ensureQueryAccounts makes sure the accounts for one database exist and are wired to the owner or the reader group.
func (s *session) ensureQueryAccounts(ctx context.Context, env *host.Env, t *Target, database string, readOnly bool) (queryNames, error) {
	n := namesFor(database)
	var owner string
	if err := s.QueryRow(ctx, `SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = $1`, database).Scan(&owner); err != nil {
		return n, actions.NotFound("There is no database " + database + ".")
	}
	if isSystemRole(owner) {
		return n, actions.Conflict("This database belongs to the server itself and cannot be opened here.")
	}
	if !readOnly {
		if err := s.ensureLogin(ctx, n.writer); err != nil {
			return n, err
		}
		if ok, err := s.isMember(ctx, n.writer, owner); err != nil {
			return n, err
		} else if !ok {
			if s.version >= 160000 {
				err = s.execf(ctx, "GRANT %I TO %I WITH INHERIT TRUE, SET TRUE", owner, n.writer)
			} else {
				err = s.execf(ctx, "GRANT %I TO %I", owner, n.writer)
			}
			if err != nil {
				return n, err
			}
		}
		return n, nil
	}
	created := false
	if ok, err := s.roleExists(ctx, n.group); err != nil {
		return n, err
	} else if !ok {
		if err := s.execf(ctx, "CREATE ROLE %I NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS", n.group); err != nil {
			return n, err
		}
		if err := s.adminMembership(ctx, n.group); err != nil {
			return n, err
		}
		created = true
	}
	if err := s.ensureLogin(ctx, n.reader); err != nil {
		return n, err
	}
	if ok, err := s.isMember(ctx, n.reader, n.group); err != nil {
		return n, err
	} else if !ok {
		if err := s.execf(ctx, "GRANT %I TO %I", n.group, n.reader); err != nil {
			return n, err
		}
	}
	// Existing tables, and schemas and tables created since the last time, are read-only for the group.
	if created || s.readerStale(ctx, env, t, database, n.group) {
		if err := setLevel(ctx, env, t, database, n.group, LevelReadOnly); err != nil {
			return n, err
		}
	}
	return n, nil
}

// readerStale reports whether the read-only group lacks SELECT on something it should have.
func (s *session) readerStale(ctx context.Context, env *host.Env, t *Target, database, group string) bool {
	d, err := open(ctx, env, t, database)
	if err != nil {
		return true
	}
	defer d.Close(ctx)
	var n int
	err = d.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM pg_class c JOIN pg_namespace ns ON ns.oid = c.relnamespace
		        WHERE c.relkind IN ('r','p','v','m','f') AND ns.nspname NOT LIKE 'pg\_%' AND ns.nspname <> 'information_schema'
		          AND NOT has_table_privilege($1, c.oid, 'SELECT'))
		     + (SELECT count(*) FROM pg_namespace ns WHERE ns.nspname NOT LIKE 'pg\_%' AND ns.nspname <> 'information_schema'
		          AND NOT has_schema_privilege($1, ns.oid, 'USAGE'))`, group).Scan(&n)
	return err != nil || n > 0
}

// openQuery connects to a database as its dedicated query account. In write mode the session then acts as the
// database owner (new objects belong to the owner); in read-only mode it can only read. Either way the login is
// an account with no powers beyond that, so nothing the customer runs can reach past their own database.
func openQuery(ctx context.Context, env *host.Env, t *Target, database string, readOnly bool) (*session, error) {
	admin, err := open(ctx, env, t, "postgres")
	if err != nil {
		return nil, err
	}
	names, err := admin.ensureQueryAccounts(ctx, env, t, database, readOnly)
	tgt := admin.target
	var owner string
	if err == nil {
		err = admin.QueryRow(ctx, `SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = $1`, database).Scan(&owner)
	}
	admin.Close(ctx)
	if err != nil {
		return nil, err
	}
	login := names.writer
	if readOnly {
		login = names.reader
	}
	tgt.User, tgt.Password = login, derivedPassword(tgt.Password, login)
	conn, err := connect(ctx, tgt, database)
	if err != nil {
		return nil, err
	}
	s := &session{Conn: conn, target: tgt}
	for _, stmt := range []string{"SET lock_timeout = '3s'", "SET idle_in_transaction_session_timeout = '30s'"} {
		if _, err := s.Exec(ctx, stmt); err != nil {
			s.Close(ctx)
			return nil, err
		}
	}
	if !readOnly {
		if err := s.execf(ctx, "SET ROLE %I", owner); err != nil {
			s.Close(ctx)
			return nil, actions.Failed("The database could not be opened.", friendly(err))
		}
	}
	return s, nil
}

// dropQueryAccounts removes the dedicated accounts of a deleted database.
func (s *session) dropQueryAccounts(ctx context.Context, database string) {
	n := namesFor(database)
	for _, r := range []string{n.writer, n.reader, n.group} {
		if ok, _ := s.roleExists(ctx, r); ok {
			_ = s.execf(ctx, "DROP ROLE IF EXISTS %I", r)
		}
	}
}

var _ = fmt.Sprint
var _ = strings.TrimSpace
