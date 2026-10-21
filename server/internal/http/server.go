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
	s.mux.HandleFunc("POST /sites/{id}/subdomains", s.guard("sites.subdomains.store", "websites.manage", s.siteSubdomainsStore))
	s.mux.HandleFunc("GET /sites/{id}/cron", s.guard("", "cron.view", s.siteCron))
	s.mux.HandleFunc("GET /sites/{id}/workers", s.guard("", "workers.manage", s.siteWorkers))
	// Subdomains: gated by the subdomains plan feature (route name) AND the
	// websites.manage permission.
	s.mux.HandleFunc("GET /sites/{id}/subdomains", s.guard("sites.subdomains", "websites.manage", s.siteSubdomains))

	// Databases list + create. The mysql feature withholds databases.view when
	// a plan disables it, so the permission guard enforces the feature too.
	s.mux.HandleFunc("GET /databases", s.guard("", "databases.view", s.databasesIndex))
	s.mux.HandleFunc("POST /databases", s.guard("", "databases.manage", s.databasesStore))
	s.mux.HandleFunc("POST /databases/{id}/drop", s.guard("", "databases.manage", s.databasesDrop))

	// DNS zones list. dns.manage is withheld by the dns plan feature when off.
	s.mux.HandleFunc("GET /dns", s.guard("", "dns.manage", s.dnsIndex))

	// Email domains list. mail.manage is withheld by the email plan feature.
	s.mux.HandleFunc("GET /mail", s.guard("", "mail.manage", s.mailIndex))

	// Audit log viewer (staff only; no tenant scope).
	s.mux.HandleFunc("GET /audit", s.guard("", "audit.view", s.auditIndex))

	// Panel users list — scoped by manageableBy (staff: all; reseller: own
	// customers).
	s.mux.HandleFunc("GET /users", s.guard("", "users.view", s.usersIndex))

	// Roles (staff) and hosting plans (staff / reseller-own).
	s.mux.HandleFunc("GET /roles", s.guard("", "users.view", s.rolesIndex))
	s.mux.HandleFunc("GET /plans", s.guard("", "plans.manage", s.plansIndex))

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
