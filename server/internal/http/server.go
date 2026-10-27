// Package http wires the Go panel's HTTP server: routing (stdlib mux),
// middleware (security headers, request logging, session, auth, CSRF) and
// handlers. Routes are ported from panel/routes/web.php incrementally; see
// docs/migration.
package http

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/mchkhawarshahzad/whacp/server/internal/clientip"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	"github.com/mchkhawarshahzad/whacp/server/internal/security"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
	"github.com/mchkhawarshahzad/whacp/server/internal/twofactor"
	"github.com/mchkhawarshahzad/whacp/server/internal/web"
	wa "github.com/mchkhawarshahzad/whacp/server/internal/webauthn"
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
	policy    *security.Policy
	loginIPs  *security.LoginIpVerifier
	hulk      *security.Hulk
	cfDo      cfTransport // per-website Cloudflare transport (nil = real)
	apiAuth   *auth.APIAuthenticator
	wa        *wa.Service
	mailer    *mail.Mailer
}

// New builds the server and registers the routes implemented so far. It
// returns an error if the templates cannot be parsed.
func New(cfg config.Config, db *sql.DB, log *slog.Logger) (*Server, error) {
	tpl, err := web.New()
	if err != nil {
		return nil, err
	}
	c := crypto.New(cfg.KeyFile)
	// Fail at start-up, not at the first login, when the panel key is missing
	// or unreadable (every sealed session would otherwise fail silently).
	if _, err := c.Encrypt([]byte("startup-check")); err != nil {
		return nil, fmt.Errorf("panel key %s is not usable: %w", cfg.KeyFile, err)
	}
	proxies, badProxies := clientip.Parse(cfg.TrustedProxies)
	if len(badProxies) > 0 {
		return nil, fmt.Errorf("WHACP_TRUSTED_PROXIES has invalid entries: %v", badProxies)
	}
	clientip.SetTrusted(proxies)
	users := identity.NewRepository(db)
	tf := twofactor.New(db, c)
	al := audit.New(db, log)
	st := settings.NewRepository(db)
	policy := security.NewPolicy(st)
	ml := mail.New(st, c, log)
	resets := auth.NewResetService(db, users, ml, st, policy, al, log)
	wasvc := wa.New(db)
	s := &Server{
		cfg:       cfg,
		db:        db,
		log:       log,
		mux:       http.NewServeMux(),
		sessions:  session.NewManager(c, session.DefaultCookieName, cfg.Production),
		auth:      auth.NewService(db, users, auth.NewThrottle(db), tf, al).WithIpVerification(security.NewLoginIpVerifier(db), ml, st),
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
		policy:    policy,
		loginIPs:  security.NewLoginIpVerifier(db),
		hulk:      security.NewHulk(db, st),
		cfDo:      cfTransport(nil),
		apiAuth:   auth.NewAPIAuthenticator(db, users),
		wa:        wasvc,
		mailer:    ml,
	}
	s.agent = newAgentRouter(s, s.agent.(*agent.Client))
	s.routes()
	s.installNav()
	s.auth.WithBruteForce(hulkAuth{s})
	return s, nil
}

// Handler returns the fully wrapped http.Handler (middleware applied).
func (s *Server) Handler() http.Handler {
	return s.securityHeaders(s.requestLog(s.styledNotFound(formSwitches(s.mux))))
}

// styledNotFound shows the panel's own page for an address no route serves
// (the router answers those with a bare "404 page not found").
func (s *Server) styledNotFound(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&notFoundWriter{ResponseWriter: w, s: s, r: r}, r)
	})
}

type notFoundWriter struct {
	http.ResponseWriter
	s       *Server
	r       *http.Request
	swapped bool
}

func (w *notFoundWriter) WriteHeader(code int) {
	if code == http.StatusNotFound && strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		w.swapped = true
		w.Header().Del("Content-Type")
		w.Header().Del("X-Content-Type-Options")
		w.s.notFound(w.ResponseWriter, w.r)
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *notFoundWriter) Write(b []byte) (int, error) {
	if w.swapped {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

// formSwitches makes a ticked switch read as on. A switch posts a hidden 0
// before its checkbox's 1 (so an unticked one still sends a value); PHP's
// $_POST keeps the last value, but r.FormValue returns the first, which made
// every ticked switch look off wherever a handler read it that way.
func formSwitches(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
			if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") && r.ParseForm() == nil {
				for k, v := range r.PostForm {
					if len(v) == 2 && v[0] == "0" && v[1] == "1" {
						r.PostForm[k] = []string{"1"}
						r.Form[k] = []string{"1"}
					}
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	// Unauthenticated liveness probe.
	s.mux.HandleFunc("GET /healthz", s.health)
	// The public, signed Git push webhook (no session, no CSRF — the
	// signature and the replay table are the gates).
	s.mux.HandleFunc("POST /hooks/deploy/{token}", s.deployWebhook)

	// Reused frontend assets (Bootstrap, the 12 JS files, CSS). Serve them
	// from the embedded filesystem baked into the binary; cfg.StaticDir can
	// override for local development so designers can edit CSS/JS without
	// rebuilding.
	var assetFS http.FileSystem
	if s.cfg.StaticDir != "" {
		assetFS = http.Dir(s.cfg.StaticDir)
	} else {
		sub, err := web.Assets()
		if err != nil {
			panic(err)
		}
		assetFS = http.FS(sub)
	}
	fileHandler := http.FileServer(assetFS)
	s.mux.Handle("GET /assets/", http.StripPrefix("/assets/", cachedAssets(fileHandler)))

	// REST v1 API (ApiController). Registered before web routes so they take
	// precedence. Permissions mirror panel/routes/web.php API group.
	s.mux.HandleFunc("GET /api/v1/openapi.yaml", s.apiPublic(s.apiOpenapi))
	s.mux.HandleFunc("GET /api/v1/me", s.apiGuard("api.me", "", false, s.apiMe))
	s.mux.HandleFunc("GET /api/v1/server", s.apiGuard("api.server", "server.view", false, s.apiServerInfo))
	s.mux.HandleFunc("GET /api/v1/websites", s.apiGuard("api.websites", "websites.view", false, s.apiListWebsites))
	s.mux.HandleFunc("POST /api/v1/websites", s.apiGuard("api.websites.create", "websites.manage", true, s.apiCreateWebsite))
	s.mux.HandleFunc("GET /api/v1/websites/{id}", s.apiGuard("api.websites.show", "websites.view", false, s.apiShowWebsite))
	s.mux.HandleFunc("POST /api/v1/websites/{id}/enable", s.apiGuard("api.websites.enable", "websites.manage", true, s.apiEnableWebsite))
	s.mux.HandleFunc("POST /api/v1/websites/{id}/disable", s.apiGuard("api.websites.disable", "websites.manage", true, s.apiDisableWebsite))
	s.mux.HandleFunc("DELETE /api/v1/websites/{id}", s.apiGuard("api.websites.delete", "websites.manage", true, s.apiDeleteWebsite))
	s.mux.HandleFunc("POST /api/v1/websites/{id}/ssl", s.apiGuard("api.websites.ssl", "ssl.manage", true, s.apiIssueSsl))
	s.mux.HandleFunc("GET /api/v1/websites/{id}/backups", s.apiGuard("api.backups", "backups.view", false, s.apiListBackups))
	s.mux.HandleFunc("POST /api/v1/websites/{id}/backups", s.apiGuard("api.backups.create", "backups.manage", true, s.apiCreateBackup))
	s.mux.HandleFunc("POST /api/v1/websites/{id}/deploy", s.apiGuard("api.deploy", "deploy.manage", true, s.apiDeployWebsite))
	s.mux.HandleFunc("GET /api/v1/databases", s.apiGuard("api.databases", "databases.view", false, s.apiListDatabases))
	s.mux.HandleFunc("POST /api/v1/databases", s.apiGuard("api.databases.create", "databases.manage", true, s.apiCreateDatabase))
	s.mux.HandleFunc("GET /api/v1/jobs/{id}", s.apiGuard("api.jobs", "", false, s.apiJob))
	s.mux.HandleFunc("GET /api/v1/notifications", s.apiGuard("api.notifications", "notifications.view", false, s.apiListNotifications))
	s.mux.HandleFunc("POST /api/v1/notifications/read", s.apiGuard("api.notifications.read", "notifications.view", true, s.apiReadNotifications))

	// Authentication.
	s.mux.HandleFunc("GET /login", s.sessionCtx(s.showLogin))
	s.mux.HandleFunc("POST /login", s.sessionCtx(s.doLogin))
	s.mux.HandleFunc("POST /logout", s.sessionCtx(s.doLogout))
	// Step-up password confirmation (sensitive changes redirect here).
	s.mux.HandleFunc("GET /confirm-password", s.sessionCtx(s.requireAuth(s.confirmPasswordForm)))
	s.mux.HandleFunc("POST /confirm-password", s.sessionCtx(s.requireAuth(s.confirmPassword)))

	// Authenticated shell (dashboard). Feature pages are ported in later phases.
	s.mux.HandleFunc("GET /{$}", s.sessionCtx(s.requireAuth(s.dashboard)))
	s.mux.HandleFunc("GET /dashboard/live", s.guard("dashboard.live", "dashboard.view", s.dashboardLive))

	// Websites — list + detail. Permission-gated (websites.view) and
	// tenant-scoped; no plan feature gates the list itself.
	s.mux.HandleFunc("GET /websites", s.guard("", "websites.view", s.websitesIndex))
	s.mux.HandleFunc("GET /websites/new", s.guard("sites.create", "websites.manage", s.websitesNew))
	s.mux.HandleFunc("POST /websites/new", s.guard("sites.store", "websites.manage", s.websitesStore))
	s.mux.HandleFunc("GET /websites/{id}", s.guard("", "websites.view", s.websiteShow))
	// Importing a discovered website (import.run).
	s.mux.HandleFunc("GET /websites/{id}/import", s.guard("websites.import", "import.run", s.importPreview))
	s.mux.HandleFunc("POST /websites/{id}/import", s.guard("websites.import.run", "import.run", s.importRun))
	s.mux.HandleFunc("POST /sites/{id}/import/undo", s.guard("sites.import.undo", "import.run", s.importUndo))
	s.mux.HandleFunc("GET /sites/{id}", s.guard("sites.show", "websites.view", s.siteShow))
	s.mux.HandleFunc("GET /sites/{id}/advanced", s.guard("sites.advanced", "websites.manage", s.siteAdvanced))
	s.mux.HandleFunc("GET /sites/{id}/settings", s.guard("sites.edit", "websites.manage", s.websiteSettings))
	s.mux.HandleFunc("POST /sites/{id}/settings", s.guard("sites.update", "websites.manage", s.websiteSettingsSave))
	s.mux.HandleFunc("POST /sites/{id}/ip", s.guard("sites.ip", "server.manage", s.ipAssign))
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
	// The website's disk usage (DiskUsageController, files.manage; the
	// agent measures a folder in a background job, confined to the
	// website root, and the latest result is kept in disk_usage_scans).
	s.mux.HandleFunc("GET /sites/{id}/disk-usage", s.guard("sites.disk", "files.manage", s.diskUsageShow))
	s.mux.HandleFunc("POST /sites/{id}/disk-usage/scan", s.guard("sites.disk.scan", "files.manage", s.diskUsageScan))
	// The web terminal (TerminalController): the browser talks to the
	// agent's WebSocket directly (Nginx proxies /terminal/ws) — the panel
	// only hands out tickets and re-confirms the panel password for root
	// shells. Signing out ends the sign-in's terminals (doLogout).
	s.mux.HandleFunc("GET /sites/{id}/terminal", s.guard("sites.terminal", "files.manage", s.terminalSite))
	s.mux.HandleFunc("POST /sites/{id}/terminal/open", s.guard("sites.terminal.open", "files.manage", s.terminalOpenSite))
	// Batch 56 — the website's FTP / SFTP tab (FtpController, ftp.manage):
	// the virtual accounts (the agent keeps them) + the website's own
	// SFTP access.
	s.mux.HandleFunc("GET /sites/{id}/ftp", s.guard("sites.ftp", "ftp.manage", s.siteFtp))
	s.mux.HandleFunc("POST /sites/{id}/ftp/accounts", s.guard("sites.ftp.save", "ftp.manage", s.siteFtpSave))
	s.mux.HandleFunc("POST /sites/{id}/ftp/accounts/delete", s.guard("sites.ftp.delete", "ftp.manage", s.siteFtpDelete))
	s.mux.HandleFunc("POST /sites/{id}/sftp", s.guard("sites.sftp", "ftp.manage", s.siteSftp))
	// Batch 57 — the one-click application installer (AppsController,
	// apps.install): the Softaculous-style catalog page + the install
	// (onto the website or one of its subdomains).
	s.mux.HandleFunc("GET /sites/{id}/apps", s.guard("sites.apps", "apps.install", s.siteApps))
	s.mux.HandleFunc("POST /sites/{id}/apps/{app}", s.guard("sites.apps.install", "apps.install", s.siteAppInstall))
	s.mux.HandleFunc("POST /terminal/unlock", s.guard("sites.terminal.unlock", "files.manage", s.terminalUnlock))
	s.mux.HandleFunc("GET /terminal", s.guard("terminal", "security.manage", s.terminalIndex))
	s.mux.HandleFunc("POST /terminal/root/open", s.guard("terminal.root", "security.manage", s.terminalOpenRoot))
	s.mux.HandleFunc("POST /terminal/sessions/{id}/close", s.guard("terminal.close", "security.manage", s.terminalClose))
	// Security → Shell access (ShellController: the website accounts'
	// disabled/jailed/normal shell modes; the agent applies them while the
	// account's SSH/SFTP login is on). The confirm/undo routes keep or roll
	// back the unconfirmed security change shown on the page.
	// Security → Firewall (SecurityController::firewall et al.: the UFW
	// state, the guarded on/off + rule changes and the CSF-style
	// flood/connection-limit protections; confirm/undo via the banner).
	// Security → overview (SecurityController::index: the checks with the
	// fix/undo actions, the SSH/panel login cards, the safety rating) and
	// the anonymous MySQL users removal (security.mysql_anonymous).
	s.mux.HandleFunc("GET /security", s.guard("security", "security.view", s.securityIndex))
	s.mux.HandleFunc("POST /security/mysql-anonymous", s.guard("security.mysql_anonymous", "server.manage", s.dropAnonymousUsers))
	// Security → hardening (HardeningController: compiler access, SMTP
	// restrictions, outgoing connections, fork bomb protection, traceroute —
	// all-scope pages, role-admin changes).
	s.mux.HandleFunc("GET /security/compilers", s.guard("security.compilers", "security.view", s.hardeningCompilers))
	s.mux.HandleFunc("POST /security/compilers", s.guard("security.compilers.save", "security.manage", s.hardeningSaveCompilers))
	s.mux.HandleFunc("GET /security/smtp", s.guard("security.smtp", "security.view", s.hardeningSmtp))
	s.mux.HandleFunc("POST /security/smtp", s.guard("security.smtp.save", "security.manage", s.hardeningSaveSmtp))
	s.mux.HandleFunc("GET /security/egress", s.guard("security.egress", "security.view", s.hardeningEgress))
	s.mux.HandleFunc("POST /security/egress", s.guard("security.egress.save", "security.manage", s.hardeningSaveEgress))
	s.mux.HandleFunc("GET /security/forkbomb", s.guard("security.forkbomb", "security.view", s.hardeningForkbomb))
	s.mux.HandleFunc("POST /security/forkbomb", s.guard("security.forkbomb.save", "security.manage", s.hardeningSaveForkbomb))
	s.mux.HandleFunc("GET /security/traceroute", s.guard("security.traceroute", "security.view", s.hardeningTraceroute))
	s.mux.HandleFunc("POST /security/traceroute", s.guard("security.traceroute.save", "security.manage", s.hardeningSaveTraceroute))
	// Security → on-demand checks (SecurityChecksController: file integrity,
	// the TLS audit, the OS advisories — read-only, run on demand).
	s.mux.HandleFunc("GET /security/integrity", s.guard("security.integrity", "security.view", s.integrityPage))
	s.mux.HandleFunc("POST /security/integrity/run", s.guard("security.integrity.run", "security.view", s.runIntegrity))
	s.mux.HandleFunc("POST /security/integrity/baseline", s.guard("security.integrity.baseline", "security.manage", s.setIntegrityBaseline))
	s.mux.HandleFunc("GET /security/tls", s.guard("security.tls", "security.view", s.tlsPage))
	s.mux.HandleFunc("POST /security/tls/run", s.guard("security.tls.run", "security.view", s.runTls))
	s.mux.HandleFunc("GET /security/advisories", s.guard("security.advisories", "security.view", s.advisoriesPage))
	s.mux.HandleFunc("POST /security/advisories/run", s.guard("security.advisories.run", "security.view", s.runAdvisories))
	// Security → malware (AvController: the ClamAV status + scan settings,
	// the signature update, the server's recent scans).
	s.mux.HandleFunc("GET /security/malware", s.guard("security.malware", "security.view", s.malwareIndex))
	s.mux.HandleFunc("POST /security/malware/settings", s.guard("security.malware.settings", "security.manage", s.malwareSaveSettings))
	s.mux.HandleFunc("POST /security/malware/update", s.guard("security.malware.update", "security.manage", s.malwareUpdate))
	// Security → policies (SecurityPolicyController: the security policy
	// items + the password strength minimums; all-scope pages, role-admin
	// saves, forget for one's own verified IPs).
	s.mux.HandleFunc("GET /security/policies", s.guard("security.policies", "security.view", s.policiesPage))
	s.mux.HandleFunc("POST /security/policies", s.guard("security.policies.save", "security.manage", s.savePolicies))
	s.mux.HandleFunc("POST /security/policies/ips/forget", s.guard("security.policies.forget_ip", "security.view", s.forgetPolicyIP))
	s.mux.HandleFunc("GET /security/password-strength", s.guard("security.password_strength", "security.view", s.strengthPage))
	s.mux.HandleFunc("POST /security/password-strength", s.guard("security.password_strength.save", "security.manage", s.saveStrength))
	s.mux.HandleFunc("GET /security/firewall", s.guard("security.firewall", "security.view", s.firewallIndex))
	s.mux.HandleFunc("POST /security/firewall/toggle", s.guard("security.firewall.toggle", "security.manage", s.firewallToggle))
	s.mux.HandleFunc("POST /security/firewall/rules", s.guard("security.firewall.add", "security.manage", s.firewallAddRule))
	s.mux.HandleFunc("POST /security/firewall/rules/delete", s.guard("security.firewall.delete", "security.manage", s.firewallDeleteRule))
	s.mux.HandleFunc("POST /security/firewall/protection", s.guard("security.firewall.protection", "security.manage", s.firewallProtection))
	// Security → Brute-force protection (SecurityController's fail2ban area:
	// the jails, the banned addresses with unban, the settings form) and
	// the overview's fix action (POST /security/fix — server.manage, admin).
	s.mux.HandleFunc("GET /security/fail2ban", s.guard("security.fail2ban", "security.view", s.fail2banIndex))
	s.mux.HandleFunc("POST /security/fail2ban", s.guard("security.fail2ban.configure", "security.manage", s.fail2banConfigure))
	s.mux.HandleFunc("POST /security/fail2ban/unban", s.guard("security.fail2ban.unban", "security.manage", s.fail2banUnban))
	// Security → cPHulk brute force protection (HulkController: the settings
	// form, the countries management, the failed-logins report and the
	// block/clear actions; all-scope pages, role-admin saves).
	s.mux.HandleFunc("GET /security/hulk", s.guard("security.hulk", "security.view", s.hulkIndex))
	s.mux.HandleFunc("POST /security/hulk", s.guard("security.hulk.save", "security.manage", s.hulkSave))
	s.mux.HandleFunc("POST /security/hulk/countries", s.guard("security.hulk.countries", "security.manage", s.hulkCountries))
	s.mux.HandleFunc("POST /security/hulk/geo", s.guard("security.hulk.geo", "security.manage", s.hulkGeo))
	s.mux.HandleFunc("POST /security/hulk/unblock", s.guard("security.hulk.unblock", "security.manage", s.hulkUnblock))
	s.mux.HandleFunc("POST /security/hulk/clear", s.guard("security.hulk.clear", "security.manage", s.hulkClear))
	// Security → ModSecurity (ModSecurityController: the web application
	// firewall — server-wide on/off, the OWASP CRS settings and custom
	// rules, each website's mode/vendors/rules, the blocked/detected
	// events, and the background install/update/rollback of the connector
	// and the rule set; all-scope pages, role-admin saves). The website's
	// Web firewall page (the mode switch, owners too) lives with the
	// /sites routes below.
	s.mux.HandleFunc("GET /security/modsec", s.guard("security.modsec", "security.view", s.modsecIndex))
	s.mux.HandleFunc("POST /security/modsec", s.guard("security.modsec.configure", "security.manage", s.modsecConfigure))
	s.mux.HandleFunc("POST /security/modsec/rule", s.guard("security.modsec.rule", "security.manage", s.modsecSaveRule))
	s.mux.HandleFunc("POST /security/modsec/custom", s.guard("security.modsec.custom", "security.manage", s.modsecSaveCustom))
	s.mux.HandleFunc("POST /security/modsec/update", s.guard("security.modsec.update", "security.manage", s.modsecUpdate))
	s.mux.HandleFunc("POST /security/modsec/install", s.guard("security.modsec.install", "security.manage", s.modsecInstall))
	s.mux.HandleFunc("POST /security/modsec/crs", s.guard("security.modsec.crs", "security.manage", s.modsecInstallCrs))
	s.mux.HandleFunc("POST /security/modsec/rollback/{id}", s.guard("security.modsec.rollback", "security.manage", s.modsecRollback))
	s.mux.HandleFunc("POST /security/modsec/sites/{id}", s.guard("security.modsec.site", "security.manage", s.modsecSaveSite))
	s.mux.HandleFunc("POST /security/fix", s.guard("security.fix", "server.manage", s.securityFix))
	// Security → SSH (SecurityController's SSH area: the server settings —
	// the guarded ssh.configure — and the administrator key management,
	// administrators only; the generated private key is shown once).
	s.mux.HandleFunc("GET /security/ssh", s.guard("security.ssh", "security.view", s.sshIndex))
	s.mux.HandleFunc("POST /security/ssh", s.guard("security.ssh.configure", "security.manage", s.sshConfigure))
	s.mux.HandleFunc("POST /security/ssh/keys", s.guard("security.ssh.key_add", "security.manage", s.sshAddKey))
	s.mux.HandleFunc("POST /security/ssh/keys/generate", s.guard("security.ssh.key_generate", "security.manage", s.sshGenerateKey))
	s.mux.HandleFunc("POST /security/ssh/keys/remove", s.guard("security.ssh.key_remove", "security.manage", s.sshRemoveKey))
	s.mux.HandleFunc("GET /security/shell", s.guard("security.shell", "security.view", s.shellAccessIndex))
	s.mux.HandleFunc("POST /security/shell", s.guard("security.shell.update", "security.manage", s.shellAccessUpdate))
	s.mux.HandleFunc("POST /security/confirm", s.guard("security.confirm", "security.manage", s.securityConfirm))
	s.mux.HandleFunc("POST /security/undo", s.guard("security.undo", "security.manage", s.securityUndo))
	// The website's databases (DatabasesController::site; databases.view —
	// the mysql feature withholds it when the plan disables mysql).
	s.mux.HandleFunc("GET /sites/{id}/databases", s.guard("", "databases.view", s.siteDatabases))
	s.mux.HandleFunc("POST /sites/{id}/subdomains", s.guard("sites.subdomains.store", "websites.manage", s.siteSubdomainsStore))
	s.mux.HandleFunc("GET /sites/{id}/cron", s.guard("sites.cron", "cron.view", s.siteCron))
	s.mux.HandleFunc("POST /sites/{id}/cron", s.guard("sites.cron.store", "cron.manage", s.siteCronStore))
	s.mux.HandleFunc("GET /sites/{id}/cron/{job}/log", s.guard("sites.cron.log", "cron.view", s.siteCronLog))
	s.mux.HandleFunc("POST /sites/{id}/cron/{job}/run", s.guard("sites.cron.run", "cron.manage", s.siteCronRun))
	s.mux.HandleFunc("POST /sites/{id}/cron/{job}/toggle", s.guard("sites.cron.toggle", "cron.manage", s.siteCronToggle))
	s.mux.HandleFunc("POST /sites/{id}/cron/{job}/delete", s.guard("sites.cron.delete", "cron.manage", s.siteCronDelete))
	s.mux.HandleFunc("GET /sites/{id}/git", s.guard("sites.git", "deploy.manage", s.siteGit))
	s.mux.HandleFunc("POST /sites/{id}/git", s.guard("sites.git.configure", "deploy.manage", s.siteGitConfigure))
	s.mux.HandleFunc("POST /sites/{id}/git/deploy", s.guard("sites.git.deploy", "deploy.manage", s.siteGitDeploy))
	s.mux.HandleFunc("POST /sites/{id}/git/rotate", s.guard("sites.git.rotate", "deploy.manage", s.siteGitRotate))
	s.mux.HandleFunc("POST /sites/{id}/git/remove", s.guard("sites.git.remove", "deploy.manage", s.siteGitRemove))
	s.mux.HandleFunc("GET /sites/{id}/mongo", s.guard("sites.mongo", "websites.manage", s.siteMongo))
	s.mux.HandleFunc("POST /sites/{id}/mongo/databases", s.guard("sites.mongo.database_create", "websites.manage", s.siteMongoCreateDatabase))
	s.mux.HandleFunc("POST /sites/{id}/mongo/databases/drop", s.guard("sites.mongo.database_drop", "websites.manage", s.siteMongoDropDatabase))
	s.mux.HandleFunc("POST /sites/{id}/mongo/users", s.guard("sites.mongo.user_create", "websites.manage", s.siteMongoCreateUser))
	s.mux.HandleFunc("POST /sites/{id}/mongo/users/password", s.guard("sites.mongo.user_password", "websites.manage", s.siteMongoSetPassword))
	s.mux.HandleFunc("POST /sites/{id}/mongo/users/drop", s.guard("sites.mongo.user_drop", "websites.manage", s.siteMongoDropUser))
	s.mux.HandleFunc("POST /sites/{id}/mongo/backup", s.guard("sites.mongo.backup", "websites.manage", s.siteMongoBackup))
	s.mux.HandleFunc("POST /sites/{id}/mongo/restore", s.guard("sites.mongo.restore", "websites.manage", s.siteMongoRestore))
	s.mux.HandleFunc("POST /sites/{id}/mongo/backups/delete", s.guard("sites.mongo.backup_delete", "websites.manage", s.siteMongoDeleteBackup))
	s.mux.HandleFunc("GET /sites/{id}/mongo/browser", s.guard("sites.mongo.browser", "websites.manage", s.mongoBrowserPage))
	s.mux.HandleFunc("POST /sites/{id}/mongo/browse", s.guard("sites.mongo.browse", "websites.manage", s.mongoBrowseAPI))
	s.mux.HandleFunc("GET /sites/{id}/mongo/export", s.guard("sites.mongo.export", "websites.manage", s.mongoExport))
	s.mux.HandleFunc("GET /sites/{id}/wordpress", s.guard("sites.wordpress", "websites.view", s.wordpressSiteList))
	s.mux.HandleFunc("POST /sites/{id}/wordpress/scan", s.guard("sites.wordpress.scan", "websites.manage", s.wordpressScanSite))
	s.mux.HandleFunc("GET /sites/{id}/wordpress/{wp}", s.guard("sites.wordpress.show", "websites.view", s.wpShow))
	s.mux.HandleFunc("POST /sites/{id}/wordpress/{wp}/manage", s.guard("sites.wordpress.manage", "websites.manage", s.wpManage))
	s.mux.HandleFunc("POST /sites/{id}/wordpress/{wp}/tools", s.guard("sites.wordpress.tool", "websites.manage", s.wpTool))
	s.mux.HandleFunc("POST /sites/{id}/wordpress/{wp}/check", s.guard("sites.wordpress.check", "websites.view", s.wpCheck))
	s.mux.HandleFunc("POST /sites/{id}/wordpress/{wp}/login", s.guard("sites.wordpress.login", "websites.manage", s.wpLogin))
	s.mux.HandleFunc("GET /sites/{id}/wordpress/{wp}/logs", s.guard("sites.wordpress.logs", "websites.view", s.wpLogs))
	s.mux.HandleFunc("GET /sites/{id}/backups", s.guard("sites.backups", "backups.view", s.siteBackupsShow))
	s.mux.HandleFunc("POST /sites/{id}/backups", s.guard("sites.backups.create", "backups.manage", s.siteBackupsCreate))
	s.mux.HandleFunc("POST /sites/{id}/backups/{backup}/restore", s.guard("sites.backups.restore", "backups.manage", s.siteBackupsRestore))
	s.mux.HandleFunc("POST /sites/{id}/backups/{backup}/delete", s.guard("sites.backups.delete", "backups.manage", s.siteBackupsDelete))
	s.mux.HandleFunc("GET /sites/{id}/backups/{backup}/download", s.guard("sites.backups.download", "backups.manage", s.siteBackupsDownload))
	s.mux.HandleFunc("POST /sites/{id}/backup-schedules", s.guard("sites.backups.schedule", "backups.manage", s.siteBackupsSchedule))
	s.mux.HandleFunc("POST /sites/{id}/backup-schedules/{schedule}/delete", s.guard("sites.backups.schedule_delete", "backups.manage", s.siteBackupsScheduleDelete))
	s.mux.HandleFunc("GET /sites/{id}/security", s.guard("sites.security", "websites.view", s.siteSecurity))
	s.mux.HandleFunc("GET /sites/{id}/health", s.guard("sites.health", "websites.view", s.siteHealth))
	s.mux.HandleFunc("GET /wordpress", s.guard("wordpress", "websites.view", s.wordpressIndex))
	s.mux.HandleFunc("POST /wordpress/scan", s.guard("wordpress.scan", "websites.manage", s.wordpressScanAll))
	s.mux.HandleFunc("GET /sites/{id}/workers", s.guard("", "workers.manage", s.siteWorkers))
	// Worker write path: the plan feature withholds workers.manage when
	// disabled, so the permission guard enforces the feature too.
	s.mux.HandleFunc("POST /sites/{id}/workers", s.guard("", "workers.manage", s.workersStore))
	s.mux.HandleFunc("POST /sites/{id}/workers/{worker}/toggle", s.guard("", "workers.manage", s.workersToggle))
	s.mux.HandleFunc("POST /sites/{id}/workers/{worker}/restart", s.guard("", "workers.manage", s.workersRestart))
	s.mux.HandleFunc("POST /sites/{id}/workers/{worker}/delete", s.guard("", "workers.manage", s.workersDelete))
	// The website's Web firewall (ModSecurityController::site/saveSiteMode:
	// the page is websites.view; owners switch the mode, administrators
	// also turn single rules off from an event).
	s.mux.HandleFunc("GET /sites/{id}/firewall", s.guard("sites.firewall", "websites.view", s.siteFirewall))
	s.mux.HandleFunc("POST /sites/{id}/firewall", s.guard("sites.firewall.update", "websites.manage", s.siteFirewallMode))

	// The website's malware page (AvController::show/scan/release: the
	// website's ClamAV scans, the quarantine + release, the on-demand scan).
	s.mux.HandleFunc("GET /sites/{id}/malware", s.guard("sites.malware", "websites.view", s.siteMalware))
	s.mux.HandleFunc("POST /sites/{id}/malware/scan", s.guard("sites.malware.scan", "websites.manage", s.siteMalwareScan))
	s.mux.HandleFunc("POST /sites/{id}/malware/release", s.guard("sites.malware.release", "security.manage", s.siteMalwareRelease))

	// The website's visitor statistics (StatsController: the AWStats
	// report drawn from the data file, the on/off switch, the update).
	s.mux.HandleFunc("GET /sites/{id}/stats", s.guard("sites.stats", "logs.view", s.siteStats))
	s.mux.HandleFunc("POST /sites/{id}/stats/enable", s.guard("sites.stats.enable", "websites.manage", s.siteStatsEnable))
	s.mux.HandleFunc("POST /sites/{id}/stats/update", s.guard("sites.stats.update", "websites.manage", s.siteStatsUpdate))

	// The website's SSL/HTTPS area (SslController: the certificate state,
	// Let's Encrypt issue/renew, custom uploads, HTTPS options, security
	// headers).
	s.mux.HandleFunc("GET /sites/{id}/ssl", s.guard("sites.ssl", "ssl.view", s.siteSsl))
	s.mux.HandleFunc("POST /sites/{id}/ssl/issue", s.guard("sites.ssl.issue", "ssl.manage", s.siteSslIssue))
	s.mux.HandleFunc("POST /sites/{id}/ssl/custom", s.guard("sites.ssl.custom", "ssl.manage", s.siteSslCustom))
	s.mux.HandleFunc("POST /sites/{id}/ssl/renew", s.guard("sites.ssl.renew", "ssl.manage", s.siteSslRenew))
	s.mux.HandleFunc("POST /sites/{id}/ssl/disable", s.guard("sites.ssl.disable", "ssl.manage", s.siteSslDisable))
	s.mux.HandleFunc("POST /sites/{id}/ssl/options", s.guard("sites.ssl.options", "ssl.manage", s.siteSslOptions))
	s.mux.HandleFunc("POST /sites/{id}/security-headers", s.guard("sites.security_headers", "ssl.manage", s.siteSslSecurityHeaders))

	// The Redis key browser (server-wide, and per-website where the prefix
	// is the website's system user) + the website's Memcached page.
	s.mux.HandleFunc("GET /redis/browser", s.guard("redis.browser", "redis.manage", s.redisBrowserServerPage))
	s.mux.HandleFunc("POST /redis/browser/api", s.guard("redis.browser.api", "redis.manage", s.redisBrowserServerApi))
	s.mux.HandleFunc("GET /sites/{id}/memcached", s.guard("sites.memcached", "websites.view", s.siteMemcached))
	s.mux.HandleFunc("GET /sites/{id}/redis", s.guard("sites.redis", "websites.manage", s.redisBrowserSitePage))
	s.mux.HandleFunc("POST /sites/{id}/redis/api", s.guard("sites.redis.api", "websites.manage", s.redisBrowserSiteApi))

	// The server-side cache management: the Memcached page + its actions
	// (MemcachedController) and the Redis page + its actions
	// (RedisController/RedisService).
	s.mux.HandleFunc("GET /memcached", s.guard("memcached", "redis.manage", s.memcachedIndex))
	s.mux.HandleFunc("POST /memcached/configure", s.guard("memcached.configure", "redis.manage", s.memcachedConfigure))
	s.mux.HandleFunc("POST /memcached/flush", s.guard("memcached.flush", "redis.manage", s.memcachedFlush))
	s.mux.HandleFunc("POST /memcached/install", s.guard("memcached.install", "packages.manage", s.memcachedInstall))
	s.mux.HandleFunc("POST /memcached/restart", s.guard("memcached.restart", "services.manage", s.memcachedRestart))
	s.mux.HandleFunc("GET /redis", s.guard("redis", "redis.manage", s.redisIndex))
	s.mux.HandleFunc("POST /redis/configure", s.guard("redis.configure", "redis.manage", s.redisConfigure))
	s.mux.HandleFunc("POST /redis/password", s.guard("redis.password", "redis.manage", s.redisPassword))
	s.mux.HandleFunc("POST /redis/flush", s.guard("redis.flush", "redis.manage", s.redisFlush))
	s.mux.HandleFunc("POST /redis/users", s.guard("redis.users.store", "redis.manage", s.redisCreateUser))
	s.mux.HandleFunc("POST /redis/users/delete", s.guard("redis.users.delete", "redis.manage", s.redisDeleteUser))

	// Batch 56 — the server's FTP server (FtpController, server.manage /
	// packages.manage): pure-ftpd's install (job) + configure + disable;
	// the per-website accounts live on /sites/{id}/ftp.
	s.mux.HandleFunc("GET /ftp", s.guard("ftp", "server.manage", s.ftpIndex))
	s.mux.HandleFunc("POST /ftp/install", s.guard("ftp.install", "packages.manage", s.ftpInstall))
	s.mux.HandleFunc("POST /ftp/configure", s.guard("ftp.configure", "server.manage", s.ftpConfigure))
	s.mux.HandleFunc("POST /ftp/disable", s.guard("ftp.disable", "server.manage", s.ftpDisable))

	// Batch 69 — SystemController (packages + server settings, 14 routes).
	s.mux.HandleFunc("GET /packages", s.guard("packages", "packages.manage", s.systemPackages))
	s.mux.HandleFunc("POST /packages/install", s.guard("packages.install", "packages.manage", s.systemInstall))
	s.mux.HandleFunc("POST /packages/refresh", s.guard("packages.refresh", "packages.manage", s.systemRefresh))
	s.mux.HandleFunc("POST /packages/upgrade", s.guard("packages.upgrade", "packages.manage", s.systemUpgrade))
	s.mux.HandleFunc("POST /packages/repair", s.guard("packages.repair", "packages.manage", s.systemRepair))
	s.mux.HandleFunc("POST /server/hostname", s.guard("server.hostname", "server.manage", s.systemHostname))
	s.mux.HandleFunc("GET /server/hostname-references", s.guard("server.hostname_references", "server.manage", s.systemHostnameReferences))
	s.mux.HandleFunc("POST /server/hostname-references/webmail", s.guard("server.hostname_references.webmail", "server.manage", s.systemHostnameWebmail))
	s.mux.HandleFunc("POST /server/hostname-references/mail", s.guard("server.hostname_references.mail", "server.manage", s.systemHostnameRemail))
	s.mux.HandleFunc("POST /server/root-password", s.guard("server.root_password", "server.manage", s.systemRootPassword))
	s.mux.HandleFunc("POST /server/timezone", s.guard("server.timezone", "server.manage", s.systemTimezone))
	s.mux.HandleFunc("POST /server/graphical", s.guard("server.graphical", "server.manage", s.systemGraphical))
	s.mux.HandleFunc("POST /server/reboot", s.guard("server.reboot", "server.manage", s.systemReboot))
	s.mux.HandleFunc("POST /server/reboot/cancel", s.guard("server.reboot_cancel", "server.manage", s.systemCancelReboot))

	// Batch 69 — PhpController (8 routes: repository/install/toggle +
	// per-version show/extensions/ini/fpm + JSON modules list).
	s.mux.HandleFunc("POST /php/repository", s.guard("php.repository", "packages.manage", s.phpRepository))
	s.mux.HandleFunc("POST /php/install", s.guard("php.install", "php.manage", s.phpInstall))
	s.mux.HandleFunc("POST /php/{version}/toggle", s.guard("php.toggle", "php.manage", s.phpToggle))
	s.mux.HandleFunc("GET /php/{version}/modules", s.guard("php.modules", "php.view", s.phpModules))
	s.mux.HandleFunc("GET /php/{version}", s.guard("php.show", "php.manage", s.phpShow))
	s.mux.HandleFunc("POST /php/{version}/extensions/{op}", s.guard("php.extension", "php.manage", s.phpExtension))
	s.mux.HandleFunc("POST /php/{version}/ini", s.guard("php.ini", "php.manage", s.phpIni))
	s.mux.HandleFunc("POST /php/{version}/fpm", s.guard("php.fpm", "php.manage", s.phpFpm))

	// Batch 69 — AccountBackupsController (8 routes: account backup create/
	// download/delete/upload/preview/start/show/recreate).
	s.mux.HandleFunc("POST /backups/accounts", s.guard("backups.accounts.create", "backups.manage", s.accountBackupCreate))
	s.mux.HandleFunc("GET /backups/accounts/download", s.guard("backups.accounts.download", "backups.manage", s.accountBackupDownload))
	s.mux.HandleFunc("POST /backups/accounts/delete", s.guard("backups.accounts.delete", "backups.manage", s.accountBackupDelete))
	s.mux.HandleFunc("POST /backups/accounts/upload", s.guard("backups.accounts.upload", "backups.manage", s.accountBackupUpload))
	s.mux.HandleFunc("GET /backups/accounts/restore", s.guard("backups.accounts.preview", "backups.manage", s.accountBackupPreview))
	s.mux.HandleFunc("POST /backups/accounts/restore", s.guard("backups.accounts.restore", "users.manage", s.accountBackupStart))
	s.mux.HandleFunc("GET /backups/accounts/restores/{id}", s.guard("backups.accounts.restores", "backups.manage", s.accountBackupShow))
	s.mux.HandleFunc("POST /backups/accounts/restores/{id}/recreate", s.guard("backups.accounts.recreate", "users.manage", s.accountBackupRecreate))

	// Batch 69 — RolesController (8 routes: list/create/store/edit/update/
	// reset/destroy/two-factor).
	s.mux.HandleFunc("GET /roles", s.guard("roles", "users.view", s.rolesIndex))
	s.mux.HandleFunc("GET /roles/new", s.guard("roles.create", "roles.manage", s.rolesCreate))
	s.mux.HandleFunc("POST /roles", s.guard("roles.store", "roles.manage", s.rolesStore))
	s.mux.HandleFunc("GET /roles/{id}/edit", s.guard("roles.edit", "users.view", s.rolesEdit))
	s.mux.HandleFunc("POST /roles/{id}", s.guard("roles.update", "roles.manage", s.rolesUpdate))
	s.mux.HandleFunc("POST /roles/{id}/reset", s.guard("roles.reset", "roles.manage", s.rolesReset))
	s.mux.HandleFunc("POST /roles/{id}/two-factor", s.guard("roles.2fa", "roles.manage", s.rolesTwoFactor))
	s.mux.HandleFunc("POST /roles/{id}/delete", s.guard("roles.delete", "roles.manage", s.rolesDestroy))

	// Batch 70 — MonitoringController (7 routes: dashboard, live stats,
	// process manager, trace, signal, kill-user).
	s.mux.HandleFunc("GET /monitoring", s.guard("monitoring", "monitoring.view", s.monitoringIndex))
	s.mux.HandleFunc("GET /monitoring/live", s.guard("monitoring.live", "monitoring.view", s.monitoringLive))
	s.mux.HandleFunc("GET /monitoring/live.json", s.guard("monitoring.live.data", "monitoring.view", s.monitoringLiveData))
	s.mux.HandleFunc("GET /monitoring/processes", s.guard("monitoring.processes", "monitoring.view", s.monitoringProcesses))
	s.mux.HandleFunc("GET /monitoring/processes/trace", s.guard("monitoring.processes.trace", "server.manage", s.monitoringTrace))
	s.mux.HandleFunc("POST /monitoring/processes/signal", s.guard("monitoring.processes.signal", "server.manage", s.monitoringSignal))
	s.mux.HandleFunc("POST /monitoring/processes/kill-user", s.guard("monitoring.processes.kill_user", "server.manage", s.monitoringKillUser))

	// Batch 70 — TopbarController (5 routes: load status, restart-all,
	// WhaCP update page/check/start). All admin-only.
	s.mux.HandleFunc("GET /topbar/status", s.guard("topbar.status", "monitoring.view", s.topbarStatus))
	s.mux.HandleFunc("POST /services/restart-all", s.guard("services.restart_all", "services.manage", s.topbarRestartAll))
	s.mux.HandleFunc("POST /services/control", s.guard("services.control", "services.manage", s.servicesControl))
	s.mux.HandleFunc("POST /services/whacp/{key}/restart", s.guard("services.whacp.restart", "services.manage", s.servicesRestartWhacp))
	s.mux.HandleFunc("GET /whacp/update", s.guard("whacp.update", "server.manage", s.topbarUpdate))
	s.mux.HandleFunc("POST /whacp/update/check", s.guard("whacp.update.check", "server.manage", s.topbarUpdateCheck))
	s.mux.HandleFunc("POST /whacp/update", s.guard("whacp.update.start", "server.manage", s.topbarUpdateStart))

	// Batch 36 — per-website Cloudflare area (direct Cloudflare API; the
	// customer's own per-website token is stored encrypted).
	s.mux.HandleFunc("GET /sites/{id}/cloudflare", s.guard("sites.cloudflare", "websites.manage", s.siteCloudflare))
	s.mux.HandleFunc("POST /sites/{id}/cloudflare/connect", s.guard("sites.cloudflare.connect", "websites.manage", s.siteCloudflareConnect))
	s.mux.HandleFunc("POST /sites/{id}/cloudflare/disconnect", s.guard("sites.cloudflare.disconnect", "websites.manage", s.siteCloudflareDisconnect))
	s.mux.HandleFunc("POST /sites/{id}/cloudflare/ssl", s.guard("sites.cloudflare.ssl", "websites.manage", s.siteCloudflareSsl))
	s.mux.HandleFunc("POST /sites/{id}/cloudflare/always-https", s.guard("sites.cloudflare.always_https", "websites.manage", s.siteCloudflareAlwaysHttps))
	s.mux.HandleFunc("POST /sites/{id}/cloudflare/proxy", s.guard("sites.cloudflare.proxy", "websites.manage", s.siteCloudflareProxy))
	s.mux.HandleFunc("POST /sites/{id}/cloudflare/purge", s.guard("sites.cloudflare.purge", "websites.manage", s.siteCloudflarePurge))

	// Batch 37 — server-side Cloudflare area (one token per server, managed
	// by administrators; the account's zones).
	s.mux.HandleFunc("GET /cloudflare", s.guard("cloudflare", "server.view", s.cloudflareIndex))
	s.mux.HandleFunc("POST /cloudflare/connect", s.guard("cloudflare.connect", "server.manage", s.cloudflareConnect))
	s.mux.HandleFunc("POST /cloudflare/disconnect", s.guard("cloudflare.disconnect", "server.manage", s.cloudflareDisconnect))
	s.mux.HandleFunc("POST /cloudflare/ssl", s.guard("cloudflare.ssl", "server.manage", s.cloudflareSsl))
	s.mux.HandleFunc("POST /cloudflare/always-https", s.guard("cloudflare.always_https", "server.manage", s.cloudflareAlwaysHttps))
	s.mux.HandleFunc("POST /cloudflare/proxy", s.guard("cloudflare.proxy", "server.manage", s.cloudflareProxy))
	s.mux.HandleFunc("POST /cloudflare/purge", s.guard("cloudflare.purge", "server.manage", s.cloudflarePurge))

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

	// MongoDB (MongoController) — the server's own MongoDB: the page with its
	// six sections, the management actions, the wildcard browser and the
	// saved remote servers. Reading is mongo.view; every change mongo.manage
	// (with packages.manage for the repository/install jobs and
	// services.manage for the service controls), exactly as the PHP routes
	// declare. The browser's mutating operations re-check mongo.manage
	// inside the read-permitted endpoint.
	s.mux.HandleFunc("GET /mongo", s.guard("", "mongo.view", s.mongoIndex))
	s.mux.HandleFunc("POST /mongo/databases", s.guard("", "mongo.manage", s.mongoCreateDatabase))
	s.mux.HandleFunc("POST /mongo/databases/drop", s.guard("", "mongo.manage", s.mongoDropDatabase))
	s.mux.HandleFunc("POST /mongo/databases/bulk-drop", s.guard("", "mongo.manage", s.mongoBulkDropDatabases))
	s.mux.HandleFunc("POST /mongo/users", s.guard("", "mongo.manage", s.mongoCreateUser))
	s.mux.HandleFunc("POST /mongo/users/password", s.guard("", "mongo.manage", s.mongoSetPassword))
	s.mux.HandleFunc("POST /mongo/users/drop", s.guard("", "mongo.manage", s.mongoDropUser))
	s.mux.HandleFunc("POST /mongo/config", s.guard("", "mongo.manage", s.mongoConfig))
	s.mux.HandleFunc("POST /mongo/config-file", s.guard("", "mongo.manage", s.mongoConfigFile))
	s.mux.HandleFunc("POST /mongo/admin-password", s.guard("", "mongo.manage", s.mongoAdminPassword))
	s.mux.HandleFunc("POST /mongo/auth", s.guard("", "mongo.manage", s.mongoAuth))
	s.mux.HandleFunc("POST /mongo/note", s.guard("", "mongo.manage", s.mongoNote))
	s.mux.HandleFunc("POST /mongo/backup", s.guard("", "mongo.manage", s.mongoBackup))
	s.mux.HandleFunc("POST /mongo/restore", s.guard("", "mongo.manage", s.mongoRestore))
	s.mux.HandleFunc("POST /mongo/backups/delete", s.guard("", "mongo.manage", s.mongoDeleteBackup))
	s.mux.HandleFunc("POST /mongo/repository", s.guard("", "packages.manage", s.mongoEnableRepository))
	s.mux.HandleFunc("POST /mongo/install", s.guard("", "packages.manage", s.mongoInstall))
	s.mux.HandleFunc("POST /mongo/service", s.guard("", "services.manage", s.mongoServiceControl))
	s.mux.HandleFunc("POST /mongo/remotes", s.guard("", "mongo.manage", s.mongoRemoteAdd))
	s.mux.HandleFunc("POST /mongo/remotes/delete", s.guard("", "mongo.manage", s.mongoRemoteDelete))
	s.mux.HandleFunc("POST /mongo/remotes/test", s.guard("", "mongo.manage", s.mongoRemoteTest))
	s.mux.HandleFunc("POST /mongo/remotes/databases", s.guard("", "mongo.manage", s.mongoRemoteDatabases))
	s.mux.HandleFunc("POST /mongo/remotes/pull", s.guard("", "mongo.manage", s.mongoRemotePull))
	s.mux.HandleFunc("GET /mongo/browser", s.guard("", "mongo.view", s.mongoAdminBrowserPage))
	s.mux.HandleFunc("POST /mongo/browse", s.guard("", "mongo.view", s.mongoAdminBrowse))
	s.mux.HandleFunc("GET /mongo/export", s.guard("", "mongo.view", s.mongoAdminExport))
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
	s.mux.HandleFunc("POST /dns/install", s.guard("dns.install", "packages.manage", s.dnsInstall))
	s.mux.HandleFunc("POST /dns/setup", s.guard("dns.setup", "server.manage", s.dnsSetup))

	// Domains — every hostname served, with cert coverage.
	s.mux.HandleFunc("GET /domains", s.guard("domains", "websites.view", s.domainsIndex))
	// DNS: the website's DNS set-up page (DnsController::site), zone creation
	// (DnsController::store) and the zone's records page (DnsController::show
	// — read-only until the record routes land).
	s.mux.HandleFunc("GET /sites/{id}/dns", s.guard("sites.dns", "dns.manage", s.siteDns))
	s.mux.HandleFunc("POST /dns/zones", s.guard("dns.store", "dns.manage", s.dnsZoneStore))
	s.mux.HandleFunc("GET /dns/zones/{id}", s.guard("dns.zone", "dns.manage", s.dnsZoneShow))
	// The zone editor's write routes (record CRUD, bulk, settings, sync,
	// import/export, delete) — the zone batch.
	s.mux.HandleFunc("POST /dns/zones/{id}/records", s.guard("dns.records.store", "dns.manage", s.dnsRecordAdd))
	s.mux.HandleFunc("POST /dns/zones/{id}/records/{record}", s.guard("dns.records.update", "dns.manage", s.dnsRecordUpdate))
	s.mux.HandleFunc("POST /dns/zones/{id}/records/{record}/delete", s.guard("dns.records.delete", "dns.manage", s.dnsRecordDelete))
	s.mux.HandleFunc("POST /dns/zones/{id}/bulk", s.guard("dns.bulk", "dns.manage", s.dnsBulk))
	s.mux.HandleFunc("POST /dns/zones/{id}/settings", s.guard("dns.settings", "dns.manage", s.dnsSettings))
	s.mux.HandleFunc("POST /dns/zones/{id}/sync", s.guard("dns.sync", "dns.manage", s.dnsSync))
	s.mux.HandleFunc("GET /dns/zones/{id}/export", s.guard("dns.export", "dns.manage", s.dnsExport))
	s.mux.HandleFunc("POST /dns/zones/{id}/import", s.guard("dns.import", "dns.manage", s.dnsImport))
	s.mux.HandleFunc("POST /dns/zones/{id}/delete", s.guard("dns.delete", "dns.manage", s.dnsZoneDelete))

	// Email domains list. mail.manage is withheld by the email plan feature.
	s.mux.HandleFunc("GET /mail", s.guard("", "mail.manage", s.mailIndex))
	// Mail: the website's email set-up page (MailController::site), hosting a
	// domain (MailController::store) and the domain page (MailController::show
	// — read-only until the mailbox routes land).
	s.mux.HandleFunc("GET /sites/{id}/mail", s.guard("sites.mail", "mail.manage", s.siteMail))
	s.mux.HandleFunc("POST /mail/domains", s.guard("mail.store", "mail.manage", s.mailDomainStore))
	s.mux.HandleFunc("GET /mail/domains/{id}", s.guard("mail.domain", "mail.manage", s.mailDomainShow))
	// Mail domain page: the mailbox + forwarder CRUD, routing, the DNS
	// publish + public-DNS check and stopping hosting.
	s.mux.HandleFunc("POST /mail/domains/{id}/mailboxes", s.guard("mail.mailboxes.store", "mail.manage", s.mailMailboxSave))
	s.mux.HandleFunc("POST /mail/domains/{id}/mailboxes/{mailbox}", s.guard("mail.mailboxes.update", "mail.manage", s.mailMailboxSave))
	s.mux.HandleFunc("POST /mail/domains/{id}/mailboxes/{mailbox}/delete", s.guard("mail.mailboxes.delete", "mail.manage", s.mailMailboxDelete))
	s.mux.HandleFunc("POST /mail/domains/{id}/aliases", s.guard("mail.aliases.store", "mail.manage", s.mailAliasSave))
	s.mux.HandleFunc("POST /mail/domains/{id}/aliases/{alias}/delete", s.guard("mail.aliases.delete", "mail.manage", s.mailAliasDelete))
	s.mux.HandleFunc("POST /mail/domains/{id}/routing", s.guard("mail.routing", "mail.manage", s.mailRouting))
	s.mux.HandleFunc("POST /mail/domains/{id}/dns", s.guard("mail.dns", "mail.manage", s.mailPublishDns))
	s.mux.HandleFunc("GET /mail/domains/{id}/dns-check", s.guard("mail.dns.check", "mail.manage", s.mailDnsCheck))
	s.mux.HandleFunc("POST /mail/domains/{id}/delete", s.guard("mail.delete", "mail.manage", s.mailDomainDelete))
	// Mail domain page: the per-mailbox filters + autoresponder.
	s.mux.HandleFunc("GET /mail/domains/{id}/mailboxes/{mailbox}/filters", s.guard("mail.filters", "mail.manage", s.mailFiltersShow))
	s.mux.HandleFunc("POST /mail/domains/{id}/mailboxes/{mailbox}/filters", s.guard("mail.filters.save", "mail.manage", s.mailFilterSave))
	s.mux.HandleFunc("POST /mail/domains/{id}/mailboxes/{mailbox}/filters/{n}/toggle", s.guard("mail.filters.toggle", "mail.manage", s.mailFilterToggle))
	s.mux.HandleFunc("POST /mail/domains/{id}/mailboxes/{mailbox}/filters/{n}/delete", s.guard("mail.filters.delete", "mail.manage", s.mailFilterDelete))
	s.mux.HandleFunc("POST /mail/domains/{id}/mailboxes/{mailbox}/responder", s.guard("mail.responder", "mail.manage", s.mailResponder))
	// Mail administration (MailAdminController): the queue (list, headers,
	// actions), the delivery reports + statistics, the mail server status +
	// settings and the tools (trace, permission repair). Server
	// administrators only — the handlers gate on the all scope in addition
	// to these permissions.
	s.mux.HandleFunc("GET /mail/admin/queue", s.guard("mail.admin.queue", "server.view", s.mailAdminQueue))
	s.mux.HandleFunc("GET /mail/admin/queue/{id}", s.guard("mail.admin.message", "server.view", s.mailAdminMessage))
	s.mux.HandleFunc("POST /mail/admin/queue", s.guard("mail.admin.queue.action", "server.manage", s.mailAdminQueueAction))
	s.mux.HandleFunc("GET /mail/admin/server", s.guard("mail.admin.server", "server.view", s.mailAdminServer))
	s.mux.HandleFunc("POST /mail/admin/server", s.guard("mail.admin.settings", "server.manage", s.mailAdminSaveSettings))
	s.mux.HandleFunc("GET /mail/admin/tools", s.guard("mail.admin.tools", "server.view", s.mailAdminTools))
	s.mux.HandleFunc("POST /mail/admin/tools/repair", s.guard("mail.admin.repair", "server.manage", s.mailAdminRepair))
	s.mux.HandleFunc("GET /mail/admin/reports", s.guard("mail.admin.reports", "server.view", s.mailAdminReports))
	// Mail administration continued (MailGreylistController): the
	// settings + trusted hosts + trusted mail providers, and the triplet
	// report with the cleanup actions.
	s.mux.HandleFunc("GET /mail/admin/greylisting", s.guard("mail.greylist", "server.view", s.mailGreylistIndex))
	s.mux.HandleFunc("POST /mail/admin/greylisting", s.guard("mail.greylist.save", "server.manage", s.mailGreylistSave))
	s.mux.HandleFunc("POST /mail/admin/greylisting/trusted", s.guard("mail.greylist.trusted", "server.manage", s.mailGreylistSaveTrusted))
	s.mux.HandleFunc("POST /mail/admin/greylisting/trusted/{n}/delete", s.guard("mail.greylist.trusted.delete", "server.manage", s.mailGreylistDeleteTrusted))
	s.mux.HandleFunc("POST /mail/admin/greylisting/providers", s.guard("mail.greylist.providers", "server.manage", s.mailGreylistSaveProviders))
	s.mux.HandleFunc("POST /mail/admin/greylisting/providers/update", s.guard("mail.greylist.providers.update", "server.manage", s.mailGreylistUpdateProviders))
	s.mux.HandleFunc("GET /mail/admin/greylisting/report", s.guard("mail.greylist.report", "server.view", s.mailGreylistReport))
	s.mux.HandleFunc("POST /mail/admin/greylisting/report", s.guard("mail.greylist.cleanup", "server.manage", s.mailGreylistCleanup))
	// Outgoing mail limits (MailRateController).
	s.mux.HandleFunc("GET /mail/admin/sending-limits", s.guard("mail.limits", "server.view", s.mailRateIndex))
	s.mux.HandleFunc("POST /mail/admin/sending-limits", s.guard("mail.limits.save", "server.manage", s.mailRateSave))
	s.mux.HandleFunc("POST /mail/admin/sending-limits/reset", s.guard("mail.limits.reset", "server.manage", s.mailRateReset))

	// IP addresses (IpController) — the pool, the routed IPv6 ranges and
	// the migration wizard; server administrators only (checked in the
	// handlers, the PHP Scope::isAll denial).
	s.mux.HandleFunc("GET /ips", s.guard("ips", "server.view", s.ipIndex))
	s.mux.HandleFunc("POST /ips", s.guard("ips.store", "server.manage", s.ipStore))
	s.mux.HandleFunc("POST /ips/rebuild", s.guard("ips.rebuild", "server.manage", s.ipRebuild))
	s.mux.HandleFunc("GET /ips/ranges", s.guard("ips.ranges", "server.view", s.ipRanges))
	s.mux.HandleFunc("POST /ips/ranges", s.guard("ips.ranges.store", "server.manage", s.ipStoreRange))
	s.mux.HandleFunc("POST /ips/ranges/{id}", s.guard("ips.ranges.update", "server.manage", s.ipUpdateRange))
	s.mux.HandleFunc("POST /ips/ranges/{id}/delete", s.guard("ips.ranges.delete", "server.manage", s.ipDeleteRange))
	s.mux.HandleFunc("POST /ips/ranges/{id}/allocate", s.guard("ips.ranges.allocate", "server.manage", s.ipAllocate))
	s.mux.HandleFunc("GET /ips/migrate", s.guard("ips.migrate", "server.view", s.ipMigratePage))
	s.mux.HandleFunc("POST /ips/migrate", s.guard("ips.migrate.run", "server.manage", s.ipRunMigration))
	s.mux.HandleFunc("POST /ips/{id}", s.guard("ips.update", "server.manage", s.ipUpdate))
	// POST /ips/{id}/delete would overlap POST /ips/ranges/{id} ("/ips/ranges/
	// delete" matches both and neither is more specific), which Go's mux
	// rejects at start-up. Register the wildcard-action form instead: the
	// literal "ranges" pattern is the more specific one, and only the "delete"
	// action is served here.
	ipDelete := s.guard("ips.delete", "server.manage", s.ipDelete)
	s.mux.HandleFunc("POST /ips/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("action") != "delete" {
			http.NotFound(w, r)
			return
		}
		ipDelete(w, r)
	})

	// Discovery (DiscoveryController): the read-only inventory pages. Every
	// page is tenant-scoped in the handler (DiscoveryService::forScope); only
	// the re-scan needs discovery.run, and it still changes nothing on the
	// server.
	s.mux.HandleFunc("GET /discovery", s.guard("discovery", "websites.view", s.discoveryIndex))
	s.mux.HandleFunc("POST /discovery/run", s.guard("discovery.run", "discovery.run", s.discoveryRun))
	s.mux.HandleFunc("GET /php", s.guard("php", "php.view", s.discoveryPHP))
	s.mux.HandleFunc("GET /ssl", s.guard("ssl", "ssl.view", s.discoverySSL))
	s.mux.HandleFunc("GET /cron", s.guard("cron", "cron.view", s.discoveryCron))

	// Application Manager (AppManagerController).
	s.mux.HandleFunc("GET /apps-manager", s.guard("apps.manager", "settings.view", s.appsManagerIndex))
	s.mux.HandleFunc("POST /apps-manager/installer", s.guard("apps.manager.installer", "settings.manage", s.appsManagerInstaller))
	s.mux.HandleFunc("POST /apps-manager/category", s.guard("apps.manager.category", "settings.manage", s.appsManagerCategory))
	s.mux.HandleFunc("POST /apps-manager/app", s.guard("apps.manager.app", "settings.manage", s.appsManagerApp))

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

	// Hosting plans (staff / reseller-own); roles are registered with the
	// RolesController routes above.
	s.mux.HandleFunc("GET /plans", s.guard("", "plans.manage", s.plansIndex))

	// Background job log (the job viewer polls it). Authenticated only —
	// visibility is per job (JobTracker.VisibleTo), so no permission, as in
	// PHP (route jobs.show).
	s.mux.HandleFunc("GET /jobs/{id}", s.guard("", "", s.jobsShow))

	// Backups — server-wide page + remote storage + encryption key + server
	// configuration backups (BackupsController). The per-website backups tab
	// lives on /sites/{id}/backups.
	s.mux.HandleFunc("GET /backups", s.guard("backups", "backups.view", s.backupsIndex))
	s.mux.HandleFunc("POST /backups/server", s.guard("backups.server", "backups.manage", s.backupsCreateServer))
	s.mux.HandleFunc("POST /backups/server/{backup}/delete", s.guard("backups.server.delete", "backups.manage", s.backupsDeleteServer))
	s.mux.HandleFunc("GET /backups/server/{backup}/download", s.guard("backups.server.download", "backups.manage", s.backupsDownloadServer))
	s.mux.HandleFunc("POST /backups/server/{backup}/restore", s.guard("backups.server.restore", "server.manage", s.backupsRestoreServer))
	s.mux.HandleFunc("POST /backups/storages", s.guard("backups.storages.store", "backups.manage", s.backupsStoreStorage))
	s.mux.HandleFunc("GET /backups/storages/confirm", s.guard("backups.storages.confirm", "backups.manage", s.backupsConfirmStorage))
	s.mux.HandleFunc("POST /backups/storages/confirm", s.guard("backups.storages.trust", "backups.manage", s.backupsTrustStorage))
	s.mux.HandleFunc("GET /backups/storages/google/callback", s.guard("backups.storages.google", "backups.manage", s.backupsGoogleCallback))
	s.mux.HandleFunc("POST /backups/storages/{storage}/active", s.guard("backups.storages.active", "backups.manage", s.backupsActivateStorage))
	s.mux.HandleFunc("POST /backups/storages/{storage}/delete", s.guard("backups.storages.delete", "backups.manage", s.backupsDeleteStorage))
	s.mux.HandleFunc("POST /backups/storages/{storage}/scan", s.guard("backups.storages.scan", "backups.manage", s.backupsScanStorage))
	s.mux.HandleFunc("POST /backups/storages/{storage}/test", s.guard("backups.storages.test", "backups.manage", s.backupsTestStorage))
	s.mux.HandleFunc("POST /backups/storages/{storage}", s.guard("backups.storages.update", "backups.manage", s.backupsUpdateStorage))
	s.mux.HandleFunc("POST /backups/key/download", s.guard("backups.key.download", "backups.manage", s.backupsDownloadKey))
	s.mux.HandleFunc("POST /backups/key/import", s.guard("backups.key.import", "backups.manage", s.backupsImportKey))
	s.mux.HandleFunc("POST /backups/schedules", s.guard("backups.schedules.store", "backups.manage", s.backupsStoreServerSchedule))
	s.mux.HandleFunc("POST /backups/schedules/{schedule}/delete", s.guard("backups.schedules.delete", "backups.manage", s.backupsDeleteServerSchedule))

	// Notifications (per-recipient visibility).
	s.mux.HandleFunc("GET /notifications", s.guard("", "notifications.view", s.notificationsIndex))
	s.mux.HandleFunc("POST /notifications/read", s.guard("", "notifications.view", s.notificationsRead))
	s.mux.HandleFunc("GET /notifications/summary", s.guard("", "notifications.view", s.notificationsSummary))
	s.mux.HandleFunc("POST /notifications/email", s.guard("", "notifications.view", s.notificationsEmail))

	// Own profile (authenticated; no special permission).
	s.mux.HandleFunc("GET /profile", s.guard("", "", s.profileShow))
	s.mux.HandleFunc("POST /profile/tokens", s.guard("tokens.store", "api.tokens", s.apiTokenStore))
	s.mux.HandleFunc("POST /profile/tokens/{id}/revoke", s.guard("", "api.tokens", s.apiTokenRevoke))

	// Two-factor sign-in: the second-factor step of a sign-in (the pending
	// state lives in the sealed cookie; no signed-in session yet) plus the
	// profile self-service (setup / enable / disable / recovery).
	s.mux.HandleFunc("GET /login/two-factor", s.sessionCtx(s.showTwoFactor))
	s.mux.HandleFunc("POST /login/two-factor", s.sessionCtx(s.doTwoFactor))
	// IP verification (sign-in from a new address): the emailed-code flow is
	// not ported yet; the page renders with a notice and a cancel button, and
	// POST redirects back to /login.
	s.mux.HandleFunc("GET /login/verify-ip", s.sessionCtx(s.verifyIpForm))
	s.mux.HandleFunc("POST /login/verify-ip", s.sessionCtx(s.verifyIp))
	// WebAuthn (security keys) sign-in + profile management. The WebAuthn
	// library isn't vendored in Go yet, so the JSON endpoints return 501
	// errors; DELETE for a profile key IS implemented (DELETE FROM
	// webauthn_credentials WHERE id = ? AND user_id = ?) so users can remove
	// a stuck key and recover access.
	s.mux.HandleFunc("POST /login/two-factor/key/options", s.sessionCtx(s.webauthnLoginOptions))
	s.mux.HandleFunc("POST /login/two-factor/key", s.sessionCtx(s.webauthnLogin))
	s.mux.HandleFunc("POST /profile/security-keys/options", s.guard("profile.2fa.key.options", "", s.webauthnProfileOptions))
	s.mux.HandleFunc("POST /profile/security-keys", s.guard("profile.2fa.key.add", "", s.webauthnProfileAdd))
	s.mux.HandleFunc("POST /profile/security-keys/{id}/delete", s.guard("profile.2fa.key.delete", "", s.webauthnProfileDelete))
	s.mux.HandleFunc("POST /profile/two-factor/setup", s.guard("", "", s.profileTwoFactorSetup))
	s.mux.HandleFunc("POST /profile/two-factor/enable", s.guard("", "", s.profileTwoFactorEnable))
	s.mux.HandleFunc("POST /profile/two-factor/disable", s.guard("", "", s.profileTwoFactorDisable))
	s.mux.HandleFunc("POST /profile/two-factor/recovery", s.guard("", "", s.profileTwoFactorRecovery))
	s.mux.HandleFunc("POST /profile/sessions/revoke", s.guard("", "", s.profileRevokeSessions))
	s.mux.HandleFunc("POST /profile/password", s.guard("profile.password", "", s.profilePassword))

	// Password reset by email (minimal layout, no signed-in session needed;
	// the answer is the same whether or not the account exists).
	s.mux.HandleFunc("GET /password/forgot", s.sessionCtx(s.showForgot))
	s.mux.HandleFunc("POST /password/forgot", s.sessionCtx(s.doForgot))
	s.mux.HandleFunc("GET /password/reset/{token}", s.sessionCtx(s.showReset))
	s.mux.HandleFunc("POST /password/reset/{token}", s.sessionCtx(s.doReset))

	// FFmpeg (administrators; installation, plan-based access).
	s.mux.HandleFunc("GET /ffmpeg", s.guard("ffmpeg", "server.view", s.ffmpegIndex))
	s.mux.HandleFunc("POST /ffmpeg/install", s.guard("ffmpeg.install", "packages.manage", s.ffmpegInstall))
	s.mux.HandleFunc("POST /ffmpeg/uninstall", s.guard("ffmpeg.uninstall", "packages.manage", s.ffmpegUninstall))
	s.mux.HandleFunc("POST /ffmpeg/apply", s.guard("ffmpeg.apply", "server.manage", s.ffmpegApply))

	// Nginx (administrators; service state, test/reload, tuning).
	s.mux.HandleFunc("GET /nginx", s.guard("nginx", "server.view", s.nginxIndex))
	s.mux.HandleFunc("POST /nginx/test", s.guard("nginx.test", "server.view", s.nginxTest))
	s.mux.HandleFunc("POST /nginx/reload", s.guard("nginx.reload", "services.manage", s.nginxReload))
	s.mux.HandleFunc("POST /nginx/tuning", s.guard("nginx.tuning", "server.manage", s.nginxTuning))

	// Runtimes (Node.js/Python versions for website workers and apps).
	s.mux.HandleFunc("GET /runtimes", s.guard("runtimes", "server.view", s.runtimesIndex))
	s.mux.HandleFunc("POST /runtimes/install", s.guard("runtimes.install", "server.manage", s.runtimesInstall))
	s.mux.HandleFunc("POST /runtimes/remove", s.guard("runtimes.remove", "server.manage", s.runtimesRemove))

	// Server services listing and system accounts (GET pages).
	s.mux.HandleFunc("GET /server", s.guard("server", "server.view", s.serverIndex))
	s.mux.HandleFunc("GET /server/health", s.guard("server.health", "server.view", s.serverHealth))
	s.mux.HandleFunc("GET /services", s.guard("services", "services.view", s.servicesIndex))
	s.mux.HandleFunc("GET /system-users", s.guard("system_users", "system_users.view", s.systemUsersIndex))

	// Plugins (administrators; plugin runtime is a stub in Go).
	s.mux.HandleFunc("GET /admin/plugins", s.guard("plugins", "plugins.manage", s.pluginsIndex))
	s.mux.HandleFunc("POST /admin/plugins/{name}/toggle", s.guard("plugins.toggle", "plugins.manage", s.pluginsToggle))
	s.mux.HandleFunc("GET /plugins/{name}", s.guard("", "", s.pluginDispatch))
	s.mux.HandleFunc("POST /plugins/{name}", s.guard("", "", s.pluginDispatch))
	s.mux.HandleFunc("GET /plugins/{name}/{path...}", s.guard("", "", s.pluginDispatch))
	s.mux.HandleFunc("POST /plugins/{name}/{path...}", s.guard("", "", s.pluginDispatch))

	// Components (server status + fix actions).
	s.mux.HandleFunc("GET /components", s.guard("components", "server.view", s.componentsIndex))
	s.mux.HandleFunc("POST /components/profile", s.guard("components.profile", "server.manage", s.componentsProfile))
	s.mux.HandleFunc("POST /components/{id}/{step}", s.guard("components.fix", "server.view", s.componentsFix))

	// Feature lists (administrators manage plans' allowed features).
	s.mux.HandleFunc("GET /features", s.guard("features", "plans.manage", s.featuresIndex))
	s.mux.HandleFunc("POST /features", s.guard("features.store", "plans.manage", s.featuresStore))
	s.mux.HandleFunc("POST /features/{id}/delete", s.guard("features.delete", "plans.manage", s.featuresDestroy))

	// Hosting plans (staff + resellers via PlanService::visible scoping).
	s.mux.HandleFunc("POST /plans", s.guard("plans.store", "plans.manage", s.plansStore))
	s.mux.HandleFunc("POST /plans/{id}/delete", s.guard("plans.delete", "plans.manage", s.plansDestroy))

	// Resource limits (cgroups) and disk quotas — placeholder handlers until
	// ResourceLimitService/DiskQuotaService are ported; the index page shows
	// a friendly notice and POST routes return a flash error instead of 404.
	s.mux.HandleFunc("GET /limits", s.guard("limits", "server.view", s.limitsIndex))
	s.mux.HandleFunc("POST /limits/apply", s.guard("limits.apply", "server.manage", s.limitsApply))
	s.mux.HandleFunc("POST /limits/quota/enable", s.guard("limits.quota.enable", "server.manage", s.limitsEnableQuota))

	// PHP Manager (EasyApache-style version/extension chooser). Export is
	// fully implemented (downloads saved php_profile JSON); review/apply/
	// import are stubs that flash a friendly "not yet ported" error; index
	// shows live discovery + available data from the agent.
	s.mux.HandleFunc("GET /php/manager", s.guard("php.manager", "packages.manage", s.phpManagerIndex))
	s.mux.HandleFunc("POST /php/manager/apply", s.guard("php.manager.apply", "packages.manage", s.phpManagerApply))
	s.mux.HandleFunc("GET /php/manager/export", s.guard("php.manager.export", "packages.manage", s.phpManagerExport))
	s.mux.HandleFunc("POST /php/manager/import", s.guard("php.manager.import", "packages.manage", s.phpManagerImport))
	s.mux.HandleFunc("POST /php/manager/review", s.guard("php.manager.review", "packages.manage", s.phpManagerReview))

	// Setup wizard: index shows live components/metrics from the agent with
	// a stub notice about the step runner; POST /setup/step returns a 501
	// JSON response (ServerSetupService is not ported); POST /setup/dismiss
	// is fully implemented (sets the seen+done flags + audit).
	s.mux.HandleFunc("GET /setup", s.guard("setup", "server.manage", s.setupIndex))
	s.mux.HandleFunc("POST /setup/step", s.guard("setup.step", "server.manage", s.setupStep))
	s.mux.HandleFunc("POST /setup/dismiss", s.guard("setup.dismiss", "server.manage", s.setupDismiss))

	// Mail server admin install/setup actions (POST; index and domain/
	// mailbox CRUD live in mail.go). These call the agent packages.install /
	// mail.configure / mail.spam_configure / mail.webmail_configure and
	// track jobs where applicable.
	s.mux.HandleFunc("POST /mail/install", s.guard("mail.install", "packages.manage", s.mailInstall))
	s.mux.HandleFunc("POST /mail/setup", s.guard("mail.setup", "server.manage", s.mailSetup))
	s.mux.HandleFunc("POST /mail/spam/install", s.guard("mail.spam.install", "packages.manage", s.mailInstallSpam))
	s.mux.HandleFunc("POST /mail/spam", s.guard("mail.spam", "server.manage", s.mailSpam))
	s.mux.HandleFunc("POST /mail/webmail/install", s.guard("mail.webmail.install", "packages.manage", s.mailInstallWebmail))
	s.mux.HandleFunc("POST /mail/webmail", s.guard("mail.webmail", "server.manage", s.mailWebmail))

	// Panel settings pages (basic/tweak/mail/test + generic update). The
	// generic update enforces a key allowlist so a rogue form cannot
	// smuggle arbitrary keys into the settings table. Mail test is a stub
	// until Mailer::send is ported.
	s.mux.HandleFunc("GET /settings", s.guard("settings", "settings.view", s.settingsIndex))
	s.mux.HandleFunc("POST /settings", s.guard("settings.update", "settings.manage", s.settingsUpdate))
	s.mux.HandleFunc("GET /settings/basic", s.guard("settings.basic", "settings.view", s.settingsBasic))
	s.mux.HandleFunc("POST /settings/basic", s.guard("settings.basic.save", "settings.manage", s.settingsBasicSave))
	s.mux.HandleFunc("GET /settings/tweak", s.guard("settings.tweak", "settings.view", s.settingsTweak))
	s.mux.HandleFunc("POST /settings/tweak", s.guard("settings.tweak.save", "settings.manage", s.settingsTweakSave))
	s.mux.HandleFunc("POST /settings/mail", s.guard("settings.mail", "settings.manage", s.settingsMail))
	s.mux.HandleFunc("POST /settings/mail/test", s.guard("settings.mail.test", "settings.manage", s.settingsMailTest))

	// Hosting accounts: create form shows a stub notice with the visible plan
	// list; store/terminate/confirm are CSRF-checked stubs returning a
	// friendly error (AccountService::create/terminate are large multi-step
	// flows). Bandwidth best-effort calls the agent.
	s.mux.HandleFunc("GET /accounts/new", s.guard("accounts.create", "users.manage", s.accountsCreate))
	s.mux.HandleFunc("POST /accounts", s.guard("accounts.store", "users.manage", s.accountsStore))
	s.mux.HandleFunc("GET /accounts/bandwidth", s.guard("accounts.bandwidth", "users.manage", s.accountsBandwidth))
	s.mux.HandleFunc("POST /accounts/terminate", s.guard("accounts.terminate", "users.manage", s.accountsTerminate))
	s.mux.HandleFunc("POST /accounts/terminate/confirm", s.guard("accounts.terminate.confirm", "users.manage", s.accountsTerminateConfirm))

	// Panel users CRUD (admin + reseller scoping). Users create/edit render
	// forms with reference data (roles/plans) and a stub notice; store/update/
	// destroy/suspend/unsuspend/plan/limits/options/bandwidth-reset are CSRF-
	// checked stubs. resetTwoFactor is fully implemented (disables TOTP and
	// removes WebAuthn credentials + audit).
	s.mux.HandleFunc("GET /users/new", s.guard("users.create", "users.manage", s.usersCreate))
	s.mux.HandleFunc("POST /users", s.guard("users.store", "users.manage", s.usersStore))
	s.mux.HandleFunc("GET /users/{id}/edit", s.guard("users.edit", "users.manage", s.usersEdit))
	s.mux.HandleFunc("POST /users/{id}", s.guard("users.update", "users.manage", s.usersUpdate))
	s.mux.HandleFunc("POST /users/{id}/delete", s.guard("users.delete", "users.manage", s.usersDestroy))
	s.mux.HandleFunc("POST /users/{id}/two-factor/reset", s.guard("users.2fa.reset", "users.manage", s.usersResetTwoFactor))
	s.mux.HandleFunc("POST /users/{id}/suspend", s.guard("users.suspend", "users.manage", s.usersSuspend))
	s.mux.HandleFunc("POST /users/{id}/unsuspend", s.guard("users.unsuspend", "users.manage", s.usersUnsuspend))
	s.mux.HandleFunc("GET /users/{id}/plan", s.guard("users.plan", "users.manage", s.usersPlan))
	s.mux.HandleFunc("POST /users/{id}/plan", s.guard("users.plan.change", "users.manage", s.usersChangePlan))
	s.mux.HandleFunc("POST /users/{id}/limits", s.guard("users.limits", "users.manage", s.usersSaveLimits))
	s.mux.HandleFunc("POST /users/{id}/hosting-options", s.guard("users.options", "users.manage", s.usersSaveOptions))
	s.mux.HandleFunc("POST /users/{id}/bandwidth/reset", s.guard("users.bandwidth.reset", "users.manage", s.usersResetBandwidth))

	// SSL Center: host/cert/key/tls/services/autossl pages pull live data
	// from the agent where possible (ssl.store/ssl.keys/ssl.tls_get/
	// ssl.services/ssl.autossl_status); POST endpoints either call agent
	// actions (enableRenewal, deleteCertificate, deleteKey, saveTls,
	// autosslRun) with audit logging or return friendly stubs for the
	// certificate-issuance / CSR-upload flows that need file handling.
	s.mux.HandleFunc("GET /ssl/hosts", s.guard("ssl.hosts", "ssl.view", s.sslHosts))
	s.mux.HandleFunc("POST /ssl/renewal", s.guard("ssl.renewal.enable", "ssl.manage", s.sslEnableRenewal))
	s.mux.HandleFunc("GET /ssl/certificates", s.guard("ssl.certificates", "ssl.view", s.sslCertificates))
	s.mux.HandleFunc("POST /ssl/certificates/delete", s.guard("ssl.certificates.delete", "ssl.manage", s.sslDeleteCertificate))
	s.mux.HandleFunc("GET /ssl/keys", s.guard("ssl.keys", "ssl.view", s.sslKeys))
	s.mux.HandleFunc("POST /ssl/keys", s.guard("ssl.keys.create", "ssl.manage", s.sslCreateCsr))
	s.mux.HandleFunc("POST /ssl/keys/install", s.guard("ssl.keys.install", "ssl.manage", s.sslInstallKey))
	s.mux.HandleFunc("POST /ssl/keys/delete", s.guard("ssl.keys.delete", "ssl.manage", s.sslDeleteKey))
	s.mux.HandleFunc("GET /ssl/keys/csr", s.guard("ssl.keys.csr", "ssl.view", s.sslCsrDownload))
	s.mux.HandleFunc("GET /ssl/tls", s.guard("ssl.tls", "ssl.view", s.sslTls))
	s.mux.HandleFunc("POST /ssl/tls", s.guard("ssl.tls.save", "server.manage", s.sslSaveTls))
	s.mux.HandleFunc("GET /ssl/services", s.guard("ssl.services", "ssl.view", s.sslServices))
	s.mux.HandleFunc("POST /ssl/services/account", s.guard("ssl.services.account", "server.manage", s.sslAccountRegister))
	s.mux.HandleFunc("POST /ssl/services/cert", s.guard("ssl.services.cert", "server.manage", s.sslServiceCert))
	s.mux.HandleFunc("POST /ssl/services/hostname", s.guard("ssl.services.hostname", "server.manage", s.sslHostnameIssue))
	s.mux.HandleFunc("POST /ssl/services/mail-tls", s.guard("ssl.services.mail_tls", "server.manage", s.sslMailTls))
	s.mux.HandleFunc("GET /ssl/autossl", s.guard("ssl.autossl", "ssl.view", s.sslAutoSsl))
	s.mux.HandleFunc("POST /ssl/autossl/run", s.guard("ssl.autossl.run", "ssl.manage", s.sslAutoSslRun))

	// Managed servers (staff).
	s.mux.HandleFunc("GET /servers", s.guard("", "servers.manage", s.serversIndex))
	s.mux.HandleFunc("POST /servers", s.guard("servers.store", "servers.manage", s.serversStore))
	s.mux.HandleFunc("POST /servers/{id}/check", s.guard("servers.check", "servers.manage", s.serversCheck))
	s.mux.HandleFunc("POST /servers/{id}/delete", s.guard("servers.delete", "servers.manage", s.serversDestroy))
	s.mux.HandleFunc("POST /servers/switch", s.guard("servers.switch", "", s.serversSwitch))
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
	ctxServer
)

func sessionFrom(ctx context.Context) *session.Session {
	s, _ := ctx.Value(ctxSession).(*session.Session)
	return s
}

func userFrom(ctx context.Context) *identity.User {
	u, _ := ctx.Value(ctxUser).(*identity.User)
	return u
}

// cachedAssets lets browsers keep the CSS, scripts and fonts: the embedded
// files carry no modification time, so without these headers every page load
// downloaded all of them again (slow over a distant link). Each file gets an
// ETag from its content (answered 304 when unchanged) and may be reused for an
// hour without asking.
func cachedAssets(next http.Handler) http.Handler {
	var mu sync.Mutex
	tags := map[string]string{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		mu.Lock()
		tag, ok := tags[path]
		mu.Unlock()
		if !ok {
			rec := httptest.NewRecorder()
			next.ServeHTTP(rec, r.Clone(r.Context()))
			if rec.Code == http.StatusOK {
				sum := sha1.Sum(rec.Body.Bytes())
				tag = `"` + hex.EncodeToString(sum[:8]) + `"`
				mu.Lock()
				tags[path] = tag
				mu.Unlock()
			}
		}
		if tag != "" {
			w.Header().Set("ETag", tag)
			w.Header().Set("Cache-Control", "public, max-age=3600")
			if r.Header.Get("If-None-Match") == tag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
