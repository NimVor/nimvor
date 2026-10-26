package http

// This file ports the website hub page (SitesController::show — the
// Overview tab) and the advanced page (SitesController::advanced): the
// hero card with the live facts, the "Site managing" tile grid with the
// feature search, and the server/runtime details with the recent tasks and
// the danger zone.
//
// Routes:
//   GET /sites/{id}           (websites.view)
//   GET /sites/{id}/advanced  (websites.manage)
//
// Tiles and search entries that would link to an area the Go panel has not
// ported yet (FTP, DNS, mail, applications) are left out; they come back
// with their areas. The hub's import banner is part of the import area.

import (
	"context"
	"encoding/json"
	"errors"
	"hash/crc32"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// SiteTile is one tile of the hub's "Site managing" grid.
type SiteTile struct {
	Label string
	Icon  string
	URL   string
	Note  string
	Key   string // the lower-cased label (data-wcp-tile)
}

// SiteSearchEntry is one entry of the hub's feature search list.
type SiteSearchEntry struct {
	Label    string
	URL      string
	Icon     string
	Feature  string // lower-cased label + the extra search words
	LabelKey string // the lower-cased label
}

// SiteInspection is the agent's sites.inspect reply (the state of the
// website on disk).
type SiteInspection struct {
	Domain        string   `json:"domain"`
	ConfigExists  bool     `json:"config_exists"`
	Enabled       bool     `json:"enabled"`
	PoolVersions  []string `json:"pool_versions"`
	RootExists    bool     `json:"root_exists"`
	DiskBytes     int64    `json:"disk_bytes"`
	DiskComplete  bool     `json:"disk_complete"`
	ConfigPreview string   `json:"config_preview"`
	FPMService    string   `json:"fpm_service"`
}

// showSiteOf resolves the website for a /sites/{id} route (nil = not found
// or no access for the signed-in user).
func (s *Server) showSiteOf(r *http.Request, id string) *hosting.Website {
	user := userFrom(r.Context())
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil
	}
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), n, user)
	if err != nil || site == nil {
		return nil
	}
	return site
}

// siteShow renders the website hub page (route "sites.show").
func (s *Server) siteShow(w http.ResponseWriter, r *http.Request) {
	site := s.showSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.showNotFound(w)
		return
	}
	ctx := r.Context()
	user := userFrom(ctx)
	// A running import may have finished (or failed) in the meantime: finish
	// it, like SitesController::show does before rendering.
	impSvc := s.importService(r, user)
	if stillThere, err := impSvc.Refresh(ctx, site); err != nil {
		s.log.Error("import refresh", "id", site.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	} else if !stillThere {
		sess := sessionFrom(r.Context())
		sess.Flash("error", "The import of "+site.Domain+" failed; the old website is unchanged. See the job log for details.")
		http.Redirect(w, r, "/websites", http.StatusSeeOther)
		return
	}
	// The refresh may have activated the website: re-read the row.
	if fresh, err := hosting.NewWebsiteRepository(s.db).FindFull(ctx, s.currentServerID(r), site.ID, user); err == nil {
		site = fresh
	}
	importRec, err := impSvc.ImportOf(ctx, site.ID)
	if err != nil {
		s.log.Error("import record", "id", site.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ov := s.siteOverview(r, user)
	summary, err := ov.Summary(ctx, s.currentServerID(r), site, user)
	if err != nil {
		s.log.Error("site summary", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	serverIP, _ := ov.ServerIP(ctx)
	dbBytes, err := ov.DatabaseBytes(ctx, s.currentServerID(r), site, user)
	if err != nil {
		s.log.Error("site database bytes", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	inspect, inspectError := s.sitesInspect(ctx, site.Domain)
	tiles := s.showTiles(r, site, summary, inspect)
	initial := ""
	if len(site.Domain) > 0 {
		initial = strings.ToUpper(site.Domain[:1])
	}
	dbBytesText := ""
	if dbBytes != nil {
		dbBytesText = bytesText(*dbBytes)
	}
	importDate := ""
	if importRec != nil && importRec.CreatedAt != "" {
		importDate = importRec.CreatedAt
		if t, err := time.Parse(time.RFC3339, importRec.CreatedAt); err == nil {
			importDate = t.UTC().Format("2006-01-02")
		}
	}
	sess := sessionFrom(r.Context())
	okMsg, _ := sess.PullFlash("success")
	errMsg, _ := sess.PullFlash("error")
	jobMsg, _ := sess.PullFlash("job")
	data := map[string]any{
		"User":           user,
		"CSRF":           session.CSRFToken(sess),
		"Site":           site,
		"Summary":        summary,
		"ServerIP":       serverIP,
		"DBBytesText":    dbBytesText,
		"Inspect":        inspect,
		"InspectError":   inspectError,
		"ConfigMissing":  inspect != nil && !inspect.ConfigExists,
		"Handler":        showHandler(site),
		"Initial":        initial,
		"SSLLabel":       showSSLLabel(site),
		"Disk":           showDisk(inspect),
		"LastBackupAgo":  showLastBackupAgo(summary),
		"Tint":           showTint(site.Domain),
		"BandwidthReset": user.RoleScope == "all",
		"CanManage":      s.can(r, "websites.manage"),
		"CanImport":      s.can(r, "import.run"),
		"Import":         importRec,
		"ImportDate":     importDate,
		"Success":        okMsg,
		"Error":          errMsg,
		"Job":            jobMsg,
		"Tiles":          tiles,
		"Search":         s.showSearch(r, site, tiles),
	}
	s.render(w, "site_show", data)
}

// siteAdvanced renders the website's advanced page (route "sites.advanced").
func (s *Server) siteAdvanced(w http.ResponseWriter, r *http.Request) {
	site := s.showSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.showNotFound(w)
		return
	}
	ctx := r.Context()
	user := userFrom(ctx)
	jobs, err := hosting.NewJobTracker(s.db).RecentFor(ctx, s.currentServerID(r), "website", strconv.FormatInt(site.ID, 10), 10)
	if err != nil {
		s.log.Error("recent jobs", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	inspect, inspectError := s.sitesInspect(ctx, site.Domain)
	type jobRow struct {
		JobID  string
		Kind   string
		Status string
		Ago    string
	}
	jobRows := make([]jobRow, 0, len(jobs))
	for _, j := range jobs {
		jobRows = append(jobRows, jobRow{JobID: j.JobID, Kind: j.Kind, Status: j.Status, Ago: timeAgo(j.CreatedAt)})
	}
	sess := sessionFrom(r.Context())
	okMsg, _ := sess.PullFlash("success")
	errMsg, _ := sess.PullFlash("error")
	data := map[string]any{
		"User":           user,
		"CSRF":           session.CSRFToken(sess),
		"Site":           site,
		"Inspect":        inspect,
		"InspectError":   inspectError,
		"ConfigMissing":  inspect != nil && !inspect.ConfigExists,
		"KindLabel":      showKindLabel(site.Kind),
		"Disk":           showDisk(inspect),
		"LastAppliedAgo": timeAgo(site.AppliedAt.String),
		"Jobs":           jobRows,
		"OwnerName":      site.OwnerName.String,
		"IPPool":         s.advancedIPPool(r, user, site),
		"Success":        okMsg,
		"Error":          errMsg,
	}
	s.render(w, "site_advanced", data)
}

// showNotFound renders the 404 page for a website the user cannot see
// (mirrors SitesController::notFound).
func (s *Server) showNotFound(w http.ResponseWriter) {
	renderStatus(w, s, "error", map[string]any{
		"Title":   "Website not found",
		"Message": "This website does not exist or you do not have access to it.",
	}, http.StatusNotFound)
}

// siteOverview builds the read-only overview service.
func (s *Server) siteOverview(r *http.Request, user *identity.User) *hosting.SiteOverview {
	return hosting.NewSiteOverview(s.db, s.agent, s.settings, s.databaseService(r, user))
}

// sitesInspect asks the agent for the website's on-disk state
// (SitesController::inspect), failing safe: (nil, message) when the agent
// is unreachable or replies unreadably.
func (s *Server) sitesInspect(ctx context.Context, domain string) (*SiteInspection, string) {
	res, err := s.agent.Call(ctx, "sites.inspect", map[string]any{"domain": domain})
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			return nil, ae.Message
		}
		s.log.Error("sites inspect", "error", err)
		return nil, "The WhaCP agent could not be reached."
	}
	var out SiteInspection
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, "The WhaCP agent answered with an unreadable state."
	}
	return &out, ""
}

// showTiles is the hub's "Site managing" grid: one tile per area the
// website has and the user may open (SiteMenu::url's permission gate), each
// with its small note. Areas the Go panel has not ported yet (FTP, DNS,
// mail, applications) have no tile until they land.
func (s *Server) showTiles(r *http.Request, site *hosting.Website, summary *hosting.SiteSummary, inspect *SiteInspection) []SiteTile {
	id := "/sites/" + strconv.FormatInt(site.ID, 10)
	php := site.Kind == "php"
	var tiles []SiteTile
	add := func(perm, label, icon, url, note string) {
		if s.can(r, perm) {
			tiles = append(tiles, SiteTile{Label: label, Icon: icon, URL: url, Note: note, Key: strings.ToLower(label)})
		}
	}
	add("files.manage", "Files", "folder2-open", id+"/files", showDisk(inspect))
	add("databases.view", "Databases", "database", id+"/databases", strconv.Itoa(summary.Databases))
	add("websites.manage", "MongoDB", "database-fill-gear", id+"/mongo", "")
	add("ssl.view", "SSL certificates", "shield-lock", id+"/ssl", showSSLState(site))
	add("cron.view", "Scheduler", "calendar3", id+"/cron", showCount(summary.Cron))
	add("backups.view", "Backup copies", "archive", id+"/backups", showCount(summary.Backups))
	add("files.manage", "Terminal", "terminal", id+"/terminal", "")
	add("websites.view", "Web firewall", "shield-shaded", id+"/firewall", "")
	add("websites.manage", "Settings", "gear", id+"/settings", "")
	if php {
		add("websites.manage", "PHP settings", "filetype-php", id+"/settings#php", site.PhpVersion)
		add("websites.view", "WordPress", "wordpress", id+"/wordpress", "")
	}
	add("deploy.manage", "Git deployment", "git", id+"/git", showGitBranch(summary))
	add("workers.manage", "Workers", "gear-wide-connected", id+"/workers", showCount(summary.Workers))
	add("websites.manage", "Redis", "lightning", id+"/redis", "")
	add("logs.view", "Logs", "journal-text", id+"/logs", "")
	add("logs.view", "Statistics", "bar-chart-line", id+"/stats", "")
	add("websites.manage", "Subdomains", "diagram-3", id+"/subdomains", showCount(summary.Subdomains))
	add("websites.manage", "Addon domains", "plus-square", id+"/addons", showCount(summary.Addons))
	add("websites.manage", "Manual settings", "code-slash", id+"/advanced", "")
	return tiles
}

// showSearch is the hub's feature-search list: the tiles plus the common
// phrasings (only the ones whose target is ported).
func (s *Server) showSearch(r *http.Request, site *hosting.Website, tiles []SiteTile) []SiteSearchEntry {
	id := "/sites/" + strconv.FormatInt(site.ID, 10)
	php := site.Kind == "php"
	var out []SiteSearchEntry
	for _, t := range tiles {
		out = append(out, SiteSearchEntry{
			Label: t.Label, URL: t.URL, Icon: t.Icon,
			Feature:  strings.ToLower(t.Label),
			LabelKey: strings.ToLower(t.Label),
		})
	}
	add := func(perm, label, url, icon, words string) {
		if s.can(r, perm) {
			out = append(out, SiteSearchEntry{
				Label: label, URL: url, Icon: icon,
				Feature:  strings.ToLower(label + " " + words),
				LabelKey: strings.ToLower(label),
			})
		}
	}
	add("databases.manage", "Create database", id+"/databases#new-database", "database", "mysql mariadb new add")
	add("websites.manage", "MongoDB databases", id+"/mongo", "database-fill-gear", "mongo mongodb nosql document collection database")
	add("websites.manage", "Browse MongoDB collections", id+"/mongo?section=databases", "table", "mongo mongodb browse collection document")
	add("ssl.manage", "Install SSL certificate", id+"/ssl#issue", "shield-lock", "https let's encrypt letsencrypt certificate tls")
	add("ssl.manage", "Upload SSL certificate", id+"/ssl#upload-certificate", "shield-lock", "custom pem own")
	add("websites.manage", "Create subdomain", id+"/subdomains#new-subdomain", "diagram-3", "subdomain sub blog shop")
	add("websites.manage", "Add addon domain", id+"/addons#new-addon", "plus-square", "addon domain another website parked")
	if php {
		add("websites.manage", "Change PHP version", id+"/settings#php", "filetype-php", "php version upgrade memory limit upload")
	}
	add("websites.manage", "Domains and aliases", id+"/settings#domains", "globe2", "alias www domain")
	add("websites.manage", "Redirects", id+"/settings#requests", "signpost", "redirect 301")
	add("cron.manage", "Create cron job", id+"/cron#new-cron", "calendar3", "schedule task crontab scheduler")
	add("backups.manage", "Back up now", id+"/backups#backup-now", "archive", "backup copy")
	add("backups.view", "Restore backup", id+"/backups#backup-list", "archive", "restore recover undo")
	add("logs.view", "View error log", id+"/logs", "journal-text", "error access log debug")
	add("logs.view", "Visitor statistics (AWStats)", id+"/stats", "bar-chart-line", "awstats analytics visitors traffic bandwidth stats")
	add("websites.manage", "Disable or delete website", id+"/advanced#danger-zone", "exclamation-octagon", "disable delete remove danger")
	return out
}

func showHandler(site *hosting.Website) string {
	switch site.Kind {
	case "php":
		return "PHP-FPM"
	case "proxy":
		return "Reverse proxy"
	default:
		return "Static files"
	}
}

func showSSLLabel(site *hosting.Website) string {
	switch site.SSLMode {
	case "letsencrypt":
		return "Let's Encrypt"
	case "custom":
		return "Own certificate"
	default:
		if site.HTTPS {
			return "Installed"
		}
		return "Not installed"
	}
}

// showSSLState is the SSL tile's note ("On" / "Off").
func showSSLState(site *hosting.Website) string {
	if site.HTTPS {
		return "On"
	}
	return "Off"
}

// showDisk is the agent's disk usage, "X+" when the count was truncated
// (format_bytes + the completeness mark); "" when the agent cannot be
// asked.
func showDisk(inspect *SiteInspection) string {
	if inspect == nil {
		return ""
	}
	s := bytesText(inspect.DiskBytes)
	if !inspect.DiskComplete {
		s += "+"
	}
	return s
}

func showGitBranch(summary *hosting.SiteSummary) string {
	if summary.Git == nil {
		return ""
	}
	return summary.Git.Branch
}

func showLastBackupAgo(summary *hosting.SiteSummary) string {
	if summary.LastBackup == "" {
		return ""
	}
	return timeAgo(summary.LastBackup)
}

// showCount is a count note shown only when the count is above zero.
func showCount(n int) string {
	if n > 0 {
		return strconv.Itoa(n)
	}
	return ""
}

// showTint is the hero thumbnail's background tint (crc32 of the domain,
// as the PHP view computes it).
func showTint(domain string) int {
	return int(crc32.ChecksumIEEE([]byte(domain))) % 6
}

// showKindLabel mirrors WebsiteService::KINDS.
func showKindLabel(kind string) string {
	switch kind {
	case "php":
		return "PHP application"
	case "static":
		return "Static files"
	case "proxy":
		return "Reverse proxy (Node.js, Python, …)"
	default:
		return kind
	}
}
