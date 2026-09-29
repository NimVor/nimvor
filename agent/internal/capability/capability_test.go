package capability

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

const vDebian = `nginx version: nginx/1.24.0 (Ubuntu)
built with OpenSSL 3.0.13 30 Jan 2024
TLS SNI support enabled
configure arguments: --with-cc-opt='-g -O2' --prefix=/usr/share/nginx --conf-path=/etc/nginx/nginx.conf --modules-path=/usr/lib/nginx/modules --with-compat --with-http_ssl_module
`

const vSource = `nginx version: nginx/1.27.3
configure arguments: --prefix=/opt/nginx --with-compat
`

// machine is a test host with nginx -V output v, nginx -T output t (and
// its failure), module files, packages owning files, and libraries.
type machine struct {
	*testhost.Host
	v, t      string
	tErr      error
	owners    map[string]string
	loadErr   string
	needed    map[string][]string
	runpath   map[string][]string
	loadTests int
}

func newMachine(t *testing.T) *machine {
	m := &machine{Host: testhost.New(t), owners: map[string]string{}, needed: map[string][]string{}, runpath: map[string][]string{}}
	m.Respond = func(id string, args []string) (string, error, bool) {
		switch {
		case id == "nginx" && len(args) == 1 && args[0] == "-V":
			return m.v, nil, true
		case id == "nginx" && len(args) == 1 && args[0] == "-T":
			return m.t, m.tErr, true
		case id == "nginx" && args[0] == "-t":
			m.loadTests++
			if m.loadErr != "" {
				return m.loadErr, errors.New("exit status 1"), true
			}
			return "", nil, true
		case id == "dpkg-query":
			if p, ok := m.owners[args[len(args)-1]]; ok {
				return p + ": " + args[len(args)-1] + "\n", nil, true
			}
			return "dpkg-query: no path found", errors.New("exit status 1"), true
		}
		return "", nil, false
	}
	return m
}

func (m *machine) file(p string) {
	os.MkdirAll(filepath.Join(m.Base, filepath.Dir(p)), 0o755)
	os.WriteFile(filepath.Join(m.Base, p), []byte("\x7fELF"), 0o644)
}

func (m *machine) linker() *Linker {
	l := NewLinker(m.Env)
	l.Needed = func(real string) ([]string, []string, error) {
		rel := "/" + strings.TrimPrefix(strings.TrimPrefix(real, m.Base), "/")
		return m.needed[rel], m.runpath[rel], nil
	}
	return l
}

func (m *machine) detect(c NginxModule, testLoad bool) Status {
	n, _ := Inspect(context.Background(), m.Env)
	return DetectModule(context.Background(), m.Env, n, m.linker(), c, testLoad)
}

func TestParseV(t *testing.T) {
	n := ParseV(vDebian)
	if n.Version != "1.24.0" || n.ModulesPath != "/usr/lib/nginx/modules" || n.Prefix != "/usr/share/nginx" || !n.Compat {
		t.Fatalf("%+v", n)
	}
	s := ParseV("nginx version: nginx/1.20.1\nconfigure arguments: --add-module=/build/ModSecurity-nginx --add-dynamic-module=/build/ngx_brotli\n")
	if s.Prefix != "/usr/local/nginx" || len(s.AddModules) != 1 || s.Compat {
		t.Fatalf("%+v", s)
	}
	loaded := ParseLoaded("# configuration file /etc/nginx/modules-enabled/50.conf:\nload_module modules/ngx_http_brotli_filter_module.so;\n# load_module /x/commented.so;\n  load_module \"/usr/lib/nginx/modules/ngx_http_modsecurity_module.so\";\n", "/usr/share/nginx")
	if !loaded["/usr/share/nginx/modules/ngx_http_brotli_filter_module.so"] || !loaded["/usr/lib/nginx/modules/ngx_http_modsecurity_module.so"] || len(loaded) != 2 {
		t.Fatalf("%v", loaded)
	}
}

// Installed from a package and loaded: functional, with its package and
// the library resolved through /etc/ld.so.conf.d.
func TestPackagedModuleIsFunctional(t *testing.T) {
	m := newMachine(t)
	mod := "/usr/lib/nginx/modules/ngx_http_modsecurity_module.so"
	m.v, m.t = vDebian, "load_module "+mod+";\n"
	m.file(mod)
	m.file("/usr/lib/x86_64-linux-gnu/libmodsecurity.so.3")
	os.MkdirAll(filepath.Join(m.Base, "etc/ld.so.conf.d"), 0o755)
	os.WriteFile(filepath.Join(m.Base, "etc/ld.so.conf"), []byte("include /etc/ld.so.conf.d/*.conf\n"), 0o644)
	os.WriteFile(filepath.Join(m.Base, "etc/ld.so.conf.d/x86_64-linux-gnu.conf"), []byte("# Multiarch support\n/usr/lib/x86_64-linux-gnu\n"), 0o644)
	m.needed[mod] = []string{"libmodsecurity.so.3"}
	m.owners[mod] = "libnginx-mod-http-modsecurity"
	s := m.detect(ModSecurity, true)
	if s.State != Functional || s.Package != "libnginx-mod-http-modsecurity" || s.Libraries[0].Path != "/usr/lib/x86_64-linux-gnu/libmodsecurity.so.3" || !s.Modules[0].Loaded {
		t.Fatalf("%+v", s)
	}
	if m.loadTests != 0 {
		t.Fatal("a loaded module is not test-loaded again")
	}
}

// Built from source, in the prefix's module folder, its library found
// through RUNPATH ($ORIGIN): functional without any package.
func TestSourceBuiltModuleIsFunctional(t *testing.T) {
	m := newMachine(t)
	mod := "/opt/nginx/modules/ngx_http_modsecurity_module.so"
	m.v, m.t = vSource, "load_module modules/ngx_http_modsecurity_module.so;\n"
	m.file(mod)
	m.file("/opt/modsecurity/lib/libmodsecurity.so.3")
	m.needed[mod] = []string{"libmodsecurity.so.3"}
	m.runpath[mod] = []string{"$ORIGIN/../../modsecurity/lib"}
	s := m.detect(ModSecurity, true)
	if s.State != Functional || s.Package != "" || s.Libraries[0].Path != "/opt/modsecurity/lib/libmodsecurity.so.3" {
		t.Fatalf("%+v", s)
	}
}

func TestStaticModuleIsFunctional(t *testing.T) {
	m := newMachine(t)
	m.v = "nginx version: nginx/1.26.2\nconfigure arguments: --with-compat --add-module=/usr/src/ngx_brotli\n"
	if s := m.detect(Brotli, true); s.State != Functional || !s.Modules[0].Static {
		t.Fatalf("%+v", s)
	}
}

// Copied by hand into a module folder but not loaded: installed, and the
// test load says whether nginx could load it.
func TestInstalledNotLoaded(t *testing.T) {
	m := newMachine(t)
	m.v = vDebian
	m.file("/usr/lib/nginx/modules/ngx_http_brotli_filter_module.so")
	m.file("/usr/lib/nginx/modules/ngx_http_brotli_static_module.so")
	s := m.detect(Brotli, true)
	if s.State != Installed || !strings.Contains(s.Reason, "does not load it") || !strings.Contains(s.Reason, "loads correctly") || len(s.Modules) != 2 {
		t.Fatalf("%+v", s)
	}
	m.loadErr = `nginx: [emerg] module "/usr/lib/nginx/modules/ngx_http_brotli_filter_module.so" is not binary compatible in /x/nginx.conf:1`
	if s := m.detect(Brotli, true); s.State != Installed || !strings.Contains(s.Reason, "not binary compatible") {
		t.Fatalf("%+v", s)
	}
}

func TestUnusableModules(t *testing.T) {
	m := newMachine(t)
	mod := "/usr/lib/nginx/modules/ngx_http_modsecurity_module.so"
	m.v, m.t = vDebian, "load_module "+mod+";\n"
	m.file(mod)
	m.needed[mod] = []string{"libmodsecurity.so.3"}
	if s := m.detect(ModSecurity, true); s.State != Installed || !strings.Contains(s.Reason, "libmodsecurity.so.3") {
		t.Fatalf("library missing: %+v", s)
	}
	m.file("/usr/lib/libmodsecurity.so.3")
	m.tErr = errors.New("exit status 1")
	m.t = "nginx: [emerg] unknown directive \"modsecurity\""
	os.MkdirAll(filepath.Join(m.Base, "etc/nginx/modules-enabled"), 0o755)
	os.WriteFile(filepath.Join(m.Base, "etc/nginx/modules-enabled/50-mod-http-modsecurity.conf"), []byte("load_module "+mod+";\n"), 0o644)
	if s := m.detect(ModSecurity, true); s.State != Installed || !strings.Contains(s.Reason, "configuration test fails") {
		t.Fatalf("config broken: %+v", s)
	}
	// Loaded from a path that no longer exists.
	os.Remove(filepath.Join(m.Base, "etc/nginx/modules-enabled/50-mod-http-modsecurity.conf"))
	m.tErr, m.t = nil, "load_module /gone/ngx_http_modsecurity_module.so;\n"
	os.Remove(filepath.Join(m.Base, mod))
	if s := m.detect(ModSecurity, false); s.State != Installed || !strings.Contains(s.Reason, "does not exist") {
		t.Fatalf("gone: %+v", s)
	}
}

func TestMissing(t *testing.T) {
	m := newMachine(t)
	m.v = vDebian
	s := m.detect(ModSecurity, true)
	if s.State != Missing || !strings.Contains(s.Reason, "/usr/lib/nginx/modules") {
		t.Fatalf("%+v", s)
	}
	m.Runner.Unavailable["nginx"] = true
	if s := Modules(context.Background(), m.Env, false)["brotli"]; s.State != Missing || s.Reason != "Nginx is not installed." {
		t.Fatalf("%+v", s)
	}
}
