package sites

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/users"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// Register adds the website actions.
func Register(r *actions.Registry, env *host.Env) {
	m := &Manager{Env: env}
	r.Register(actions.Action{Name: "sites.apply", Mutating: true, Description: "Create or update a WhaCP website (Nginx, PHP-FPM pool, directories, system user).", Handler: m.handleApply})
	r.Register(actions.Action{Name: "sites.delete", Mutating: true, Description: "Remove a WhaCP website's configuration; optionally move its files to the trash.", Handler: m.handleDelete})
	r.Register(actions.Action{Name: "sites.inspect", Description: "State of a WhaCP website on disk.", Handler: m.handleInspect})
	r.Register(actions.Action{Name: "sites.list", Description: "WhaCP-managed websites present on this server.", Handler: m.handleList})
}

// Manager implements the site lifecycle.
type Manager struct{ Env *host.Env }

func (m *Manager) layout(s Spec) Layout {
	e := m.Env
	name := "whacp-" + s.Domain + ".conf"
	l := Layout{
		NginxFile: filepath.Join(e.M.NginxAvailable, name),
		NginxLink: filepath.Join(e.M.NginxEnabled, name),
		Socket:    filepath.Join(e.M.PHPSocketDir, "whacp-"+s.Domain+".sock"),
		LogDir:    filepath.Join(s.Root, "logs"),
		TmpDir:    filepath.Join(s.Root, "tmp"),
		ACMERoot:  e.M.ACMERoot,
		WebUser:   e.M.WebUser,
	}
	if s.PHPVersion != "" {
		l.PoolFile = filepath.Join(fmt.Sprintf(e.M.PHPPoolDir, s.PHPVersion), name)
	}
	return l
}

// Result reports what an apply changed.
type Result struct {
	Domain  string   `json:"domain"`
	Changed []string `json:"changed"`
	Backup  string   `json:"backup,omitempty"`
	User    UserInfo `json:"user"`
	Created bool     `json:"user_created"`
	Warning string   `json:"warning,omitempty"`
}

// UserInfo describes the site account.
type UserInfo struct {
	Name string `json:"name"`
	UID  int    `json:"uid"`
	GID  int    `json:"gid"`
}

func (m *Manager) handleApply(ctx context.Context, p json.RawMessage) (any, error) {
	var s Spec
	if err := host.Decode(p, &s); err != nil {
		return nil, err
	}
	return m.Apply(ctx, s)
}

// Apply converges the server to the spec. Running it twice with the same
// spec changes nothing the second time.
func (m *Manager) Apply(ctx context.Context, s Spec) (*Result, error) {
	return m.ApplyIgnoring(ctx, s, nil)
}

// ApplyIgnoring is Apply for importing a website: vhost files listed in
// ignore (the old configuration being replaced) do not count as conflicts.
// It is not reachable through sites.apply.
func (m *Manager) ApplyIgnoring(ctx context.Context, s Spec, ignore []string) (*Result, error) {
	e := m.Env
	if err := s.Normalize(e.Cfg.SitesRoot, e.Cfg.StateDir); err != nil {
		return nil, host.Invalid(err)
	}
	if s.Kind == "php" && !e.Runner.Available(fmt.Sprintf(e.M.PHPFPMBinary, s.PHPVersion)) {
		return nil, actions.Failed(fmt.Sprintf("PHP %s (FPM) is not installed on this server. Install it on the PHP page first.", s.PHPVersion), "")
	}
	if s.HTTPS {
		for _, f := range []string{s.CertFile, s.KeyFile} {
			if !e.Root.Exists(f) {
				return nil, actions.Failed("The certificate files for this website do not exist. Issue the certificate first.", "")
			}
		}
	}
	if err := m.checkConflicts(ctx, s, ignore); err != nil {
		return nil, err
	}
	if err := e.PrecheckNginx(ctx); err != nil {
		return nil, err
	}

	// 1. System user.
	u, created, err := m.ensureUser(ctx, s)
	if err != nil {
		return nil, err
	}
	rollbackUser := func() {
		if created {
			_, _ = e.Runner.Run(context.Background(), "userdel", "--", s.User)
		}
	}
	web, err := e.WebUser()
	if err != nil {
		rollbackUser()
		return nil, actions.Failed("The web server account "+e.M.WebUser+" does not exist.", "")
	}
	l := m.layout(s)
	l.HTTP2Direct = m.nginxAtLeast(ctx, 1, 25, 1)
	l.IPv6 = e.IPv6()

	// 2. Directories (idempotent; permissions are re-applied each time).
	owner := agentfs.Owner{UID: u.UID, GID: u.GID}
	dirs := e.Changes("site-dirs-" + s.Domain)
	dirs.Mkdir(e.Cfg.SitesRoot, 0o755, agentfs.RootOwner)
	dirs.Mkdir(e.M.ACMERoot, 0o755, agentfs.RootOwner)
	// Site root: owner full access, web group may only traverse.
	dirs.Mkdir(s.Root, 0o710, agentfs.Owner{UID: u.UID, GID: web.GID})
	newDocroot := !e.Root.Exists(s.DocumentRoot)
	dirs.Mkdir(s.DocumentRoot, fs.ModeSetgid|0o750, agentfs.Owner{UID: u.UID, GID: web.GID})
	for _, d := range []string{"logs", "tmp", "backups"} {
		dirs.Mkdir(filepath.Join(s.Root, d), 0o750, owner)
	}
	if newDocroot {
		dirs.Write(filepath.Join(s.DocumentRoot, "index.html"), []byte(DefaultIndex(s.Domain)), 0o644, agentfs.Owner{UID: u.UID, GID: web.GID})
	}
	dirsApplied, err := dirs.Apply()
	if err != nil {
		rollbackUser()
		return nil, host.FailWithValidation("Could not prepare the website directories", err)
	}

	// 3. Configuration, validated before anything is reloaded.
	cfg := e.Changes("site-" + s.Domain)
	cfg.Write(filepath.Join(e.M.NginxEnabled, "whacp-00-proxy-map.conf"), []byte(RenderProxyMap()), 0o644, agentfs.RootOwner)
	cfg.Write(l.NginxFile, []byte(RenderNginx(s, l)), 0o644, agentfs.RootOwner)
	if s.Enabled {
		cfg.Link(l.NginxFile, l.NginxLink)
	} else {
		cfg.Remove(l.NginxLink)
	}
	fpmVersions := map[string]bool{}
	for _, old := range m.poolFiles(s.Domain) {
		if old != l.PoolFile {
			cfg.Remove(old)
			fpmVersions[versionOfPool(old)] = true
		}
	}
	if s.PHPVersion != "" {
		if s.Enabled {
			cfg.Write(l.PoolFile, []byte(RenderPool(s, l)), 0o644, agentfs.RootOwner)
		} else {
			cfg.Remove(l.PoolFile) // a disabled site keeps no PHP processes
		}
		fpmVersions[s.PHPVersion] = true
	}
	applied, err := cfg.Commit(func() error {
		if err := e.NginxTest(ctx); err != nil {
			return err
		}
		for v := range fpmVersions {
			if v != "" && e.Runner.Available(fmt.Sprintf(e.M.PHPFPMBinary, v)) {
				if err := e.FPMTest(ctx, v); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		if newDocroot || created {
			_ = dirsApplied.Rollback()
		}
		rollbackUser()
		return nil, host.FailWithValidation("The website configuration was not applied", err)
	}

	res := &Result{Domain: s.Domain, Changed: append(dirsApplied.Changed, applied.Changed...), Backup: applied.Backup, User: UserInfo{u.Name, u.UID, u.GID}, Created: created}
	if len(applied.Changed) == 0 {
		return res, nil
	}
	// 4. Reload: PHP first (so the socket exists), then Nginx. On failure,
	// restore the previous configuration and reload again.
	if err := m.reload(ctx, fpmVersions); err != nil {
		_ = applied.Rollback()
		_ = m.reload(ctx, fpmVersions)
		return nil, host.FailWithValidation("The services did not accept the new configuration", err)
	}
	e.Log.Info("site applied", "domain", s.Domain, "changed", len(res.Changed), "enabled", s.Enabled)
	return res, nil
}

func (m *Manager) reload(ctx context.Context, fpm map[string]bool) error {
	versions := make([]string, 0, len(fpm))
	for v := range fpm {
		if v != "" && m.Env.Runner.Available(fmt.Sprintf(m.Env.M.PHPFPMBinary, v)) {
			versions = append(versions, v)
		}
	}
	sort.Strings(versions)
	for _, v := range versions {
		if err := m.Env.ReloadFPM(ctx, v); err != nil {
			return err
		}
	}
	return m.Env.ReloadNginx(ctx)
}

// checkConflicts refuses hostnames already served by configuration that
// WhaCP does not manage, or by another WhaCP site.
func (m *Manager) checkConflicts(ctx context.Context, s Spec, ignore []string) error {
	ng := m.Env.Discovery.Nginx(ctx)
	mine := "whacp-" + s.Domain + ".conf"
	skip := map[string]bool{}
	for _, f := range ignore {
		skip[f] = true
	}
	want := map[string]bool{}
	for _, n := range s.Names() {
		want[n] = true
	}
	for _, v := range ng.VHosts {
		if filepath.Base(v.File) == mine || skip[v.File] {
			continue
		}
		for _, n := range v.ServerNames {
			if want[strings.ToLower(n)] {
				owner := "configuration not managed by WhaCP"
				if strings.HasPrefix(filepath.Base(v.File), "whacp-") {
					owner = "another WhaCP website"
				}
				state := "enabled"
				if !v.Enabled {
					state = "disabled"
				}
				return actions.Conflict(fmt.Sprintf("%s is already configured in %s (%s, %s). WhaCP will not take it over automatically; remove or rename that configuration first, or use Import.", n, v.File, owner, state))
			}
		}
	}
	return nil
}

var usernameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,30}$`)

// ensureUser returns the site account, creating it when requested.
func (m *Manager) ensureUser(ctx context.Context, s Spec) (users.User, bool, error) {
	e := m.Env
	if u, err := e.LookupUser(s.User); err == nil {
		if u.UID < 1000 || u.UID >= 60000 {
			return u, false, actions.InvalidParams("user: " + s.User + " is a system account; choose a regular account for websites")
		}
		return u, false, nil
	}
	if !s.CreateUser {
		return users.User{}, false, actions.InvalidParams("user: " + s.User + " does not exist; enable 'create user' or choose an existing account")
	}
	if !usernameRe.MatchString(s.User) {
		return users.User{}, false, actions.InvalidParams("user: invalid name")
	}
	out, err := e.Runner.Run(ctx, "useradd", "--home-dir", s.Root, "--no-create-home", "--shell", "/usr/sbin/nologin", "--user-group", "--comment", "WhaCP website "+s.Domain, "--", s.User)
	if err != nil {
		return users.User{}, false, actions.Failed("Could not create the system user "+s.User+".", host.Output(out))
	}
	u, err := e.LookupUser(s.User)
	if err != nil {
		return u, true, actions.Failed("The system user was created but cannot be found.", "")
	}
	return u, true, nil
}

var poolVersionRe = regexp.MustCompile(`/([0-9]+\.[0-9]+)/`)

func versionOfPool(p string) string {
	if mm := poolVersionRe.FindStringSubmatch(p); mm != nil {
		return mm[1]
	}
	return ""
}

// poolFiles lists existing pool files for a domain across PHP versions.
func (m *Manager) poolFiles(domain string) []string {
	matches, _ := m.Env.Root.Glob(filepath.Join(fmt.Sprintf(m.Env.M.PHPPoolDir, "*"), "whacp-"+domain+".conf"))
	return matches
}

func (m *Manager) nginxAtLeast(ctx context.Context, maj, min, patch int) bool {
	out, _ := m.Env.Runner.Run(ctx, "nginx", "-v")
	mm := regexp.MustCompile(`nginx/([0-9]+)\.([0-9]+)\.([0-9]+)`).FindStringSubmatch(string(out))
	if mm == nil {
		return false
	}
	v := [3]int{}
	for i := range v {
		v[i], _ = strconv.Atoi(mm[i+1])
	}
	want := [3]int{maj, min, patch}
	for i := range v {
		if v[i] != want[i] {
			return v[i] > want[i]
		}
	}
	return true
}

// DeleteRequest removes a site.
type DeleteRequest struct {
	Domain      string `json:"domain"`
	User        string `json:"user"`
	RemoveFiles bool   `json:"remove_files"`
	RemoveUser  bool   `json:"remove_user"`
}

func (m *Manager) handleDelete(ctx context.Context, p json.RawMessage) (any, error) {
	var in DeleteRequest
	if err := host.Decode(p, &in); err != nil {
		return nil, err
	}
	if err := validate.Domain("domain", in.Domain); err != nil {
		return nil, host.Invalid(err)
	}
	e := m.Env
	name := "whacp-" + in.Domain + ".conf"
	if err := e.PrecheckNginx(ctx); err != nil {
		return nil, err
	}
	set := e.Changes("site-delete-" + in.Domain)
	set.Remove(filepath.Join(e.M.NginxEnabled, name))
	set.Remove(filepath.Join(e.M.NginxAvailable, name))
	fpm := map[string]bool{}
	for _, pf := range m.poolFiles(in.Domain) {
		set.Remove(pf)
		fpm[versionOfPool(pf)] = true
	}
	applied, err := set.Commit(func() error { return e.NginxTest(ctx) })
	if err != nil {
		return nil, host.FailWithValidation("The website was not removed", err)
	}
	if len(applied.Changed) > 0 {
		if err := m.reload(ctx, fpm); err != nil {
			_ = applied.Rollback()
			_ = m.reload(ctx, fpm)
			return nil, host.FailWithValidation("The website was not removed", err)
		}
	}
	res := map[string]any{"domain": in.Domain, "removed": applied.Changed, "backup": applied.Backup}
	var warnings []string

	root := filepath.Join(e.Cfg.SitesRoot, in.Domain)
	if in.RemoveFiles && e.Root.Exists(root) {
		trash := filepath.Join(e.Cfg.StateDir, "trash")
		if _, err := e.Writer.MkdirAll(trash, 0o700, agentfs.RootOwner); err == nil {
			dest := filepath.Join(trash, in.Domain+"-"+e.Now().Format("20060102T150405Z"))
			if err := e.Writer.Rename(root, dest); err != nil {
				warnings = append(warnings, "The website files were kept in "+root+" because they could not be moved to the trash ("+errText(err)+").")
			} else {
				res["trash"] = dest
			}
		}
	}
	if in.RemoveUser && in.User != "" {
		if err := validate.Username("user", in.User); err == nil {
			if others := m.sitesUsing(in.User); len(others) > 0 {
				warnings = append(warnings, "The system user "+in.User+" was kept because other websites use it: "+strings.Join(others, ", "))
			} else if u, err := e.LookupUser(in.User); err == nil && u.UID >= 1000 && u.UID < 60000 {
				if out, err := e.Runner.Run(ctx, "userdel", "--", in.User); err != nil {
					warnings = append(warnings, "The system user could not be removed: "+host.Output(out))
				}
			}
		}
	}
	res["warnings"] = warnings
	e.Log.Info("site deleted", "domain", in.Domain, "files", in.RemoveFiles, "user", in.RemoveUser)
	return res, nil
}

func errText(err error) string {
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err.Error()
	}
	return err.Error()
}

var poolUserRe = regexp.MustCompile(`(?m)^user = (\S+)$`)

// sitesUsing returns WhaCP sites whose pool runs as user.
func (m *Manager) sitesUsing(user string) []string {
	var out []string
	matches, _ := m.Env.Root.Glob(filepath.Join(fmt.Sprintf(m.Env.M.PHPPoolDir, "*"), "whacp-*.conf"))
	for _, f := range matches {
		b, err := m.Env.Root.ReadFile(f)
		if err != nil {
			continue
		}
		if mm := poolUserRe.FindSubmatch(b); mm != nil && string(mm[1]) == user {
			d := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "whacp-"), ".conf")
			out = append(out, d)
		}
	}
	return out
}

// Inspection is the on-disk state of a WhaCP site.
type Inspection struct {
	Domain        string   `json:"domain"`
	ConfigExists  bool     `json:"config_exists"`
	Enabled       bool     `json:"enabled"`
	PoolVersions  []string `json:"pool_versions"`
	RootExists    bool     `json:"root_exists"`
	DiskBytes     int64    `json:"disk_bytes"`
	DiskComplete  bool     `json:"disk_complete"`
	ConfigPreview string   `json:"config_preview,omitempty"`
}

func (m *Manager) handleInspect(ctx context.Context, p json.RawMessage) (any, error) {
	var in struct {
		Domain string `json:"domain"`
	}
	if err := host.Decode(p, &in); err != nil {
		return nil, err
	}
	if err := validate.Domain("domain", in.Domain); err != nil {
		return nil, host.Invalid(err)
	}
	e := m.Env
	name := "whacp-" + in.Domain + ".conf"
	res := Inspection{Domain: in.Domain, PoolVersions: []string{}}
	if b, err := e.Root.ReadFile(filepath.Join(e.M.NginxAvailable, name)); err == nil {
		res.ConfigExists = true
		res.ConfigPreview = string(b)
	}
	res.Enabled = e.Root.Exists(filepath.Join(e.M.NginxEnabled, name))
	for _, pf := range m.poolFiles(in.Domain) {
		res.PoolVersions = append(res.PoolVersions, versionOfPool(pf))
	}
	root := filepath.Join(e.Cfg.SitesRoot, in.Domain)
	if e.Root.IsDir(root) {
		res.RootExists = true
		res.DiskBytes, res.DiskComplete, _ = e.Root.DirSize(root, 200000)
	}
	return res, nil
}

func (m *Manager) handleList(ctx context.Context, p json.RawMessage) (any, error) {
	e := m.Env
	matches, _ := e.Root.Glob(filepath.Join(e.M.NginxAvailable, "whacp-*.conf"))
	out := []map[string]any{}
	for _, f := range matches {
		d := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "whacp-"), ".conf")
		if validate.Domain("d", d) != nil {
			continue
		}
		out = append(out, map[string]any{"domain": d, "enabled": e.Root.Exists(filepath.Join(e.M.NginxEnabled, filepath.Base(f)))})
	}
	return out, nil
}
