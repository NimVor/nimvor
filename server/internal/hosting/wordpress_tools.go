package hosting

// The WordPress toolkit's switches and tools for one installation
// (WordPressToolsService.php) — the read side used by the overview page
// (protection, the security measures' status, restore points, the clone /
// sync targets, the debug log). The changing tools (debug, cron takeover,
// password + hotlink protection, vulnerability protection, passwords,
// security apply / roll back, clone, sync, restore) follow with their
// routes.
//
// Who may use it is decided by the http layer (the website must be visible
// to the user, websites.manage for changes). Copies are only made between
// websites of the same owner, and the databases they touch are checked
// against the panel's own records, never taken from wp-config.php.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// WpPasswordProtect is one protected folder (protect.status).
type WpPasswordProtect struct {
	Path string `json:"path"`
	User string `json:"user"`
}

// WpProtection is the website's password and hotlink protection
// (user names only).
type WpProtection struct {
	Password  []WpPasswordProtect
	Hotlink   map[string]any // nil when off; "allowed" is the list of sites
	WordPress map[string]any
}

// WpMeasure is one security hardening measure (wordpress.security).
type WpMeasure struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Kind        string `json:"kind"` // web | wordpress
	Help        string `json:"help"`
	Applied     bool   `json:"applied"`
	CanRollback bool   `json:"can_rollback"`
}

// WpSecurity is the security measures' state (wordpress.security).
type WpSecurity struct {
	Measures []WpMeasure
	Problems []string
}

// WpRestorePoint is one saved restore point (wordpress.restore_points).
type WpRestorePoint struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Reason   string `json:"reason"`
	Database bool   `json:"database"`
}

// WpDebugLog is the end of the WordPress debug log.
type WpDebugLog struct {
	Path   string
	Exists bool
	Lines  []string
	Size   int
}

// WpVulnItem is one found vulnerability (the check's stored items).
type WpVulnItem struct {
	Severity string `json:"severity"`
	Score    string `json:"score"`
	Title    string `json:"title"`
	Version  string `json:"version"`
	Name     string `json:"name"`
	Cve      string `json:"cve"`
	FixedIn  string `json:"fixed_in"`
	Unfixed  bool   `json:"unfixed"`
	Link     string `json:"link"`
}

// WpStoredVulnerabilities are the vulnerabilities stored on the
// installation row (WordPressToolsService::storedVulnerabilities).
type WpStoredVulnerabilities struct {
	Items     []WpVulnItem
	CheckedAt string // '' when never checked
}

// WordPressTools manages the toolkit's switches and tools.
type WordPressTools struct {
	db       *sql.DB
	agent    AgentCaller
	serverID int64
	sites    *WebsiteRepository
	audit    Auditor
}

// NewWordPressTools builds the service.
func NewWordPressTools(db *sql.DB, ag AgentCaller, serverID int64, sites *WebsiteRepository, audit Auditor) *WordPressTools {
	return &WordPressTools{db: db, agent: ag, serverID: serverID, sites: sites, audit: audit}
}

// WpTarget is the agent's target for one installation.
func WpTarget(site *Website, install *WordPressInstall) map[string]any {
	return map[string]any{
		"domain": site.Domain, "user": site.SystemUser, "root": site.Root,
		"php_version": site.PhpVersion, "path": install.Path,
	}
}

var wpUrlPathRe = regexp.MustCompile(`^[A-Za-z0-9._~-]+(/[A-Za-z0-9._~-]+)*$`)

// WpURLPath is the installation's URL path on its website ("/" or "/blog/")
// (WordPressToolsService::urlPath).
func WpURLPath(site *Website, install *WordPressInstall) (string, error) {
	dir := strings.TrimRight(site.Root, "/")
	if install.Path != "" {
		dir += "/" + install.Path
	}
	doc := strings.TrimRight(site.DocumentRoot, "/")
	if dir != doc && !strings.HasPrefix(dir, doc+"/") {
		return "", &UserError{What: "This installation is not inside the website's document root, so web server protection cannot be applied to it."}
	}
	rest := strings.Trim(dir[len(doc):], "/")
	if rest != "" && !wpUrlPathRe.MatchString(rest) {
		return "", &UserError{What: "The installation's folder name has characters web server rules cannot use."}
	}
	if rest == "" {
		return "/", nil
	}
	return "/" + rest + "/", nil
}

// wpWritable refuses a change on a suspended account.
func wpWritable(site *Website) error {
	if site.AccountSuspended {
		return &UserError{What: "The hosting account is suspended; WordPress cannot be changed."}
	}
	return nil
}

// Protection is the website's password and hotlink protection
// (WordPressToolsService::protection).
func (s *WordPressTools) Protection(ctx context.Context, site *Website) (*WpProtection, error) {
	res, err := s.agent.Call(ctx, "protect.status", map[string]any{"domain": site.Domain})
	if err != nil {
		return nil, err
	}
	var o struct {
		Password  []WpPasswordProtect `json:"password"`
		Hotlink   map[string]any      `json:"hotlink"`
		WordPress map[string]any      `json:"wordpress"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	if o.Password == nil {
		o.Password = []WpPasswordProtect{}
	}
	return &WpProtection{Password: o.Password, Hotlink: o.Hotlink, WordPress: o.WordPress}, nil
}

var wpMeasureRe = regexp.MustCompile(`^[a-z_]{3,40}$`)

// Security is the security measures' status, or apply / roll back some of
// them (WordPressToolsService::security).
func (s *WordPressTools) Security(ctx context.Context, site *Website, install *WordPressInstall, op string, measures []string) (*WpSecurity, error) {
	if op != "status" {
		if err := wpWritable(site); err != nil {
			return nil, err
		}
	}
	clean := []string{}
	seen := map[string]bool{}
	for _, m := range measures {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] || !wpMeasureRe.MatchString(m) {
			continue
		}
		seen[m] = true
		clean = append(clean, m)
	}
	path, err := WpURLPath(site, install)
	if err != nil {
		return nil, err
	}
	res, err := s.agent.Call(ctx, "wordpress.security", map[string]any{
		"domain": site.Domain, "user": site.SystemUser, "root": site.Root,
		"php_version": site.PhpVersion, "path": install.Path,
		"url_path": path, "op": op, "measures": clean,
	}, 240*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Measures []WpMeasure `json:"measures"`
		Problems []string    `json:"problems"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	if o.Measures == nil {
		o.Measures = []WpMeasure{}
	}
	if o.Problems == nil {
		o.Problems = []string{}
	}
	if op != "status" {
		s.auditWordPress(ctx, "wordpress.security_"+op, "website", fmt.Sprintf("%d", site.ID), map[string]any{
			"domain": site.Domain, "path": install.Path, "measures": clean,
		})
	}
	return &WpSecurity{Measures: o.Measures, Problems: o.Problems}, nil
}

// RestorePoints are the restore points of this installation (saved before a
// sync or clone replaced it) (WordPressToolsService::restorePoints).
func (s *WordPressTools) RestorePoints(ctx context.Context, site *Website, install *WordPressInstall) ([]WpRestorePoint, error) {
	res, err := s.agent.Call(ctx, "wordpress.restore_points", map[string]any{"domain": site.Domain})
	if err != nil {
		return nil, err
	}
	var o struct {
		RestorePoints []WpRestorePoint `json:"restore_points"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	out := []WpRestorePoint{}
	for _, p := range o.RestorePoints {
		if p.Path == install.Path {
			out = append(out, p)
		}
	}
	return out, nil
}

// CopyTargets are the websites an installation can be cloned or synced to:
// the owner's other PHP websites (and the same website)
// (WordPressToolsService::copyTargets).
func (s *WordPressTools) CopyTargets(ctx context.Context, site *Website, actor *identity.User) ([]Website, error) {
	all, err := s.sites.All(ctx, s.serverID, actor)
	if err != nil {
		return nil, err
	}
	out := []Website{}
	for _, st := range all {
		if st.Kind == "php" && st.PhpVersion != "" && st.OwnerID.Valid && site.OwnerID.Valid &&
			st.OwnerID.Int64 == site.OwnerID.Int64 && st.SystemUser != "" {
			out = append(out, st)
		}
	}
	return out, nil
}

// DebugLog is the end of the WordPress debug log
// (WordPressToolsService::debugLog).
func (s *WordPressTools) DebugLog(ctx context.Context, site *Website, install *WordPressInstall) (*WpDebugLog, error) {
	target := WpTarget(site, install)
	target["op"] = "debug_log"
	res, err := s.agent.Call(ctx, "wordpress.manage", target, 90*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Path   string   `json:"path"`
		Exists bool     `json:"exists"`
		Lines  []string `json:"lines"`
		Size   int      `json:"size"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	if o.Lines == nil {
		o.Lines = []string{}
	}
	return &WpDebugLog{Path: o.Path, Exists: o.Exists, Lines: o.Lines, Size: o.Size}, nil
}

// auditWordPress records a WordPress toolkit action.
func (s *WordPressTools) auditWordPress(ctx context.Context, action, targetType, target string, details map[string]any) {
	if s.audit != nil {
		s.audit(ctx, action, targetType, target, "success", details)
	}
}

// WpStoredVulnerabilitiesOf reads the vulnerabilities stored on the
// installation row (WordPressToolsService::storedVulnerabilities).
func WpStoredVulnerabilitiesOf(install *WordPressInstall) *WpStoredVulnerabilities {
	out := &WpStoredVulnerabilities{Items: []WpVulnItem{}}
	if install.Vulns != "" {
		if err := json.Unmarshal([]byte(install.Vulns), &out.Items); err != nil || out.Items == nil {
			out.Items = []WpVulnItem{}
		}
	}
	if install.VulnsCheckedAt.Valid {
		out.CheckedAt = install.VulnsCheckedAt.String
	}
	return out
}
