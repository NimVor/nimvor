package hosting

import (
	"context"
	"database/sql"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// AddonConversionService converts an addon domain into a hosting account of
// its own (AddonConversionService): a new panel login (a hosting customer
// with a plan) and a new system account that runs the website. Nothing is
// copied: the website folder is re-owned in place, its databases, DNS zone
// and email domain (with the mail data) change owner, and its cron jobs,
// workers and FTP accounts move to the new system account. Subdomains of the
// addon domain go with it. If a step on the server fails, everything done so
// far is undone.
type AddonConversionService struct {
	db       *sql.DB
	agent    AgentCaller
	accounts *AccountService
	users    *identity.Repository
	sites    *WebsiteRepository
	websites *WebsiteService
	cron     *CronService
	workers  *WorkerService
	limits   *PlanLimits
	audit    Auditor
	serverID int64
}

// NewAddonConversionService builds the service.
func NewAddonConversionService(db *sql.DB, agent AgentCaller, accounts *AccountService, users *identity.Repository, sites *WebsiteRepository, websites *WebsiteService, cron *CronService, workers *WorkerService, limits *PlanLimits, audit Auditor, serverID int64) *AddonConversionService {
	return &AddonConversionService{
		db: db, agent: agent, accounts: accounts, users: users, sites: sites,
		websites: websites, cron: cron, workers: workers, limits: limits,
		audit: audit, serverID: serverID,
	}
}

// IsAddon reports whether the site is an addon domain of its parent — a child
// website whose domain is not under the parent's (AddonConversionService::isAddon).
func IsAddon(site *Website, parent *Website) bool {
	return parent != nil && !strings.HasSuffix(site.Domain, "."+parent.Domain)
}

// ConversionPreview is what moves with the addon domain (for the
// confirmation, AddonConversionService::preview).
type ConversionPreview struct {
	Websites  []Website
	Databases []string
	DNS       bool
	Mail      bool
	Cron      int
	Workers   int
}

// ConvertedAccount is the result of Convert (AddonConversionService::convert's
// return value).
type ConvertedAccount struct {
	ID       int64
	Username string
	Password sql.NullString // set only when generated
}

// Preview reports what would move with the addon domain.
func (s *AddonConversionService) Preview(ctx context.Context, site *Website) (*ConversionPreview, error) {
	family, err := s.family(ctx, site)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(family))
	for _, w := range family {
		ids = append(ids, w.ID)
	}
	list, args := inList(ids)
	out := &ConversionPreview{Websites: make([]Website, 0, len(family))}
	for _, w := range family {
		out.Websites = append(out.Websites, *w)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM dns_zones WHERE website_id IN "+list, args...).Scan(&n); err != nil {
		return nil, err
	}
	out.DNS = n > 0
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM mail_domains WHERE website_id IN "+list, args...).Scan(&n); err != nil {
		return nil, err
	}
	out.Mail = n > 0
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM cron_jobs WHERE website_id IN "+list, args...).Scan(&out.Cron); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM workers WHERE website_id IN "+list, args...).Scan(&out.Workers); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT name FROM databases WHERE website_id IN "+list+" ORDER BY name", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out.Databases = append(out.Databases, name)
	}
	return out, rows.Err()
}

// Convert performs the conversion (AddonConversionService::convert). site is
// the full, access-checked addon row; in carries the account form.
func (s *AddonConversionService) Convert(ctx context.Context, site *Website, in map[string]string, actor *identity.User) (*ConvertedAccount, error) {
	var parent *Website
	if site.ParentID.Valid {
		p, err := s.sites.FindFull(ctx, s.serverID, site.ParentID.Int64, nil)
		if err != nil {
			return nil, err
		}
		parent = p
	}
	if !IsAddon(site, parent) {
		return nil, &UserError{What: "Only an addon domain can become an account of its own."}
	}
	if site.AccountSuspended {
		return nil, &UserError{What: "The hosting account is suspended; unsuspend it first."}
	}
	username := strings.ToLower(strings.TrimSpace(in["username"]))
	// The domain's folder becomes the new account's home: it must be the
	// standard <sites_root>/<domain> (what the agent re-owns).
	if path.Base(site.Root) != site.Domain {
		return nil, &UserError{What: "The website folder is not the standard one for " + site.Domain + "; it cannot be converted."}
	}
	if used, err := s.sites.UsingSystemUser(ctx, s.serverID, username); err != nil {
		return nil, err
	} else if len(used) > 0 {
		return nil, &ValidationError{Fields: map[string]string{"username": "The system user " + username + " is in use. Choose another name."}}
	}
	// A reseller converts only for their own customers, into their own
	// customers. (The route resolves the site under the same scope; this is
	// the service's own check, as in PHP.)
	if site.OwnerID.Valid {
		if _, err := s.sites.Find(ctx, s.serverID, site.ID, actor); err != nil {
			return nil, &UserError{What: "That website does not exist."}
		}
	} else if actor == nil || actor.RoleScope != "all" {
		return nil, &UserError{What: "That website does not exist."}
	}

	// 1. The panel login (checks username, email, password and plan). The
	// options start from the account defaults (PHP merges
	// AccountOptions::DEFAULTS into the input).
	acctIn := map[string]string{
		"username":              username,
		"email":                 in["email"],
		"display_name":          in["display_name"],
		"password":              in["password"],
		"password_confirmation": in["password_confirmation"],
		"generate":              in["generate"],
		"plan_id":               in["plan_id"],
		"domain":                "",
		"dns_dkim":              "1",
		"dns_spf":               "1",
		"dns_dmarc":             "1",
		"dns_registrar_ns":      "",
		"dns_overwrite":         "",
		"mail_routing":          "auto",
		"spam_filter":           "1",
		"spam_box":              "1",
	}
	account, err := s.accounts.Create(ctx, acctIn, actor)
	if err != nil {
		return nil, err
	}
	newID := account.ID
	family, err := s.family(ctx, site)
	if err != nil {
		return nil, err
	}
	done, ftp, err := s.convertOnServer(ctx, site, family, username, newID)
	if err != nil {
		s.undo(ctx, done, ftp, newID)
		s.recordAudit(ctx, "account.convert_addon", "website", strconv.FormatInt(site.ID, 10), "failure", map[string]any{
			"domain": site.Domain, "username": username, "error": err.Error(),
		})
		return nil, err
	}

	// 4. Ownership in the panel: databases (and users only they use), DNS,
	// email.
	ids := make([]int64, 0, len(family))
	for _, w := range family {
		ids = append(ids, w.ID)
	}
	if err := s.moveOwnership(ctx, ids, newID); err != nil {
		return nil, err
	}
	domains := make([]string, 0, len(family))
	for _, w := range family {
		domains = append(domains, w.Domain)
	}
	s.recordAudit(ctx, "account.convert_addon", "website", strconv.FormatInt(site.ID, 10), "success", map[string]any{
		"domain":   site.Domain,
		"from":     site.SystemUser,
		"username": username,
		"user_id":  newID,
		"websites": domains,
	})
	return &ConvertedAccount{ID: newID, Username: account.Username, Password: account.Password}, nil
}

// convertOnServer runs the server-side steps (AddonConversionService::convert,
// steps 2–3): the system account, the re-owned folders, and what runs as the
// account. done/ftp are filled as things succeed so undo can reverse exactly
// what happened.
func (s *AddonConversionService) convertOnServer(ctx context.Context, site *Website, family []*Website, username string, newID int64) (done []*Website, ftp []*Website, err error) {
	if err := s.limits.AssertCanCreate(ctx, "websites", &newID, 1); err != nil {
		return nil, nil, err
	}
	// 2. The system account, the folders re-owned, the files inside.
	for i, w := range family {
		if err := s.websites.MoveToAccount(ctx, w, username, &newID, i == 0, i == 0); err != nil {
			return done, ftp, err
		}
		done = append(done, w)
		// A folder of its own (<sites root>/<domain>): its files and FTP
		// accounts; one inside another website's folder went with it.
		if path.Base(w.Root) == w.Domain && (i == 0 || !strings.HasPrefix(w.Root, strings.TrimRight(site.Root, "/")+"/")) {
			moved, err := s.sites.FindFull(ctx, s.serverID, w.ID, nil)
			if err != nil {
				return done, ftp, err
			}
			if _, err := s.websites.Permissions(ctx, moved, true); err != nil {
				return done, ftp, err
			}
			if _, err := s.agent.Call(ctx, "ftp.move_site", map[string]any{"domain": w.Domain, "user": username}, 120*time.Second); err != nil {
				return done, ftp, err
			}
			ftp = append(ftp, w)
		}
	}
	// 3. What runs as the account: cron jobs and workers.
	for _, w := range family {
		moved, err := s.sites.FindFull(ctx, s.serverID, w.ID, nil)
		if err != nil {
			return done, ftp, err
		}
		if err := s.cron.Apply(ctx, moved); err != nil {
			return done, ftp, err
		}
		workers, err := s.workers.ForSite(ctx, w.ID)
		if err != nil {
			return done, ftp, err
		}
		for i := range workers {
			if err := s.workers.Apply(ctx, moved, workers[i]); err != nil {
				return done, ftp, err
			}
		}
	}
	return done, ftp, nil
}

// undo puts the websites back on their previous account and removes the new
// login (AddonConversionService::undo). Best effort: the failure itself is
// reported to the caller.
func (s *AddonConversionService) undo(ctx context.Context, done []*Website, ftp []*Website, newID int64) {
	for i := len(done) - 1; i >= 0; i-- {
		w := done[i]
		owner := (*int64)(nil)
		if w.OwnerID.Valid {
			owner = &w.OwnerID.Int64
		}
		if err := s.websites.MoveToAccount(ctx, w, w.SystemUser, owner, false, false); err != nil {
			continue
		}
		_ = s.sites.UpdateFields(ctx, w.ID, map[string]any{
			"parent_id": int64OrNil(ownerPtr(w.ParentID)),
		})
		if containsWebsite(ftp, w) {
			if moved, err := s.sites.FindFull(ctx, s.serverID, w.ID, nil); err == nil {
				_, _ = s.websites.Permissions(ctx, moved, true)
				_, _ = s.agent.Call(ctx, "ftp.move_site", map[string]any{"domain": w.Domain, "user": w.SystemUser}, 120*time.Second)
			}
		}
		if moved, err := s.sites.FindFull(ctx, s.serverID, w.ID, nil); err == nil {
			_ = s.cron.Apply(ctx, moved)
		}
	}
	_ = s.users.Delete(ctx, newID)
}

// moveOwnership re-points the family's databases (and the database users only
// they use), DNS zones and mail domains at the new account
// (AddonConversionService::convert step 4).
func (s *AddonConversionService) moveOwnership(ctx context.Context, ids []int64, newID int64) error {
	list, args := inList(ids)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`UPDATE database_users SET owner_id = ? WHERE id IN
		 (SELECT g.user_id FROM database_grants g JOIN databases d ON d.id = g.database_id WHERE d.website_id IN `+list+`)
		 AND id NOT IN
		 (SELECT g.user_id FROM database_grants g JOIN databases d ON d.id = g.database_id WHERE d.website_id IS NULL OR d.website_id NOT IN `+list+`)`,
		append([]any{newID}, append(args, args...)...)...)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE databases SET owner_id = ? WHERE website_id IN "+list, append([]any{newID}, args...)...); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE dns_zones SET owner_id = ? WHERE website_id IN "+list, append([]any{newID}, args...)...); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE mail_domains SET owner_id = ? WHERE website_id IN "+list, append([]any{newID}, args...)...); err != nil {
		return err
	}
	return tx.Commit()
}

// family is the addon website and its own children (AddonConversionService::family).
func (s *AddonConversionService) family(ctx context.Context, site *Website) ([]*Website, error) {
	children, err := s.sites.Children(ctx, s.serverID, site.ID)
	if err != nil {
		return nil, err
	}
	out := []*Website{site}
	for i := range children {
		// Children() returns display rows; the conversion needs the full row
		// (root, parent). PHP's children() SELECTs the full row.
		full, err := s.sites.FindFull(ctx, s.serverID, children[i].ID, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, full)
	}
	return out, nil
}

// recordAudit is the nil-safe audit hook (same contract as WebsiteService's).
func (s *AddonConversionService) recordAudit(ctx context.Context, action, targetType, target, result string, details map[string]any) {
	if s.audit != nil {
		s.audit(ctx, action, targetType, target, result, details)
	}
}

// inList renders a bound `IN (?,?,…)` clause (empty slice → `IN (NULL)`).
func inList(ids []int64) (string, []any) {
	if len(ids) == 0 {
		return "(NULL)", []any{}
	}
	q := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		q[i] = "?"
		args[i] = id
	}
	return "(" + strings.Join(q, ",") + ")", args
}

func containsWebsite(list []*Website, w *Website) bool {
	for _, x := range list {
		if x.ID == w.ID {
			return true
		}
	}
	return false
}
