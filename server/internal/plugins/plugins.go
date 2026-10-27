// Package plugins discovers, enables and talks to panel plugins.
//
// A plugin is a separate program an administrator installs and runs on the
// server. The panel never executes plugin code: it reads the plugin's
// manifest (plugin.json), enforces sign-in, CSRF and the route's permission,
// and forwards the request over the plugin's Unix socket (see proxy.go).
//
// Layout: <plugins dir>/<name>/plugin.json. Folders and files that are
// group- or world-writable, and symbolic links, make a plugin unusable, so
// nobody but the owner can change an enabled plugin's manifest.
package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// EnabledKey is the setting holding the enabled plugin names (JSON list).
const EnabledKey = "_plugins_enabled"

var (
	nameRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)
	permRe   = regexp.MustCompile(`^[a-z0-9.-]{1,80}$`)
	pathRe   = regexp.MustCompile(`^/[a-z0-9/{}_-]*$`)
	iconRe   = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	socketRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,200}$`)
)

// Events a plugin can subscribe to.
var Events = []string{"website.created", "website.updated", "website.deleting", "website.deleted"}

// Route is a page or action the plugin serves under /plugins/<name>.
type Route struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	Permission string `json:"permission"`

	re *regexp.Regexp
}

// NavEntry is a sidebar entry.
type NavEntry struct {
	Label      string `json:"label"`
	Icon       string `json:"icon"`
	Path       string `json:"path"`
	Permission string `json:"permission"`
}

// Manifest is plugin.json.
type Manifest struct {
	Name        string            `json:"name"`
	Title       string            `json:"title"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Author      string            `json:"author"`
	License     string            `json:"license"`
	Permissions map[string]string `json:"permissions"`
	Socket      string            `json:"socket"`
	Routes      []Route           `json:"routes"`
	Nav         []NavEntry        `json:"nav"`
	Events      []string          `json:"events"`
}

// Plugin is a plugin folder with what was found wrong with it.
type Plugin struct {
	Name     string
	Path     string
	Manifest Manifest
	Problems []string
	Enabled  bool
}

// Settings is the part of the settings store the manager uses.
type Settings interface {
	String(ctx context.Context, key, def string) (string, error)
	Set(ctx context.Context, key string, value any, actor int64) error
}

// Store is the part of the database the manager uses.
type Store interface {
	// PermissionExists reports whether a permission is in the catalogue.
	PermissionExists(ctx context.Context, name string) bool
	// SyncPermissions registers the plugin's permissions and grants them to
	// administrators.
	SyncPermissions(ctx context.Context, plugin string, perms map[string]string) error
	// DropPermissions removes the plugin's permissions.
	DropPermissions(ctx context.Context, plugin string) error
}

// Manager reads the plugins directory.
type Manager struct {
	dir      string
	settings Settings
	store    Store
}

// NewManager returns a manager of the plugins in dir.
func NewManager(dir string, settings Settings, store Store) *Manager {
	return &Manager{dir: dir, settings: settings, store: store}
}

// Dir is the plugins directory.
func (m *Manager) Dir() string { return m.dir }

// Discover lists every plugin folder (one holding a plugin.json), by name.
func (m *Manager) Discover(ctx context.Context) []*Plugin {
	enabled := map[string]bool{}
	for _, n := range m.EnabledNames(ctx) {
		enabled[n] = true
	}
	files, _ := filepath.Glob(filepath.Join(m.dir, "*", "plugin.json"))
	var out []*Plugin
	for _, f := range files {
		path := filepath.Dir(f)
		name := filepath.Base(path)
		p := &Plugin{Name: name, Path: path, Enabled: enabled[name]}
		p.Manifest, p.Problems = m.Inspect(ctx, name, path)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Inspect validates one plugin folder.
func (m *Manager) Inspect(ctx context.Context, name, path string) (Manifest, []string) {
	var man Manifest
	if !nameRe.MatchString(name) {
		return man, []string{"The folder name must be 2–40 lowercase letters, digits or dashes."}
	}
	raw, err := os.ReadFile(filepath.Join(path, "plugin.json"))
	if err != nil || json.Unmarshal(raw, &man) != nil {
		return Manifest{}, []string{"plugin.json is not valid JSON."}
	}
	var problems []string
	for k, v := range map[string]string{"name": man.Name, "title": man.Title, "version": man.Version} {
		if v == "" {
			problems = append(problems, fmt.Sprintf(`plugin.json must set "%s".`, k))
		}
	}
	if man.Name != name {
		problems = append(problems, `plugin.json "name" must match the folder name.`)
	}
	perms := make([]string, 0, len(man.Permissions))
	for p := range man.Permissions {
		perms = append(perms, p)
	}
	sort.Strings(perms)
	for _, p := range perms {
		if !strings.HasPrefix(p, "plugin."+name+".") || !permRe.MatchString(p) {
			problems = append(problems, fmt.Sprintf(`Permission "%s" must be named plugin.%s.<something>.`, p, name))
		}
	}
	allowed := func(p string) bool {
		if _, ok := man.Permissions[p]; ok {
			return true
		}
		return m.store != nil && m.store.PermissionExists(ctx, p)
	}
	if !socketRe.MatchString(man.Socket) || strings.Contains(man.Socket, "..") {
		problems = append(problems, `"socket" must be the absolute path of the plugin's Unix socket.`)
	}
	if len(man.Routes) == 0 && len(man.Events) == 0 {
		problems = append(problems, "The plugin declares no routes and no events.")
	}
	for i := range man.Routes {
		r := &man.Routes[i]
		r.Method = strings.ToUpper(r.Method)
		switch {
		case r.Method != "GET" && r.Method != "POST":
			problems = append(problems, "Plugin routes support GET and POST.")
		case !pathRe.MatchString(r.Path) || strings.Contains(r.Path, ".."):
			problems = append(problems, fmt.Sprintf("Invalid route path %s.", r.Path))
		case !allowed(r.Permission):
			problems = append(problems, fmt.Sprintf("Unknown permission %s on route %s; declare it in plugin.json.", r.Permission, r.Path))
		default:
			r.re = routeRegexp(r.Path)
		}
	}
	for _, n := range man.Nav {
		if !allowed(n.Permission) || !iconRe.MatchString(n.Icon) || !pathRe.MatchString(n.Path) || n.Label == "" {
			problems = append(problems, "Invalid navigation entry.")
			break
		}
	}
	for _, e := range man.Events {
		known := false
		for _, k := range Events {
			known = known || k == e
		}
		if !known {
			problems = append(problems, fmt.Sprintf("Unknown event %s.", e))
		}
	}
	problems = append(problems, writableProblems(path)...)
	return man, dedupe(problems)
}

// writableProblems reports symbolic links and group/world-writable files.
func writableProblems(root string) []string {
	var out []string
	_ = filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return nil
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			out = append(out, "Symbolic links are not allowed: "+rel)
		case fi.Mode().Perm()&0o022 != 0:
			if rel == "" {
				rel = "(plugin folder)"
			}
			out = append(out, "Writable by group or others: "+rel)
		}
		return nil
	})
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func routeRegexp(path string) *regexp.Regexp {
	path = strings.TrimRight(path, "/")
	var b strings.Builder
	b.WriteString("^")
	for _, seg := range strings.Split(path, "/")[1:] {
		b.WriteString("/")
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			b.WriteString("[^/]+")
		} else {
			b.WriteString(regexp.QuoteMeta(seg))
		}
	}
	b.WriteString("/?$")
	return regexp.MustCompile(b.String())
}

// EnabledNames are the names of the enabled plugins.
func (m *Manager) EnabledNames(ctx context.Context) []string {
	raw, err := m.settings.String(ctx, EnabledKey, "")
	if err != nil {
		return nil
	}
	var names []string
	_ = json.Unmarshal([]byte(raw), &names)
	return names
}

// SetEnabled enables or disables a plugin. Enabling needs a plugin without
// problems; its permissions are registered and granted to administrators.
func (m *Manager) SetEnabled(ctx context.Context, name string, enable bool, actor int64) error {
	var p *Plugin
	for _, c := range m.Discover(ctx) {
		if c.Name == name {
			p = c
		}
	}
	if p == nil {
		return fmt.Errorf("Unknown plugin.")
	}
	if enable && len(p.Problems) > 0 {
		return fmt.Errorf("The plugin cannot be enabled: %s", strings.Join(p.Problems, " "))
	}
	var names []string
	for _, n := range m.EnabledNames(ctx) {
		if n != name {
			names = append(names, n)
		}
	}
	if enable {
		names = append(names, name)
		if err := m.store.SyncPermissions(ctx, name, p.Manifest.Permissions); err != nil {
			return err
		}
	} else if err := m.store.DropPermissions(ctx, name); err != nil {
		return err
	}
	sort.Strings(names)
	b, _ := json.Marshal(names)
	return m.settings.Set(ctx, EnabledKey, string(b), actor)
}

// Active are the enabled plugins that are usable.
func (m *Manager) Active(ctx context.Context) []*Plugin {
	var out []*Plugin
	for _, p := range m.Discover(ctx) {
		if p.Enabled && len(p.Problems) == 0 {
			out = append(out, p)
		}
	}
	return out
}

// Match finds the enabled plugin route serving method and the path after
// /plugins/<name>.
func (m *Manager) Match(ctx context.Context, name, method, path string) (*Plugin, *Route) {
	for _, p := range m.Active(ctx) {
		if p.Name != name {
			continue
		}
		for i := range p.Manifest.Routes {
			r := &p.Manifest.Routes[i]
			if r.Method == method && r.re != nil && r.re.MatchString(path) {
				return p, r
			}
		}
	}
	return nil, nil
}
