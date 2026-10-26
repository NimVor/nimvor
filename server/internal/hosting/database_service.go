package hosting

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

func errorsIsNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// AgentCaller is the subset of the agent client the hosting services need. The
// real *agent.Client satisfies it; tests supply a fake over a Unix socket.
type AgentCaller interface {
	Call(ctx context.Context, action string, params any, timeout ...time.Duration) (json.RawMessage, error)
}

// reservedDBNames can never be used for a managed database.
var reservedDBNames = map[string]bool{
	"mysql": true, "sys": true, "information_schema": true, "performance_schema": true,
}

var dbNameRe = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
var prefixStripRe = regexp.MustCompile(`[^a-z0-9_]`)

// NameTaken reports whether a managed database name is taken on a server.
func (r *DatabaseRepository) NameTaken(ctx context.Context, serverID int64, name string) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx, "SELECT 1 FROM databases WHERE server_id = ? AND name = ?", serverID, name).Scan(&one)
	if errorsIsNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// InsertDatabase records a created database and returns its id.
func (r *DatabaseRepository) InsertDatabase(ctx context.Context, serverID int64, name string, websiteID, ownerID *int64) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		"INSERT INTO databases (server_id, website_id, owner_id, name, created_at) VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'))",
		serverID, websiteID, ownerID, name)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DatabaseService creates databases and database users via the agent,
// enforcing validation, name prefixing per tenant, plan limits and the
// remote-hosts setting. Ported from Services\DatabaseService (create, drop,
// createUser, addHost, resetPassword, revealPassword, grant, dropUser,
// hostFrom, remoteHostsAllowed).
type DatabaseService struct {
	db       *DatabaseRepository
	sites    *WebsiteRepository
	limits   *PlanLimits
	agent    AgentCaller
	crypto   *crypto.Crypto
	settings *settings.Repository
	auditlog func(action, targetType, target string, details map[string]any)
}

// NewDatabaseService builds the service. crypto seals stored passwords
// (nil: storing is refused); settings supplies db_customer_remote_hosts
// (nil: remote hosts allowed, the PHP default); auditlog records a
// privileged action (the interim structured hook for the audit_logs row).
func NewDatabaseService(db *DatabaseRepository, sites *WebsiteRepository, limits *PlanLimits, agent AgentCaller, crypto *crypto.Crypto, settings *settings.Repository, auditlog func(action, targetType, target string, details map[string]any)) *DatabaseService {
	return &DatabaseService{db: db, sites: sites, limits: limits, agent: agent, crypto: crypto, settings: settings, auditlog: auditlog}
}

// prefix returns the per-tenant name prefix: staff get none; an own-scope
// account gets "<sanitized-username(≤16)>_". Mirrors DatabaseService::prefix.
func prefix(u *identity.User) string {
	if u == nil || u.RoleScope == "all" {
		return ""
	}
	p := prefixStripRe.ReplaceAllString(strings.ToLower(u.Username), "")
	if len(p) > 16 {
		p = p[:16]
	}
	return p + "_"
}

// CreateDatabase validates and creates a database. Returns the new id. Errors
// are *ValidationError (bad form), *UserError (friendly failure, incl. plan
// limit or an agent failure), or a plain error (unexpected). Mirrors the
// database branch of DatabaseService::create (without the optional user).
func (s *DatabaseService) CreateDatabase(ctx context.Context, serverID int64, actor *identity.User, rawName string, websiteID *int64) (int64, error) {
	name := prefix(actor) + strings.ToLower(strings.TrimSpace(rawName))

	ve := &ValidationError{}
	if !dbNameRe.MatchString(name) || reservedDBNames[name] {
		ve.Add("name", "Use up to 64 lowercase letters, digits or underscores.")
	} else if taken, err := s.db.NameTaken(ctx, serverID, name); err != nil {
		return 0, err
	} else if taken {
		ve.Add("name", "A database with this name already exists.")
	}
	if websiteID != nil {
		// The website must be one the actor may see (tenant check).
		if _, err := s.sites.Find(ctx, serverID, *websiteID, actor); err != nil {
			ve.Add("website_id", "Choose one of your websites.")
		}
	}
	if ve.Any() {
		return 0, ve
	}

	// Owner: for staff, the website's owner (if attached), else unassigned;
	// for an own-scope account, themselves. Mirrors the $ownerId computation.
	var ownerID *int64
	if IsAll(actor) {
		if websiteID != nil {
			if site, err := s.sites.Find(ctx, serverID, *websiteID, nil); err == nil && site.OwnerID.Valid {
				o := site.OwnerID.Int64
				ownerID = &o
			}
		}
	} else {
		o := actor.ID
		ownerID = &o
	}

	if err := s.limits.AssertCanCreate(ctx, "databases", ownerID, 1); err != nil {
		return 0, err // *UserError
	}

	// Mutating agent action (the agent enforces Mutating + the write gate).
	if _, err := s.agent.Call(ctx, "db.create", map[string]any{"name": name}); err != nil {
		return 0, &UserError{
			What: "The database could not be created on the server.",
			Why:  err.Error(),
			Fix:  "Check the database service is running and try again.",
		}
	}

	id, err := s.db.InsertDatabase(ctx, serverID, name, websiteID, ownerID)
	if err != nil {
		return 0, err
	}
	if s.auditlog != nil {
		s.auditlog("database.create", "database", name, nil)
	}
	return id, nil
}

// CreateWithUser is CreateDatabase plus its dedicated user — the
// app-installer's DatabaseService::create with create_user + store_password:
// the database, a localhost user granted all on it (the password sealed and
// stored), both audited. Returns the database id, the (prefixed) name, the
// username and the plaintext password (stored sealed, shown once).
func (s *DatabaseService) CreateWithUser(ctx context.Context, serverID int64, actor *identity.User, name, username string, websiteID *int64) (dbID int64, dbName, user, password string, err error) {
	return s.CreateWithUserStore(ctx, serverID, actor, name, username, websiteID, true)
}

// CreateWithUserStore is CreateWithUser with the choice of keeping the
// password (sealed) so it can be shown again; the form leaves it off by default.
func (s *DatabaseService) CreateWithUserStore(ctx context.Context, serverID int64, actor *identity.User, name, username string, websiteID *int64, store bool) (dbID int64, dbName, user, password string, err error) {
	pfx := prefix(actor)
	rawName := strings.ToLower(strings.TrimSpace(name))
	name = pfx + rawName
	rawUser := strings.ToLower(strings.TrimSpace(username))
	if rawUser == "" {
		rawUser = rawName[:min(16, len(rawName))]
	}
	username = pfx + rawUser
	ve := &ValidationError{}
	if !dbNameRe.MatchString(name) || reservedDBNames[name] {
		ve.Add("name", "Use up to 64 lowercase letters, digits or underscores.")
	} else if taken, e := s.db.NameTaken(ctx, serverID, name); e != nil {
		return 0, "", "", "", e
	} else if taken {
		ve.Add("name", "A database with this name already exists.")
	}
	if !dbUsernameRe.MatchString(username) {
		ve.Add("username", "Use up to 32 lowercase letters, digits or underscores (not already used).")
	} else if taken, e := s.db.DBUserTaken(ctx, serverID, username, "localhost"); e != nil {
		return 0, "", "", "", e
	} else if taken {
		ve.Add("username", "Use up to 32 lowercase letters, digits or underscores (not already used).")
	}
	if websiteID != nil {
		if _, e := s.sites.Find(ctx, serverID, *websiteID, actor); e != nil {
			ve.Add("website_id", "Choose one of your websites.")
		}
	}
	if ve.Any() {
		return 0, "", "", "", ve
	}

	var ownerID *int64
	if IsAll(actor) {
		if websiteID != nil {
			if site, e := s.sites.Find(ctx, serverID, *websiteID, nil); e == nil && site.OwnerID.Valid {
				o := site.OwnerID.Int64
				ownerID = &o
			}
		}
	} else {
		o := actor.ID
		ownerID = &o
	}
	if err := s.limits.AssertCanCreate(ctx, "databases", ownerID, 1); err != nil {
		return 0, "", "", "", err
	}

	if _, err := s.agent.Call(ctx, "db.create", map[string]any{"name": name}); err != nil {
		return 0, "", "", "", &UserError{
			What: "The database could not be created on the server.",
			Why:  err.Error(),
			Fix:  "Check the database service is running and try again.",
		}
	}
	id, err := s.db.InsertDatabase(ctx, serverID, name, websiteID, ownerID)
	if err != nil {
		return 0, "", "", "", err
	}
	if s.auditlog != nil {
		s.auditlog("database.create", "database", name, nil)
	}
	password, err = cryptoPassword(24)
	if err != nil {
		return 0, "", "", "", err
	}
	if _, err := s.agent.Call(ctx, "db.user_create", map[string]any{"user": username, "host": "localhost", "password": password}); err != nil {
		return 0, "", "", "", &UserError{
			What: "The database was created, but its user could not be: " + err.Error(),
			Fix:  "Create the user from the Users tab.",
		}
	}
	if _, err := s.agent.Call(ctx, "db.grant", map[string]any{"user": username, "host": "localhost", "database": name, "privileges": "all"}); err != nil {
		return 0, "", "", "", &UserError{
			What: "The database was created, but its user could not be: " + err.Error(),
			Fix:  "Create the user from the Users tab.",
		}
	}
	enc := ""
	if store {
		enc, err = s.crypto.Encrypt([]byte(password))
		if err != nil {
			return 0, "", "", "", err
		}
	}
	uid, err := s.db.InsertDBUser(ctx, serverID, username, "localhost", ownerID, enc)
	if err != nil {
		return 0, "", "", "", err
	}
	all := "all"
	if err := s.db.SetDBGrant(ctx, id, uid, &all); err != nil {
		return 0, "", "", "", err
	}
	if s.auditlog != nil {
		s.auditlog("database.user_create", "database_user", username, map[string]any{"database": name, "stored": store})
	}
	return id, name, username, password, nil
}

// cryptoPassword is the PHP's Crypto::password: n characters from the
// unambiguous alphabet.
func cryptoPassword(n int) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789-_.!@#%+="
	buf := make([]byte, n)
	for i := range buf {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		buf[i] = alphabet[j.Int64()]
	}
	return string(buf), nil
}

// DropDatabase deletes a database the actor may see. The typed confirm must
// equal the database name (guards against accidental drops). It calls the
// destructive db.drop agent action, removes the record, and audits. Returns
// the dropped database's name. Mirrors DatabaseService::drop + the controller's
// scoped lookup. Errors are *UserError (bad confirm / agent failure / not
// found) or a plain error.
func (s *DatabaseService) DropDatabase(ctx context.Context, serverID int64, actor *identity.User, id int64, confirm string) (string, error) {
	db, err := s.db.Find(ctx, serverID, id, actor)
	if err != nil {
		return "", err // ErrDatabaseNotFound (out of tenant) or a real error
	}
	if confirm != db.Name {
		return "", &UserError{What: "Type the database name to confirm.", Fix: "Enter the exact database name."}
	}
	if _, err := s.agent.Call(ctx, "db.drop", map[string]any{"name": db.Name, "confirm": db.Name}); err != nil {
		return "", &UserError{
			What: "The database could not be dropped on the server.",
			Why:  err.Error(),
			Fix:  "Check the database service is running and try again.",
		}
	}
	if err := s.db.DeleteDatabase(ctx, db.ID); err != nil {
		return "", err
	}
	if s.auditlog != nil {
		s.auditlog("database.drop", "database", db.Name, nil)
	}
	return db.Name, nil
}

// DBSize is one schema's live size (an agent db.overview row).
type DBSize struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"size_bytes"`
	Tables int    `json:"tables"`
}

// DBOverview is the live server state (the agent's db.overview reply).
type DBOverview struct {
	Version   string   `json:"version"`
	Databases []DBSize `json:"databases"`
}

// Live returns the live server state (per-database sizes and table counts).
// When the database server cannot be queried (the agent call fails) it
// returns (nil, nil) — the view then shows a warning and falls back to the
// WhaCP records. Mirrors DatabaseService::live.
func (s *DatabaseService) Live(ctx context.Context) (*DBOverview, error) {
	res, err := s.agent.Call(ctx, "db.overview", nil)
	if err != nil {
		return nil, nil
	}
	var out DBOverview
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
