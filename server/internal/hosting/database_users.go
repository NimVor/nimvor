package hosting

// This file ports the database-user half of DatabaseService (createUser,
// addHost, resetPassword, revealPassword, grant, dropUser, hostFrom,
// remoteHostsAllowed) plus the database_users / database_grants repository
// methods the panel needs for them.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"math/big"
	"net"
	"regexp"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// ErrDBUserNotFound is returned when no database user matches within the
// caller's scope (the PHP controller's "User not found." branches).
var ErrDBUserNotFound = errors.New("hosting: database user not found")

// ErrNoStoredDBPassword is returned when a database user has no encrypted
// password kept, so reveal is refused (reset it to get a new one).
var ErrNoStoredDBPassword = errors.New("hosting: no stored database password")

// DBPrivilegeLabels mirrors DatabaseService::PRIVILEGES — the privilege sets
// a database user can be granted on a database.
var DBPrivilegeLabels = map[string]string{
	"all":       "All privileges",
	"readwrite": "Read & write (no GRANT)",
	"readonly":  "Read only",
}

// DBPrivilegeOptions is DBPrivilegeLabels in the PHP definition order, for
// form selects.
var DBPrivilegeOptions = []struct {
	Value string
	Label string
}{
	{"all", "All privileges"},
	{"readwrite", "Read & write (no GRANT)"},
	{"readonly", "Read only"},
}

// DBUser is one database login visible to the caller (the display columns of
// DatabaseRepository::users). PasswordEnc is only populated by the
// single-user lookup; the list never returns it.
type DBUser struct {
	ID          int64
	OwnerID     sql.NullInt64
	Username    string
	Host        string
	Stored      bool
	PasswordEnc string
	CreatedAt   string
}

// Grant is one database a login may use (database_grants joined to the
// database name), DatabaseRepository::grantsOf.
type Grant struct {
	DatabaseID int64
	Name       string
	Privileges string
}

// DBUserGrant is one login on a database — the databases table's "Username"
// column (DatabaseRepository::databases' grants join).
type DBUserGrant struct {
	UserID     int64
	Username   string
	Host       string
	Privileges string
}

var (
	dbUsernameRe = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
	// A customer may give an IP address or an IPv4 network with % wildcards
	// (203.0.113.%); administrators may also use host names and netmasks.
	dbHostNetRe   = regexp.MustCompile(`^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d|%)){0,2}\.%$`)
	dbHostStaffRe = regexp.MustCompile(`^([0-9A-Fa-f.:%/]{1,60}|[A-Za-z0-9.-]{1,60})$`)
)

// Prefix returns the per-tenant database-user name prefix (staff: none; an
// own-scope account: "<sanitized-username(≤16)>_"). Exposed for the form's
// prefix hint. Mirrors DatabaseService::prefix.
func Prefix(u *identity.User) string { return prefix(u) }

// DBUsers lists the database users visible to the user, tenant-scoped.
// Mirrors DatabaseRepository::users.
func (r *DatabaseRepository) DBUsers(ctx context.Context, serverID int64, u *identity.User) ([]DBUser, error) {
	cond, params := Condition(u, "owner_id")
	q := `SELECT id, owner_id, username, host, password_enc != '' AS stored, created_at
	      FROM database_users WHERE server_id = ? AND ` + cond + " ORDER BY username"
	args := append([]any{serverID}, params...)
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DBUser
	for rows.Next() {
		var du DBUser
		if err := rows.Scan(&du.ID, &du.OwnerID, &du.Username, &du.Host, &du.Stored, &du.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, du)
	}
	return out, rows.Err()
}

// FindDBUser returns one database user by id on a server, scoped to the
// caller (a user outside the caller's tenant reads as ErrDBUserNotFound).
// Mirrors DatabaseRepository::user (SELECT *, incl. password_enc).
func (r *DatabaseRepository) FindDBUser(ctx context.Context, serverID, id int64, u *identity.User) (*DBUser, error) {
	cond, params := Condition(u, "owner_id")
	args := append([]any{serverID, id}, params...)
	var du DBUser
	err := r.db.QueryRowContext(ctx,
		`SELECT id, owner_id, username, host, password_enc != '' AS stored, password_enc, created_at
		 FROM database_users WHERE server_id = ? AND id = ? AND `+cond, args...).
		Scan(&du.ID, &du.OwnerID, &du.Username, &du.Host, &du.Stored, &du.PasswordEnc, &du.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDBUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &du, nil
}

// DBUserTaken reports whether a (username, host) login already exists on the
// server. Mirrors DatabaseRepository::userTaken.
func (r *DatabaseRepository) DBUserTaken(ctx context.Context, serverID int64, username, host string) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx,
		"SELECT 1 FROM database_users WHERE server_id = ? AND username = ? AND host = ?",
		serverID, username, host).Scan(&one)
	if errorsIsNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// InsertDBUser records a created login and returns its id. Mirrors
// DatabaseRepository::insertUser.
func (r *DatabaseRepository) InsertDBUser(ctx context.Context, serverID int64, username, host string, ownerID *int64, passwordEnc string) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		"INSERT INTO database_users (server_id, owner_id, username, host, password_enc, created_at) VALUES (?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'))",
		serverID, ownerID, username, host, passwordEnc)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetDBUserPasswordEnc updates (or clears) the stored encrypted password.
// Mirrors DatabaseRepository::setPasswordEnc.
func (r *DatabaseRepository) SetDBUserPasswordEnc(ctx context.Context, id int64, enc string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE database_users SET password_enc = ? WHERE id = ?", enc, id)
	return err
}

// DeleteDBUser removes a login record (its grants cascade). Mirrors
// DatabaseRepository::deleteUser.
func (r *DatabaseRepository) DeleteDBUser(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM database_users WHERE id = ?", id)
	return err
}

// DBGrantsOf lists the databases a login may use. Mirrors
// DatabaseRepository::grantsOf.
func (r *DatabaseRepository) DBGrantsOf(ctx context.Context, userID int64) ([]Grant, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT g.database_id, d.name, g.privileges
		 FROM database_grants g JOIN databases d ON d.id = g.database_id
		 WHERE g.user_id = ? ORDER BY d.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.DatabaseID, &g.Name, &g.Privileges); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetDBGrant sets the privilege set for (database, login); a nil privileges
// revokes it (deletes the row). Mirrors DatabaseRepository::setGrant.
func (r *DatabaseRepository) SetDBGrant(ctx context.Context, databaseID, userID int64, privileges *string) error {
	if privileges == nil {
		_, err := r.db.ExecContext(ctx,
			"DELETE FROM database_grants WHERE database_id = ? AND user_id = ?", databaseID, userID)
		return err
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO database_grants (database_id, user_id, privileges) VALUES (?, ?, ?)
		 ON CONFLICT(database_id, user_id) DO UPDATE SET privileges = excluded.privileges`,
		databaseID, userID, *privileges)
	return err
}

// DBGrantsFor returns, per database id, the logins granted on it (the
// databases table's "Username" column). One query for the whole page —
// DatabaseRepository::databases does the same join per row.
func (r *DatabaseRepository) DBGrantsFor(ctx context.Context, databaseIDs []int64) (map[int64][]DBUserGrant, error) {
	out := map[int64][]DBUserGrant{}
	if len(databaseIDs) == 0 {
		return out, nil
	}
	q := `SELECT g.database_id, g.privileges, u.id, u.username, u.host
	      FROM database_grants g JOIN database_users u ON u.id = g.user_id
	      WHERE g.database_id IN (` + placeholders(len(databaseIDs)) + `) ORDER BY u.username`
	args := make([]any, len(databaseIDs))
	for i, id := range databaseIDs {
		args[i] = id
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g DBUserGrant
		var dbID int64
		if err := rows.Scan(&dbID, &g.Privileges, &g.UserID, &g.Username, &g.Host); err != nil {
			return nil, err
		}
		out[dbID] = append(out[dbID], g)
	}
	return out, rows.Err()
}

// placeholders renders n SQL positional placeholders.
func placeholders(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('?')
	}
	return b.String()
}

// dbPasswordAlphabet mirrors Crypto::$alphabet — printable ASCII without
// quotes, backslashes, spaces, or the ambiguous I/l/O/0/1.
const dbPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789-_.!@#%+="

// dbPassword generates a random database password: 24 characters from the
// unambiguous set (Crypto::password's default).
func dbPassword() (string, error) {
	buf := make([]byte, 24)
	for i := range buf {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(dbPasswordAlphabet))))
		if err != nil {
			return "", err
		}
		buf[i] = dbPasswordAlphabet[n.Int64()]
	}
	return string(buf), nil
}

// RemoteHostsAllowed reports whether the actor may let database users connect
// from other hosts (Settings: db_customer_remote_hosts; administrators
// always may). Mirrors DatabaseService::remoteHostsAllowed.
func (s *DatabaseService) RemoteHostsAllowed(ctx context.Context, actor *identity.User) (bool, error) {
	if IsAll(actor) {
		return true, nil
	}
	if s.settings == nil {
		return true, nil
	}
	return s.settings.Bool(ctx, "db_customer_remote_hosts", true)
}

// DBUserHostFrom resolves the form's "connect from" fields to a host and
// validates it. Mirrors DatabaseService::hostFrom.
func (s *DatabaseService) DBUserHostFrom(hostChoice, hostIP, host string, actor *identity.User) (string, error) {
	var h string
	switch hostChoice {
	case "localhost":
		h = "localhost"
	case "any":
		h = "%"
	case "ip":
		h = strings.TrimSpace(hostIP)
	default:
		// The PHP form's fallback branch: trim((string)($input['host'] ?? 'localhost')).
		if host == "" {
			h = "localhost"
		} else {
			h = strings.TrimSpace(host)
		}
	}
	ok := h == "localhost" || h == "%" || net.ParseIP(h) != nil || dbHostNetRe.MatchString(h)
	if !ok && IsAll(actor) {
		ok = dbHostStaffRe.MatchString(h)
	}
	if !ok {
		msg := "Enter an IP address (e.g. 203.0.113.5) or a network with % (e.g. 203.0.113.%)."
		if IsAll(actor) {
			msg = "Use localhost, an IP address, a network or %."
		}
		return "", &ValidationError{Fields: map[string]string{"host": msg}}
	}
	return h, nil
}

// allowedHost enforces the remote-hosts setting for non-localhost hosts.
// Mirrors DatabaseService::allowedHost.
func (s *DatabaseService) allowedHost(ctx context.Context, host string, actor *identity.User) (string, error) {
	if host != "localhost" {
		ok, err := s.RemoteHostsAllowed(ctx, actor)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", &ValidationError{Fields: map[string]string{
				"host": "Database users may connect from this server only (your hosting provider's setting).",
			}}
		}
	}
	return host, nil
}

// storedPassword seals the password for storage ("" when not kept).
func (s *DatabaseService) storedPassword(store bool, password string) (string, error) {
	if !store {
		return "", nil
	}
	if s.crypto == nil {
		return "", errors.New("hosting: encryption key not configured")
	}
	return s.crypto.Encrypt([]byte(password))
}

// CreateDBUser creates a database login with a generated password (shown
// once; kept encrypted only when store is true). The caller's name prefix is
// applied to the username for non-staff. Mirrors DatabaseService::createUser.
func (s *DatabaseService) CreateDBUser(ctx context.Context, serverID int64, actor *identity.User, rawUsername, host string, store bool) (string, string, error) {
	username := prefix(actor) + strings.ToLower(strings.TrimSpace(rawUsername))
	h, err := s.allowedHost(ctx, host, actor)
	if err != nil {
		return "", "", err
	}
	host = h
	const takenMsg = "Use up to 32 lowercase letters, digits or underscores (not already used)."
	ve := &ValidationError{}
	if !dbUsernameRe.MatchString(username) {
		ve.Add("username", takenMsg)
	} else if taken, err := s.db.DBUserTaken(ctx, serverID, username, host); err != nil {
		return "", "", err
	} else if taken {
		ve.Add("username", takenMsg)
	}
	if ve.Any() {
		return "", "", ve
	}
	password, err := dbPassword()
	if err != nil {
		return "", "", err
	}
	if _, err := s.agent.Call(ctx, "db.user_create", map[string]any{
		"user": username, "host": host, "password": password,
	}); err != nil {
		return "", "", &UserError{
			What: "The database user could not be created on the server.",
			Why:  err.Error(),
			Fix:  "Check the database service is running and try again.",
		}
	}
	var ownerID *int64
	if !IsAll(actor) {
		o := actor.ID
		ownerID = &o
	}
	enc, err := s.storedPassword(store, password)
	if err != nil {
		return "", "", err
	}
	if _, err := s.db.InsertDBUser(ctx, serverID, username, host, ownerID, enc); err != nil {
		return "", "", err
	}
	if s.auditlog != nil {
		s.auditlog("database.user_create", "database_user", username+"@"+host, map[string]any{"stored": store})
	}
	return username, password, nil
}

// AddDBUserHost lets an existing login also connect from another host: a
// separate account (same name, new host) with its own new password and the
// same database access. Returns the login's username and new password.
// Mirrors DatabaseService::addHost. (The PHP leaves an agent failure in the
// copy loop unhandled — a 500; the Go port reports it with the manual-fix
// note instead, since the new login already exists by then.)
func (s *DatabaseService) AddDBUserHost(ctx context.Context, serverID int64, actor *identity.User, id int64, host string, store bool) (string, string, error) {
	u, err := s.db.FindDBUser(ctx, serverID, id, actor)
	if err != nil {
		return "", "", err
	}
	h, err := s.allowedHost(ctx, host, actor)
	if err != nil {
		return "", "", err
	}
	host = h
	if taken, err := s.db.DBUserTaken(ctx, serverID, u.Username, host); err != nil {
		return "", "", err
	} else if taken {
		return "", "", &ValidationError{Fields: map[string]string{
			"host": u.Username + " may already connect from " + host + ".",
		}}
	}
	password, err := dbPassword()
	if err != nil {
		return "", "", err
	}
	if _, err := s.agent.Call(ctx, "db.user_create", map[string]any{
		"user": u.Username, "host": host, "password": password,
	}); err != nil {
		return "", "", &UserError{
			What: "The remote login could not be created on the server.",
			Why:  err.Error(),
			Fix:  "Check the database service is running and try again.",
		}
	}
	enc, err := s.storedPassword(store, password)
	if err != nil {
		return "", "", err
	}
	newID, err := s.db.InsertDBUser(ctx, serverID, u.Username, host, ownerPtr(u.OwnerID), enc)
	if err != nil {
		return "", "", err
	}
	grants, err := s.db.DBGrantsOf(ctx, u.ID)
	if err != nil {
		return "", "", err
	}
	for _, g := range grants {
		if _, err := s.agent.Call(ctx, "db.grant", map[string]any{
			"user": u.Username, "host": host, "database": g.Name,
			"privileges": g.Privileges, "revoke": false,
		}); err != nil {
			return "", "", &UserError{
				What: "The remote login was created, but its database access could not be copied.",
				Why:  err.Error(),
				Fix:  "Assign the databases to " + u.Username + "@" + host + " manually.",
			}
		}
		if err := s.db.SetDBGrant(ctx, g.DatabaseID, newID, &g.Privileges); err != nil {
			return "", "", err
		}
	}
	if s.auditlog != nil {
		s.auditlog("database.user_create", "database_user", u.Username+"@"+host,
			map[string]any{"stored": store, "copied_from": u.Host})
	}
	return u.Username, password, nil
}

// ResetDBUserPassword generates a new password for a login (shown once;
// kept encrypted only when store is true). Returns the login's username and
// the new password. Mirrors DatabaseService::resetPassword.
func (s *DatabaseService) ResetDBUserPassword(ctx context.Context, serverID int64, actor *identity.User, id int64, store bool) (string, string, error) {
	u, err := s.db.FindDBUser(ctx, serverID, id, actor)
	if err != nil {
		return "", "", err
	}
	password, err := dbPassword()
	if err != nil {
		return "", "", err
	}
	if _, err := s.agent.Call(ctx, "db.user_password", map[string]any{
		"user": u.Username, "host": u.Host, "password": password,
	}); err != nil {
		return "", "", &UserError{
			What: "The password could not be changed on the server.",
			Why:  err.Error(),
			Fix:  "Check the database service is running and try again.",
		}
	}
	enc, err := s.storedPassword(store, password)
	if err != nil {
		return "", "", err
	}
	if err := s.db.SetDBUserPasswordEnc(ctx, u.ID, enc); err != nil {
		return "", "", err
	}
	if s.auditlog != nil {
		s.auditlog("database.user_password", "database_user", u.Username+"@"+u.Host, map[string]any{"stored": store})
	}
	return u.Username, password, nil
}

// RevealDBUserPassword decrypts the stored password of a login (audited).
// Returns ErrNoStoredDBPassword when none is kept. Mirrors
// DatabaseService::revealPassword.
func (s *DatabaseService) RevealDBUserPassword(ctx context.Context, serverID int64, actor *identity.User, id int64) (string, string, error) {
	u, err := s.db.FindDBUser(ctx, serverID, id, actor)
	if err != nil {
		return "", "", err
	}
	if u.PasswordEnc == "" {
		return "", "", ErrNoStoredDBPassword
	}
	if s.auditlog != nil {
		s.auditlog("database.password_reveal", "database_user", u.Username+"@"+u.Host, nil)
	}
	plain, err := s.crypto.Decrypt(u.PasswordEnc)
	if err != nil {
		return "", "", &UserError{
			What: "The stored password could not be decrypted.",
			Why:  err.Error(),
			Fix:  "Reset the password to get a new one.",
		}
	}
	return u.Username, string(plain), nil
}

// GrantDBAccess grants — or, with privileges == nil, revokes — a login's
// access to one database (db.grant with the revoke flag). Returns the
// database name and the login's username for the success message. Mirrors
// DatabaseService::grant.
func (s *DatabaseService) GrantDBAccess(ctx context.Context, serverID int64, actor *identity.User, databaseID, userID int64, privileges *string) (string, string, error) {
	db, err := s.db.Find(ctx, serverID, databaseID, actor)
	if err != nil {
		return "", "", err
	}
	u, err := s.db.FindDBUser(ctx, serverID, userID, actor)
	if err != nil {
		return "", "", err
	}
	if privileges != nil && !dbPrivilegeSet(*privileges) {
		return "", "", &ValidationError{Fields: map[string]string{"privileges": "Choose a privilege set."}}
	}
	revoke := privileges == nil
	agentPrivs := "all"
	if privileges != nil {
		agentPrivs = *privileges
	}
	if _, err := s.agent.Call(ctx, "db.grant", map[string]any{
		"user": u.Username, "host": u.Host, "database": db.Name,
		"privileges": agentPrivs, "revoke": revoke,
	}); err != nil {
		return "", "", &UserError{
			What: "The privileges could not be updated on the server.",
			Why:  err.Error(),
			Fix:  "Check the database service is running and try again.",
		}
	}
	if err := s.db.SetDBGrant(ctx, db.ID, u.ID, privileges); err != nil {
		return "", "", err
	}
	if s.auditlog != nil {
		action := "database.grant"
		if revoke {
			action = "database.revoke"
		}
		var pv any
		if privileges != nil {
			pv = *privileges
		}
		s.auditlog(action, "database", db.Name, map[string]any{"user": u.Username, "privileges": pv})
	}
	return db.Name, u.Username, nil
}

// DropDBUser removes a database login (db.user_drop) and its record (grants
// cascade). Mirrors DatabaseService::dropUser.
func (s *DatabaseService) DropDBUser(ctx context.Context, serverID int64, actor *identity.User, id int64) error {
	u, err := s.db.FindDBUser(ctx, serverID, id, actor)
	if err != nil {
		return err
	}
	if _, err := s.agent.Call(ctx, "db.user_drop", map[string]any{
		"user": u.Username, "host": u.Host,
	}); err != nil {
		return &UserError{
			What: "The database user could not be removed on the server.",
			Why:  err.Error(),
			Fix:  "Check the database service is running and try again.",
		}
	}
	if err := s.db.DeleteDBUser(ctx, u.ID); err != nil {
		return err
	}
	if s.auditlog != nil {
		s.auditlog("database.user_drop", "database_user", u.Username+"@"+u.Host, nil)
	}
	return nil
}

func dbPrivilegeSet(p string) bool {
	_, ok := DBPrivilegeLabels[p]
	return ok
}
