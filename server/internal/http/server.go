// Package http wires the Go panel's HTTP server: routing (stdlib mux),
// middleware (security headers, request logging, session, auth, CSRF) and
// handlers. Routes are ported from panel/routes/web.php incrementally; see
// docs/migration.
package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/auth"
	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/mail"
	"github.com/mchkhawarshad/whacp/server/internal/security"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
	"github.com/mchkhawarshahzad/whacp/server/internal/web"
	"github.com/mchkhawarshad/whacp/server/internal/twofactor"
)

// Server holds the dependencies shared by handlers.
type Server struct {
	cfg       config.Config
	db        *sql.DB
	log       *slog.Logger
	mux       *http.ServeMux
	sessions  *session.Manager
	auth      *auth.Service
	users     *identity.Repository
	roles     *authz.RoleRepository
	features  *authz.FeatureService
	tpl       *web.Templates
	agent     hosting.AgentCaller
	audit     *audit.Logger
	crypto    *crypto.Crypto
	settings  *settings.Repository
	resets    *auth.ResetService
	twoFactor *twofactor.Service
}

// New builds the server and registers the routes implemented so far. It
// returns an error if the templates cannot be parsed.
func New(cfg config.Config, db *sql.DB, log *slog.Logger) (*Server, error) {
	tpl, err := web.New()
	if err != nil {
		return nil, err
	}
	c := crypto.New(cfg.KeyFile)
	users := identity.NewRepository(db)
	tf := twofactor.New(db, c)
	al := audit.New(db, log)
	st := settings.NewRepository(db)
	ml := mail.New(st, c, log)
	resets := auth.NewResetService(db, users, ml, st, security.NewPolicy(st), al, log)
	s := &Server{
		cfg:       cfg,
		db:        db,
		log:       log,
		mux:       http.NewServeMux(),
		sessions:  session.NewManager(c, session.DefaultCookieName, cfg.Production),
		auth:      auth.NewService(db, users, auth.NewThrottle(db), tf, al),
		users:     users,
		roles:     authz.NewRoleRepository(db),
		features:  authz.NewFeatureService(db),
		tpl:       tpl,
		agent:     agent.New(cfg.AgentSocket, cfg.AgentTokenFile, 30*time.Second),
		audit:     al,
		crypto:    c,
		settings:  st,
		resets:    resets,
		twoFactor: tf,
	}
	s.routes()
	return s, nil
}

// Handler returns the fully wrapped http.Handler (middleware applied).
func (s *Server) Handler() http.Handler {
	return s.securityHeaders(s.requestLog(s.mux))
}

func (s *Server) routes() {
	// Unauthenticated liveness probe — parity with the PHP /healthz.
	s.mux.HandleFunc("GET /healthz", s.health)

	// Reused frontend assets (Bootstrap, the 12 JS files, CSS) served as-is.
	if s.cfg.StaticDir != "" {
		fs := http.FileServer(http.Dir(s.cfg.StaticDir))
		s.mux.Handle("GET /assets/", http.StripPrefix("/assets/", fs))
	}

	// Authentication.
	s.mux.HandleFunc("GET /login", s.sessionCtx(s.showLogin))
	s.mux.HandleFunc("POST /login", s.sessionCtx(s.doLogin))
	s.mux.HandleFunc("POST /logout", s.sessionCtx(s.doLogout))

	// Authenticated shell (dashboard). Feature pages are ported in later phases.
	s.mux.HandleFunc("GET /{$}", s.sessionCtx(s.requireAuth(s.dashboard)))

	// Websites — list + detail. Permission-gated (websites.view) and
	// tenant-scoped; no plan feature gates the list itself.
	s.mux.HandleFunc("GET /websites", s.guard("", "websites.view", s.websitesIndex))
	s.mux.HandleFunc("GET /websites/new", s.guard("sites.create", "websites.manage", s.websitesNew))
	s.mux.HandleFunc("POST /websites/new", s.guard("sites.store", "websites.manage", s.websitesStore))
	s.mux.HandleFunc("GET /websites/{id}", s.guard("", "websites.view", s.websiteShow))
	s.mux.HandleFunc("GET /sites/{id}/settings", s.guard("sites.edit", "websites.manage", s.websiteSettings))
	s.mux.HandleFunc("POST /sites/{id}/settings", s.guard("sites.update", "websites.manage", s.websiteSettingsSave))
	s.mux.HandleFunc("POST /sites/{id}/toggle", s.guard("sites.toggle", "websites.manage", s.websiteToggle))
	s.mux.HandleFunc("POST /sites/{id}/delete", s.guard("sites.delete", "websites.manage", s.websiteDelete))
	s.mux.HandleFunc("GET /sites/{id}/addons", s.guard("sites.addons", "websites.manage", s.siteAddons))
	s.mux.HandleFunc("POST /sites/{id}/addons", s.guard("sites.addons.store", "websites.manage", s.siteAddonsStore))
	// Addon domain → hosting account (AddonConversionController, users.manage).
	s.mux.HandleFunc("GET /sites/{id}/convert", s.guard("sites.convert", "users.manage", s.siteConvertPage))
	s.mux.HandleFunc("POST /sites/{id}/convert", s.guard("sites.convert", "users.manage", s.siteConvert))
	// The website's logs (LogsController::site, logs.view; the logs plan
	// feature gates the route name).
	s.mux.HandleFunc("GET /sites/{id}/logs", s.guard("sites.logs", "logs.view", s.siteLogs))
	// The website file manager (FilesController, files.manage; the
	// file_manager feature withholds the permission when the plan
	// disables it). All confinement is enforced by the agent.
	s.mux.HandleFunc("GET /sites/{id}/files", s.guard("sites.files", "files.manage", s.filesShow))
	s.mux.HandleFunc("GET /sites/{id}/files/edit", s.guard("sites.files.edit", "files.manage", s.filesEdit))
	s.mux.HandleFunc("POST /sites/{id}/files/api", s.guard("sites.files.api", "files.manage", s.filesApi))
	s.mux.HandleFunc("POST /sites/{id}/files/upload", s.guard("sites.files.upload", "files.manage", s.filesUpload))
	s.mux.HandleFunc("GET /sites/{id}/files/download", s.guard("sites.files.download", "files.manage", s.filesDownload))
	// The website's databases (DatabasesController::site; databases.view —
	// the mysql feature withholds it when the plan disables mysql).
	s.mux.HandleFunc("GET /sites/{id}/databases", s.guard("", "databases.view", s.siteDatabases))
	s.mux.HandleFunc("POST /sites/{id}/subdomains", s.guard("sites.subdomains.store", "websites.manage", s.siteSubdomainsStore))
	s.mux.HandleFunc("GET /sites/{id}/cron", s.guard("", "cron.view", s.siteCron))
	s.mux.HandleFunc("GET /sites/{id}/workers", s.guard("", "workers.manage", s.siteWorkers))
	// Worker write path: the plan feature withholds workers.manage when
	// disabled, so the permission guard enforces the feature too.
	s.mux.HandleFunc("POST /sites/{id}/workers", s.guard("", "workers.manage", s.workersStore))
	s.mux.HandleFunc("POST /sites/{id}/workers/{worker}/toggle", s.guard("", "workers.manage", s.workersToggle))
	s.mux.HandleFunc("POST /sites/{id}/workers/{worker}/restart", s.guard("", "workers.manage", s.workersRestart))
	s.mux.HandleFunc("POST /sites/{id}/workers/{worker}/delete", s.guard("", "workers.manage", s.workersDelete))
	// Subdomains: gated by the subdomains plan feature (route name) AND the
	// websites.manage permission.
	s.mux.HandleFunc("GET /sites/{id}/subdomains", s.guard("sites.subdomains", "websites.manage", s.siteSubdomains))

	// Advanced operations (PHP's "Advanced" tab): all websites.manage. The
	// URL paths mirror the PHP routes (POST /permissions and POST
	// /nodejs|/python update in place of the GET).
	s.mux.HandleFunc("POST /sites/{id}/restart-php", s.guard("sites.restart_php", "websites.manage", s.siteRestartPhp))
	s.mux.HandleFunc("POST /sites/{id}/reapply", s.guard("sites.reapply", "websites.manage", s.siteReapply))
	s.mux.HandleFunc("POST /sites/{id}/directory-listing", s.guard("sites.listing", "websites.manage", s.siteDirectoryListing))
	s.mux.HandleFunc("GET /sites/{id}/permissions", s.guard("sites.permissions", "websites.manage", s.siteCheckPermissions))
	s.mux.HandleFunc("POST /sites/{id}/permissions", s.guard("sites.permissions.repair", "websites.manage", s.siteRepairPermissions))
	// Node.js / Python runtime pages: each route is feature-gated by its
	// runtime (the page itself adapts to the site's app type).
	s.mux.HandleFunc("GET /sites/{id}/nodejs", s.guard("sites.nodejs", "websites.manage", s.siteRuntime("nodejs")))
	s.mux.HandleFunc("POST /sites/{id}/nodejs", s.guard("sites.nodejs.update", "websites.manage", s.siteRuntimeUpdate("nodejs")))
	s.mux.HandleFunc("GET /sites/{id}/python", s.guard("sites.python", "websites.manage", s.siteRuntime("python")))
	s.mux.HandleFunc("POST /sites/{id}/python", s.guard("sites.python.update", "websites.manage", s.siteRuntimeUpdate("python")))

	// Databases list + create. The mysql feature withholds databases.view when
	// a plan disables it, so the permission guard enforces the feature too.
	s.mux.HandleFunc("GET /databases", s.guard("", "databases.view", s.databasesIndex))
	s.mux.HandleFunc("POST /databases", s.guard("", "databases.manage", s.databasesStore))
	s.mux.HandleFunc("POST /databases/{id}/drop", s.guard("", "databases.manage", s.databasesDrop))
	s.mux.HandleFunc("POST /databases/{id}/dump", s.guard("", "databases.manage", s.databasesDump))
	s.mux.HandleFunc("POST /databases/{id}/import", s.guard("", "databases.manage", s.databasesImport))
	s.mux.HandleFunc("POST /databases/{id}/grant", s.guard("", "databases.manage", s.databasesGrant))
	// Database browser (DbBrowserController) — the per-database page and the
	// operation endpoint the reused dbbrowser.js polls, plus the server-wide
	// variants (any database by name, all-scope only). All databases.manage.
	s.mux.HandleFunc("GET /databases/browse", s.guard("databases.browser.server", "databases.manage", s.dbBrowserServer))
	s.mux.HandleFunc("POST /databases/browse/api", s.guard("databases.browser.server_api", "databases.manage", s.dbBrowserApiServer))
	s.mux.HandleFunc("GET /databases/{id}/browse", s.guard("databases.browser", "databases.manage", s.dbBrowser))
	s.mux.HandleFunc("POST /databases/{id}/browse/api", s.guard("databases.browser.api", "databases.manage", s.dbBrowserApi))
	// Database server manager (DbServerController) — server administrators
	// (all-scope; the controller denies everyone else with a 403 page):
	// status/overview, managed settings, the processlist with kill, flush,
	// a checked restart, and the database-server log viewer.
	s.mux.HandleFunc("GET /databases/server", s.guard("", "server.view", s.dbServerIndex))
	s.mux.HandleFunc("GET /databases/server/settings", s.guard("", "server.view", s.dbServerSettingsPage))
	s.mux.HandleFunc("POST /databases/server/settings", s.guard("", "server.manage", s.dbServerSaveSettings))
	s.mux.HandleFunc("GET /databases/server/connections", s.guard("", "server.view", s.dbServerConnections))
	s.mux.HandleFunc("POST /databases/server/kill", s.guard("", "server.manage", s.dbServerKill))
	s.mux.HandleFunc("POST /databases/server/flush", s.guard("", "server.manage", s.dbServerFlush))
	s.mux.HandleFunc("POST /databases/server/restart", s.guard("", "services.manage", s.dbServerRestart))
	s.mux.HandleFunc("GET /databases/server/logs", s.guard("", "logs.view", s.dbServerLogs))
	// Database users (all databases.manage; the mysql feature withholds the
	// permission when the plan disables mysql, same as the /databases routes).
	s.mux.HandleFunc("POST /database-users", s.guard("", "databases.manage", s.dbUsersCreate))
	s.mux.HandleFunc("POST /database-users/{id}/password", s.guard("", "databases.manage", s.dbUserPassword))
	s.mux.HandleFunc("POST /database-users/{id}/reveal", s.guard("", "databases.manage", s.dbUserReveal))
	s.mux.HandleFunc("POST /database-users/{id}/delete", s.guard("", "databases.manage", s.dbUserDelete))
	s.mux.HandleFunc("POST /database-users/{id}/assign", s.guard("", "databases.manage", s.dbUserAssign))
	s.mux.HandleFunc("POST /database-users/{id}/hosts", s.guard("", "databases.manage", s.dbUserAddHost))

	// DNS zones list. dns.manage is withheld by the dns plan feature when off.
	s.mux.HandleFunc("GET /dns", s.guard("", "dns.manage", s.dnsIndex))

	// Email domains list. mail.manage is withheld by the email plan feature.
	s.mux.HandleFunc("GET /mail", s.guard("", "mail.manage", s.mailIndex))
	s.mux.HandleFunc("GET /workers", s.guard("", "workers.manage", s.workersIndex))

	// Log viewer endpoints (LogsController). Source-level authorization
	// happens inside the handlers (a user may read exactly the logs of the
	// websites they can see; server logs and the panel log are staff-only) —
	// the routes themselves only carry logs.view.
	s.mux.HandleFunc("GET /logs", s.guard("", "logs.view", s.logsIndex))
	s.mux.HandleFunc("GET /logs/tail", s.guard("", "logs.view", s.logsTail))
	s.mux.HandleFunc("GET /logs/download", s.guard("", "logs.view", s.logsDownload))

	// Audit log viewer (staff only; no tenant scope).
	s.mux.HandleFunc("GET /audit", s.guard("", "audit.view", s.auditIndex))

	// Panel users list — scoped by manageableBy (staff: all; reseller: own
	// customers).
	s.mux.HandleFunc("GET /users", s.guard("", "users.view", s.usersIndex))

	// Roles (staff) and hosting plans (staff / reseller-own).
	s.mux.HandleFunc("GET /roles", s.guard("", "users.view", s.rolesIndex))
	s.mux.HandleFunc("GET /plans", s.guard("", "plans.manage", s.plansIndex))

	// Background job log (the job viewer polls it). Authenticated only —
	// visibility is per job (JobTracker.VisibleTo), so no permission, as in
	// PHP (route jobs.show).
	s.mux.HandleFunc("GET /jobs/{id}", s.guard("", "", s.jobsShow))

	// Notifications (per-recipient visibility).
	s.mux.HandleFunc("GET /notifications", s.guard("", "notifications.view", s.notificationsIndex))
	s.mux.HandleFunc("POST /notifications/read", s.guard("", "notifications.view", s.notificationsRead))

	// Own profile (authenticated; no special permission).
	s.mux.HandleFunc("GET /profile", s.guard("", "", s.profileShow))

	// Two-factor sign-in: the second-factor step of a sign-in (the pending
	// state lives in the sealed cookie; no signed-in session yet) plus the
	// profile self-service (setup / enable / disable / recovery).
	s.mux.HandleFunc("GET /login/two-factor", s.sessionCtx(s.showTwoFactor))
	s.mux.HandleFunc("POST /login/two-factor", s.sessionCtx(s.doTwoFactor))
	s.mux.HandleFunc("POST /profile/two-factor/setup", s.guard("", "", s.profileTwoFactorSetup))
	s.mux.HandleFunc("POST /profile/two-factor/enable", s.guard("", "", s.profileTwoFactorEnable))
	s.mux.HandleFunc("POST /profile/two-factor/disable", s.guard("", "", s.profileTwoFactorDisable))
	s.mux.HandleFunc("POST /profile/two-factor/recovery", s.guard("", "", s.profileTwoFactorRecovery))

	// Password reset by email (minimal layout, no signed-in session needed;
	// the answer is the same whether or not the account exists).
	s.mux.HandleFunc("GET /password/forgot", s.sessionCtx(s.showForgot))
	s.mux.HandleFunc("POST /password/forgot", s.sessionCtx(s.doForgot))
	s.mux.HandleFunc("GET /password/reset/{token}", s.sessionCtx(s.showReset))
	s.mux.HandleFunc("POST /password/reset/{token}", s.sessionCtx(s.doReset))

	// Managed servers (staff).
	s.mux.HandleFunc("GET /servers", s.guard("", "servers.manage", s.serversIndex))
}

// health mirrors HealthController::show — {"status":"ok"} or 503.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	status, code := "ok", http.StatusOK
	var one int
	if err := s.db.QueryRowContext(r.Context(), "SELECT 1").Scan(&one); err != nil || one != 1 {
		status, code = "error", http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]string{"status": status})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- request context ----

type ctxKey int

const (
	ctxSession ctxKey = iota
	ctxUser
)

func sessionFrom(ctx context.Context) *session.Session {
	s, _ := ctx.Value(ctxSession).(*session.Session)
	return s
}

func userFrom(ctx context.Context) *identity.User {
	u, _ := ctx.Value(ctxUser).(*identity.User)
	return u
}
