package phpmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// Extension is a PHP extension NimVor can install and switch on or off.
type Extension struct {
	Name        string
	Description string
	// Debian and RHEL are the package suffixes (php8.3-<Debian>,
	// php83-php-<RHEL>); "" when the extension is part of another package
	// on that system (it can then be switched, not installed).
	Debian, RHEL string
	// Modules are the modules it loads (their ini files' names).
	Modules []string
}

// Extensions is the catalog offered for every PHP version.
var Extensions = []Extension{
	{"apcu", "In-memory user cache (APCu)", "apcu", "pecl-apcu", []string{"apcu"}},
	{"bcmath", "Arbitrary precision maths", "bcmath", "bcmath", []string{"bcmath"}},
	{"bz2", "Bzip2 compression", "bz2", "", []string{"bz2"}},
	{"curl", "HTTP client (cURL)", "curl", "", []string{"curl"}},
	{"gd", "Image processing (GD)", "gd", "gd", []string{"gd"}},
	{"gmp", "Big integers (GMP)", "gmp", "gmp", []string{"gmp"}},
	{"igbinary", "Compact serializer", "igbinary", "pecl-igbinary", []string{"igbinary"}},
	{"imagick", "Image processing (ImageMagick)", "imagick", "pecl-imagick-im7", []string{"imagick"}},
	{"intl", "Internationalisation", "intl", "intl", []string{"intl"}},
	{"ldap", "LDAP client", "ldap", "ldap", []string{"ldap"}},
	{"mbstring", "Multibyte strings", "mbstring", "mbstring", []string{"mbstring"}},
	{"memcached", "Memcached client", "memcached", "pecl-memcached", []string{"memcached"}},
	{"mysql", "MySQL / MariaDB (mysqli, PDO)", "mysql", "mysqlnd", []string{"mysqlnd", "mysqli", "pdo_mysql"}},
	{"opcache", "Opcode cache", "opcache", "opcache", []string{"opcache"}},
	{"pgsql", "PostgreSQL (pgsql, PDO)", "pgsql", "pgsql", []string{"pgsql", "pdo_pgsql"}},
	{"redis", "Redis client", "redis", "pecl-redis6", []string{"redis"}},
	{"soap", "SOAP", "soap", "soap", []string{"soap"}},
	{"sqlite3", "SQLite (sqlite3, PDO)", "sqlite3", "", []string{"sqlite3", "pdo_sqlite"}},
	{"xdebug", "Debugger (development only: slows every request)", "xdebug", "pecl-xdebug3", []string{"xdebug"}},
	{"xml", "XML (DOM, SimpleXML, XMLReader/Writer, XSL)", "xml", "xml", []string{"dom", "simplexml", "xml", "xmlreader", "xmlwriter", "xsl"}},
	{"zip", "Zip archives", "zip", "pecl-zip", []string{"zip"}},
}

// panelNeeds are the extensions NimVor's panel needs from the PHP it runs on
// (/usr/bin/php): on Debian they cannot be switched off or removed there.
var panelNeeds = map[string]bool{"sqlite3": true, "curl": true, "mbstring": true}

func findExtension(name string) (Extension, bool) {
	for _, e := range Extensions {
		if e.Name == name {
			return e, true
		}
	}
	return Extension{}, false
}

func rhel(env *host.Env) bool { return env.Platform.PackageManager() == "dnf" }

// tag is "8.3" (Debian) or "83" (Remi).
func tag(env *host.Env, v string) string {
	if rhel(env) {
		return strings.ReplaceAll(v, ".", "")
	}
	return v
}

// BuiltIn reports whether the extension is compiled into PHP v: OPcache is
// part of PHP itself from 8.5 (no package; always loaded, configured with
// opcache.enable).
func (e Extension) BuiltIn(v string) bool {
	return e.Name == "opcache" && !versionLess(v, "8.5")
}

func (e Extension) pkg(env *host.Env, v string) string {
	if e.BuiltIn(v) {
		return ""
	}
	if rhel(env) {
		if e.RHEL == "" {
			return ""
		}
		return "php" + tag(env, v) + "-php-" + e.RHEL
	}
	if e.Debian == "" {
		return ""
	}
	return "php" + v + "-" + e.Debian
}

// IniDir is where PHP version v loads its ini files from for PHP-FPM.
func IniDir(env *host.Env, v string) string {
	if rhel(env) {
		return "/etc/opt/remi/php" + tag(env, v) + "/php.d"
	}
	return "/etc/php/" + v + "/fpm/conf.d"
}

// ExtensionState is one catalog entry for a PHP version.
type ExtensionState struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Package     string `json:"package"`
	Installed   bool   `json:"installed"`
	Version     string `json:"version,omitempty"`
	Enabled     bool   `json:"enabled"`
	// Protected says why it cannot be switched off or removed ("" if it can).
	Protected string `json:"protected,omitempty"`
	// BuiltIn: compiled into PHP (no package to install or remove).
	BuiltIn bool `json:"built_in,omitempty"`
}

// moduleIni is the ini file loading module m for version v ("" if none).
func moduleIni(env *host.Env, v, m string) string {
	dir := IniDir(env, v)
	matches, _ := env.Root.Glob(filepath.Join(dir, "*-"+m+".ini"))
	re := regexp.MustCompile(`^[0-9]+-` + regexp.QuoteMeta(m) + `\.ini$`)
	for _, f := range matches {
		if re.MatchString(filepath.Base(f)) {
			return f
		}
	}
	return ""
}

// activeLine matches an extension= or zend_extension= line.
var activeLine = regexp.MustCompile(`(?m)^\s*(zend_)?extension\s*=`)

const disabledMark = "; nimvor-disabled: "

// moduleEnabled: Debian links the module's ini into conf.d; on RHEL its
// php.d file loads it unless NimVor commented the line out.
func moduleEnabled(env *host.Env, v, m string) bool {
	f := moduleIni(env, v, m)
	if f == "" {
		return false
	}
	if !rhel(env) {
		return true
	}
	b, err := env.Root.ReadFile(f)
	return err == nil && activeLine.Match(b)
}

// moduleAvailable: the module's ini exists (Debian: mods-available).
func moduleAvailable(env *host.Env, v, m string) bool {
	if rhel(env) {
		return moduleIni(env, v, m) != ""
	}
	return env.Root.Exists("/etc/php/" + v + "/mods-available/" + m + ".ini")
}

// panelVersion reports whether /usr/bin/php (the panel's PHP) is version v.
func panelVersion(env *host.Env, v string) bool {
	if rhel(env) {
		return false // Remi's versions are separate from the system PHP
	}
	return path.Base(resolveLinks(env, "/usr/bin/php")) == "php"+v
}

// resolveLinks follows symbolic links (/usr/bin/php → /etc/alternatives/php
// → /usr/bin/php8.3) inside the agent's root: absolute targets are taken
// relative to it.
func resolveLinks(env *host.Env, p string) string {
	for i := 0; i < 10; i++ {
		real, err := env.Root.Path(p)
		if err != nil {
			return p
		}
		target, err := os.Readlink(real)
		if err != nil {
			return p
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(p), target)
		}
		p = filepath.Clean(target)
	}
	return p
}

// ExtensionStates lists the catalog for version v.
func ExtensionStates(ctx context.Context, env *host.Env, v string) []ExtensionState {
	var natives []string
	for _, e := range Extensions {
		if p := e.pkg(env, v); p != "" {
			natives = append(natives, p)
		}
	}
	inst := pkgs.InstalledNative(ctx, env, natives)
	panel := panelVersion(env, v)
	out := []ExtensionState{}
	for _, e := range Extensions {
		st := ExtensionState{Name: e.Name, Description: e.Description, Package: e.pkg(env, v), Enabled: true}
		if e.BuiltIn(v) {
			st.Installed, st.BuiltIn = true, true
			st.Protected = "built into PHP " + v + " (turn it off with opcache.enable in php.ini)"
			out = append(out, st)
			continue
		}
		if st.Package != "" {
			st.Version, st.Installed = inst[st.Package]
		} else {
			st.Installed = moduleAvailable(env, v, e.Modules[0])
		}
		for _, m := range e.Modules {
			if !moduleEnabled(env, v, m) {
				st.Enabled = false
			}
		}
		if !st.Installed {
			st.Enabled = false
		}
		if panel && panelNeeds[e.Name] {
			st.Protected = "NimVor's panel runs on PHP " + v + " and needs " + e.Name + "."
		}
		out = append(out, st)
	}
	return out
}

// setModules switches modules on or off for version v (all SAPIs).
func setModules(ctx context.Context, env *host.Env, v string, mods []string, on bool) error {
	if !rhel(env) {
		tool := "phpdismod"
		if on {
			tool = "phpenmod"
		}
		out, err := env.Runner.Run(ctx, tool, append([]string{"-v", v}, mods...)...)
		if err != nil {
			return fmt.Errorf("%s: %s", tool, host.Why(out, err))
		}
		return nil
	}
	set := env.Changes("php-extensions-" + v)
	for _, m := range mods {
		f := moduleIni(env, v, m)
		if f == "" {
			continue
		}
		b, err := env.Root.ReadFile(f)
		if err != nil {
			return err
		}
		var lines []string
		for _, l := range strings.Split(string(b), "\n") {
			switch {
			case !on && activeLine.MatchString(l):
				l = disabledMark + l
			case on && strings.HasPrefix(l, disabledMark):
				l = strings.TrimPrefix(l, disabledMark)
			}
			lines = append(lines, l)
		}
		mode := os.FileMode(0o644)
		if real, err := env.Root.Path(f); err == nil {
			if fi, err := os.Stat(real); err == nil {
				mode = fi.Mode().Perm()
			}
		}
		set.Write(f, []byte(strings.Join(lines, "\n")), mode, agentfs.Keep)
	}
	_, err := set.Apply()
	return err
}

type extensionReq struct {
	Version string `json:"version"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func (in *extensionReq) check(env *host.Env) (Extension, error) {
	if err := validate.PHPVersion("version", in.Version); err != nil {
		return Extension{}, host.Invalid(err)
	}
	e, ok := findExtension(in.Name)
	if !ok {
		return Extension{}, actions.InvalidParams("name: " + in.Name + " is not an extension NimVor manages")
	}
	if !env.Runner.Available(fmt.Sprintf(env.M.PHPFPMBinary, in.Version)) {
		return Extension{}, actions.Conflict("PHP " + in.Version + " (FPM) is not installed.")
	}
	return e, nil
}

func registerExtensions(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "php.extensions", Description: "The PHP extensions NimVor manages for a version: installed, enabled, protected.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in extensionReq
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.PHPVersion("version", in.Version); err != nil {
			return nil, host.Invalid(err)
		}
		return ExtensionStates(ctx, env, in.Version), nil
	}})

	r.Register(actions.Action{Name: "php.extension_install", Mutating: true, Description: "Install a PHP extension's package for a version (job); PHP-FPM is reloaded.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in extensionReq
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		e, err := in.check(env)
		if err != nil {
			return nil, err
		}
		if e.BuiltIn(in.Version) {
			return nil, actions.Conflict(e.Name + " is built into PHP " + in.Version + ": there is nothing to install, remove or switch (turn it off with opcache.enable in php.ini).")
		}
		pkg := e.pkg(env, in.Version)
		if pkg == "" {
			return nil, actions.Conflict(e.Name + " is part of PHP " + in.Version + "'s base packages on this system; switch it on instead.")
		}
		j := env.Jobs.Start("php.extension_install", "server:packages", func(ctx context.Context, log io.Writer) (any, error) {
			if err := pkgs.InstallNative(ctx, env, log, []string{pkg}); err != nil {
				return nil, err
			}
			defer env.LockWebConfig()()
			if err := env.ReloadFPM(ctx, in.Version); err != nil {
				return nil, fmt.Errorf("installed, but PHP-FPM %s did not reload: %v", in.Version, err)
			}
			fmt.Fprintf(log, "PHP-FPM %s reloaded.\n", in.Version)
			return nil, nil
		})
		return map[string]any{"job_id": j.ID, "package": pkg}, nil
	}})

	r.Register(actions.Action{Name: "php.extension_remove", Mutating: true, Description: "Remove a PHP extension's package for a version (job), only if nothing else would be removed with it; PHP-FPM is reloaded.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in extensionReq
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		e, err := in.check(env)
		if err != nil {
			return nil, err
		}
		if e.BuiltIn(in.Version) {
			return nil, actions.Conflict(e.Name + " is built into PHP " + in.Version + ": there is nothing to install, remove or switch (turn it off with opcache.enable in php.ini).")
		}
		pkg := e.pkg(env, in.Version)
		if pkg == "" {
			return nil, actions.Conflict(e.Name + " is part of PHP " + in.Version + "'s base packages on this system; switch it off instead.")
		}
		if panelVersion(env, in.Version) && panelNeeds[e.Name] {
			return nil, actions.Conflict("NimVor's panel runs on PHP " + in.Version + " and needs " + e.Name + ".")
		}
		j := env.Jobs.Start("php.extension_remove", "server:packages", func(ctx context.Context, log io.Writer) (any, error) {
			if err := pkgs.RemoveNative(ctx, env, log, []string{pkg}); err != nil {
				return nil, err
			}
			defer env.LockWebConfig()()
			if err := env.ReloadFPM(ctx, in.Version); err != nil {
				return nil, fmt.Errorf("removed, but PHP-FPM %s did not reload: %v", in.Version, err)
			}
			fmt.Fprintf(log, "PHP-FPM %s reloaded.\n", in.Version)
			return nil, nil
		})
		return map[string]any{"job_id": j.ID, "package": pkg}, nil
	}})

	r.Register(actions.Action{Name: "php.extension_set", Mutating: true, Description: "Switch a PHP extension on or off for a version (Debian: phpenmod/phpdismod; RHEL: its php.d line); PHP-FPM is checked and reloaded, and the change undone on failure.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in extensionReq
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		e, err := in.check(env)
		if err != nil {
			return nil, err
		}
		if e.BuiltIn(in.Version) {
			return nil, actions.Conflict(e.Name + " is built into PHP " + in.Version + ": there is nothing to install, remove or switch (turn it off with opcache.enable in php.ini).")
		}
		if !in.Enabled && panelVersion(env, in.Version) && panelNeeds[e.Name] {
			return nil, actions.Conflict("NimVor's panel runs on PHP " + in.Version + " and needs " + e.Name + ".")
		}
		for _, m := range e.Modules {
			if !moduleAvailable(env, in.Version, m) {
				return nil, actions.Conflict(e.Name + " is not installed for PHP " + in.Version + ".")
			}
		}
		defer env.LockWebConfig()()
		if err := setModules(ctx, env, in.Version, e.Modules, in.Enabled); err != nil {
			return nil, actions.Failed("The extension was not switched.", err.Error())
		}
		if err := env.ReloadFPM(ctx, in.Version); err != nil {
			_ = setModules(ctx, env, in.Version, e.Modules, !in.Enabled)
			_ = env.ReloadFPM(ctx, in.Version)
			return nil, host.FailWithValidation("PHP-FPM "+in.Version+" did not accept the change; it was undone", err)
		}
		return map[string]any{"name": e.Name, "enabled": in.Enabled}, nil
	}})
}
