package http

// Plugins: separate programs on the server that add pages, sidebar entries
// and event listeners. The panel reads their manifest, enforces sign-in,
// CSRF and the route's permission and forwards the request over the plugin's
// Unix socket; it never runs plugin code (internal/plugins).

import (
	"context"
	"html/template"
	"io"
	"net/http"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/plugins"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// pluginStore keeps plugin permissions in the roles tables.
type pluginStore struct{ s *Server }

func (p pluginStore) PermissionExists(ctx context.Context, name string) bool {
	var n int
	_ = p.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM permissions WHERE name = ?`, name).Scan(&n)
	return n > 0
}

func (p pluginStore) SyncPermissions(ctx context.Context, plugin string, perms map[string]string) error {
	for name, label := range perms {
		if len([]rune(label)) > 120 {
			label = string([]rune(label)[:120])
		}
		if _, err := p.s.db.ExecContext(ctx,
			`INSERT INTO permissions (name, label, grp) VALUES (?, ?, ?) ON CONFLICT(name) DO UPDATE SET label = excluded.label`,
			name, label, "Plugin: "+plugin); err != nil {
			return err
		}
		if _, err := p.s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO role_permissions (role_id, permission_id) SELECT r.id, p.id FROM roles r, permissions p WHERE r.name = 'admin' AND p.name = ?`,
			name); err != nil {
			return err
		}
	}
	return nil
}

func (p pluginStore) DropPermissions(ctx context.Context, plugin string) error {
	like := `plugin.` + plugin + `.%`
	if _, err := p.s.db.ExecContext(ctx, `DELETE FROM role_permissions WHERE permission_id IN (SELECT id FROM permissions WHERE name LIKE ?)`, like); err != nil {
		return err
	}
	_, err := p.s.db.ExecContext(ctx, `DELETE FROM permissions WHERE name LIKE ?`, like)
	return err
}

// pluginManager is the plugin manager of this panel.
func (s *Server) pluginManager() *plugins.Manager {
	return plugins.NewManager(s.cfg.PluginsDir, s.settings, pluginStore{s})
}

// pluginsIndex GET /admin/plugins — the installed plugins.
func (s *Server) pluginsIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.renderError(w, http.StatusForbidden, "Access denied", "Plugins are managed by server administrators.")
		return
	}
	sess := sessionFrom(r.Context())
	s.render(w, "plugins", map[string]any{
		"CSRF":    session.CSRFToken(sess),
		"User":    user,
		"Title":   "Plugins",
		"Plugins": s.pluginManager().Discover(r.Context()),
		"Dir":     s.cfg.PluginsDir,
		"Success": pullFlashKey(sess, "success"),
		"Error":   pullFlashKey(sess, "error"),
	})
}

// pluginsToggle POST /admin/plugins/{name}/toggle — enable or disable.
func (s *Server) pluginsToggle(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		http.Redirect(w, r, "/admin/plugins", http.StatusSeeOther)
		return
	}
	name := r.PathValue("name")
	enable := r.PostFormValue("enabled") == "1"
	if err := s.pluginManager().SetEnabled(r.Context(), name, enable, user.ID); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/admin/plugins", http.StatusSeeOther)
		return
	}
	action := "plugin.disable"
	msg := "Plugin " + name + " disabled."
	if enable {
		action = "plugin.enable"
		msg = "Plugin " + name + " enabled. Its pages appear in the sidebar for users with its permissions."
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{Action: action, TargetType: "plugin", TargetID: name, Result: audit.Success,
		UserID: &uid, Username: user.Username, IP: clientIP(r), UserAgent: r.UserAgent()})
	sess.Flash("success", msg)
	http.Redirect(w, r, "/admin/plugins", http.StatusSeeOther)
}

// pluginDispatch serves /plugins/{name}/... for the enabled plugins: the
// route must be declared in the manifest, the user must hold its permission,
// and a POST needs the CSRF token. The plugin's page is shown in the layout.
func (s *Server) pluginDispatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess := sessionFrom(ctx)
	user := userFrom(ctx)
	rest := "/" + strings.Trim(r.PathValue("path"), "/")
	p, route := s.pluginManager().Match(ctx, r.PathValue("name"), r.Method, rest)
	if route == nil {
		s.renderError(w, http.StatusNotFound, "Not found", "There is no such page.")
		return
	}
	if ok, err := s.authorizer().Can(ctx, user, route.Permission); err != nil || !ok {
		s.deny(w, r, "You do not have permission to access this page.")
		return
	}
	var body []byte
	ctype := r.Header.Get("Content-Type")
	if r.Method == http.MethodPost {
		token := r.Header.Get("X-CSRF-Token")
		if strings.HasPrefix(ctype, "application/x-www-form-urlencoded") {
			// The form is already read (switch handling); forward it as sent.
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			if err := r.ParseForm(); err != nil {
				http.Error(w, "The request is too large.", http.StatusRequestEntityTooLarge)
				return
			}
			if token == "" {
				token = r.PostForm.Get("_token")
			}
			body = []byte(r.PostForm.Encode())
		} else {
			var err error
			if body, err = readLimited(w, r, 1<<20); err != nil {
				http.Error(w, "The request is too large.", http.StatusRequestEntityTooLarge)
				return
			}
		}
		if !session.ValidateCSRF(sess, token) {
			http.Error(w, "Page expired", http.StatusBadRequest)
			return
		}
	}
	res, err := plugins.Forward(ctx, p, plugins.Request{
		Method: r.Method, Path: rest, Query: r.URL.RawQuery, Body: body, Type: ctype,
		UserID: itoa(user.ID), User: user.Username, Role: user.RoleName, CSRF: session.CSRFToken(sess),
		Server: itoa(s.currentServerID(r)),
	})
	if err != nil {
		s.log.Warn("plugin did not answer", "plugin", p.Name, "error", err)
		s.renderError(w, http.StatusBadGateway, "Plugin unavailable", "The plugin "+p.Manifest.Title+" is not running or did not answer. Ask your server administrator.")
		return
	}
	switch {
	case res.Status >= 300 && res.Status < 400:
		if !plugins.SafeRedirect(res.Location) {
			s.renderError(w, http.StatusBadGateway, "Plugin error", "The plugin sent an invalid redirect.")
			return
		}
		http.Redirect(w, r, res.Location, http.StatusSeeOther)
	case res.Type == "text/html":
		title := res.Title
		if title == "" {
			title = p.Manifest.Title
		}
		w.WriteHeader(res.Status)
		s.renderPluginPage(w, user, title, session.CSRFToken(sess), res.Body)
	case res.Type == "application/json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(res.Status)
		_, _ = w.Write(res.Body)
	default:
		s.renderError(w, http.StatusBadGateway, "Plugin error", "The plugin sent an answer the panel cannot show.")
	}
}

func (s *Server) renderPluginPage(w http.ResponseWriter, user any, title, csrf string, body []byte) {
	// The plugin's HTML is trusted like the plugin itself (it is installed by
	// the server administrator); the CSP still blocks inline scripts.
	s.render(w, "plugin_page", map[string]any{"User": user, "Title": title, "CSRF": csrf, "Body": template.HTML(body)})
}

func readLimited(w http.ResponseWriter, r *http.Request, max int64) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(w, r.Body, max))
}

// pluginEvents delivers a website event to the subscribed plugins.
func (s *Server) pluginEvents(event string, payload map[string]any) {
	m := s.pluginManager()
	go func() {
		for name, err := range m.Emit(context.Background(), event, payload) {
			s.log.Warn("plugin event not delivered", "plugin", name, "event", event, "error", err)
		}
	}()
}
