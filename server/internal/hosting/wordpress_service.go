package hosting

// WordPressService — the WordPress toolkit (WordPressService.php): the
// agent finds installations in websites (reading files only) and manages
// them with WP-CLI as the website account, with the website's plugins and
// themes not loaded. The panel keeps what the scans report and decides who
// may see and change which installation (the website's visibility and
// websites.manage; the "wordpress" feature for hosting plans).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// WordPressInstall is one WordPress installation with the website that
// hosts it (wordpress_installs JOIN websites).
type WordPressInstall struct {
	ID               int64
	WebsiteID        int64
	Path             string
	Version          string
	DBName           string
	DBHost           string
	TablePrefix      string
	Debug            bool
	FileEditDisabled bool
	ConfigMode       string
	Maintenance      bool
	SiteURL          string
	Latest           string
	PluginUpdates    int
	ThemeUpdates     int
	Issues           int
	ScannedAt        string
	CheckedAt        sql.NullString

	// The toolkit switches and state (0038).
	VulnProtection bool
	Vulns          string
	VulnsCheckedAt sql.NullString
	ClonedFrom     sql.NullInt64
	CronJobID      sql.NullInt64

	// The hosting website (the join).
	Domain       string
	HTTPS        bool
	PHPVersion   string
	SystemUser   string
	Root         string
	DocumentRoot string
	OwnerName    sql.NullString

	// Derived.
	Outdated bool
	URL      *string
}

// WordPressScan is the result of a scan (WordPressService::scan).
type WordPressScan struct {
	Found   int
	Errors  []string
	Scanned int
}

// WordPressService manages the panel's WordPress installations.
type WordPressService struct {
	db       *sql.DB
	agent    AgentCaller
	serverID int64
	audit    Auditor
}

// NewWordPressService builds the service.
func NewWordPressService(db *sql.DB, ag AgentCaller, serverID int64, audit Auditor) *WordPressService {
	return &WordPressService{db: db, agent: ag, serverID: serverID, audit: audit}
}

// wpVersionCompare is PHP's version_compare($a, $b) (-1, 0, 1), used for
// WordPress and PHP versions. It is a direct port of php_version_compare so
// every edge case (1.0 < 1.0.0, 1.0rc1 < 1.0 < 1.0pl1, ...) matches PHP —
// the vulnerability-range check in wordpress_vulns.go depends on it.
func wpVersionCompare(a, b string) int {
	if a == "" && b == "" {
		return 0
	}
	if a == "" {
		return -1
	}
	if b == "" {
		return 1
	}
	return phpVersionCompareParts(canonVersion(a), canonVersion(b))
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAlnum(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// canonVersion is php_canonicalize_version: '-', '_' and '+' (and any other
// non-alphanumeric) become '.', and a '.' is inserted wherever digits meet
// non-digits ("1.0rc1" -> "1.0.rc.1").
func canonVersion(v string) []string {
	isNDig := func(c byte) bool { return !isDigit(c) && c != '.' }
	out := []byte{v[0]}
	lp := v[0]
	for i := 1; i < len(v); i++ {
		c := v[i]
		lq := out[len(out)-1]
		switch {
		case c == '-' || c == '_' || c == '+':
			if lq != '.' {
				out = append(out, '.')
			}
		case (isNDig(lp) && isDigit(c)) || (isDigit(lp) && isNDig(c)):
			if lq != '.' {
				out = append(out, '.')
			}
			out = append(out, c)
		case !isAlnum(c):
			if lq != '.' {
				out = append(out, '.')
			}
		default:
			out = append(out, c)
		}
		lp = c
	}
	return strings.Split(string(out), ".")
}

// specialForm orders the non-numeric parts like php's special_forms table;
// "#" stands for a number. An unknown string sorts below everything (-6).
func specialForm(s string) int {
	for _, f := range []struct {
		name string
		rank int
	}{
		{"dev", 0}, {"alpha", 1}, {"a", 1}, {"beta", 2}, {"b", 2},
		{"RC", 3}, {"rc", 3}, {"#", 4}, {"pl", 5}, {"p", 5},
	} {
		if strings.HasPrefix(s, f.name) { // strncmp(form, name, len(name))
			return f.rank
		}
	}
	return -6
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

func partIsNum(p string) bool { return p != "" && isDigit(p[0]) }

func phpVersionCompareParts(x, y []string) int {
	i, cmp := 0, 0
	for ; i < len(x) && i < len(y); i++ {
		p1, p2 := x[i], y[i]
		switch {
		case partIsNum(p1) && partIsNum(p2):
			n1, _ := strconv.ParseInt(p1, 10, 64)
			n2, _ := strconv.ParseInt(p2, 10, 64)
			switch {
			case n1 < n2:
				cmp = -1
			case n1 > n2:
				cmp = 1
			}
		case !partIsNum(p1) && !partIsNum(p2):
			cmp = sign(specialForm(p1) - specialForm(p2))
		case partIsNum(p1):
			cmp = sign(specialForm("#") - specialForm(p2))
		default:
			cmp = sign(specialForm(p1) - specialForm("#"))
		}
		if cmp != 0 {
			return cmp
		}
	}
	// Equal so far: the version with parts left over decides.
	if i < len(x) {
		if partIsNum(x[i]) {
			return 1
		}
		return phpVersionCompareParts(x[i:], []string{"#"})
	}
	if i < len(y) {
		if partIsNum(y[i]) {
			return -1
		}
		return phpVersionCompareParts([]string{"#"}, y[i:])
	}
	return 0
}

// WpBaseURL is the installation's address, from the website's own domain
// and document root (never from what WordPress says about itself); ” when
// the installation is outside the document root (WordPressService::baseUrl).
func WpBaseURL(site *Website, install *WordPressInstall) string {
	dir := strings.TrimRight(site.Root, "/")
	if install.Path != "" {
		dir += "/" + install.Path
	}
	doc := strings.TrimRight(site.DocumentRoot, "/")
	if dir != doc && !strings.HasPrefix(dir, doc+"/") {
		return ""
	}
	rest := dir[len(doc):]
	parts := strings.Split(rest, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	scheme := "http"
	if site.HTTPS {
		scheme = "https"
	}
	return scheme + "://" + site.Domain + strings.Join(parts, "/")
}

// WordPressOutdated reports whether a newer WordPress is known for the
// installation (WordPressService::outdated).
func WordPressOutdated(version, latest string) bool {
	return latest != "" && version != "" && wpVersionCompare(version, latest) < 0
}

// BaseURL is the installation's address, from the website's own domain and
// document root (never from what WordPress says about itself).
func (i *WordPressInstall) BaseURL() *string {
	dir := strings.TrimRight(i.Root, "/")
	if i.Path != "" {
		dir += "/" + i.Path
	}
	doc := strings.TrimRight(i.DocumentRoot, "/")
	if dir != doc && !strings.HasPrefix(dir, doc+"/") {
		return nil
	}
	rest := dir[len(doc):]
	parts := strings.Split(rest, "/")
	for k, p := range parts {
		parts[k] = url.PathEscape(p)
	}
	scheme := "http://"
	if i.HTTPS {
		scheme = "https://"
	}
	u := scheme + i.Domain + strings.Join(parts, "/")
	return &u
}

// wordpressInstallSQL is the scan columns shared by Installs and Install.
const wordpressInstallSQL = `SELECT i.id, i.website_id, i.path, i.version, i.db_name, i.db_host, i.table_prefix,
       i.debug, i.file_edit_disabled, i.config_mode, i.maintenance, i.siteurl, i.latest,
       i.plugin_updates, i.theme_updates, i.issues, i.scanned_at, i.checked_at,
       i.vuln_protection, i.vulns, i.vulns_checked_at, i.cloned_from, i.cron_job_id,
       w.domain, w.https, w.php_version, w.system_user, w.root, w.document_root, u.username
FROM wordpress_installs i JOIN websites w ON w.id = i.website_id LEFT JOIN users u ON u.id = w.owner_id`

func (i *WordPressInstall) derive() {
	i.Outdated = WordPressOutdated(i.Version, i.Latest)
	i.URL = i.BaseURL()
}

func (i *WordPressInstall) scan(row scanner) error {
	return row.Scan(&i.ID, &i.WebsiteID, &i.Path, &i.Version, &i.DBName, &i.DBHost, &i.TablePrefix,
		&i.Debug, &i.FileEditDisabled, &i.ConfigMode, &i.Maintenance, &i.SiteURL, &i.Latest,
		&i.PluginUpdates, &i.ThemeUpdates, &i.Issues, &i.ScannedAt, &i.CheckedAt,
		&i.VulnProtection, &i.Vulns, &i.VulnsCheckedAt, &i.ClonedFrom, &i.CronJobID,
		&i.Domain, &i.HTTPS, &i.PHPVersion, &i.SystemUser, &i.Root, &i.DocumentRoot, &i.OwnerName)
}

// scanner is the row interface shared by *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// Installs is the installations the user may see, with their website
// (WordPressService::installs).
func (s *WordPressService) Installs(ctx context.Context, u *identity.User, websiteID *int64, filter string) ([]WordPressInstall, error) {
	args := []any{s.serverID}
	cond := "1 = 1"
	if !IsAll(u) {
		cond = "w.owner_id = ?"
		args = append(args, u.ID)
	}
	q := wordpressInstallSQL + " WHERE i.server_id = ? AND " + cond
	if websiteID != nil {
		q += " AND i.website_id = ?"
		args = append(args, *websiteID)
	}
	q += " ORDER BY w.domain, i.path"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WordPressInstall{}
	for rows.Next() {
		var i WordPressInstall
		if err := i.scan(rows); err != nil {
			return nil, err
		}
		i.derive()
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch filter {
	case "outdated":
		out = wpFilter(out, func(i WordPressInstall) bool {
			return i.Outdated || i.PluginUpdates+i.ThemeUpdates > 0
		})
	case "issues":
		out = wpFilter(out, func(i WordPressInstall) bool { return i.Issues > 0 })
	}
	return out, nil
}

func wpFilter(in []WordPressInstall, keep func(WordPressInstall) bool) []WordPressInstall {
	out := []WordPressInstall{}
	for _, i := range in {
		if keep(i) {
			out = append(out, i)
		}
	}
	return out
}

// Install is one installation of a website (WordPressService::install).
func (s *WordPressService) Install(ctx context.Context, site *Website, id int64) (*WordPressInstall, error) {
	var i WordPressInstall
	err := s.db.QueryRowContext(ctx, wordpressInstallSQL+
		" WHERE i.id = ? AND i.website_id = ? AND i.server_id = ?", id, site.ID, s.serverID).
		Scan(&i.ID, &i.WebsiteID, &i.Path, &i.Version, &i.DBName, &i.DBHost, &i.TablePrefix,
			&i.Debug, &i.FileEditDisabled, &i.ConfigMode, &i.Maintenance, &i.SiteURL, &i.Latest,
			&i.PluginUpdates, &i.ThemeUpdates, &i.Issues, &i.ScannedAt, &i.CheckedAt,
			&i.VulnProtection, &i.Vulns, &i.VulnsCheckedAt, &i.ClonedFrom, &i.CronJobID,
			&i.Domain, &i.HTTPS, &i.PHPVersion, &i.SystemUser, &i.Root, &i.DocumentRoot, &i.OwnerName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &UserError{What: "That WordPress installation was not found; scan the website again."}
	}
	if err != nil {
		return nil, err
	}
	i.derive()
	return &i, nil
}

// wpWithin reports whether p is base or inside base (a path prefix).
func wpWithin(p, base string) bool {
	return p == base || strings.HasPrefix(p, strings.TrimRight(base, "/")+"/")
}

// Scan looks for WordPress installations in websites: each website folder
// once (a website inside another's folder is scanned with it), each
// installation recorded under the website whose document root holds it
// (WordPressService::scan).
func (s *WordPressService) Scan(ctx context.Context, sites []Website) (*WordPressScan, error) {
	// Only PHP websites with a PHP version can hold WordPress.
	var php []Website
	for _, st := range sites {
		if st.Kind == "php" && st.PhpVersion != "" {
			php = append(php, st)
		}
	}
	if len(php) == 0 {
		return nil, &UserError{What: "There is no PHP website to scan."}
	}
	var tops []Website
	for _, st := range php {
		top := true
		for _, o := range php {
			if o.ID != st.ID && o.Root != st.Root && wpWithin(st.Root, o.Root) {
				top = false
				break
			}
		}
		if top {
			tops = append(tops, st)
		}
	}
	siteParams := make([]map[string]any, 0, len(tops))
	for _, t := range tops {
		siteParams = append(siteParams, map[string]any{
			"domain": t.Domain, "user": t.SystemUser, "root": t.Root, "php_version": t.PhpVersion,
		})
	}
	res, err := s.agent.Call(ctx, "wordpress.scan", map[string]any{"sites": siteParams}, 290*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Errors []struct {
			Domain string `json:"domain"`
			Error  string `json:"error"`
		} `json:"errors"`
		Installs []struct {
			Domain           string `json:"domain"`
			Path             string `json:"path"`
			Version          string `json:"version"`
			DBName           string `json:"db_name"`
			DBHost           string `json:"db_host"`
			TablePrefix      string `json:"table_prefix"`
			Debug            bool   `json:"debug"`
			FileEditDisabled bool   `json:"file_edit_disabled"`
			ConfigMode       string `json:"config_mode"`
			Maintenance      bool   `json:"maintenance"`
		} `json:"installs"`
		Latest string `json:"latest"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	byDomain := map[string]Website{}
	for _, t := range tops {
		byDomain[strings.ToLower(t.Domain)] = t
	}
	failed := map[string]string{}
	for _, e := range o.Errors {
		failed[strings.ToLower(e.Domain)] = e.Error
	}
	// Websites whose folders were scanned: their old rows are replaced.
	covered := map[int64]Website{}
	for _, st := range php {
		for _, t := range tops {
			if _, bad := failed[strings.ToLower(t.Domain)]; !bad && wpWithin(st.Root, t.Root) {
				covered[st.ID] = st
			}
		}
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	latest := ""
	if wpLatestRe.MatchString(o.Latest) {
		latest = o.Latest
	}
	keep := map[int64][]string{}
	found := 0
	for _, inst := range o.Installs {
		top, known := byDomain[strings.ToLower(inst.Domain)]
		if !known {
			continue
		}
		dir := strings.TrimRight(top.Root, "/")
		if inst.Path != "" {
			dir += "/" + inst.Path
		}
		// The website (of the same account) whose document root holds it.
		var best Website
		bestLen := -1
		for _, st := range covered {
			if st.SystemUser != top.SystemUser {
				continue
			}
			for _, base := range []struct {
				path  string
				bonus int
			}{{st.DocumentRoot, 1000}, {st.Root, 0}} {
				if wpWithin(dir, base.path) && len(base.path)+base.bonus > bestLen {
					best = st
					bestLen = len(base.path) + base.bonus
				}
			}
		}
		if bestLen < 0 {
			continue
		}
		path := strings.TrimLeft(dir[len(strings.TrimRight(best.Root, "/")):], "/")
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO wordpress_installs (server_id, website_id, path, version, db_name, db_host, table_prefix,
			    debug, file_edit_disabled, config_mode, maintenance, latest, scanned_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (website_id, path) DO UPDATE SET version = excluded.version, db_name = excluded.db_name,
			   db_host = excluded.db_host, table_prefix = excluded.table_prefix,
			   debug = excluded.debug, file_edit_disabled = excluded.file_edit_disabled,
			   config_mode = excluded.config_mode, maintenance = excluded.maintenance,
			   latest = CASE WHEN excluded.latest <> '' THEN excluded.latest ELSE wordpress_installs.latest END,
			   scanned_at = excluded.scanned_at`,
			s.serverID, best.ID, path, inst.Version, inst.DBName, inst.DBHost, inst.TablePrefix,
			boolInt(inst.Debug), boolInt(inst.FileEditDisabled), inst.ConfigMode, boolInt(inst.Maintenance), latest, now); err != nil {
			return nil, err
		}
		keep[best.ID] = append(keep[best.ID], path)
		found++
	}
	for wid := range covered {
		paths := keep[wid]
		q := "DELETE FROM wordpress_installs WHERE website_id = ?"
		args := []any{wid}
		if len(paths) > 0 {
			q += " AND path NOT IN (" + strings.Repeat("?,", len(paths)-1) + "?)"
			for _, p := range paths {
				args = append(args, p)
			}
		}
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			return nil, err
		}
	}
	outErrors := []string{}
	for domain, msg := range failed {
		outErrors = append(outErrors, domain+": "+msg)
	}
	s.auditWordPress(ctx, "wordpress.scan", "server", fmt.Sprintf("%d", s.serverID), map[string]any{
		"websites": len(tops), "found": found,
	})
	return &WordPressScan{Found: found, Errors: outErrors, Scanned: len(tops)}, nil
}

var wpLatestRe = regexp.MustCompile(`^[0-9][0-9A-Za-z.+-]{0,30}$`)

// auditWordPress records a WordPress action (the audit hook is already
// bound to the acting user).
func (s *WordPressService) auditWordPress(ctx context.Context, action, targetType, target string, details map[string]any) {
	if s.audit != nil {
		s.audit(ctx, action, targetType, target, "success", details)
	}
}

// WordPressCheck is one security and health check.
type WordPressCheck struct {
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"` // '' when there is no one-click fix
}

// WpAdmin is one administrator of the installation.
type WpAdmin struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

// WpPlugin is one installed plugin (wordpress.info).
type WpPlugin struct {
	Name       string `json:"name"`
	Title      string `json:"title"`
	Version    string `json:"version"`
	Update     string `json:"update"` // '' when current
	Active     bool   `json:"active"`
	AutoUpdate bool   `json:"auto_update"`
	MustUse    bool   `json:"must_use"`
}

// WpTheme is one installed theme (wordpress.info).
type WpTheme struct {
	Name       string `json:"name"`
	Title      string `json:"title"`
	Version    string `json:"version"`
	Update     string `json:"update"`
	Active     bool   `json:"active"`
	Parent     bool   `json:"parent"`
	AutoUpdate bool   `json:"auto_update"`
}

// WpCoreInfo is the update state of the WordPress core.
type WpCoreInfo struct {
	Latest    string `json:"latest"` // '' when WordPress.org was not reached
	PhpNeeded string `json:"php_needed"`
	CheckedAt int64  `json:"checked_at"`
}

// WpDbInfo is the installation's database (wordpress.info).
type WpDbInfo struct {
	Name   string `json:"name"`
	Host   string `json:"host"`
	Prefix string `json:"prefix"`
	Tables int    `json:"tables"`
	Bytes  int64  `json:"bytes"`
}

// WpConstants are the WordPress constants the overview reads.
type WpConstants struct {
	Debug            bool
	DebugDisplay     bool
	FileEditDisabled bool
	FileModsDisabled bool
	AutoCore         string // undefined '' or the PHP literal "'minor'", "true", "false"
	AutoDisabled     bool
	DisableCron      bool
}

// WordPressInfo is the installation's overview (wordpress.info) and the
// checks computed from it (WordPressService::info).
type WordPressInfo struct {
	Title            string `json:"title"`
	Version          string `json:"version"`
	SiteURL          string `json:"siteurl"`
	Core             WpCoreInfo
	Constants        WpConstants
	Plugins          []WpPlugin
	Themes           []WpTheme
	Admins           []WpAdmin
	Maintenance      bool `json:"maintenance"`
	Multisite        bool `json:"multisite"`
	SearchEngines    bool `json:"search_engines"`
	AdminLoginExists bool `json:"admin_login_exists"`
	DB               WpDbInfo
	MySQL            string `json:"mysql"`
	ConfigMode       string `json:"config_mode"`
	Checks           []WordPressCheck
}

// Info is the installation's overview (as the website account) with the
// security checks; the stored summary is refreshed
// (WordPressService::info).
func (s *WordPressService) Info(ctx context.Context, site *Website, install *WordPressInstall, force bool) (*WordPressInfo, error) {
	timeout := 120 * time.Second
	if force {
		timeout = 180 * time.Second
	}
	res, err := s.agent.Call(ctx, "wordpress.info", map[string]any{
		"domain": site.Domain, "user": site.SystemUser, "root": site.Root,
		"php_version": site.PhpVersion, "path": install.Path, "force": force,
	}, timeout)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Title         string         `json:"title"`
		Version       string         `json:"version"`
		SiteURL       string         `json:"siteurl"`
		Core          WpCoreInfo     `json:"core"`
		Constants     map[string]any `json:"constants"`
		Plugins       []WpPlugin     `json:"plugins"`
		Themes        []WpTheme      `json:"themes"`
		Admins        []WpAdmin      `json:"admins"`
		Maintenance   bool           `json:"maintenance"`
		Multisite     bool           `json:"multisite"`
		SearchEngines bool           `json:"search_engines"`
		AdminExists   bool           `json:"admin_login_exists"`
		DB            WpDbInfo       `json:"db"`
		MySQL         string         `json:"mysql"`
		ConfigMode    string         `json:"config_mode"`
	}
	if err := json.Unmarshal(res, &raw); err != nil {
		return nil, err
	}
	consts := WpConstants{}
	for k, v := range raw.Constants {
		switch k {
		case "WP_DEBUG":
			consts.Debug = wpAsBool(v)
		case "WP_DEBUG_DISPLAY":
			consts.DebugDisplay = wpAsBool(v)
		case "DISALLOW_FILE_EDIT":
			consts.FileEditDisabled = wpAsBool(v)
		case "DISALLOW_FILE_MODS":
			consts.FileModsDisabled = wpAsBool(v)
		case "WP_AUTO_UPDATE_CORE":
			switch t := v.(type) {
			case string:
				consts.AutoCore = t
			case bool:
				if t {
					consts.AutoCore = "true"
				} else {
					consts.AutoCore = "false"
				}
			}
		case "AUTOMATIC_UPDATER_DISABLED":
			consts.AutoDisabled = wpAsBool(v)
		case "DISABLE_WP_CRON":
			consts.DisableCron = wpAsBool(v)
		}
	}
	if raw.Plugins == nil {
		raw.Plugins = []WpPlugin{}
	}
	if raw.Themes == nil {
		raw.Themes = []WpTheme{}
	}
	if raw.Admins == nil {
		raw.Admins = []WpAdmin{}
	}
	info := &WordPressInfo{
		Title: raw.Title, Version: raw.Version, SiteURL: raw.SiteURL, Core: raw.Core,
		Constants: consts, Plugins: raw.Plugins, Themes: raw.Themes, Admins: raw.Admins,
		Maintenance: raw.Maintenance, Multisite: raw.Multisite, SearchEngines: raw.SearchEngines,
		AdminLoginExists: raw.AdminExists, DB: raw.DB, MySQL: raw.MySQL, ConfigMode: raw.ConfigMode,
	}
	info.Checks = WordPressChecks(info, site)
	pluginUpdates, themeUpdates, issues := 0, 0, 0
	for _, p := range info.Plugins {
		if p.Update != "" {
			pluginUpdates++
		}
	}
	for _, t := range info.Themes {
		if t.Update != "" {
			themeUpdates++
		}
	}
	for _, c := range info.Checks {
		if c.Level != "info" {
			issues++
		}
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE wordpress_installs SET version = ?, siteurl = ?,
		 latest = CASE WHEN ? <> '' THEN ? ELSE latest END,
		 plugin_updates = ?, theme_updates = ?, issues = ?, maintenance = ?,
		 debug = ?, file_edit_disabled = ?, config_mode = ?, checked_at = ? WHERE id = ?`,
		info.Version, info.SiteURL, info.Core.Latest, info.Core.Latest,
		pluginUpdates, themeUpdates, issues, boolInt(info.Maintenance),
		boolInt(info.Constants.Debug), boolInt(info.Constants.FileEditDisabled),
		info.ConfigMode, time.Now().UTC().Format("2006-01-02T15:04:05Z"), install.ID)
	if err != nil {
		return nil, err
	}
	return info, nil
}

// wpAsBool is PHP's !empty() for the constants the agent reports as JSON
// booleans (or strings).
func wpAsBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "1" || t == "true"
	case float64:
		return t != 0
	}
	return false
}

// WordPressChecks are the security and health checks: [level
// (danger|warning|info), title, detail, fix op or ”] (WordPressService::checks).
func WordPressChecks(info *WordPressInfo, site *Website) []WordPressCheck {
	c := []WordPressCheck{}
	add := func(level, title, detail string, fixOp ...string) {
		fix := ""
		if len(fixOp) > 0 {
			fix = fixOp[0] // optional, PHP's $fix = ''
		}
		c = append(c, WordPressCheck{Level: level, Title: title, Detail: detail, Fix: fix})
	}
	if info.Core.Latest != "" && wpVersionCompare(info.Version, info.Core.Latest) < 0 {
		add("danger", "WordPress "+info.Core.Latest+" is available",
			"This site runs "+info.Version+". Updates close security holes; back the website up first.", "core_update")
	}
	needed := info.Core.PhpNeeded
	if needed != "" && wpVersionCompare(site.PhpVersion, needed) < 0 {
		add("danger", "The newest WordPress needs PHP "+needed,
			"The website uses PHP "+site.PhpVersion+"; choose a newer PHP version under PHP & settings.")
	} else if wpVersionCompare(site.PhpVersion, "8.1") < 0 {
		add("warning", "PHP "+site.PhpVersion+" no longer gets security fixes",
			"Choose PHP 8.1 or newer under PHP & settings (check that the plugins support it).")
	}
	pTitles := []string{}
	for _, p := range info.Plugins {
		if p.Update != "" {
			pTitles = append(pTitles, p.Title)
		}
	}
	if len(pTitles) > 0 {
		add("warning", fmt.Sprintf("%d plugin update(s) available", len(pTitles)), strings.Join(pTitles, ", "), "plugin_update")
	}
	tTitles := []string{}
	for _, t := range info.Themes {
		if t.Update != "" {
			tTitles = append(tTitles, t.Title)
		}
	}
	if len(tTitles) > 0 {
		add("warning", fmt.Sprintf("%d theme update(s) available", len(tTitles)), strings.Join(tTitles, ", "), "theme_update")
	}
	if info.Constants.Debug {
		add("warning", "Debugging is on",
			"WP_DEBUG can show errors, paths and queries to visitors.", "debug_off")
	}
	if !info.Constants.FileEditDisabled && !info.Constants.FileModsDisabled {
		add("warning", "The theme and plugin file editor is on",
			"Anyone who gets into an administrator account can change PHP code from the dashboard.", "disable_file_edit")
	}
	if mode := info.ConfigMode; mode != "" {
		if n, err := strconv.ParseInt(mode, 8, 64); err == nil && n&0o027 != 0 {
			add("warning", "wp-config.php is readable by others ("+mode+")",
				"It holds the database password; only the website account needs to read it.", "protect_config")
		}
	}
	if info.AdminLoginExists {
		add("warning", "A user is called \"admin\"",
			"It is the first name attackers try. Create another administrator in WordPress and delete this one.")
	}
	if site.HTTPS && strings.HasPrefix(info.SiteURL, "http://") {
		add("warning", "WordPress uses http:// although the website has HTTPS",
			"Change the WordPress and site address to https:// under Settings → General in WordPress.")
	} else if !site.HTTPS {
		add("warning", "The website has no HTTPS",
			"Sign-ins and cookies travel unencrypted. Issue a certificate under SSL / HTTPS.")
	}
	auto := info.Constants.AutoCore
	if info.Constants.AutoDisabled || auto == "false" {
		add("warning", "Automatic WordPress updates are off",
			"Security releases are not installed by themselves.")
	}
	inactive := 0
	for _, p := range info.Plugins {
		if !p.Active {
			inactive++
		}
	}
	if inactive > 0 {
		add("info", fmt.Sprintf("%d inactive plugin(s)", inactive),
			"Plugins that are not used are still reachable on the web; delete them in WordPress if you do not need them.")
	}
	if info.Maintenance {
		add("info", "Maintenance mode is on",
			"Visitors see \"Briefly unavailable for scheduled maintenance\".", "maintenance_off")
	}
	if !info.SearchEngines {
		add("info", "Search engines are asked not to index this site",
			"Settings → Reading → Search engine visibility.", "search_engines_on")
	}
	if info.Core.Latest == "" {
		// WordPress records the time of a check before asking, so only a
		// missing answer tells that WordPress.org was not reached.
		add("warning", "Updates could not be checked",
			"WordPress.org could not be reached from this server, so available updates are not known. Use \"Check for updates\" to try again.")
	}
	return c
}

// WordPressManageResult is the manage reply
// (WordPressService::manage): the user-facing message, the started job
// (updates run in the background) or the verify_core outcome.
type WordPressManageResult struct {
	Message  string
	Job      string   // "" when the operation finished itself
	JobKind  string   // the job's tracked label (for the tracker)
	Files    []string // verify_core: the differing files (nil otherwise)
	VerifyOK bool     // verify_core: every core file matches
}

// wpOps are the panel's operations (WordPressService::OPS, labels only —
// the agent decides what runs and where).
var wpOps = map[string]string{
	"core_update":        "Update WordPress",
	"plugin_update":      "Update plugins",
	"theme_update":       "Update themes",
	"plugin_activate":    "Activate plugin",
	"plugin_deactivate":  "Deactivate plugin",
	"plugin_auto_on":     "Turn automatic updates on",
	"plugin_auto_off":    "Turn automatic updates off",
	"theme_activate":     "Activate theme",
	"theme_auto_on":      "Turn automatic updates on",
	"theme_auto_off":     "Turn automatic updates off",
	"maintenance_on":     "Turn maintenance mode on",
	"maintenance_off":    "Turn maintenance mode off",
	"disable_file_edit":  "Turn off the file editor",
	"debug_off":          "Turn debugging off",
	"protect_config":     "Protect wp-config.php",
	"core_auto_update":   "Automatic WordPress updates",
	"verify_core":        "Verify core files",
	"search_engines_on":  "Let search engines index the site",
	"search_engines_off": "Ask search engines not to index the site",
}

var wpItemRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// Manage runs one operation on the installation (WordPressService::manage):
// the updates run as agent jobs, the verify lists the differing core
// files, the rest finish on the spot.
func (s *WordPressService) Manage(ctx context.Context, site *Website, install *WordPressInstall, op string, items []string, value string) (*WordPressManageResult, error) {
	label, ok := wpOps[op]
	if !ok {
		return nil, &UserError{What: "Unknown operation."}
	}
	if site.AccountSuspended {
		return nil, &UserError{What: "The hosting account is suspended; WordPress cannot be changed."}
	}
	clean := []string{}
	seen := map[string]bool{}
	for _, it := range items {
		if it == "" || seen[it] || !wpItemRe.MatchString(it) {
			continue
		}
		seen[it] = true
		clean = append(clean, it)
	}
	params := WpTarget(site, install)
	params["op"] = op
	params["items"] = clean
	if op == "core_auto_update" {
		if value != "minor" && value != "major" && value != "off" {
			return nil, &UserError{What: "Choose minor, all or no automatic updates."}
		}
		params["value"] = value
	}
	timeout := 150 * time.Second
	if op == "verify_core" {
		timeout = 330 * time.Second
	}
	res, err := s.agent.Call(ctx, "wordpress.manage", params, timeout)
	if err != nil {
		return nil, err
	}
	s.auditWordPress(ctx, "wordpress."+op, "website", fmt.Sprintf("%d", site.ID), map[string]any{
		"domain": site.Domain, "path": install.Path, "items": clean, "value": value,
	})
	where := site.Domain
	if install.Path != "" {
		where += " (" + install.Path + ")"
	}
	out := &WordPressManageResult{}
	var o struct {
		JobID string   `json:"job_id"`
		OK    bool     `json:"ok"`
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	if o.JobID != "" {
		out.Job = o.JobID
		out.JobKind = label + ": " + where
		out.Message = label + " for " + where + " started; you will be notified when it finishes."
		return out, nil
	}
	if op == "verify_core" {
		if o.Files == nil {
			o.Files = []string{}
		}
		out.VerifyOK = o.OK
		if o.OK {
			out.Message = "All WordPress core files match the official release."
		} else {
			out.Message = strconv.Itoa(len(o.Files)) + " core file(s) differ from the official release."
		}
		out.Files = o.Files
		return out, nil
	}
	out.Message = label + ": done for " + where + "."
	return out, nil
}

var wpLoginFileRe = regexp.MustCompile(`^whacp-login-[a-f0-9]{32}\.php$`)

// LoginURL is a one-time sign-in address for an administrator of the
// installation (WordPressService::loginUrl).
func (s *WordPressService) LoginURL(ctx context.Context, site *Website, install *WordPressInstall, adminID int64) (string, error) {
	if site.AccountSuspended {
		return "", &UserError{What: "The hosting account is suspended."}
	}
	base := install.BaseURL()
	if base == nil {
		return "", &UserError{What: "This installation is not inside the website's document root, so it cannot be opened from here."}
	}
	res, err := s.agent.Call(ctx, "wordpress.login", map[string]any{
		"domain": site.Domain, "user": site.SystemUser, "root": site.Root,
		"php_version": site.PhpVersion, "path": install.Path, "admin_id": adminID,
	}, 90*time.Second)
	if err != nil {
		return "", err
	}
	var o struct {
		File string `json:"file"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return "", err
	}
	if !wpLoginFileRe.MatchString(o.File) {
		return "", &UserError{What: "The sign-in link could not be created."}
	}
	s.auditWordPress(ctx, "wordpress.login", "website", fmt.Sprintf("%d", site.ID), map[string]any{
		"domain": site.Domain, "path": install.Path, "admin_id": adminID,
	})
	return strings.TrimRight(*base, "/") + "/" + o.File, nil
}

// DatabaseID is the panel's ID of a database the user may open in the
// browser, by name within the user's visibility (WordPressService::databaseId).
func (s *WordPressService) DatabaseID(ctx context.Context, u *identity.User, name string) (int64, bool) {
	cond := "1 = 1"
	args := []any{s.serverID, name}
	if !IsAll(u) {
		cond = "owner_id = ?"
		args = append(args, u.ID)
	}
	var id int64
	err := s.db.QueryRowContext(ctx,
		"SELECT id FROM databases WHERE server_id = ? AND name = ? AND "+cond, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false
	}
	if err != nil {
		return 0, false
	}
	return id, true
}
