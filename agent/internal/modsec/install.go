package modsec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/capability"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
	"github.com/NimVor/nimvor/agent/internal/txn"
)

// Installing the ModSecurity connector (libmodsecurity 3 and the official
// ModSecurity-nginx module) as one transaction (see package txn):
//
//  1. detect the OS, the installed Nginx (version, --with-compat, modules
//     folder) and an existing connector; a working one is left alone
//     (unless an upgrade is asked for);
//  2. snapshot /etc/nginx, the ModSecurity and CRS configuration, the
//     linker configuration and NimVor's state before any change;
//  3. prefer the distribution's package when installing it would not
//     touch Nginx itself (checked with a dry run); otherwise build the
//     connector from source for the exact installed Nginx version, from
//     that version's verified source (the distribution's signed source
//     package, or nginx.org with a checksum the administrator gives),
//     with pinned, checksum-verified ModSecurity releases, as an
//     unprivileged build account;
//  4. install new files under /usr/local/lib/nimvor (versioned: nothing
//     that works is replaced), check the module's libraries and that
//     Nginx loads it with a throwaway configuration, switch the load_module
//     line atomically, run nginx -t, reload, and check again;
//  5. on any failure roll everything back (files, packages it installed,
//     the build account) and reload the previous configuration.
//
// The Nginx binary is never installed, replaced or upgraded here.

// Pinned releases (SHA-256 as published with each release).
const (
	ModSecurityVersion = "3.0.14"
	modsecuritySHA256  = "f7599057b35e67ab61764265daddf9ab03c35cee1e55527547afb073ce8f04e8"
	ConnectorVersion   = "1.0.4"
	connectorSHA256    = "6bdc7570911be884c1e43aaf85046137f9fde0cfa0dd4a55b853c81c45a13313"
)

// Where things go.
const (
	TxKind       = "modsec"
	BuildAccount = "nimvor-build"
	BuildHome    = "/var/lib/nimvor-build"
	LibRoot      = "/usr/local/lib/nimvor/modsecurity"
	ModuleDir    = "/usr/local/lib/nimvor/nginx-modules"
	loadName     = "nimvor-modsecurity.conf"
	maxDownload  = 64 << 20
	snapshotMax  = 256 << 20
)

// Sources are where releases are downloaded from and their checksums
// (tests replace them).
type Sources struct {
	ModSecurity       string
	ModSecuritySHA256 string
	Connector         string
	ConnectorSHA256   string
	NginxOrg          string // fmt pattern with the version
	Client            *http.Client
}

// DefaultSources are the official releases.
var DefaultSources = Sources{
	ModSecurity:       "https://github.com/owasp-modsecurity/ModSecurity/releases/download/v" + ModSecurityVersion + "/modsecurity-v" + ModSecurityVersion + ".tar.gz",
	ModSecuritySHA256: modsecuritySHA256,
	Connector:         "https://github.com/owasp-modsecurity/ModSecurity-nginx/releases/download/v" + ConnectorVersion + "/ModSecurity-nginx-v" + ConnectorVersion + ".tar.gz",
	ConnectorSHA256:   connectorSHA256,
	NginxOrg:          "https://nginx.org/download/nginx-%s.tar.gz",
	Client:            &http.Client{Timeout: 10 * time.Minute},
}

// InstallOptions choose how.
type InstallOptions struct {
	// Upgrade replaces a working connector built by NimVor (the working one
	// is the rollback target).
	Upgrade bool `json:"upgrade"`
	// Method: "auto" (package when compatible, else source) or "source".
	Method string `json:"method"`
	// NginxSourceSHA256: for an Nginx that no package installed, the
	// SHA-256 of nginx-<version>.tar.gz from nginx.org.
	NginxSourceSHA256 string `json:"nginx_source_sha256"`
	// RetentionDays keeps finished transactions (the last good one always).
	RetentionDays int `json:"retention_days"`
}

// SourceInfo records what was built from.
type SourceInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Origin  string `json:"origin"`
	SHA256  string `json:"sha256,omitempty"`
}

// InstallMeta is what an installation records (in its transaction).
type InstallMeta struct {
	OS          string            `json:"os"`
	Nginx       string            `json:"nginx_version"`
	NginxBinary string            `json:"nginx_package,omitempty"`
	Configure   string            `json:"nginx_configure"`
	ModulesPath string            `json:"modules_path"`
	Before      capability.Status `json:"before"`
	After       capability.Status `json:"after"`
	Method      string            `json:"method"`
	Packages    map[string]string `json:"packages_before,omitempty"`
	Sources     []SourceInfo      `json:"sources,omitempty"`
	Module      string            `json:"module,omitempty"`
	LoadFile    string            `json:"load_file,omitempty"`
	LibDir      string            `json:"lib_dir,omitempty"`
}

var installMu sync.Mutex

// installerPaths are what the installer may write: its own versioned files,
// the build account's folder, the load_module file, and nginx.conf only to
// add an include when it has no module folder.
var installerPaths = []string{
	"/usr/local/lib/nimvor/modsecurity", LibRoot + "/**", ModuleDir, ModuleDir + "/**", BuildHome, BuildHome + "/**",
	"/etc/nginx/nginx.conf", "/etc/nginx/modules-enabled/50-" + loadName, "/usr/share/nginx/modules/" + loadName,
	"/etc/nginx/conf.modules.d/" + loadName, "/etc/nginx/nimvor-modules", "/etc/nginx/nimvor-modules/" + loadName,
	CRSRoot, CRSRoot + "/**",
}

var policyMu sync.Mutex

// allowInstallerPaths adds installerPaths to the writer's policy (once).
func allowInstallerPaths(e *host.Env) {
	policyMu.Lock()
	defer policyMu.Unlock()
	for _, p := range e.Writer.Policy.Patterns {
		if p == installerPaths[0] {
			return
		}
	}
	e.Writer.Policy.Patterns = append(e.Writer.Policy.Patterns, installerPaths...)
}

// installer holds one run's state.
type installer struct {
	e    *host.Env
	src  Sources
	opt  InstallOptions
	log  io.Writer
	tx   *txn.Tx
	meta InstallMeta
	n    capability.Nginx
	// createdAccount: the build account was made by this run.
	createdAccount bool
	build          string // build folder (host path)
}

func (in *installer) logf(format string, a ...any) {
	if in.tx != nil {
		in.tx.Logf(format, a...)
		return
	}
	fmt.Fprintf(in.log, format+"\n", a...)
	in.e.Log.Info("modsec install: " + fmt.Sprintf(format, a...))
}

// Hooks undo packages and the build account, then reload the restored
// configuration (checked first).
func Hooks(e *host.Env, log io.Writer) txn.Hooks {
	return txn.Hooks{
		RemovePackages: func(ctx context.Context, names []string) error {
			return pkgs.RemoveNative(ctx, e, log, names)
		},
		RemoveAccount: func(ctx context.Context, name string) error {
			if name != BuildAccount {
				return fmt.Errorf("not NimVor's build account: %s", name)
			}
			if out, err := e.Runner.Run(ctx, "userdel", "--remove", name); err != nil {
				return errors.New(host.Output(out))
			}
			return nil
		},
		AfterRestore: func(ctx context.Context) error {
			if err := e.NginxTest(ctx); err != nil {
				return fmt.Errorf("the restored Nginx configuration does not pass nginx -t: %v (the snapshot in the transaction folder holds the previous /etc/nginx)", err)
			}
			if err := e.ReloadNginx(ctx); err != nil {
				// A failed reload leaves the running Nginx on the
				// configuration it had: the one now restored on disk.
				fmt.Fprintf(log, "Warning: the reload after restoring failed (%v); the running Nginx keeps its previous configuration, which the restored files match.\n", err)
				e.Log.Warn("modsec rollback: reload failed", "error", err.Error())
			}
			return nil
		},
	}
}

// RecoverInstalls rolls back installations interrupted by an agent stop.
func RecoverInstalls(ctx context.Context, e *host.Env, log io.Writer) []string {
	installMu.Lock()
	defer installMu.Unlock()
	allowInstallerPaths(e)
	return txn.Recover(ctx, e, TxKind, Hooks(e, log), log)
}

// Install installs (or upgrades) the connector; see the package comment.
func Install(ctx context.Context, e *host.Env, src Sources, opt InstallOptions, log io.Writer) (map[string]any, error) {
	installMu.Lock()
	defer installMu.Unlock()
	if opt.Method == "" {
		opt.Method = "auto"
	}
	if opt.Method != "auto" && opt.Method != "source" {
		return nil, actions.InvalidParams("method: auto or source")
	}
	if opt.RetentionDays <= 0 {
		opt.RetentionDays = 30
	}
	allowInstallerPaths(e)
	in := &installer{e: e, src: src, opt: opt, log: log}
	for _, line := range txn.Recover(ctx, e, TxKind, Hooks(e, log), log) {
		in.logf("%s", line)
	}
	n, ok := capability.Inspect(ctx, e)
	if !ok {
		return nil, actions.Conflict("Nginx is not installed.")
	}
	in.n = n
	before := capability.DetectModule(ctx, e, n, capability.NewLinker(e), capability.ModSecurity, true)
	in.logf("Nginx %s (modules: %s, --with-compat: %v); ModSecurity connector: %s %s", n.Version, orNone(n.ModulesPath), n.Compat, before.State, before.Reason)
	if before.State == capability.Functional && !opt.Upgrade {
		in.logf("The ModSecurity connector already works; nothing was changed.")
		return map[string]any{"changed": false, "state": before.State, "connector": before}, nil
	}
	if before.State == capability.Functional && before.Package != "" {
		return nil, actions.Conflict("The working connector comes from the package " + before.Package + "; it is updated with the package manager (Server → Packages), not here.")
	}
	if !n.ConfigOK {
		return nil, actions.Conflict("The current Nginx configuration does not pass nginx -t; fix it first. Nothing was changed: " + n.ConfigErr)
	}
	in.meta = InstallMeta{OS: e.Platform.Info().ID + " " + e.Platform.Info().VersionID, Nginx: n.Version, Configure: n.Configure, ModulesPath: n.ModulesPath, Before: before}
	in.meta.NginxBinary = capability.Owner(ctx, e, nginxBinary(ctx, e))

	res, err := in.run(ctx, before)
	if err != nil {
		return nil, err
	}
	eff := effective(e)
	if deleted := txn.Prune(e, TxKind, time.Duration(opt.RetentionDays)*24*time.Hour, func(t *txn.Tx) bool { return t.ID == eff }); len(deleted) > 0 {
		in.logf("Removed %d old installation record(s) (older than %d days; the last good one is kept).", len(deleted), opt.RetentionDays)
	}
	return res, nil
}

func nginxBinary(ctx context.Context, e *host.Env) string {
	for _, p := range []string{"/usr/sbin/nginx", "/usr/bin/nginx", "/usr/local/sbin/nginx", "/usr/local/nginx/sbin/nginx"} {
		if e.Root.Exists(p) {
			return p
		}
	}
	return ""
}

// run tries the package (when allowed and harmless), then the source.
func (in *installer) run(ctx context.Context, before capability.Status) (map[string]any, error) {
	// Present but not loaded, and it loads: only the load_module line.
	if before.State == capability.Installed && strings.Contains(before.Reason, "loads correctly") && !in.opt.Upgrade {
		return in.transaction(ctx, "enable", func(ctx context.Context) error {
			return in.enable(ctx, before.Modules[0].Path)
		})
	}
	if in.opt.Method == "auto" {
		if name, ok := in.e.Platform.PackageName("libnginx-mod-http-modsecurity"); ok && pkgs.Available(in.e) && before.State == capability.Missing {
			if why := in.packageSafe(ctx, name); why != "" {
				in.logf("The package %s is not used: %s", name, why)
			} else {
				res, err := in.transaction(ctx, "package", func(ctx context.Context) error { return in.viaPackage(ctx, name) })
				if err == nil {
					return res, nil
				}
				in.logf("The package route failed and was rolled back (%v); building from source instead.", err)
			}
		}
	}
	return in.transaction(ctx, "source", in.fromSource)
}

// transaction wraps a method: begin, snapshot, do, validate, commit — or
// roll back.
func (in *installer) transaction(ctx context.Context, method string, do func(context.Context) error) (map[string]any, error) {
	tx, err := txn.Begin(in.e, TxKind, in.log)
	if err != nil {
		return nil, fmt.Errorf("the installation record could not be written; nothing was changed: %v", err)
	}
	in.tx = tx
	in.meta.Method = in.method(method)
	in.meta.Packages = pkgs.InstalledNative(ctx, in.e, relevantPackages(in.e))
	tx.SetMeta(in.meta)
	confDir := filepath.Dir(in.mainConf())
	paths := []string{confDir, "/etc/modsecurity", "/etc/ld.so.conf", "/etc/ld.so.conf.d", statePath(in.e), "/usr/share/nginx/modules"}
	if crs := FindCRS(in.e); crs.Available {
		paths = append(paths, filepath.Dir(crs.Setup))
	}
	if err := tx.Snapshot(snapshotMax, uniq(paths)...); err != nil {
		tx.Rollback(ctx, Hooks(in.e, in.log), err)
		return nil, fmt.Errorf("the backup before installing failed; nothing was changed: %v", err)
	}
	err = do(ctx)
	if err == nil {
		err = in.validate(ctx)
	}
	if err != nil {
		if rerr := tx.Rollback(ctx, Hooks(in.e, in.log), err); rerr != nil {
			in.cleanupBuild(ctx)
			return nil, fmt.Errorf("%v — and the rollback was incomplete: %v. Transaction %s holds the backup (snapshot folder) for manual recovery", err, rerr, tx.ID)
		}
		in.cleanupBuild(ctx)
		return nil, fmt.Errorf("%v — everything was rolled back (transaction %s)", err, tx.ID)
	}
	in.tx.SetMeta(in.meta)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	in.cleanupBuild(ctx)
	return map[string]any{"changed": true, "transaction": tx.ID, "method": in.meta.Method, "connector": in.meta.After, "module": in.meta.Module}, nil
}

func (in *installer) method(m string) string {
	if in.opt.Upgrade {
		return m + " (upgrade)"
	}
	return m
}

// validate: nginx -t, reload, and the connector must now be functional.
func (in *installer) validate(ctx context.Context) error {
	if err := in.e.NginxTest(ctx); err != nil {
		return fmt.Errorf("nginx -t failed after the change: %v", err)
	}
	if err := in.e.ReloadNginx(ctx); err != nil {
		return fmt.Errorf("Nginx could not be reloaded: %v", err)
	}
	n, _ := capability.Inspect(ctx, in.e)
	after := capability.DetectModule(ctx, in.e, n, capability.NewLinker(in.e), capability.ModSecurity, false)
	in.meta.After = after
	if after.State != capability.Functional {
		return fmt.Errorf("the connector is still not working after the installation: %s", after.Reason)
	}
	in.logf("The ModSecurity connector works: %s", after.Modules[0].Path)
	return nil
}

func relevantPackages(e *host.Env) []string {
	var out []string
	for _, n := range []string{"nginx", "nginx-core", "nginx-common", "libnginx-mod-http-modsecurity", "libmodsecurity3", "libmodsecurity-dev", "modsecurity-crs", "libmodsecurity", "libmodsecurity-devel"} {
		out = append(out, n)
	}
	return out
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// mainConf is nginx.conf.
func (in *installer) mainConf() string {
	if m := regexp.MustCompile(`--conf-path=(\S+)`).FindStringSubmatch(in.n.Configure); m != nil {
		return strings.Trim(m[1], `'"`)
	}
	return "/etc/nginx/nginx.conf"
}

var includeRe = regexp.MustCompile(`^\s*include\s+"?([^";\s]*modules[^";\s]*\*\.conf)"?\s*;`)

// loadFile is where NimVor's load_module line goes: a folder of module
// configuration that nginx.conf includes at its top level. Without one,
// nginx.conf gets an include of NimVor's own folder (recorded and rolled
// back like any other change).
func (in *installer) loadFile() (file, include string, err error) {
	conf := in.mainConf()
	b, err := in.e.Root.ReadFile(conf)
	if err != nil {
		return "", "", fmt.Errorf("%s could not be read: %v", conf, err)
	}
	depth := 0
	for _, line := range strings.Split(string(b), "\n") {
		code := strings.SplitN(line, "#", 2)[0]
		if depth == 0 {
			if m := includeRe.FindStringSubmatch(code); m != nil {
				dir := m[1]
				if !strings.HasPrefix(dir, "/") {
					dir = filepath.Join(filepath.Dir(conf), dir)
				}
				name := loadName
				if strings.HasSuffix(filepath.Dir(dir), "modules-enabled") || strings.Contains(dir, "modules-enabled") {
					name = "50-" + loadName
				}
				return filepath.Join(filepath.Dir(dir), name), "", nil
			}
		}
		depth += strings.Count(code, "{") - strings.Count(code, "}")
	}
	dir := filepath.Join(filepath.Dir(conf), "nimvor-modules")
	return filepath.Join(dir, loadName), "include " + dir + "/*.conf;", nil
}

// enable writes the load_module line for module (after checking that
// Nginx loads it), adding the include to nginx.conf when needed.
func (in *installer) enable(ctx context.Context, module string) error {
	if missing, err := capability.NewLinker(in.e).Resolve(module); err != nil || len(missing) > 0 {
		return fmt.Errorf("the module's libraries do not resolve: %v %s", err, strings.Join(missing, ", "))
	}
	if err := capability.TestLoad(ctx, in.e, module); err != nil {
		return fmt.Errorf("Nginx cannot load %s: %v", module, err)
	}
	in.logf("Nginx loads %s (checked with a throwaway configuration).", module)
	file, include, err := in.loadFile()
	if err != nil {
		return err
	}
	if include != "" {
		conf := in.mainConf()
		b, _ := in.e.Root.ReadFile(conf)
		if err := in.tx.MkdirAll(filepath.Dir(file), 0o755, agentfs.RootOwner); err != nil {
			return err
		}
		if err := in.tx.WriteFile(conf, append([]byte("# Added by NimVor: dynamic modules it installs.\n"+include+"\n"), b...), 0o644, agentfs.RootOwner); err != nil {
			return err
		}
	}
	line := "# Managed by NimVor: the ModSecurity connector. Changes here are overwritten.\nload_module " + module + ";\n"
	if err := in.tx.WriteFile(file, []byte(line), 0o644, agentfs.RootOwner); err != nil {
		return err
	}
	in.meta.Module, in.meta.LoadFile = module, file
	return nil
}

// packageSafe dry-runs the package installation: it must not remove or
// replace Nginx or anything else ("" = safe).
func (in *installer) packageSafe(ctx context.Context, name string) string {
	if in.e.Platform.PackageManager() == "dnf" {
		out, _ := in.e.Runner.RunWith(ctx, "dnf", execx.Opts{Timeout: 5 * time.Minute}, "install", "--assumeno", "--", name)
		for _, l := range strings.Split(string(out), "\n") {
			f := strings.Fields(l)
			if len(f) > 0 && (strings.HasPrefix(f[0], "nginx") && !strings.Contains(l, "mod")) {
				return "installing it would change the Nginx package itself"
			}
			if strings.HasPrefix(strings.TrimSpace(l), "Removing") || strings.HasPrefix(strings.TrimSpace(l), "Downgrading") {
				return "installing it would remove or downgrade packages"
			}
		}
		return ""
	}
	out, err := in.e.Runner.RunWith(ctx, "apt-get", execx.Opts{Timeout: 5 * time.Minute}, "-s", "-o", "Debug::NoLocking=1", "install", "--no-install-recommends", "--", name)
	if err != nil {
		return "the package manager would not install it: " + host.Output(out)
	}
	for _, l := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(l, "Remv "):
			return "installing it would remove " + strings.Fields(l)[1]
		case strings.HasPrefix(l, "Inst nginx ") || strings.HasPrefix(l, "Inst nginx-core ") || strings.HasPrefix(l, "Inst nginx-light ") || strings.HasPrefix(l, "Inst nginx-full ") || strings.HasPrefix(l, "Inst nginx-extras "):
			return "installing it would install or replace the Nginx binary (" + strings.Fields(l)[1] + ")"
		}
	}
	return ""
}

// viaPackage installs the distribution's connector package.
func (in *installer) viaPackage(ctx context.Context, name string) error {
	before := pkgs.InstalledNative(ctx, in.e, []string{name, "libmodsecurity3", "libnginx-mod-http-ndk"})
	in.logf("Installing the package %s…", name)
	err := pkgs.InstallNative(ctx, in.e, in.log, []string{name})
	// Record what is new even when the installation failed half-way.
	after := pkgs.InstalledNative(ctx, in.e, []string{name, "libmodsecurity3", "libnginx-mod-http-ndk"})
	for p := range after {
		if _, had := before[p]; !had {
			if rerr := in.tx.Package(p); rerr != nil {
				return rerr
			}
		}
	}
	if err != nil {
		return fmt.Errorf("the package %s could not be installed: %v", name, err)
	}
	in.meta.Sources = append(in.meta.Sources, SourceInfo{Name: name, Version: after[name], Origin: "package"})
	return nil
}

// RollbackInstall undoes a completed installation — the newest one still in
// effect — as a transaction of its own: the files it replaced get their
// earlier content back, the files it created are removed, packages it
// installed are removed; then nginx -t and a reload. If that fails, the
// rollback itself is undone and the installation stays in effect.
func RollbackInstall(ctx context.Context, e *host.Env, id string, log io.Writer) (map[string]any, error) {
	installMu.Lock()
	defer installMu.Unlock()
	allowInstallerPaths(e)
	old, err := txn.Load(e, TxKind, id)
	if err != nil {
		return nil, actions.NotFound("There is no ModSecurity installation " + id + ".")
	}
	if old.Status != txn.Committed {
		return nil, actions.Conflict("Installation " + id + " is " + old.Status + "; only a completed installation that is still in effect can be rolled back.")
	}
	if eff := effective(e); eff != id {
		return nil, actions.Conflict("A later installation (" + eff + ") is in effect; roll that one back first.")
	}
	if n, ok := capability.Inspect(ctx, e); !ok || !n.ConfigOK {
		return nil, actions.Conflict("The current Nginx configuration does not pass nginx -t; fix it first. Nothing was changed.")
	}
	tx, err := txn.Begin(e, TxKind, log)
	if err != nil {
		return nil, err
	}
	tx.SetMeta(InstallMeta{Method: "rollback of " + id})
	if err := tx.Snapshot(snapshotMax, uniq([]string{"/etc/nginx", "/usr/share/nginx/modules", statePath(e)})...); err != nil {
		tx.Rollback(ctx, txn.Hooks{}, err)
		return nil, fmt.Errorf("the backup before rolling back failed; nothing was changed: %v", err)
	}
	var pkgList []string
	for _, c := range old.Changes {
		if c.Op == "package" {
			pkgList = append(pkgList, c.Name)
		}
	}
	fail := func(cause error) (map[string]any, error) {
		if len(pkgList) > 0 {
			// Put back what was removed before undoing the file changes.
			pkgs.InstallNative(ctx, e, log, pkgList)
		}
		if rerr := tx.Rollback(ctx, txn.Hooks{AfterRestore: Hooks(e, log).AfterRestore}, cause); rerr != nil {
			return nil, fmt.Errorf("rolling back %s failed (%v), and undoing that was incomplete: %v; transaction %s holds the backup", id, cause, rerr, tx.ID)
		}
		return nil, fmt.Errorf("rolling back %s failed: %v — nothing was changed (the installation stays in effect)", id, cause)
	}
	if err := tx.Revert(old); err != nil {
		return fail(err)
	}
	if len(pkgList) > 0 {
		tx.Logf("Removing packages the installation added: %s", strings.Join(pkgList, " "))
		if err := pkgs.RemoveNative(ctx, e, log, pkgList); err != nil {
			pkgList = nil // nothing was removed
			return fail(err)
		}
	}
	if err := e.NginxTest(ctx); err != nil {
		hint := ""
		if Load(e).Config.Enabled {
			hint = " (ModSecurity is on: turn it off before removing the connector)"
		}
		return fail(fmt.Errorf("nginx -t fails without the installation%s: %v", hint, err))
	}
	if err := e.ReloadNginx(ctx); err != nil {
		return fail(fmt.Errorf("Nginx could not be reloaded: %v", err))
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	old.MarkRolledBack(tx.ID)
	tx.Logf("Installation %s rolled back.", id)
	return map[string]any{"rolled_back": id, "transaction": tx.ID}, nil
}

// Installs lists the installation records, newest first.
func Installs(e *host.Env) []map[string]any {
	out := []map[string]any{}
	eff := effective(e)
	for _, t := range txn.List(e, TxKind) {
		var m InstallMeta
		json.Unmarshal(t.Meta, &m)
		out = append(out, map[string]any{
			"id": t.ID, "status": t.Status, "started_at": t.StartedAt, "finished_at": t.FinishedAt, "rolled_back_at": t.RolledBackAt,
			"error": t.Error, "method": m.Method, "nginx_version": m.Nginx, "os": m.OS, "sources": m.Sources, "module": m.Module,
			"load_file": m.LoadFile, "changes": len(t.Changes), "snapshots": t.Snapshots, "folder": t.Dir(),
			"can_roll_back": t.ID == eff, "log": t.Log,
		})
	}
	return out
}

func isRollback(t *txn.Tx) bool {
	var m InstallMeta
	json.Unmarshal(t.Meta, &m)
	return strings.HasPrefix(m.Method, "rollback")
}

// effective is the installation in effect: the newest completed one that
// was not rolled back (rollback records do not count).
func effective(e *host.Env) string {
	for _, t := range txn.List(e, TxKind) {
		if t.Status == txn.Committed && !isRollback(t) {
			return t.ID
		}
	}
	return ""
}
