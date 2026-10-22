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

// wpVersionCompare mirrors PHP's version_compare for the dotted versions
// the toolkit compares (WordPress releases, PHP versions): numeric parts
// compare numerically (a missing part is 0), and the release specials
// order dev < pl < alpha < beta < rc < release.
func wpVersionCompare(a, b string) int {
	parts := func(s string) []string {
		return strings.FieldsFunc(strings.ToLower(strings.TrimSpace(s)), func(r rune) bool {
			return r == '.' || r == '-' || r == '+' || r == '_'
		})
	}
	ra, rb := parts(a), parts(b)
	for i := 0; i < len(ra) || i < len(rb); i++ {
		x, y := "", ""
		if i < len(ra) {
			x = ra[i]
		}
		if i < len(rb) {
			y = rb[i]
		}
		if x == "" && y == "" {
			continue
		}
		if x == "" {
			if _, err := strconv.Atoi(y); err == nil {
				continue // a missing part is 0
			}
			return 1 // release beats a special string
		}
		if y == "" {
			if _, err := strconv.Atoi(x); err == nil {
				continue
			}
			return -1
		}
		nx, ex := strconv.Atoi(x)
		ny, ey := strconv.Atoi(y)
		if !ex && !ey {
			if nx != ny {
				if nx < ny {
					return -1
				}
				return 1
			}
			continue
		}
		if ex && ey {
			rx, ry := wpVersionRank(x), wpVersionRank(y)
			if rx != ry {
				if rx < ry {
					return -1
				}
				return 1
			}
			return strings.Compare(x, y)
		}
		// One side numeric, the other a string: PHP prepends "0" to the
		// string and compares as text.
		if ex {
			if strings.Compare("0"+x, strconv.Itoa(ny)) < 0 {
				return -1
			}
			return 1
		}
		if strings.Compare(strconv.Itoa(nx), "0"+y) < 0 {
			return -1
		}
		return 1
	}
	return 0
}

func wpVersionRank(s string) int {
	switch s {
	case "dev":
		return 0
	case "pl":
		return 1
	case "alpha", "a":
		return 2
	case "beta", "b":
		return 3
	case "rc", "pr":
		return 4
	default:
		return 5
	}
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
		Errors  []struct {
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
