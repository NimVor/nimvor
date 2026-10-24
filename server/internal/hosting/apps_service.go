package hosting

// This file ports the one-click application installer: AppCatalogService
// (the admin-managed catalog) + AppInstallerService (the install flow:
// the dedicated database + user, the agent's apps.install job, the
// app_installs history) and the AppsController page data. The
// Application Manager (apps-manager) stays with its own batch.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// AppMeta is the built-in knowledge of an application (AppInstallerService
// constants): the title, the description and (for staged apps) where the
// web installer lives and the web-root subfolder.
type AppMeta struct {
	Title       string
	Description string
	Staged      bool
	Finish      string // the staged app's installer path ("" = none)
	DocrootSub  string // the web-root subfolder ("" = the document root)
}

// apps is AppInstallerService::APPS + STAGED (the built-in set; the
// catalog adds the per-deployment enable/visibility flags on top).
var apps = map[string]AppMeta{
	"wordpress":  {Title: "WordPress", Description: "Blog and website builder. Installed into the document root with a new database and an administrator account."},
	"laravel":    {Title: "Laravel", Description: "PHP framework skeleton via Composer, with the database configured in .env. The website is served from <folder>/public."},
	"joomla":     {Title: "Joomla", Description: "Content management system. Staged into the document root with a new database; finish in its web installer.", Staged: true, Finish: "/installation/"},
	"drupal":     {Title: "Drupal", Description: "Content management framework. Staged into the document root with a new database; finish in its web installer.", Staged: true, Finish: "/core/install.php"},
	"phpbb":      {Title: "phpBB", Description: "Forum software. Staged into the document root with a new database; finish in its web installer.", Staged: true, Finish: "/install/"},
	"smf":        {Title: "SMF", Description: "Simple Machines Forum. Staged into the document root with a new database; finish in its web installer.", Staged: true, Finish: "/install.php"},
	"prestashop": {Title: "PrestaShop", Description: "E-commerce platform. Staged into the document root with a new database; finish in its web installer.", Staged: true, Finish: "/install/"},
	"opencart":   {Title: "OpenCart", Description: "E-commerce platform. Staged with a new database; the website is served from <folder>/upload.", Staged: true, Finish: "/install/", DocrootSub: "upload"},
	"abantecart": {Title: "AbanteCart", Description: "E-commerce platform. Staged with a new database; the website is served from <folder>/public_html.", Staged: true, Finish: "/install/", DocrootSub: "public_html"},
	"nextcloud":  {Title: "Nextcloud", Description: "Self-hosted files, calendar and collaboration. Staged; finish in its web installer.", Staged: true, Finish: "/index.php"},
	"moodle":     {Title: "Moodle", Description: "Learning management system. Staged; finish in its web installer.", Staged: true, Finish: "/install.php"},
	"matomo":     {Title: "Matomo", Description: "Privacy-friendly web analytics. Staged; finish in its web installer.", Staged: true, Finish: "/index.php"},
	"mediawiki":  {Title: "MediaWiki", Description: "The wiki engine behind Wikipedia. Staged; finish in its web installer.", Staged: true, Finish: "/mw-config/index.php"},
	"grav":       {Title: "Grav", Description: "Flat-file CMS — no database. Staged; ready to use after install.", Staged: true, Finish: "/"},
}

// AppCatalogService ---------------------------------------------------------

// AppCategory is one app_categories row with its applications (the table
// is keyed by slug — there is no id column).
type AppCategory struct {
	Slug    string
	Name    string
	Icon    string
	Sort    int
	Enabled bool
	Apps    []AppEntry
}

// AppEntry is one app_catalog row (keyed by slug too).
type AppEntry struct {
	Slug            string
	CategorySlug    string
	Name            string
	Icon            string
	Description     string
	NeedsDB         bool
	Enabled         bool
	CustomerVisible bool
	Sort            int
}

// AppCatalogService is the admin-managed application catalog.
type AppCatalogService struct {
	db       *sql.DB
	settings *settings.Repository
}

// NewAppCatalogService builds the catalog service.
func NewAppCatalogService(db *sql.DB, settings *settings.Repository) *AppCatalogService {
	return &AppCatalogService{db: db, settings: settings}
}

// InstallerEnabled is the global switch (apps_installer_enabled; the PHP
// default is ON).
func (s *AppCatalogService) InstallerEnabled(ctx context.Context) bool {
	on, err := s.settings.Bool(ctx, "apps_installer_enabled", true)
	if err != nil {
		return true
	}
	return on
}

// Find is one catalog row by slug (nil when absent).
func (s *AppCatalogService) Find(ctx context.Context, slug string) (*AppEntry, error) {
	var a AppEntry
	err := s.db.QueryRowContext(ctx,
		`SELECT slug, category_slug, name, icon, description, needs_db, enabled, customer_visible, sort
		 FROM app_catalog WHERE slug = ?`, slug).
		Scan(&a.Slug, &a.CategorySlug, &a.Name, &a.Icon, &a.Description, &a.NeedsDB, &a.Enabled, &a.CustomerVisible, &a.Sort)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ForCustomer is the categories a customer may install from, each with its
// visible, enabled applications; empty when the installer is off.
func (s *AppCatalogService) ForCustomer(ctx context.Context) ([]AppCategory, error) {
	if !s.InstallerEnabled(ctx) {
		return nil, nil
	}
	return s.grouped(ctx, "a.enabled = 1 AND a.customer_visible = 1", true)
}

// InstallableBy reports whether the actor may install the app now (the
// installer switch, the app + category flags, customer visibility).
func (s *AppCatalogService) InstallableBy(ctx context.Context, slug string, u *identity.User) (bool, error) {
	if !s.InstallerEnabled(ctx) {
		return false, nil
	}
	app, err := s.Find(ctx, slug)
	if err != nil || app == nil || !app.Enabled {
		return false, err
	}
	var catEnabled bool
	err = s.db.QueryRowContext(ctx, `SELECT enabled FROM app_categories WHERE slug = ?`, app.CategorySlug).Scan(&catEnabled)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !catEnabled {
		return false, nil
	}
	if IsAll(u) {
		return true, nil // administrators/resellers see everything
	}
	return app.CustomerVisible, nil
}

func (s *AppCatalogService) grouped(ctx context.Context, appWhere string, onlyEnabledCategories bool) ([]AppCategory, error) {
	q := `SELECT slug, name, icon, sort, enabled FROM app_categories`
	if onlyEnabledCategories {
		q += ` WHERE enabled = 1`
	}
	q += ` ORDER BY sort, name`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cats []AppCategory
	for rows.Next() {
		var c AppCategory
		if err := rows.Scan(&c.Slug, &c.Name, &c.Icon, &c.Sort, &c.Enabled); err != nil {
			return nil, err
		}
		cats = append(cats, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]AppCategory, 0, len(cats))
	for _, c := range cats {
		aq := `SELECT a.slug, a.category_slug, a.name, a.icon, a.description, a.needs_db, a.enabled, a.customer_visible, a.sort
			FROM app_catalog a WHERE a.category_slug = ?`
		if appWhere != "" {
			aq += ` AND ` + appWhere
		}
		aq += ` ORDER BY a.sort, a.name`
		arows, err := s.db.QueryContext(ctx, aq, c.Slug)
		if err != nil {
			return nil, err
		}
		for arows.Next() {
			var a AppEntry
			if err := arows.Scan(&a.Slug, &a.CategorySlug, &a.Name, &a.Icon, &a.Description, &a.NeedsDB, &a.Enabled, &a.CustomerVisible, &a.Sort); err != nil {
				arows.Close()
				return nil, err
			}
			c.Apps = append(c.Apps, a)
		}
		err = arows.Err()
		arows.Close()
		if err != nil {
			return nil, err
		}
		if len(c.Apps) == 0 && onlyEnabledCategories {
			continue // hide empty categories from customers
		}
		out = append(out, c)
	}
	return out, nil
}

// ForAdmin is every category with every application, for the Application
// Manager (AppCatalogService::forAdmin).
func (s *AppCatalogService) ForAdmin(ctx context.Context) ([]AppCategory, error) {
	return s.grouped(ctx, "", false)
}

// SetInstallerEnabled flips the global installer switch
// (AppCatalogService::setInstallerEnabled).
func (s *AppCatalogService) SetInstallerEnabled(ctx context.Context, on bool, actor int64) error {
	return s.settings.Set(ctx, "apps_installer_enabled", on, actor)
}

// SetCategoryEnabled flips one category's flag (AppCatalogService::setCategoryEnabled).
func (s *AppCatalogService) SetCategoryEnabled(ctx context.Context, slug string, on bool) error {
	b := 0
	if on {
		b = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE app_categories SET enabled = ? WHERE slug = ?`, b, slug)
	return err
}

// SetAppFlag flips one application's enabled or customer_visible flag
// (AppCatalogService::setAppFlag; a wrong field is a no-op).
func (s *AppCatalogService) SetAppFlag(ctx context.Context, slug, field string, on bool) error {
	if field != "enabled" && field != "customer_visible" {
		return nil
	}
	b := 0
	if on {
		b = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE app_catalog SET `+field+` = ? WHERE slug = ?`, b, slug)
	return err
}

// AppInstallerService -------------------------------------------------------

// AppInstaller installs one-click applications.
type AppInstaller struct {
	db        *sql.DB
	agent     AgentCaller
	databases *DatabaseService
	sites     *WebsiteRepository
	catalog   *AppCatalogService
	websites  *WebsiteService
	auditlog  func(action, targetType, target string, details map[string]any)
}

// NewAppInstaller builds the service.
func NewAppInstaller(db *sql.DB, agent AgentCaller, databases *DatabaseService,
	sites *WebsiteRepository, catalog *AppCatalogService, websites *WebsiteService,
	auditlog func(action, targetType, target string, details map[string]any)) *AppInstaller {
	return &AppInstaller{db: db, agent: agent, databases: databases, sites: sites,
		catalog: catalog, websites: websites, auditlog: auditlog}
}

// RelativeDocroot is the website's document root relative to its root
// (AppInstallerService::relativeDocroot).
func RelativeDocroot(site *Website) string {
	root := strings.TrimRight(site.Root, "/") + "/"
	doc := site.DocumentRoot
	if strings.HasPrefix(doc, root) {
		return strings.TrimPrefix(doc, root)
	}
	return "public_html"
}

// FinishURL is the staged app's installer URL ("" for non-staged apps).
func FinishURL(site *Website, app string) string {
	m, ok := apps[app]
	if !ok || !m.Staged {
		return ""
	}
	scheme := "http://"
	if site.HTTPS {
		scheme = "https://"
	}
	return scheme + site.Domain + m.Finish
}

// AppInstallResult is the install flow's outcome (the job + the one-time
// WordPress credentials when generated).
type AppInstallResult struct {
	Job           string
	AdminUser     string
	AdminPassword string
}

var appPathRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,60}$`)
var appAdminUserRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{3,60}$`)
var appEmailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
var appDBBaseRe = regexp.MustCompile(`[^a-z0-9]`)

// forbiddenAppPaths are the folder names an application may not take.
var forbiddenAppPaths = map[string]bool{"logs": true, "tmp": true, "backups": true, ".ssh": true, ".": true, "..": true}

// Install runs the one-click install: the validation, the dedicated
// database + user, the agent's apps.install job and the app_installs row
// (AppInstallerService::install).
func (s *AppInstaller) Install(ctx context.Context, serverID int64, site *Website, app string, input map[string]string, u *identity.User) (*AppInstallResult, error) {
	ok, err := s.catalog.InstallableBy(ctx, app, u)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &UserError{What: "This application is not available for installation."}
	}
	catalogRow, err := s.catalog.Find(ctx, app)
	if err != nil {
		return nil, err
	}
	meta, known := apps[app]
	appName := app
	if known {
		appName = meta.Title
	}
	if catalogRow != nil && catalogRow.Name != "" {
		appName = catalogRow.Name
	}
	needsDB := true
	if catalogRow != nil {
		needsDB = catalogRow.NeedsDB
	}
	if site.Kind != "php" || site.PhpVersion == "" {
		return nil, &UserError{
			What: appName + " needs a PHP website.",
			Fix:  "Change the website type to PHP under Settings first.",
		}
	}
	ve := &ValidationError{}
	scheme := "http://"
	if site.HTTPS {
		scheme = "https://"
	}
	params := map[string]any{
		"app":         app,
		"domain":      site.Domain,
		"user":        site.SystemUser,
		"root":        site.Root,
		"php_version": site.PhpVersion,
		"url":         scheme + site.Domain,
	}
	var adminPassword string
	switch {
	case app == "wordpress":
		params["path"] = RelativeDocroot(site)
		params["title"] = nonEmpty(input["title"], site.Domain)
		params["admin_user"] = nonEmpty(input["admin_user"], "admin")
		params["admin_email"] = input["admin_email"]
		au, _ := params["admin_user"].(string)
		if !appAdminUserRe.MatchString(au) {
			ve.Add("admin_user", "Use 3–60 letters, digits or . _ @ -")
		}
		if !appEmailRe.MatchString(input["admin_email"]) {
			ve.Add("admin_email", "Enter the administrator's email address.")
		}
		if len([]rune(params["title"].(string))) > 100 {
			ve.Add("title", "Keep the title under 100 characters.")
		}
		adminPassword, err = cryptoPassword(20)
		if err != nil {
			return nil, err
		}
		params["admin_password"] = adminPassword
	case meta.Staged:
		params["path"] = RelativeDocroot(site)
	default:
		path := strings.Trim(input["path"], " /")
		if path == "" {
			path = "app"
		}
		params["path"] = path
		if !appPathRe.MatchString(path) || forbiddenAppPaths[path] {
			ve.Add("path", "Choose a new folder name, e.g. app.")
		}
		params["migrate"] = input["migrate"] == "1"
	}
	if ve.Any() {
		return nil, ve
	}

	// A database and user for the application (the password sealed and
	// stored so it can be looked up later under Databases). Flat-file apps
	// (e.g. Grav) need none.
	var databaseID *int64
	var databaseName, dbUser, dbPassword string
	if needsDB {
		base := appDBBaseRe.ReplaceAllString(strings.ToLower(strings.Split(site.Domain, ".")[0]), "")
		if base == "" {
			base = "site"
		}
		if len(base) > 10 {
			base = base[:10]
		}
		suffix := "_app"
		if app == "wordpress" {
			suffix = "_wp"
		}
		for i := 0; i < 10; i++ {
			name := base + suffix
			if i > 0 {
				name = base + fmt.Sprint(i)
			}
			dbID, dbName, un, pw, err := s.databases.CreateWithUser(ctx, serverID, u, name, name, &site.ID)
			if err == nil {
				databaseID = &dbID
				databaseName, dbUser, dbPassword = dbName, un, pw
				break
			}
			var vErr *ValidationError
			if errors.As(err, &vErr) && (vErr.Fields["name"] == "" && vErr.Fields["username"] == "") {
				return nil, err // not a name clash: it will not heal on retry
			}
		}
		if databaseID == nil {
			return nil, &UserError{What: "No free database name was found for the application."}
		}
		params["db"] = map[string]any{"name": databaseName, "user": dbUser, "password": dbPassword, "host": "localhost"}
	}

	res, err := s.agent.Call(ctx, "apps.install", params)
	if err != nil {
		s.rollbackAppDatabase(ctx, serverID, u, databaseID, dbUser)
		return nil, err
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(res, &out); err != nil || out.JobID == "" {
		s.rollbackAppDatabase(ctx, serverID, u, databaseID, dbUser)
		return nil, err
	}
	adminUser := ""
	if au, _ := params["admin_user"].(string); au != "" {
		adminUser = au
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO app_installs (website_id, app, path, database_id, admin_user, job_id, user_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'))`,
		site.ID, app, params["path"], databaseID, adminUser, out.JobID, u.ID); err != nil {
		return nil, err
	}
	if s.auditlog != nil {
		s.auditlog("app.install", "website", fmt.Sprint(site.ID), map[string]any{
			"domain": site.Domain, "app": app, "path": params["path"], "database": databaseName,
		})
	}
	resOut := &AppInstallResult{Job: out.JobID}
	if adminPassword != "" {
		resOut.AdminUser = adminUser
		resOut.AdminPassword = adminPassword
	}
	return resOut, nil
}

// rollbackAppDatabase removes the database (and its user) when the agent
// install fails before anything was installed (the PHP's catch).
func (s *AppInstaller) rollbackAppDatabase(ctx context.Context, serverID int64, u *identity.User, databaseID *int64, dbUser string) {
	if databaseID == nil {
		return
	}
	db, err := s.databases.db.Find(ctx, serverID, *databaseID, u)
	if err != nil || db == nil {
		return
	}
	_, _ = s.databases.DropDatabase(ctx, serverID, u, *databaseID, db.Name)
	users, err := s.databases.db.DBUsers(ctx, serverID, u)
	if err != nil {
		return
	}
	for _, usr := range users {
		if usr.Username == dbUser {
			_, _ = s.agent.Call(ctx, "db.user_drop", map[string]any{"user": usr.Username, "host": usr.Host})
			_ = s.databases.db.DeleteDBUser(ctx, usr.ID)
		}
	}
}

// Refresh updates running installations from their jobs and points Laravel
// / staged sites at their web root once done (AppInstallerService::refresh).
func (s *AppInstaller) Refresh(ctx context.Context, site *Website) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, app, path, job_id FROM app_installs WHERE website_id = ? AND status = 'running'`, site.ID)
	if err != nil {
		return err
	}
	type pending struct {
		id    int64
		app   string
		path  string
		jobID string
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.app, &p.path, &p.jobID); err != nil {
			rows.Close()
			return err
		}
		list = append(list, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range list {
		res, err := s.agent.Call(ctx, "jobs.get", map[string]any{"id": p.jobID})
		if err != nil {
			var ae *agent.Error
			if errors.As(err, &ae) && ae.Code == "not_found" {
				if _, err := s.db.ExecContext(ctx,
					`UPDATE app_installs SET status = 'unknown', finished_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = ?`, p.id); err != nil {
					return err
				}
			}
			continue
		}
		var job struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(res, &job); err != nil {
			continue
		}
		if job.Status != "succeeded" && job.Status != "failed" {
			continue
		}
		if len(job.Error) > 500 {
			job.Error = job.Error[:500]
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE app_installs SET status = ?, error = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = ?`,
			job.Status, job.Error, p.id); err != nil {
			return err
		}
		// Point the website at the app's web root when it is a subfolder.
		sub := ""
		if p.app == "laravel" {
			sub = "public"
		} else if m, ok := apps[p.app]; ok {
			sub = m.DocrootSub
		}
		if job.Status == "succeeded" && sub != "" {
			fresh, err := s.sites.FindFull(ctx, site.ServerID, site.ID, nil)
			if err == nil && fresh != nil {
				_ = s.websites.patch(ctx, fresh, map[string]any{
					"document_root": strings.TrimRight(fresh.Root, "/") + "/" + p.path + "/" + sub,
				}, "app.document_root")
			}
		}
	}
	return nil
}

// AppInstallRow is one app_installs row with the database name joined.
type AppInstallRow struct {
	ID           int64
	App          string
	Path         string
	AdminUser    string
	Status       string
	Error        string
	JobID        string
	CreatedAt    string
	DatabaseName string
}

// History is the website's installations, newest first
// (AppInstallerService::history).
func (s *AppInstaller) History(ctx context.Context, siteID int64) ([]AppInstallRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id, a.app, a.path, a.admin_user, a.status, a.error, a.job_id, a.created_at,
		        COALESCE(d.name, '')
		 FROM app_installs a LEFT JOIN databases d ON d.id = a.database_id
		 WHERE a.website_id = ? ORDER BY a.id DESC`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppInstallRow
	for rows.Next() {
		var r AppInstallRow
		if err := rows.Scan(&r.ID, &r.App, &r.Path, &r.AdminUser, &r.Status, &r.Error, &r.JobID, &r.CreatedAt, &r.DatabaseName); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AppsMeta is the built-in knowledge of one application (the PHP's
// AppInstallerService::APPS[$app] lookup, ok = known).
func AppsMeta(slug string) (AppMeta, bool) {
	m, ok := apps[slug]
	return m, ok
}

// nonEmpty trims a form value with a fallback.
func nonEmpty(v, def string) string {
	if t := strings.TrimSpace(v); t != "" {
		return t
	}
	return def
}
