package dbtools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/apps"
	"github.com/NimVor/nimvor/agent/internal/dbmgr"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/logging"
	"github.com/NimVor/nimvor/agent/internal/selinux"
)

var mu sync.Mutex

var listenRe = regexp.MustCompile(`(?m)^\s*listen\s+(?:\[::\]:)?([0-9]+)\s+ssl`)

// panelPort is the panel's public HTTPS port, read from its Nginx site; 0 when it has none.
func (e env) panelPort() int {
	b, err := e.Root.ReadFile(e.siteFile())
	if err != nil {
		return 0
	}
	if m := listenRe.FindSubmatch(b); m != nil {
		var n int
		fmt.Sscanf(string(m[1]), "%d", &n)
		return n
	}
	return 0
}

func (e env) status() map[string]any {
	st := e.load()
	var list []map[string]any
	for _, t := range Tools {
		in, ok := st.Tools[t.ID]
		list = append(list, map[string]any{
			"id": t.ID, "name": t.Name, "description": t.Description, "installed": ok,
			"installed_at": in.InstalledAt, "path": "/dbtools/" + t.ID + "/",
		})
	}
	return map[string]any{"tools": list, "php": st.PHP, "panel_https": e.panelPort() > 0}
}

// snippetFor is the Nginx snippet for the installed tools (sorted), or "" when there are none.
func (e env) installedIDs(st State) []string {
	var ids []string
	for id := range st.Tools {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func Register(r *actions.Registry, he *host.Env) {
	e := env{he}
	registerSignon(r, e)

	r.Register(actions.Action{Name: "dbtools.status", Description: "Which database tools (phpMyAdmin, Adminer) are installed, and whether the panel is served over HTTPS so they can be reached.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := host.Decode(raw, &struct{}{}); err != nil {
			return nil, err
		}
		return e.status(), nil
	}})

	r.Register(actions.Action{Name: "dbtools.install", Mutating: true, Description: "Install phpMyAdmin or Adminer (downloaded from the vendor and checked against its published checksum) into a read-only folder, run it in its own PHP-FPM pool and account, and serve it on the panel's HTTPS address under /dbtools/<tool>/. Validated with nginx -t and php-fpm -t, checked by loading the page, rolled back on failure. Runs as a job.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		in := struct {
			Tool string `json:"tool"`
		}{}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		t, ok := toolByID(in.Tool)
		if !ok {
			return nil, actions.InvalidParams("tool must be phpmyadmin or adminer")
		}
		if e.panelPort() == 0 {
			return nil, actions.Conflict("The panel is not served over HTTPS by NimVor's own Nginx site (install.sh --public-https), so the database tools have no address to be reached on.")
		}
		j := e.Jobs.Start("dbtools.install", "dbtools", func(ctx context.Context, log io.Writer) (any, error) {
			return e.install(ctx, t, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})

	r.Register(actions.Action{Name: "dbtools.remove", Mutating: true, Description: "Remove a database tool. When the last one is removed, its PHP-FPM pool and Nginx snippet go too.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		in := struct {
			Tool string `json:"tool"`
		}{}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		t, ok := toolByID(in.Tool)
		if !ok {
			return nil, actions.InvalidParams("tool must be phpmyadmin or adminer")
		}
		defer e.LockWebConfig()()
		mu.Lock()
		defer mu.Unlock()
		return e.remove(ctx, t)
	}})
}

func (e env) install(ctx context.Context, t Tool, log io.Writer) (any, error) {
	defer e.LockWebConfig()()
	mu.Lock()
	defer mu.Unlock()
	st := e.load()
	v, err := e.phpFor(ctx)
	if err != nil {
		return nil, err
	}
	if err := e.PrecheckNginx(ctx); err != nil {
		return nil, err
	}
	own, gid, err := e.ensureAccount(ctx)
	if err != nil {
		return nil, err
	}
	site, err := e.Root.ReadFile(e.siteFile())
	if err != nil {
		return nil, actions.Conflict("The panel's Nginx site could not be read.")
	}
	newSite, siteChanged, err := withInclude(string(site))
	if err != nil {
		return nil, actions.Conflict(err.Error())
	}

	// Folders: the tools' folder is the web server's to read and nobody's to write; tmp is the pool's.
	for _, d := range []struct {
		path string
		mode os.FileMode
		own  agentfs.Owner
	}{
		{Dir, 0o755, agentfs.RootOwner}, {e.wwwDir(), 0o755, agentfs.RootOwner}, {e.wwwDir() + "/dbtools", 0o755, agentfs.RootOwner},
		{Dir + "/tmp", 0o750, own}, {Dir + "/tmp/sessions", 0o750, own}, {Dir + "/tmp/phpmyadmin", 0o750, own},
	} {
		if _, err := e.Writer.MkdirAll(d.path, d.mode, d.own); err != nil {
			return nil, actions.Failed("A folder for the database tools could not be created: "+d.path, err.Error())
		}
	}
	realTool, err := e.Root.Path(e.toolDir(t.ID))
	if err != nil {
		return nil, err
	}
	realNew := realTool + ".new"
	os.RemoveAll(realNew)
	if err := os.MkdirAll(realNew, 0o755); err != nil {
		return nil, actions.Failed("The folder for "+t.Name+" could not be created.", err.Error())
	}
	cleanup := func() { os.RemoveAll(realNew) }
	if err := apps.Fetch(ctx, e.Env, apps.DefaultSources, t.ID, t.Def, realNew, rootUID(), gid, log); err != nil {
		cleanup()
		return nil, actions.Failed(t.Name+" could not be downloaded.", err.Error())
	}
	if err := openPerms(realNew, gid); err != nil {
		cleanup()
		return nil, actions.Failed(t.Name+"'s files could not be prepared.", err.Error())
	}
	fmt.Fprintf(log, "Downloaded and checked %s.\n", t.Name)

	// Swap the new copy in, keeping the old one until the configuration works.
	realOld := realTool + ".old"
	os.RemoveAll(realOld)
	hadOld := false
	if _, err := os.Lstat(realTool); err == nil {
		if err := os.Rename(realTool, realOld); err != nil {
			cleanup()
			return nil, actions.Failed("The existing "+t.Name+" could not be replaced.", err.Error())
		}
		hadOld = true
	}
	if err := os.Rename(realNew, realTool); err != nil {
		if hadOld {
			os.Rename(realOld, realTool)
		}
		cleanup()
		return nil, actions.Failed(t.Name+" could not be put in place.", err.Error())
	}
	restoreFiles := func() {
		os.RemoveAll(realTool)
		if hadOld {
			os.Rename(realOld, realTool)
		}
	}

	prevPHP := st.PHP
	st.PHP = v
	st.Tools[t.ID] = Installed{InstalledAt: e.Now().UTC().Format(time.RFC3339)}
	ids := e.installedIDs(st)

	set := e.Changes("dbtools")
	e.EnsurePHPSocketDir(set)
	set.Mkdir(snippetDir, 0o755, agentfs.RootOwner)
	set.Write(e.poolFile(v), []byte(e.renderPool()), 0o644, agentfs.RootOwner)
	if prevPHP != "" && prevPHP != v {
		set.Remove(e.poolFile(prevPHP))
	}
	set.Write(e.snippetFile(), []byte(e.renderSnippet(ids)), 0o644, agentfs.RootOwner)
	if siteChanged {
		set.Write(e.siteFile(), []byte(newSite), 0o644, agentfs.RootOwner)
	}
	e.save(set, st)
	applied, err := set.Commit(func() error {
		if err := e.FPMTest(ctx, v); err != nil {
			return err
		}
		return e.NginxTest(ctx)
	})
	if err != nil {
		restoreFiles()
		return nil, host.FailWithValidation("The database tools were not set up", err)
	}
	reload := func() {
		e.ReloadFPM(ctx, v)
		if prevPHP != "" && prevPHP != v {
			e.ReloadFPM(ctx, prevPHP)
		}
		e.ReloadNginx(ctx)
	}
	fail := func(msg string, cause error) (any, error) {
		e.Log.Warn("database tools were not set up", "step", msg, "tool", t.ID, "detail", logging.Redact(cause.Error()))
		applied.Rollback()
		restoreFiles()
		reload()
		return nil, actions.Failed(msg+" The previous configuration was restored.", cause.Error())
	}
	if e.M.PHPSocketTmpfiles != "" {
		selinux.Restore(ctx, e.Env, e.M.PHPSocketDir)
	}
	if err := selinux.Label(ctx, e.Env, e.wwwDir(), "httpd_sys_content_t"); err != nil {
		return fail("The tools' folder could not be labelled for SELinux.", err)
	}
	if err := selinux.Label(ctx, e.Env, Dir+"/tmp", "httpd_sys_rw_content_t"); err != nil {
		return fail("The tools' temporary folder could not be labelled for SELinux.", err)
	}
	if err := selinux.Boolean(ctx, e.Env, "httpd_can_network_connect_db"); err != nil {
		return fail("SELinux does not let the web server reach the database server.", err)
	}
	if err := e.ReloadFPM(ctx, v); err != nil {
		return fail("PHP-FPM did not load the pool.", err)
	}
	if prevPHP != "" && prevPHP != v {
		e.ReloadFPM(ctx, prevPHP)
	}
	if err := e.ReloadNginx(ctx); err != nil {
		return fail("Nginx did not reload.", err)
	}
	if !e.Cfg.SimulateCommands && e.UnitActive(ctx, e.M.NginxUnit) {
		url := fmt.Sprintf("https://127.0.0.1:%d/dbtools/%s/", e.panelPort(), t.ID)
		if err := probe(ctx, url); err != nil {
			return fail(t.Name+"'s page did not load.", err)
		}
	}
	os.RemoveAll(realOld)
	fmt.Fprintf(log, "%s is served at /dbtools/%s/ on the panel's address.\n", t.Name, t.ID)
	return map[string]any{"tool": t.ID, "path": "/dbtools/" + t.ID + "/", "php": v}, nil
}

func (e env) remove(ctx context.Context, t Tool) (any, error) {
	st := e.load()
	if _, ok := st.Tools[t.ID]; !ok {
		return map[string]any{"removed": false}, nil
	}
	delete(st.Tools, t.ID)
	ids := e.installedIDs(st)
	set := e.Changes("dbtools-remove")
	php := st.PHP
	if len(ids) == 0 {
		set.Remove(e.snippetFile())
		if php != "" {
			set.Remove(e.poolFile(php))
		}
		st.PHP = ""
	} else {
		set.Write(e.snippetFile(), []byte(e.renderSnippet(ids)), 0o644, agentfs.RootOwner)
	}
	e.save(set, st)
	applied, err := set.Commit(func() error { return e.NginxTest(ctx) })
	if err != nil {
		return nil, host.FailWithValidation("The database tool was not removed", err)
	}
	if err := e.ReloadNginx(ctx); err != nil {
		applied.Rollback()
		e.ReloadNginx(ctx)
		return nil, actions.Failed("Nginx did not reload; the tool stays installed.", err.Error())
	}
	if php != "" && len(ids) == 0 {
		if err := e.ReloadFPM(ctx, php); err != nil {
			e.Log.Warn("php-fpm reload after removing the database tools pool failed", "err", err)
		}
	}
	if real, err := e.Root.Path(e.toolDir(t.ID)); err == nil {
		os.RemoveAll(real)
	}
	if len(ids) == 0 {
		if _, err := e.sweep(ctx, dbmgr.Client{Env: e.Env}, true); err != nil {
			e.Log.Warn("sign-in accounts could not be locked after removing the last database tool", "err", err)
		}
	}
	return map[string]any{"removed": true}, nil
}

// refreshFiles rewrites the files NimVor itself generates inside an installed tool (Adminer's front page, the sign-in
// pages) when they differ from what this version writes. Files with a secret in them (phpMyAdmin's configuration) and
// empty placeholders are left alone, and so is everything the vendor shipped. It reports whether it changed anything.
func (e env) refreshFiles(st State) (bool, error) {
	changed := false
	for id := range st.Tools {
		t, ok := toolByID(id)
		if !ok {
			continue
		}
		for name, content := range t.Def.Files {
			if content == "" || strings.Contains(content, "{{") || strings.Contains(name, "/") {
				continue
			}
			path := e.toolDir(id) + "/" + name
			have, err := e.Root.ReadFile(path)
			if err != nil || string(have) == content {
				continue // missing (the tool is being installed or removed) or already current
			}
			if _, err := e.Writer.WriteFile(path, []byte(content), 0o644, agentfs.RootOwner); err != nil {
				return changed, err
			}
			changed = true
		}
	}
	return changed, nil
}

// refresh brings what NimVor wrote for the installed tools up to date: their generated files, and the Nginx snippet and
// PHP-FPM pool when they differ from what this version writes (validated with nginx -t and php-fpm -t, rolled back if
// it fails). It reports whether it changed anything. The caller holds the web configuration lock and mu.
func (e env) refresh(ctx context.Context) (bool, error) {
	st := e.load()
	ids := e.installedIDs(st)
	if len(ids) == 0 || st.PHP == "" {
		return false, nil
	}
	filesChanged, ferr := e.refreshFiles(st)
	wantSnippet, wantPool := e.renderSnippet(ids), e.renderPool()
	haveSnippet, _ := e.Root.ReadFile(e.snippetFile())
	havePool, _ := e.Root.ReadFile(e.poolFile(st.PHP))
	if string(haveSnippet) == wantSnippet && string(havePool) == wantPool {
		return filesChanged, ferr
	}
	set := e.Changes("dbtools-refresh")
	set.Write(e.snippetFile(), []byte(wantSnippet), 0o644, agentfs.RootOwner)
	set.Write(e.poolFile(st.PHP), []byte(wantPool), 0o644, agentfs.RootOwner)
	applied, err := set.Commit(func() error {
		if err := e.FPMTest(ctx, st.PHP); err != nil {
			return err
		}
		return e.NginxTest(ctx)
	})
	if err != nil {
		return filesChanged, err
	}
	if err := e.ReloadFPM(ctx, st.PHP); err != nil {
		applied.Rollback()
		e.ReloadFPM(ctx, st.PHP)
		return filesChanged, err
	}
	if err := e.ReloadNginx(ctx); err != nil {
		applied.Rollback()
		e.ReloadFPM(ctx, st.PHP)
		e.ReloadNginx(ctx)
		return filesChanged, err
	}
	return true, nil
}
