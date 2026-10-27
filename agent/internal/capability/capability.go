// Package capability detects whether a functionality WhaCP depends on is
// available, whatever put it there: a distribution package, an rpm, a
// module compiled from source or a dynamic module copied by hand. It looks
// at the functionality itself (the Nginx module file, the libraries it
// needs, whether Nginx loads it; the program on the PATH), not at package
// names. Package installation stays the preferred way to add something
// that is missing; a package is never required when the functionality is
// already there.
//
// A capability is in one of three states:
//
//	missing     not installed
//	installed   present but not loaded or not usable (Reason says why)
//	functional  present and working
package capability

import (
	"bufio"
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// States.
const (
	Missing    = "missing"
	Installed  = "installed"
	Functional = "functional"
)

// NginxModule describes an Nginx module capability.
type NginxModule struct {
	ID   string
	Name string
	// Modules are the module names (ngx_http_…_module); the first is the
	// one that must be loaded, the others are reported when present.
	Modules []string
	// Libraries the module needs beyond the C library (reported, and
	// resolved like the dynamic linker would).
	Libraries []string
	// Static matches a --add-module= path of a module built into nginx.
	Static *regexp.Regexp
	// Package is WhaCP's (Debian) name of the package that provides it.
	Package string
}

// Known Nginx module capabilities.
var (
	ModSecurity = NginxModule{
		ID: "modsecurity", Name: "ModSecurity for Nginx",
		Modules:   []string{"ngx_http_modsecurity_module"},
		Libraries: []string{"libmodsecurity.so.3"},
		Static:    regexp.MustCompile(`(?i)modsecurity-nginx`),
		Package:   "libnginx-mod-http-modsecurity",
	}
	Brotli = NginxModule{
		ID: "brotli", Name: "Brotli compression for Nginx",
		Modules: []string{"ngx_http_brotli_filter_module", "ngx_http_brotli_static_module"},
		Static:  regexp.MustCompile(`(?i)ngx_brotli`),
		Package: "libnginx-mod-brotli",
	}
	NginxModules = []NginxModule{ModSecurity, Brotli}
)

// Library is a shared library a module needs.
type Library struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"` // where it resolves ("" = not found)
}

// Module is one module file found.
type Module struct {
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"`
	Loaded bool   `json:"loaded"`
	Static bool   `json:"static,omitempty"`
}

// Status is a capability's state.
type Status struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	State     string    `json:"state"`
	Reason    string    `json:"reason,omitempty"`
	Modules   []Module  `json:"modules,omitempty"`
	Libraries []Library `json:"libraries,omitempty"`
	// Package that owns the module file ("" = none: built from source or
	// copied by hand).
	Package string `json:"package,omitempty"`
	Nginx   string `json:"nginx_version,omitempty"`
}

// Nginx is what "nginx -V" and the active configuration say.
type Nginx struct {
	Version     string
	ModulesPath string
	Prefix      string
	Configure   string
	Compat      bool
	AddModules  []string
	// Loaded maps module file paths to true for load_module lines of the
	// active configuration (nginx -T); ConfigOK reports whether it passed.
	Loaded    map[string]bool
	ConfigOK  bool
	ConfigErr string
}

var (
	versionRe    = regexp.MustCompile(`nginx version: [^/]+/([0-9][0-9A-Za-z.\-]*)`)
	loadModuleRe = regexp.MustCompile(`^\s*load_module\s+"?([^";\s]+)"?\s*;`)
	argRe        = regexp.MustCompile(`--([a-z-]+)(?:=(\S+))?`)
)

// ParseV reads "nginx -V" output.
func ParseV(out string) Nginx {
	n := Nginx{Loaded: map[string]bool{}}
	if m := versionRe.FindStringSubmatch(out); m != nil {
		n.Version = m[1]
	}
	for _, line := range strings.Split(out, "\n") {
		if a, ok := strings.CutPrefix(line, "configure arguments:"); ok {
			n.Configure = strings.TrimSpace(a)
		}
	}
	for _, m := range argRe.FindAllStringSubmatch(n.Configure, -1) {
		switch m[1] {
		case "modules-path":
			n.ModulesPath = strings.Trim(m[2], `'"`)
		case "prefix":
			n.Prefix = strings.Trim(m[2], `'"`)
		case "with-compat":
			n.Compat = true
		case "add-module":
			n.AddModules = append(n.AddModules, strings.Trim(m[2], `'"`))
		}
	}
	if n.Prefix == "" {
		n.Prefix = "/usr/local/nginx"
	}
	return n
}

// ParseLoaded collects load_module paths from "nginx -T" output (relative
// paths are relative to the prefix).
func ParseLoaded(out, prefix string) map[string]bool {
	loaded := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if m := loadModuleRe.FindStringSubmatch(sc.Text()); m != nil {
			p := m[1]
			if !strings.HasPrefix(p, "/") {
				p = path.Join(prefix, p)
			}
			loaded[path.Clean(p)] = true
		}
	}
	return loaded
}

// Inspect runs nginx -V and nginx -T.
func Inspect(ctx context.Context, e *host.Env) (Nginx, bool) {
	if !e.Runner.Available("nginx") {
		return Nginx{Loaded: map[string]bool{}}, false
	}
	out, _ := e.Runner.Run(ctx, "nginx", "-V")
	n := ParseV(string(out))
	t, err := e.Runner.Run(ctx, "nginx", "-T")
	n.Loaded = ParseLoaded(string(t), n.Prefix)
	n.ConfigOK = err == nil
	if err != nil {
		// A failing configuration is not printed: read the load_module
		// lines of the main file and the usual module folders instead.
		n.ConfigErr = host.Output(t)
		conf := "/etc/nginx/nginx.conf"
		if m := regexp.MustCompile(`--conf-path=(\S+)`).FindStringSubmatch(n.Configure); m != nil {
			conf = strings.Trim(m[1], `'"`)
		}
		files := []string{conf}
		for _, g := range []string{"/etc/nginx/modules-enabled/*.conf", "/etc/nginx/conf.modules.d/*.conf", "/usr/share/nginx/modules/*.conf"} {
			m, _ := e.Root.Glob(g)
			files = append(files, m...)
		}
		for _, f := range files {
			if b, err := e.Root.ReadFile(f); err == nil {
				for p := range ParseLoaded(string(b), n.Prefix) {
					n.Loaded[p] = true
				}
			}
		}
	}
	return n, true
}

// ModuleDirs are where dynamic modules are looked for: the path compiled
// into nginx, the distributions' usual places and WhaCP's own.
func ModuleDirs(n Nginx) []string {
	dirs := []string{}
	if n.ModulesPath != "" {
		dirs = append(dirs, n.ModulesPath)
	}
	dirs = append(dirs, path.Join(n.Prefix, "modules"),
		"/usr/lib/nginx/modules", "/usr/lib64/nginx/modules", "/usr/share/nginx/modules",
		"/etc/nginx/modules", "/usr/local/nginx/modules", "/usr/local/lib/whacp/nginx-modules")
	seen := map[string]bool{}
	out := []string{}
	for _, d := range dirs {
		if d = path.Clean(d); !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// Linker resolves shared libraries like the dynamic linker: the object's
// RUNPATH (or RPATH), then /etc/ld.so.conf's folders, then the defaults.
type Linker struct {
	e    *host.Env
	dirs []string
	// Needed returns an ELF object's needed libraries and search path
	// (replaceable in tests).
	Needed func(real string) (needed, runpath []string, err error)
}

// ReadNeeded reads an ELF object's needed libraries and search path; tests
// of other packages replace it (their fixtures are not real objects).
var ReadNeeded = elfNeeded

// NewLinker reads /etc/ld.so.conf (with its includes).
func NewLinker(e *host.Env) *Linker {
	l := &Linker{e: e, Needed: ReadNeeded}
	seen := map[string]bool{}
	l.readConf("/etc/ld.so.conf", seen, 0)
	l.dirs = append(l.dirs, "/lib64", "/usr/lib64", "/lib", "/usr/lib")
	return l
}

func (l *Linker) readConf(file string, seen map[string]bool, depth int) {
	if depth > 8 || seen[file] {
		return
	}
	seen[file] = true
	b, err := l.e.Root.ReadFile(file)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		switch {
		case line == "":
		case strings.HasPrefix(line, "include "):
			pat := strings.TrimSpace(strings.TrimPrefix(line, "include "))
			if !strings.HasPrefix(pat, "/") {
				pat = path.Join(path.Dir(file), pat)
			}
			matches, _ := l.e.Root.Glob(pat)
			sort.Strings(matches)
			for _, m := range matches {
				l.readConf(m, seen, depth+1)
			}
		case strings.HasPrefix(line, "/"):
			l.dirs = append(l.dirs, path.Clean(line))
		}
	}
}

func elfNeeded(real string) ([]string, []string, error) {
	f, err := elf.Open(real)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	needed, err := f.ImportedLibraries()
	if err != nil {
		return nil, nil, err
	}
	var rp []string
	for _, tag := range []elf.DynTag{elf.DT_RUNPATH, elf.DT_RPATH} {
		if v, err := f.DynString(tag); err == nil {
			for _, s := range v {
				rp = append(rp, strings.Split(s, ":")...)
			}
		}
	}
	return needed, rp, nil
}

// Find returns where lib resolves for an object at objPath with runpath.
func (l *Linker) Find(lib, objPath string, runpath []string) string {
	if strings.Contains(lib, "/") {
		if l.e.Root.Exists(lib) {
			return lib
		}
		return ""
	}
	dirs := []string{}
	for _, d := range runpath {
		d = strings.ReplaceAll(strings.ReplaceAll(d, "${ORIGIN}", path.Dir(objPath)), "$ORIGIN", path.Dir(objPath))
		if strings.HasPrefix(d, "/") {
			dirs = append(dirs, d)
		}
	}
	for _, d := range append(dirs, l.dirs...) {
		p := path.Join(d, lib)
		if real, err := l.e.Root.Path(p); err == nil {
			if info, err := os.Stat(real); err == nil && info.Mode().IsRegular() {
				return p
			}
		}
	}
	return ""
}

// Resolve checks every library an object needs, and theirs (up to a few
// levels); it returns the libraries that do not resolve.
func (l *Linker) Resolve(obj string) (missing []string, err error) {
	seen := map[string]bool{}
	var walk func(p string, depth int) error
	walk = func(p string, depth int) error {
		real, err := l.e.Root.Path(p)
		if err != nil {
			return err
		}
		needed, rp, err := l.Needed(real)
		if err != nil {
			return err
		}
		for _, lib := range needed {
			if seen[lib] {
				continue
			}
			seen[lib] = true
			found := l.Find(lib, p, rp)
			if found == "" {
				missing = append(missing, lib)
				continue
			}
			if depth < 4 {
				if err := walk(found, depth+1); err != nil {
					// An unreadable dependency is not a missing one.
					continue
				}
			}
		}
		return nil
	}
	if err := walk(obj, 0); err != nil {
		return nil, err
	}
	sort.Strings(missing)
	return missing, nil
}

// LibraryPath returns where a library resolves for an object (or "").
func (l *Linker) LibraryPath(obj, lib string) string {
	real, err := l.e.Root.Path(obj)
	if err != nil {
		return ""
	}
	_, rp, _ := l.Needed(real)
	return l.Find(lib, obj, rp)
}

// Owner returns the package that installed a file ("" when none), also
// through links (Nginx may load a module through a linked folder).
func Owner(ctx context.Context, e *host.Env, file string) string {
	if p := owner(ctx, e, file); p != "" {
		return p
	}
	if real, err := e.Root.Path(file); err == nil {
		if resolved, err := filepath.EvalSymlinks(real); err == nil && resolved != real {
			if rel, err := filepath.Rel(realRoot(e), resolved); err == nil && !strings.HasPrefix(rel, "..") {
				return owner(ctx, e, "/"+filepath.ToSlash(rel))
			}
		}
	}
	return ""
}

// realRoot is where the agent's root ("/") is on the host.
func realRoot(e *host.Env) string {
	r, _ := e.Root.Path("/")
	return r
}

func owner(ctx context.Context, e *host.Env, file string) string {
	if e.Platform.PackageManager() == "dnf" {
		if !e.Runner.Available("rpm") {
			return ""
		}
		out, err := e.Runner.Run(ctx, "rpm", "-qf", "--qf", `%{NAME}`, "--", file)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	if !e.Runner.Available("dpkg-query") {
		return ""
	}
	out, err := e.Runner.Run(ctx, "dpkg-query", "-S", "--", file)
	if err != nil {
		return ""
	}
	// "libnginx-mod-http-modsecurity: /usr/lib/nginx/modules/…"
	name, _, _ := strings.Cut(strings.SplitN(string(out), "\n", 2)[0], ":")
	return strings.TrimSpace(name)
}

// TestLoad checks that nginx can load a module file: a throwaway
// configuration that only loads it, tested with nginx -t (catches a
// module built for another Nginx version or with missing libraries).
func TestLoad(ctx context.Context, e *host.Env, module string, extra ...string) error {
	tmp := filepath.Join(e.Cfg.StateDir, "tmp")
	if _, err := e.Writer.MkdirAll(tmp, 0o700, agentfs.RootOwner); err != nil {
		return err
	}
	realTmp, err := e.Root.Path(tmp)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp(realTmp, "nginx-load-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	var b strings.Builder
	for _, m := range append([]string{module}, extra...) {
		fmt.Fprintf(&b, "load_module %s;\n", m)
	}
	b.WriteString("pid " + filepath.Join(dir, "nginx.pid") + ";\nerror_log stderr;\nevents {}\nhttp {\n    access_log off;\n}\n")
	conf := filepath.Join(dir, "nginx.conf")
	if err := os.WriteFile(conf, []byte(b.String()), 0o600); err != nil {
		return err
	}
	// Paths as nginx sees them (the host's).
	hostDir := filepath.Join(tmp, filepath.Base(dir))
	out, err := e.Runner.Run(ctx, "nginx", "-t", "-q", "-p", hostDir, "-c", filepath.Join(hostDir, "nginx.conf"))
	if err != nil {
		msg := host.Output(out)
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

// DetectModule reports an Nginx module capability. testLoad also checks,
// for a module found but not loaded, whether nginx could load it.
func DetectModule(ctx context.Context, e *host.Env, n Nginx, l *Linker, c NginxModule, testLoad bool) Status {
	s := Status{ID: c.ID, Name: c.Name, State: Missing, Nginx: n.Version}
	for _, a := range n.AddModules {
		if c.Static.MatchString(a) {
			s.State = Functional
			for _, m := range c.Modules {
				s.Modules = append(s.Modules, Module{Name: m, Static: true, Loaded: true})
			}
			return s
		}
	}
	// The module files: in the module folders, or wherever the active
	// configuration loads them from.
	candidates := map[string][]string{}
	for _, m := range c.Modules {
		for p := range n.Loaded {
			// Also a versioned file (name-<version>.so) the configuration loads.
			if b := path.Base(p); b == m+".so" || (strings.HasPrefix(b, m+"-") && strings.HasSuffix(b, ".so")) {
				candidates[m] = append(candidates[m], p)
			}
		}
		for _, d := range ModuleDirs(n) {
			p := path.Join(d, m+".so")
			if real, err := e.Root.Path(p); err == nil {
				if info, err := os.Stat(real); err == nil && info.Mode().IsRegular() {
					candidates[m] = append(candidates[m], p)
				}
			}
		}
	}
	main := c.Modules[0]
	for _, m := range c.Modules {
		mod := Module{Name: m}
		for _, p := range candidates[m] {
			if n.Loaded[p] {
				mod.Path, mod.Loaded = p, true
				break
			}
			if mod.Path == "" && e.Root.Exists(p) {
				mod.Path = p
			}
		}
		if mod.Path != "" {
			s.Modules = append(s.Modules, mod)
		}
	}
	var primary *Module
	for i := range s.Modules {
		if s.Modules[i].Name == main {
			primary = &s.Modules[i]
		}
	}
	if primary == nil {
		s.Reason = "The module " + main + ".so was not found in " + strings.Join(ModuleDirs(n), ", ") + "."
		return s
	}
	s.State = Installed
	s.Package = Owner(ctx, e, primary.Path)
	if !e.Root.Exists(primary.Path) {
		s.Reason = "The configuration loads " + primary.Path + ", which does not exist."
		return s
	}
	missing, err := l.Resolve(primary.Path)
	for _, lib := range c.Libraries {
		s.Libraries = append(s.Libraries, Library{Name: lib, Path: l.LibraryPath(primary.Path, lib)})
	}
	switch {
	case err != nil:
		s.Reason = primary.Path + " is not a readable shared object: " + err.Error()
		return s
	case len(missing) > 0:
		s.Reason = primary.Path + " needs libraries that are not installed: " + strings.Join(missing, ", ") + "."
		return s
	}
	if primary.Loaded {
		if !n.ConfigOK {
			s.Reason = "Nginx loads the module, but its configuration test fails: " + n.ConfigErr
			return s
		}
		s.State = Functional
		return s
	}
	s.Reason = "The module is installed but Nginx does not load it (no load_module line in the active configuration)."
	if testLoad {
		if err := TestLoad(ctx, e, primary.Path); err != nil {
			s.Reason = "The module is installed but Nginx cannot load it: " + err.Error()
		} else {
			s.Reason += " It loads correctly."
		}
	}
	return s
}

// Modules detects every known Nginx module capability.
func Modules(ctx context.Context, e *host.Env, testLoad bool) map[string]Status {
	out := map[string]Status{}
	n, ok := Inspect(ctx, e)
	l := NewLinker(e)
	for _, c := range NginxModules {
		if !ok {
			out[c.ID] = Status{ID: c.ID, Name: c.Name, State: Missing, Reason: "Nginx is not installed."}
			continue
		}
		out[c.ID] = DetectModule(ctx, e, n, l, c, testLoad)
	}
	return out
}

// Command reports a program capability: functional when it is on the PATH
// (from a package or not).
func Command(e *host.Env, id, name string) Status {
	if e.Runner.Available(id) {
		return Status{ID: id, Name: name, State: Functional}
	}
	return Status{ID: id, Name: name, State: Missing, Reason: name + " was not found."}
}
