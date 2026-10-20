package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
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

// DatabaseService creates databases via the agent, enforcing validation, name
// prefixing per tenant, and plan limits. Ported from the core of
// DatabaseService::create (the optional db-user/grant sub-flow is a later
// batch; see MIGRATION-STATUS.md).
type DatabaseService struct {
	db       *DatabaseRepository
	sites    *WebsiteRepository
	limits   *PlanLimits
	agent    AgentCaller
	auditlog func(action, target string)
}

// NewDatabaseService builds the service. auditlog records a privileged action
// (the DB audit_logs row is ported in the audit batch; this is the interim
// structured-log hook).
func NewDatabaseService(db *DatabaseRepository, sites *WebsiteRepository, limits *PlanLimits, agent AgentCaller, auditlog func(action, target string)) *DatabaseService {
	return &DatabaseService{db: db, sites: sites, limits: limits, agent: agent, auditlog: auditlog}
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
		s.auditlog("database.create", name)
	}
	return id, nil
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
		s.auditlog("database.drop", db.Name)
	}
	return db.Name, nil
}
