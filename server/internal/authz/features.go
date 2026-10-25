// Package authz ports the panel's authorization: role-based permission checks
// and plan feature gating. Enforcement is server-side — a disabled feature
// withholds its permissions and refuses its routes by name; hiding menu
// entries is a consequence, not the mechanism (see FeatureService.php and
// Authorizer.php).
package authz

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// Feature describes one gated capability. Mirrors an entry of
// FeatureService::FEATURES: the permissions it grants and the route-name
// prefixes it owns (routes that share a permission with other features are
// matched by name instead).
type Feature struct {
	Key           string
	Label         string
	Permissions   []string
	RoutePrefixes []string
	Group         string
}

// Features is the registry, keyed like FeatureService::FEATURES. It is the
// authoritative list; keep it in sync with the PHP constant.
var Features = map[string]Feature{
	"php":           {"php", "PHP & website settings", nil, []string{"sites.edit", "sites.update"}, "Websites"},
	"mysql":         {"mysql", "MySQL / MariaDB databases", []string{"databases.view", "databases.manage"}, nil, "Databases"},
	"mongodb":       {"mongodb", "MongoDB databases", nil, []string{"sites.mongo"}, "Databases"},
	"email":         {"email", "Email", []string{"mail.manage"}, nil, "Email"},
	"dns":           {"dns", "DNS zone editor", []string{"dns.manage"}, nil, "Domains"},
	"file_manager":  {"file_manager", "File manager", []string{"files.manage"}, nil, "Files"},
	"ssl":           {"ssl", "SSL / HTTPS", []string{"ssl.view", "ssl.manage"}, nil, "Websites"},
	"subdomains":    {"subdomains", "Subdomains", nil, []string{"sites.subdomains"}, "Domains"},
	"addon_domains": {"addon_domains", "Addon domains", nil, []string{"sites.addons"}, "Domains"},
	"cloudflare":    {"cloudflare", "Cloudflare", nil, []string{"sites.cloudflare"}, "Websites"},
	"ftp":           {"ftp", "FTP / SFTP accounts", []string{"ftp.manage"}, nil, "Files"},
	"cron":          {"cron", "Cron jobs", []string{"cron.view", "cron.manage"}, nil, "Automation"},
	"backups":       {"backups", "Backups", []string{"backups.view", "backups.manage"}, nil, "Backups"},
	"auto_backups":  {"auto_backups", "Show scheduled (automatic) backups to the customer", nil, nil, "Backups"},
	"apps":          {"apps", "Application installer", []string{"apps.install"}, nil, "Websites"},
	"git":           {"git", "Git deployment", []string{"deploy.manage"}, nil, "Automation"},
	"workers":       {"workers", "Workers (background processes)", []string{"workers.manage"}, nil, "Automation"},
	"redis":         {"redis", "Redis", nil, []string{"sites.redis"}, "Caching"},
	"memcached":     {"memcached", "Memcached", nil, []string{"sites.memcached"}, "Caching"},
	"nodejs":        {"nodejs", "Node.js runtime & settings", nil, []string{"sites.nodejs"}, "Runtimes"},
	"python":        {"python", "Python runtime & settings", nil, []string{"sites.python"}, "Runtimes"},
	"logs":          {"logs", "Logs", nil, []string{"sites.logs"}, "Monitoring"},
	"stats":         {"stats", "Visitor statistics", nil, []string{"sites.stats"}, "Monitoring"},
	"site_health":   {"site_health", "Site health", nil, []string{"sites.health"}, "Monitoring"},
	"malware":       {"malware", "Malware scan", nil, []string{"sites.malware"}, "Security"},
	"firewall":      {"firewall", "Web firewall (ModSecurity)", nil, []string{"sites.firewall"}, "Security"},
	"site_security": {"site_security", "Site security overview", nil, []string{"sites.security"}, "Security"},
	"ssh":           {"ssh", "SSH shell & web terminal", nil, []string{"sites.terminal"}, "Files"},
	"wordpress":     {"wordpress", "WordPress Toolkit", nil, []string{"sites.wordpress", "wordpress"}, "Websites"},
	"type_php":      {"type_php", "Create PHP sites", nil, nil, "Site types"},
	"type_static":   {"type_static", "Create static sites", nil, nil, "Site types"},
	"type_nodejs":   {"type_nodejs", "Create Node.js sites", nil, nil, "Site types"},
	"type_python":   {"type_python", "Create Python sites", nil, nil, "Site types"},
	"type_proxy":    {"type_proxy", "Create reverse-proxy sites", nil, nil, "Site types"},
}

// DefaultOff are the features that are OFF unless a feature list opts in — the
// reverse of the default. Mirrors FeatureService::DEFAULT_OFF.
var DefaultOff = []string{"auto_backups", "type_nodejs", "type_python", "type_proxy"}

func isDefaultOff(f string) bool {
	for _, d := range DefaultOff {
		if d == f {
			return true
		}
	}
	return false
}

// FeatureService resolves which features a hosting account's plan allows.
type FeatureService struct{ db *sql.DB }

// NewFeatureService wraps a database handle.
func NewFeatureService(db *sql.DB) *FeatureService { return &FeatureService{db: db} }

// DisabledFor returns the features turned off for a hosting account (empty for
// staff / server-wide roles). Mirrors FeatureService::disabledFor, including
// the reseller-parent plan slot and the default-off opt-in rule. Any error
// (which should not happen for a well-formed DB) is treated as "nothing
// disabled" only when the account is staff; for a hosting account a query
// error disables nothing but is surfaced to the caller.
func (s *FeatureService) DisabledFor(ctx context.Context, u *identity.User) ([]string, error) {
	if u == nil || u.RoleScope != "own" {
		return nil, nil
	}
	// The plan "slots" that apply: the account's own, and (for a reseller's
	// customer) the reseller's. A NULL slot is a plan with no feature list.
	var parentID sql.NullInt64
	var ownList, resList sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT u.parent_id, pu.feature_list_id AS own_list, pr.feature_list_id AS res_list
		FROM users u
		LEFT JOIN plans pu ON pu.id = u.plan_id
		LEFT JOIN users r ON r.id = u.parent_id
		LEFT JOIN plans pr ON pr.id = r.plan_id
		WHERE u.id = ?`, u.ID).Scan(&parentID, &ownList, &resList)
	if err != nil {
		return nil, err
	}

	// slots preserves "a slot with no list" as a nil entry (needed by the
	// default-off rule); listIDs is the distinct non-null set for the queries.
	type slot struct {
		id    int64
		valid bool
	}
	slots := []slot{{ownList.Int64, ownList.Valid}}
	if parentID.Valid {
		slots = append(slots, slot{resList.Int64, resList.Valid})
	}
	seen := map[int64]bool{}
	var listIDs []int64
	for _, sl := range slots {
		if sl.valid && !seen[sl.id] {
			seen[sl.id] = true
			listIDs = append(listIDs, sl.id)
		}
	}

	disabled := map[string]bool{}
	optIn := map[int64]map[string]bool{} // list_id -> feature -> true
	if len(listIDs) > 0 {
		place := strings.TrimSuffix(strings.Repeat("?,", len(listIDs)), ",")
		args := make([]any, len(listIDs))
		for i, id := range listIDs {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT feature FROM feature_list_disabled WHERE list_id IN ("+place+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var f string
			if err := rows.Scan(&f); err != nil {
				rows.Close()
				return nil, err
			}
			disabled[f] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()

		erows, err := s.db.QueryContext(ctx, "SELECT list_id, feature FROM feature_list_enabled WHERE list_id IN ("+place+")", args...)
		if err != nil {
			return nil, err
		}
		for erows.Next() {
			var lid int64
			var f string
			if err := erows.Scan(&lid, &f); err != nil {
				erows.Close()
				return nil, err
			}
			if optIn[lid] == nil {
				optIn[lid] = map[string]bool{}
			}
			optIn[lid][f] = true
		}
		if err := erows.Err(); err != nil {
			erows.Close()
			return nil, err
		}
		erows.Close()
	}

	// Default-off features are on only when every applicable plan slot has a
	// list that opts in.
	for _, f := range DefaultOff {
		for _, sl := range slots {
			if !sl.valid || !optIn[sl.id][f] {
				disabled[f] = true
				break
			}
		}
	}

	// Return only known feature keys. Order is unspecified (callers test
	// membership, not position), matching array_intersect's role in the PHP.
	out := make([]string, 0, len(disabled))
	for f := range disabled {
		if _, ok := Features[f]; ok {
			out = append(out, f)
		}
	}
	return out, nil
}

// FeatureEnabled reports whether a plan feature is on for the account
// (FeatureService::enabledFor): not in the account's disabled list. Staff and
// server-wide roles never have disabled features, so every key is enabled.
func (s *FeatureService) FeatureEnabled(ctx context.Context, u *identity.User, key string) (bool, error) {
	disabled, err := s.DisabledFor(ctx, u)
	if err != nil {
		return false, err
	}
	for _, d := range disabled {
		if d == key {
			return false, nil
		}
	}
	return true, nil
}

// PermissionAllowed reports whether a plan-gated permission is allowed for the
// account. Mirrors FeatureService::permissionAllowed.
func (s *FeatureService) PermissionAllowed(ctx context.Context, u *identity.User, permission string) (bool, error) {
	disabled, err := s.DisabledFor(ctx, u)
	if err != nil {
		return false, err
	}
	for _, f := range disabled {
		for _, p := range Features[f].Permissions {
			if p == permission {
				return false, nil
			}
		}
	}
	return true, nil
}

// RouteAllowed reports whether a route owned by a disabled feature is allowed.
// Mirrors FeatureService::routeAllowed / featureForRoute.
func (s *FeatureService) RouteAllowed(ctx context.Context, u *identity.User, route string) (bool, error) {
	if route == "" {
		return true, nil
	}
	disabled, err := s.DisabledFor(ctx, u)
	if err != nil {
		return false, err
	}
	return featureForRoute(route, disabled) == "", nil
}

// featureForRoute returns the disabled feature owning a route, or "".
func featureForRoute(route string, disabled []string) string {
	for _, f := range disabled {
		for _, prefix := range Features[f].RoutePrefixes {
			if route == prefix || strings.HasPrefix(route, prefix+".") {
				return f
			}
		}
	}
	return ""
}

// FeatureList is one row returned by Lists (matches the PHP
// FeatureService::lists shape).
type FeatureList struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	Plans        int      `json:"plans"`
	Disabled     []string `json:"disabled"`
	EnabledOptIn []string `json:"enabled_optin"`
}

// Lists returns all feature lists, each with the current count of plans
// using it and its disabled / opt-in feature names (FeatureService::lists).
func (s *FeatureService) Lists(ctx context.Context) ([]FeatureList, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT l.id, l.name, l.description, l.created_at, l.updated_at,
		(SELECT COUNT(*) FROM plans p WHERE p.feature_list_id = l.id) AS plans
		FROM feature_lists l ORDER BY l.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeatureList
	for rows.Next() {
		var fl FeatureList
		if err := rows.Scan(&fl.ID, &fl.Name, &fl.Description, &fl.CreatedAt, &fl.UpdatedAt, &fl.Plans); err != nil {
			return nil, err
		}
		out = append(out, fl)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		id := out[i].ID
		dRows, err := s.db.QueryContext(ctx, "SELECT feature FROM feature_list_disabled WHERE list_id = ?", id)
		if err == nil {
			for dRows.Next() {
				var f string
				if dRows.Scan(&f) == nil {
					out[i].Disabled = append(out[i].Disabled, f)
				}
			}
			dRows.Close()
		}
		eRows, err := s.db.QueryContext(ctx, "SELECT feature FROM feature_list_enabled WHERE list_id = ?", id)
		if err == nil {
			for eRows.Next() {
				var f string
				if eRows.Scan(&f) == nil {
					out[i].EnabledOptIn = append(out[i].EnabledOptIn, f)
				}
			}
			eRows.Close()
		}
	}
	return out, nil
}

// ErrFeatureListNotFound is returned by Delete / Save(id) when the list
// doesn't exist.
var ErrFeatureListNotFound = errors.New("feature list not found")

// Save creates or updates a feature list. name is required (max 60 chars)
// and unique; description is optional (max 300); features is the set of
// enabled features, intersected with the known feature keys. Mirrors
// FeatureService::save.
func (s *FeatureService) Save(ctx context.Context, id *int64, name, description string, features []string, actorID int64) (int64, error) {
	name = strings.TrimSpace(name)
	desc := strings.TrimSpace(description)
	if name == "" || len(name) > 60 {
		return 0, errors.New("Enter a name (up to 60 characters).")
	}
	if len(desc) > 300 {
		return 0, errors.New("Keep the description under 300 characters.")
	}
	var existing int64
	var args []any
	q := "SELECT id FROM feature_lists WHERE name = ?"
	args = append(args, name)
	if id != nil {
		q += " AND id != ?"
		args = append(args, *id)
	}
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&existing); err == nil {
		return 0, errors.New("A feature list with this name exists.")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	enabled := []string{}
	for _, f := range features {
		if _, ok := Features[f]; ok {
			enabled = append(enabled, f)
		}
	}
	normalKeys := []string{}
	for k := range Features {
		if !isDefaultOff(k) {
			normalKeys = append(normalKeys, k)
		}
	}
	enabledSet := map[string]bool{}
	for _, f := range enabled {
		enabledSet[f] = true
	}
	disabled := []string{}
	for _, k := range normalKeys {
		if !enabledSet[k] {
			disabled = append(disabled, k)
		}
	}
	optIn := []string{}
	for _, f := range enabled {
		if isDefaultOff(f) {
			optIn = append(optIn, f)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var outID int64
	if id == nil {
		res, err := tx.ExecContext(ctx, "INSERT INTO feature_lists (name, description, created_at, updated_at) VALUES (?, ?, ?, ?)", name, desc, now, now)
		if err != nil {
			return 0, err
		}
		outID, err = res.LastInsertId()
		if err != nil {
			return 0, err
		}
	} else {
		outID = *id
		if err := tx.QueryRowContext(ctx, "SELECT id FROM feature_lists WHERE id = ?", outID).Scan(&outID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return 0, ErrFeatureListNotFound
			}
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE feature_lists SET name = ?, description = ?, updated_at = ? WHERE id = ?", name, desc, now, outID); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM feature_list_disabled WHERE list_id = ?", outID); err != nil {
		return 0, err
	}
	for _, f := range disabled {
		if _, err := tx.ExecContext(ctx, "INSERT INTO feature_list_disabled (list_id, feature) VALUES (?, ?)", outID, f); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM feature_list_enabled WHERE list_id = ?", outID); err != nil {
		return 0, err
	}
	for _, f := range optIn {
		if _, err := tx.ExecContext(ctx, "INSERT INTO feature_list_enabled (list_id, feature) VALUES (?, ?)", outID, f); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return outID, nil
}

// Delete removes a feature list, nulling it on any plans that reference
// it. Returns the removed list's name (FeatureService::delete).
func (s *FeatureService) Delete(ctx context.Context, id int64) (string, error) {
	var name string
	if err := s.db.QueryRowContext(ctx, "SELECT name FROM feature_lists WHERE id = ?", id).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrFeatureListNotFound
		}
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE plans SET feature_list_id = NULL WHERE feature_list_id = ?", id); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM feature_list_disabled WHERE list_id = ?", id); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM feature_list_enabled WHERE list_id = ?", id); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM feature_lists WHERE id = ?", id); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return name, nil
}
