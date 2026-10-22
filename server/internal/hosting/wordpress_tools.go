package hosting

// The WordPress toolkit's switches and tools for one installation
// (WordPressToolsService.php): the read side used by the overview page
// (protection, the security measures' status, restore points, the clone /
// sync targets, the debug log) and the changing tools behind the tools
// route (debug, cron takeover, password + hotlink protection, the
// vulnerability protection, the administrator and database passwords, the
// vulnerability-database check, the security apply / roll back, and the
// copy tools — clone, sync, restore).
//
// Who may use it is decided by the http layer (the website must be visible
// to the user, websites.manage for changes). Copies are only made between
// websites of the same owner, and the databases they touch are checked
// against the panel's own records, never taken from wp-config.php.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
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
	Kind     string `json:"kind"`
	Slug     string `json:"slug"`
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
	crypto   *crypto.Crypto
	cron     *CronService
	vulns    *WpVulnerabilities
	dbs      *DatabaseService
	audit    Auditor
}

// NewWordPressTools builds the service.
func NewWordPressTools(db *sql.DB, ag AgentCaller, serverID int64, sites *WebsiteRepository, crypto *crypto.Crypto, cron *CronService, vulns *WpVulnerabilities, dbs *DatabaseService, audit Auditor) *WordPressTools {
	return &WordPressTools{db: db, agent: ag, serverID: serverID, sites: sites, crypto: crypto, cron: cron, vulns: vulns, dbs: dbs, audit: audit}
}

// WpDebugFlags are the debug switches the toolkit can turn on or off
// (WordPressToolsService::DEBUG, in the order the service applies them).
var WpDebugFlags = []string{"WP_DEBUG", "WP_DEBUG_LOG", "WP_DEBUG_DISPLAY", "SCRIPT_DEBUG", "SAVEQUERIES"}

// WpRecommendedMeasures are the measures the one-click "Secure
// (recommended)" applies (WordPressToolsService::RECOMMENDED).
var WpRecommendedMeasures = []string{"block_sensitive_files", "block_author_scans", "no_php_in_uploads", "no_php_in_includes", "block_bad_bots", "disable_file_edit", "disable_pingbacks", "disable_script_concat", "config_permissions"}

// wpManage runs one WordPress operation on the agent
// (WordPressToolsService::manage).
func (s *WordPressTools) wpManage(ctx context.Context, site *Website, install *WordPressInstall, op string, extra map[string]any, timeout time.Duration) (json.RawMessage, error) {
	target := WpTarget(site, install)
	target["op"] = op
	for k, v := range extra {
		target[k] = v
	}
	return s.agent.Call(ctx, "wordpress.manage", target, timeout)
}

// SetDebug saves the debugging switches (WordPressToolsService::setDebug).
func (s *WordPressTools) SetDebug(ctx context.Context, site *Website, install *WordPressInstall, items []string) (string, error) {
	if err := wpWritable(site); err != nil {
		return "", err
	}
	on := []string{}
	seen := map[string]bool{}
	for _, flag := range WpDebugFlags {
		for _, in := range items {
			if in == flag && !seen[flag] {
				seen[flag] = true
				on = append(on, flag)
			}
		}
	}
	if _, err := s.wpManage(ctx, site, install, "debug", map[string]any{"items": on}, 150*time.Second); err != nil {
		return "", err
	}
	debug := 0
	if seen["WP_DEBUG"] {
		debug = 1
	}
	if _, err := s.db.Exec(`UPDATE wordpress_installs SET debug = ? WHERE id = ?`, debug, install.ID); err != nil {
		return "", err
	}
	s.auditWordPress(ctx, "wordpress.debug", "website", fmt.Sprintf("%d", site.ID), map[string]any{"domain": site.Domain, "path": install.Path, "enabled": on})
	if len(on) == 0 {
		return "Debugging is off.", nil
	}
	return "Debugging settings saved: " + strings.Join(on, ", ") + ".", nil
}

var wpProtectUserRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
var wpSecretRe = regexp.MustCompile(`[\x00\r\n]`)

// wpSecretOK is the 8-to-128-character password check (no NUL, CR or LF).
func wpSecretOK(secret string) bool {
	return len(secret) >= 8 && len(secret) <= 128 && !wpSecretRe.MatchString(secret)
}

// SetPasswordProtection turns the installation's password protection on or
// off (WordPressToolsService::setPasswordProtection).
func (s *WordPressTools) SetPasswordProtection(ctx context.Context, site *Website, install *WordPressInstall, on bool, user, password string) (string, error) {
	if err := wpWritable(site); err != nil {
		return "", err
	}
	path, err := WpURLPath(site, install)
	if err != nil {
		return "", err
	}
	if on {
		ve := &ValidationError{}
		if !wpProtectUserRe.MatchString(user) {
			ve.Add("protect_user", "Use up to 64 letters, digits, dots, dashes or underscores.")
		}
		if !wpSecretOK(password) {
			ve.Add("protect_password", "Use 8 to 128 characters.")
		}
		if ve.Any() {
			return "", ve
		}
	}
	if _, err := s.agent.Call(ctx, "protect.password", map[string]any{
		"domain": site.Domain, "path": path, "enabled": on, "user": user, "password": password, "realm": "Restricted area",
	}, 120*time.Second); err != nil {
		return "", err
	}
	who := ""
	if on {
		who = user
	}
	s.auditWordPress(ctx, "wordpress.password_protection", "website", fmt.Sprintf("%d", site.ID), map[string]any{
		"domain": site.Domain, "path": install.Path, "on": on, "user": who, "url_path": path,
	})
	if on {
		return "Password protection is on: visitors to " + site.Domain + strings.TrimRight(path, "/") + "/ must sign in as " + user + ".", nil
	}
	return "Password protection is off.", nil
}

var wpHostRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// SetHotlink turns the website's hotlink protection on or off
// (WordPressToolsService::setHotlink).
func (s *WordPressTools) SetHotlink(ctx context.Context, site *Website, on bool, allowed []string) (string, error) {
	if err := wpWritable(site); err != nil {
		return "", err
	}
	clean := []string{}
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if !wpHostRe.MatchString(a) {
			return "", &ValidationError{Fields: map[string]string{
				"hotlink_allowed": fmt.Sprintf("%q is not a host name (a leading *. is allowed).", a),
			}}
		}
		clean = append(clean, a)
	}
	if _, err := s.agent.Call(ctx, "protect.hotlink", map[string]any{
		"domain": site.Domain, "enabled": on, "allowed": clean,
	}, 120*time.Second); err != nil {
		return "", err
	}
	s.auditWordPress(ctx, "wordpress.hotlink", "website", fmt.Sprintf("%d", site.ID), map[string]any{
		"domain": site.Domain, "on": on, "allowed": clean,
	})
	if on {
		if len(clean) != 0 {
			return "Hotlink protection is on: images and media are only served to pages of this website and " + strings.Join(clean, ", ") + ".", nil
		}
		return "Hotlink protection is on: images and media are only served to pages of this website.", nil
	}
	return "Hotlink protection is off.", nil
}

// AdminPassword changes a WordPress administrator's password (and signs the
// administrator out everywhere else) (WordPressToolsService::adminPassword).
func (s *WordPressTools) AdminPassword(ctx context.Context, site *Website, install *WordPressInstall, adminID int, password, confirm string) (string, error) {
	if err := wpWritable(site); err != nil {
		return "", err
	}
	ve := &ValidationError{}
	if !wpSecretOK(password) {
		ve.Add("admin_password", "Use 8 to 128 characters.")
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(confirm)) != 1 {
		ve.Add("admin_password_confirm", "The passwords do not match.")
	}
	if adminID < 1 {
		ve.Add("admin_id", "Choose an administrator.")
	}
	if ve.Any() {
		return "", ve
	}
	res, err := s.wpManage(ctx, site, install, "admin_password", map[string]any{
		"items": []string{strconv.Itoa(adminID)}, "password": password,
	}, 150*time.Second)
	if err != nil {
		return "", err
	}
	var o struct {
		Login string `json:"login"`
	}
	_ = json.Unmarshal(res, &o)
	who := fmt.Sprintf("#%d", adminID)
	if o.Login != "" {
		who = o.Login
	}
	s.auditWordPress(ctx, "wordpress.admin_password", "website", fmt.Sprintf("%d", site.ID), map[string]any{
		"domain": site.Domain, "path": install.Path, "admin_id": adminID,
	})
	return "The password of the WordPress administrator " + who + " was changed and their other sessions were signed out.", nil
}

// wpCronCmdRe is the cron command the agent may return for the takeover
// (WordPressToolsService::setCronTakeover's guard).
var wpCronCmdRe = regexp.MustCompile(`^/[A-Za-z0-9/._-]+ /usr/local/lib/whacp/wp-cli\.phar --path=/[A-Za-z0-9/._~-]+ cron event run --due-now --quiet$`)

// SetCronTakeover takes wp-cron.php over with a system cron job (every five
// minutes), or gives WordPress its scheduled tasks on visits back
// (WordPressToolsService::setCronTakeover).
func (s *WordPressTools) SetCronTakeover(ctx context.Context, site *Website, install *WordPressInstall, on bool) (string, error) {
	if err := wpWritable(site); err != nil {
		return "", err
	}
	jobID := install.CronJobID
	if on {
		res, err := s.wpManage(ctx, site, install, "cron_takeover_on", nil, 150*time.Second)
		if err != nil {
			return "", err
		}
		var o struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(res, &o)
		if !wpCronCmdRe.MatchString(o.Command) {
			return "", &UserError{What: "The agent returned an unexpected cron command."}
		}
		exists := false
		if jobID.Valid {
			j, err := s.cron.Find(ctx, site, jobID.Int64)
			if err != nil {
				return "", err
			}
			exists = j != nil
		}
		if !exists {
			folder := "/"
			if install.Path != "" {
				folder = install.Path
			}
			err := s.cron.Save(ctx, site, 0, map[string]string{
				"schedule":    "*/5 * * * *",
				"command":     o.Command,
				"description": "WordPress scheduled tasks (" + folder + ") — taken over from wp-cron.php",
				"enabled":     "1",
			}, "")
			if err != nil {
				// The takeover itself is undone again.
				_, _ = s.wpManage(ctx, site, install, "cron_takeover_off", nil, 150*time.Second)
				return "", err
			}
			var newID int64
			_ = s.db.QueryRow(`SELECT id FROM cron_jobs WHERE website_id = ? AND command = ? ORDER BY id DESC LIMIT 1`, site.ID, o.Command).Scan(&newID)
			if newID > 0 {
				if _, err := s.db.Exec(`UPDATE wordpress_installs SET cron_job_id = ? WHERE id = ?`, newID, install.ID); err != nil {
					return "", err
				}
			} else if _, err := s.db.Exec(`UPDATE wordpress_installs SET cron_job_id = NULL WHERE id = ?`, install.ID); err != nil {
				return "", err
			}
		}
		s.auditWordPress(ctx, "wordpress.cron_takeover", "website", fmt.Sprintf("%d", site.ID), map[string]any{
			"domain": site.Domain, "path": install.Path, "on": true,
		})
		return "wp-cron.php is taken over: WordPress's scheduled tasks now run every 5 minutes from a cron job (listed under Cron jobs).", nil
	}
	if _, err := s.wpManage(ctx, site, install, "cron_takeover_off", nil, 150*time.Second); err != nil {
		return "", err
	}
	if jobID.Valid {
		j, err := s.cron.Find(ctx, site, jobID.Int64)
		if err != nil {
			return "", err
		}
		if j != nil {
			if err := s.cron.Delete(ctx, site, jobID.Int64); err != nil {
				return "", err
			}
		}
	}
	if _, err := s.db.Exec(`UPDATE wordpress_installs SET cron_job_id = NULL WHERE id = ?`, install.ID); err != nil {
		return "", err
	}
	s.auditWordPress(ctx, "wordpress.cron_takeover", "website", fmt.Sprintf("%d", site.ID), map[string]any{
		"domain": site.Domain, "path": install.Path, "on": false,
	})
	return "WordPress runs its scheduled tasks on visits again (wp-cron.php).", nil
}

// DBPassword changes the installation's database password (and in its
// wp-config.php) and returns the new password to show once
// (WordPressToolsService::dbPassword).
func (s *WordPressTools) DBPassword(ctx context.Context, site *Website, install *WordPressInstall) (string, error) {
	if err := wpWritable(site); err != nil {
		return "", err
	}
	password, err := dbPassword()
	if err != nil {
		return "", err
	}
	res, err := s.wpManage(ctx, site, install, "db_password", map[string]any{"password": password}, 150*time.Second)
	if err != nil {
		return "", err
	}
	var o struct {
		User string `json:"user"`
	}
	_ = json.Unmarshal(res, &o)
	if o.User != "" {
		// The panel's stored copy (when the owner chose to keep one).
		var id int64
		var enc string
		if err := s.db.QueryRow(`SELECT id, password_enc FROM database_users WHERE server_id = ? AND username = ? AND host = 'localhost'`, s.serverID, o.User).Scan(&id, &enc); err == nil && enc != "" {
			newEnc, err := s.crypto.Encrypt([]byte(password))
			if err != nil {
				return "", err
			}
			if _, err := s.db.Exec(`UPDATE database_users SET password_enc = ? WHERE id = ?`, newEnc, id); err != nil {
				return "", err
			}
		}
	}
	s.auditWordPress(ctx, "wordpress.db_password", "website", fmt.Sprintf("%d", site.ID), map[string]any{
		"domain": site.Domain, "path": install.Path, "db_user": o.User,
	})
	return password, nil
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

// CheckVulnerabilities checks the installation against the vulnerability
// database and keeps the result (WordPressToolsService::checkVulnerabilities).
func (s *WordPressTools) CheckVulnerabilities(install *WordPressInstall, info *WordPressInfo) ([]WpVulnItem, bool, string, error) {
	items, reachable := s.vulns.Check(info)
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	if reachable || len(items) != 0 {
		b, err := json.Marshal(items)
		if err != nil {
			return nil, false, "", err
		}
		if _, err := s.db.Exec(`UPDATE wordpress_installs SET vulns = ?, vulns_checked_at = ? WHERE id = ?`, string(b), now, install.ID); err != nil {
			return nil, false, "", err
		}
	}
	return items, reachable, now, nil
}

// SetVulnerabilityProtection records the installation's vulnerability
// protection state and builds its message (WordPressToolsService::
// setVulnerabilityProtection — the ModSecurity part runs in the http layer,
// where the modsec facilities live). held is the server-wide
// detection-only flag at the moment of switching on.
func (s *WordPressTools) SetVulnerabilityProtection(ctx context.Context, site *Website, install *WordPressInstall, on, held bool) (string, error) {
	if err := wpWritable(site); err != nil {
		return "", err
	}
	if _, err := s.db.Exec(`UPDATE wordpress_installs SET vuln_protection = ? WHERE id = ?`, on, install.ID); err != nil {
		return "", err
	}
	s.auditWordPress(ctx, "wordpress.vulnerability_protection", "website", fmt.Sprintf("%d", site.ID), map[string]any{
		"domain": site.Domain, "path": install.Path, "on": on,
	})
	if on {
		if held {
			return "Vulnerability protection is on: requests to the website are filtered by ModSecurity with the OWASP Core Rule Set (only logging until an administrator allows blocking under Security → ModSecurity), and the installation is checked for known vulnerabilities every day.", nil
		}
		return "Vulnerability protection is on: requests to the website are filtered by ModSecurity with the OWASP Core Rule Set (blocking), and the installation is checked for known vulnerabilities every day.", nil
	}
	return "Vulnerability protection is off (ModSecurity only logs for this website now).", nil
}

var (
	wpClonePathRe    = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
	wpRestorePointRe = regexp.MustCompile(`^\d{8}T\d{6}Z$`)
)

// ownedDatabase is the installation's database when it is one of the
// owner's databases in the panel ('' when not)
// (WordPressToolsService::ownedDatabase).
func (s *WordPressTools) ownedDatabase(name string, ownerID *int64) (string, error) {
	var owner sql.NullInt64
	err := s.db.QueryRow(`SELECT owner_id FROM databases WHERE server_id = ? AND name = ?`, s.serverID, name).Scan(&owner)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	want := sql.NullInt64{}
	if ownerID != nil {
		want = sql.NullInt64{Int64: *ownerID, Valid: true}
	}
	if owner.Int64 != want.Int64 || owner.Valid != want.Valid {
		return "", nil
	}
	return name, nil
}

// trackJob records a started copy job (WordPressToolsService's $jobs->track).
func (s *WordPressTools) trackJob(ctx context.Context, job, kind, targetID string, actor *identity.User) error {
	var uid *int64
	if actor != nil {
		id := actor.ID
		uid = &id
	}
	return NewJobTracker(s.db).Track(ctx, s.serverID, job, kind, "website", targetID, uid)
}

// wpCopy runs the agent's wordpress.copy (the clone / sync / restore job)
// and returns the job id.
func (s *WordPressTools) wpCopy(ctx context.Context, params map[string]any) (string, error) {
	res, err := s.agent.Call(ctx, "wordpress.copy", params, 120*time.Second)
	if err != nil {
		return "", err
	}
	var o struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(res, &o)
	return o.JobID, nil
}

// CloneTo clones an installation to another website (or folder) of the same
// owner, with a new database; returns the job id
// (WordPressToolsService::cloneTo).
func (s *WordPressTools) CloneTo(ctx context.Context, site *Website, install *WordPressInstall, input map[string]string, actor *identity.User) (string, error) {
	if err := wpWritable(site); err != nil {
		return "", err
	}
	targetID, _ := strconv.ParseInt(input["target_site"], 10, 64)
	target, err := s.sites.FindFull(ctx, s.serverID, targetID, actor)
	if err != nil {
		if err == ErrWebsiteNotFound {
			return "", &ValidationError{Fields: map[string]string{"target_site": "Choose one of the owner's PHP websites."}}
		}
		return "", err
	}
	if target.Kind != "php" || target.PhpVersion == "" ||
		!site.OwnerID.Valid || !target.OwnerID.Valid || site.OwnerID.Int64 != target.OwnerID.Int64 {
		return "", &ValidationError{Fields: map[string]string{"target_site": "Choose one of the owner's PHP websites."}}
	}
	if err := wpWritable(target); err != nil {
		return "", err
	}
	docRel := strings.Trim(strings.TrimPrefix(target.DocumentRoot, target.Root), "/")
	sub := strings.Trim(input["target_path"], "/")
	if sub != "" && !wpClonePathRe.MatchString(sub) {
		return "", &ValidationError{Fields: map[string]string{"target_path": "Use a folder name of letters, digits, dots, dashes and underscores."}}
	}
	path := strings.Trim(docRel + (func() string {
		if sub != "" {
			return "/" + sub
		}
		return ""
	})(), "/")
	var ownerID *int64
	if site.OwnerID.Valid {
		o := site.OwnerID.Int64
		ownerID = &o
	}
	if name, err := s.ownedDatabase(install.DBName, ownerID); err != nil {
		return "", err
	} else if name == "" {
		return "", &UserError{
			What: "The installation's database " + install.DBName + " is not one of the owner's databases in the panel, so it cannot be copied.",
			Fix:  "Scan the website again, or assign the database to the owner under Databases.",
		}
	}
	name := "wp" + randomHex4()
	created, err := s.dbs.CreateDatabase(ctx, s.serverID, actor, name, &target.ID)
	if err != nil {
		return "", err
	}
	user, password, err := s.dbs.CreateDBUser(ctx, s.serverID, actor, name, "localhost", false)
	if err != nil {
		return "", err
	}
	var dbUserID int64
	if err := s.db.QueryRow(`SELECT id FROM database_users WHERE server_id = ? AND username = ? AND host = 'localhost'`, s.serverID, user).Scan(&dbUserID); err != nil {
		return "", err
	}
	all := "all"
	if _, _, err := s.dbs.GrantDBAccess(ctx, s.serverID, actor, created, dbUserID, &all); err != nil {
		return "", err
	}
	var dbName string
	if err := s.db.QueryRow(`SELECT name FROM databases WHERE id = ?`, created).Scan(&dbName); err != nil {
		return "", err
	}
	urlStr := strings.TrimRight(WpBaseURL(target, &WordPressInstall{Path: path}), "/")
	params := map[string]any{
		"source":    WpTarget(site, install),
		"target":    WpTarget(target, &WordPressInstall{Path: path}),
		"mode":      "clone",
		"source_db": install.DBName,
		"db":        map[string]any{"name": dbName, "user": user, "password": password, "host": "localhost"},
		"url":       urlStr,
		"staging":   input["staging"] == "1",
		"overwrite": input["overwrite"] == "1",
	}
	job, err := s.wpCopy(ctx, params)
	if err != nil {
		// The copy failed: the fresh database is dropped again (best effort).
		_, _ = s.dbs.DropDatabase(ctx, s.serverID, actor, created, dbName)
		return "", err
	}
	if _, err := s.db.Exec(
		`INSERT INTO wordpress_installs (server_id, website_id, path, version, db_name, latest, scanned_at, cloned_from)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (website_id, path) DO UPDATE SET db_name = excluded.db_name, cloned_from = excluded.cloned_from`,
		s.serverID, target.ID, path, install.Version, dbName, install.Latest, time.Now().UTC().Format("2006-01-02T15:04:05Z"), install.ID); err != nil {
		return "", err
	}
	if err := s.trackJob(ctx, job, "Clone WordPress: "+site.Domain+" → "+urlStr, fmt.Sprintf("%d", target.ID), actor); err != nil {
		return "", err
	}
	s.auditWordPress(ctx, "wordpress.clone", "website", fmt.Sprintf("%d", site.ID), map[string]any{
		"domain": site.Domain, "path": install.Path, "target": target.Domain, "target_path": path, "database": dbName,
	})
	return job, nil
}

// Sync copies files and/or the database from one installation to another of
// the same owner (the target's wp-config.php is kept); returns the job id
// (WordPressToolsService::sync).
func (s *WordPressTools) Sync(ctx context.Context, site *Website, install *WordPressInstall, input map[string]string, actor *identity.User) (string, error) {
	if err := wpWritable(site); err != nil {
		return "", err
	}
	targetID, _ := strconv.ParseInt(input["target"], 10, 64)
	var dest WordPressInstall
	err := s.db.QueryRow(`SELECT id, website_id, path, db_name FROM wordpress_installs WHERE id = ? AND server_id = ?`, targetID, s.serverID).
		Scan(&dest.ID, &dest.WebsiteID, &dest.Path, &dest.DBName)
	if err != nil {
		if err != sql.ErrNoRows {
			return "", err
		}
		targetID = 0
	}
	var target *Website
	if targetID != 0 {
		target, err = s.sites.FindFull(ctx, s.serverID, dest.WebsiteID, actor)
		if err != nil {
			if err == ErrWebsiteNotFound {
				target = nil
			} else {
				return "", err
			}
		}
	}
	if target == nil || dest.ID == install.ID ||
		!site.OwnerID.Valid || !target.OwnerID.Valid || site.OwnerID.Int64 != target.OwnerID.Int64 {
		return "", &ValidationError{Fields: map[string]string{"target": "Choose another WordPress installation of the same owner."}}
	}
	if err := wpWritable(target); err != nil {
		return "", err
	}
	files := input["files"] == "1"
	database := input["database"] == "1"
	if !files && !database {
		return "", &ValidationError{Fields: map[string]string{"files": "Choose files, the database or both."}}
	}
	var ownerID *int64
	if site.OwnerID.Valid {
		o := site.OwnerID.Int64
		ownerID = &o
	}
	var targetOwnerID *int64
	if target.OwnerID.Valid {
		o := target.OwnerID.Int64
		targetOwnerID = &o
	}
	if database {
		if name, err := s.ownedDatabase(install.DBName, ownerID); err != nil {
			return "", err
		} else if name == "" {
			return "", &UserError{
				What: "The database " + install.DBName + " is not one of the owner's databases in the panel, so it cannot be synced.",
				Fix:  "Scan both websites again, or assign the database to the owner under Databases.",
			}
		}
		if name, err := s.ownedDatabase(dest.DBName, targetOwnerID); err != nil {
			return "", err
		} else if name == "" {
			return "", &UserError{
				What: "The database " + dest.DBName + " is not one of the owner's databases in the panel, so it cannot be synced.",
				Fix:  "Scan both websites again, or assign the database to the owner under Databases.",
			}
		}
	}
	urlStr := strings.TrimRight(WpBaseURL(target, &dest), "/")
	if urlStr == "" {
		return "", &UserError{What: "The target installation is not inside its website's document root."}
	}
	job, err := s.wpCopy(ctx, map[string]any{
		"source":    WpTarget(site, install),
		"target":    WpTarget(target, &dest),
		"mode":      "sync",
		"files":     files,
		"database":  database,
		"source_db": install.DBName,
		"target_db": dest.DBName,
		"url":       urlStr,
	})
	if err != nil {
		return "", err
	}
	what := "database"
	if files {
		if database {
			what = "files and database"
		} else {
			what = "files"
		}
	}
	if err := s.trackJob(ctx, job, "Sync WordPress "+what+": "+site.Domain+" → "+urlStr, fmt.Sprintf("%d", target.ID), actor); err != nil {
		return "", err
	}
	s.auditWordPress(ctx, "wordpress.sync", "website", fmt.Sprintf("%d", site.ID), map[string]any{
		"domain": site.Domain, "path": install.Path, "target": target.Domain, "target_path": dest.Path, "files": files, "database": database,
	})
	return job, nil
}

// Restore puts a restore point back (files and the database return to that
// moment; files added since are kept); returns the job id
// (WordPressToolsService::restore).
func (s *WordPressTools) Restore(ctx context.Context, site *Website, install *WordPressInstall, point string, actor *identity.User) (string, error) {
	if err := wpWritable(site); err != nil {
		return "", err
	}
	if !wpRestorePointRe.MatchString(point) {
		return "", &ValidationError{Fields: map[string]string{"restore_point": "Choose a restore point."}}
	}
	var ownerID *int64
	if site.OwnerID.Valid {
		o := site.OwnerID.Int64
		ownerID = &o
	}
	if name, err := s.ownedDatabase(install.DBName, ownerID); err != nil {
		return "", err
	} else if name == "" {
		return "", &UserError{What: "The installation's database is not one of the owner's databases in the panel."}
	}
	job, err := s.wpCopy(ctx, map[string]any{
		"source":        WpTarget(site, install),
		"target":        WpTarget(site, install),
		"mode":          "restore",
		"restore_point": point,
		"target_db":     install.DBName,
	})
	if err != nil {
		return "", err
	}
	if err := s.trackJob(ctx, job, "Restore WordPress: "+site.Domain+" ("+point+")", fmt.Sprintf("%d", site.ID), actor); err != nil {
		return "", err
	}
	s.auditWordPress(ctx, "wordpress.restore", "website", fmt.Sprintf("%d", site.ID), map[string]any{
		"domain": site.Domain, "path": install.Path, "restore_point": point,
	})
	return job, nil
}

// randomHex4 is six lowercase hex characters (the fresh clone database name).
func randomHex4() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte{byte(time.Now().UnixNano()), byte(time.Now().UnixNano() >> 8), byte(time.Now().UnixNano() >> 16)})
	}
	return hex.EncodeToString(b)
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
