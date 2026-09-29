package modsec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/capability"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
	"github.com/mchkhawarshahzad/whacp/agent/internal/txn"
)

// machine simulates a server for the installer: Nginx (nginx -V, -T, -t
// and reloads), apt (dry runs, installs, source packages), the build
// (tar, configure, make) and the libraries. Files are real, in a test
// root, so rollbacks are checked byte for byte.
type machine struct {
	*testhost.Host
	t            *testing.T
	installed    map[string]bool // packages
	pkgRemoves   int
	simRemovesNG bool // the package dry run would remove nginx
	failInstall  map[string]bool
	failMake     bool
	badConfig    bool // nginx -t fails once WhaCP's load file exists
	failReload   bool
	loadErr      string
	nginxOwner   string
	compat       bool
	missingLib   bool
	libEscapes   bool // the build links libmodsecurity.so.3 outside its folder
	reloads      int
	src          Sources
}

const nginxConf = "user www-data;\ninclude /etc/nginx/modules-enabled/*.conf;\nevents {}\nhttp {\n    include /etc/nginx/sites-enabled/*;\n}\n"

func newMachine(t *testing.T) *machine {
	m := &machine{Host: testhost.New(t), t: t, installed: map[string]bool{"nginx": true}, failInstall: map[string]bool{}, nginxOwner: "nginx", compat: true}
	w := func(p, s string) {
		os.MkdirAll(filepath.Join(m.Base, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(m.Base, p), []byte(s), 0o644)
	}
	w("etc/nginx/nginx.conf", nginxConf)
	w("etc/nginx/modules-enabled/10-mod-http-ndk.conf", "load_module modules/ndk_http_module.so;\n")
	w("usr/sbin/nginx", "ELF")
	w("etc/ld.so.conf", "include /etc/ld.so.conf.d/*.conf\n")
	w("etc/ld.so.conf.d/x86_64-linux-gnu.conf", "/usr/lib/x86_64-linux-gnu\n")
	w("etc/apt/sources.list.d/ubuntu.sources", "Types: deb\nURIs: http://archive.ubuntu.com/ubuntu/\nSuites: noble noble-updates\nComponents: main universe\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n")
	os.MkdirAll(filepath.Join(m.Base, "usr/local/lib/whacp"), 0o755)
	os.MkdirAll(filepath.Join(m.Base, "usr/lib/nginx/modules"), 0o755)
	os.MkdirAll(filepath.Join(m.Base, "usr/share/nginx"), 0o755)
	os.Symlink("../../lib/nginx/modules", filepath.Join(m.Base, "usr/share/nginx/modules")) // as on Debian
	os.MkdirAll(filepath.Join(m.Base, "var/lib"), 0o755)

	realNeeded := capability.ReadNeeded
	capability.ReadNeeded = func(real string) ([]string, []string, error) {
		if strings.Contains(filepath.Base(real), "modsecurity_module") {
			libs := []string{"libmodsecurity.so.3", "libc.so.6"}
			if m.missingLib {
				libs = append(libs, "libyajl.so.2")
			}
			b, _ := os.ReadFile(real)
			if strings.HasPrefix(string(b), "RPATH=") {
				return libs, []string{strings.TrimSpace(strings.TrimPrefix(string(b), "RPATH="))}, nil
			}
			return libs, nil, nil
		}
		return nil, nil, nil
	}
	t.Cleanup(func() { capability.ReadNeeded = realNeeded })
	w("usr/lib/x86_64-linux-gnu/libc.so.6", "ELF")

	// A release server for the connector and libmodsecurity.
	con, lib := []byte("connector-tarball"), []byte("libmodsecurity-tarball")
	nginxOrg := []byte("nginx-org-tarball")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/connector.tar.gz"):
			w.Write(con)
		case strings.HasSuffix(r.URL.Path, "/modsecurity.tar.gz"):
			w.Write(lib)
		case strings.HasPrefix(r.URL.Path, "/nginx-"):
			w.Write(nginxOrg)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	m.src = Sources{Connector: srv.URL + "/connector.tar.gz", ConnectorSHA256: sum(con), ModSecurity: srv.URL + "/modsecurity.tar.gz", ModSecuritySHA256: sum(lib), NginxOrg: srv.URL + "/nginx-%s.tar.gz", Client: srv.Client()}

	m.Respond = m.respond
	return m
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func (m *machine) path(p string) string { return filepath.Join(m.Base, strings.TrimPrefix(p, "/")) }

func (m *machine) exists(p string) bool { _, err := os.Lstat(m.path(p)); return err == nil }

// active nginx configuration: the load_module lines of modules-enabled.
func (m *machine) dumpT() string {
	var b strings.Builder
	files, _ := filepath.Glob(m.path("/etc/nginx/modules-enabled/*.conf"))
	for _, f := range files {
		c, _ := os.ReadFile(f)
		b.Write(c)
	}
	return b.String()
}

func (m *machine) configBroken() bool {
	return m.badConfig && m.exists("/etc/nginx/modules-enabled/50-"+loadName)
}

func arg(args []string, prefix string) string {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return strings.TrimPrefix(a, prefix)
		}
	}
	return ""
}

func (m *machine) respond(id string, args []string) (string, error, bool) {
	key := id + " " + strings.Join(args, " ")
	fail := func(msg string) (string, error, bool) { return msg, errors.New("exit status 1"), true }
	switch {
	case id == "nginx" && len(args) == 1 && args[0] == "-V":
		c := "--prefix=/usr/share/nginx --conf-path=/etc/nginx/nginx.conf --modules-path=/usr/lib/nginx/modules"
		if m.compat {
			c += " --with-compat"
		}
		return "nginx version: nginx/1.24.0 (Ubuntu)\nconfigure arguments: " + c + "\n", nil, true
	case id == "nginx" && len(args) == 1 && args[0] == "-T":
		if m.configBroken() {
			return fail("nginx: [emerg] broken")
		}
		return m.dumpT(), nil, true
	case id == "nginx" && len(args) == 1 && args[0] == "-t":
		if m.configBroken() {
			return fail("nginx: [emerg] broken")
		}
		return "", nil, true
	case id == "nginx" && args[0] == "-t":
		if m.loadErr != "" {
			return fail(m.loadErr)
		}
		return "", nil, true
	case id == "systemctl" && args[0] == "reload":
		m.reloads++
		if m.failReload {
			return fail("Job for nginx.service failed")
		}
		return "", nil, true
	case id == "dpkg-query" && args[0] == "-S":
		if strings.HasSuffix(args[len(args)-1], "/usr/sbin/nginx") && m.nginxOwner != "" {
			return m.nginxOwner + ": /usr/sbin/nginx\n", nil, true
		}
		if strings.Contains(args[len(args)-1], "/usr/lib/nginx/modules/ngx_http_modsecurity_module.so") && m.installed["libnginx-mod-http-modsecurity"] {
			return "libnginx-mod-http-modsecurity: x\n", nil, true
		}
		return fail("no path found")
	case id == "dpkg-query" && strings.Contains(key, "source:Package"):
		return "nginx\t1.24.0-2ubuntu7", nil, true
	case id == "dpkg-query" && args[0] == "-W":
		var b strings.Builder
		for _, n := range args[3:] {
			if m.installed[n] {
				fmt.Fprintf(&b, "%s\tii \t1.0\n", n)
			}
		}
		return b.String(), nil, true
	case id == "apt-get" && args[0] == "-s" && args[1] == "remove":
		var b strings.Builder
		for _, n := range args[3:] {
			fmt.Fprintf(&b, "Remv %s [1.0]\n", n)
		}
		return b.String(), nil, true
	case id == "apt-get" && args[0] == "-s":
		if m.simRemovesNG {
			return "Remv nginx [1.27.2-1~noble]\nInst nginx-common (1.24.0)\n", nil, true
		}
		return "Inst libnginx-mod-http-modsecurity (1.0.3-1build3 Ubuntu:24.04/noble [amd64])\n", nil, true
	case id == "apt-get" && contains(args, "install"):
		names := args[len(args)-1:]
		for i, a := range args {
			if a == "--" {
				names = args[i+1:]
			}
		}
		for _, n := range names {
			if m.failInstall[n] {
				return fail("E: Unable to locate package " + n)
			}
		}
		for _, n := range names {
			m.installed[n] = true
			if n == "libnginx-mod-http-modsecurity" {
				os.WriteFile(m.path("/usr/lib/nginx/modules/ngx_http_modsecurity_module.so"), []byte("ELF"), 0o644)
				os.WriteFile(m.path("/etc/nginx/modules-enabled/50-mod-http-modsecurity.conf"), []byte("load_module modules/ngx_http_modsecurity_module.so;\n"), 0o644)
				n = "libmodsecurity3"
				m.installed[n] = true
			}
			if n == "libmodsecurity3" || n == "libmodsecurity-dev" {
				os.WriteFile(m.path("/usr/lib/x86_64-linux-gnu/libmodsecurity.so.3"), []byte("ELF"), 0o644)
			}
		}
		return "", nil, true
	case id == "apt-get" && contains(args, "remove"):
		m.pkgRemoves++
		for i, a := range args {
			if a == "--" {
				for _, n := range args[i+1:] {
					delete(m.installed, n)
					if n == "libnginx-mod-http-modsecurity" {
						os.Remove(m.path("/usr/lib/nginx/modules/ngx_http_modsecurity_module.so"))
						os.Remove(m.path("/etc/nginx/modules-enabled/50-mod-http-modsecurity.conf"))
					}
				}
			}
		}
		return "", nil, true
	case id == "apt-get" && contains(args, "source"):
		lists := arg(args, "Dir::State::Lists=")
		dl := filepath.Join(filepath.Dir(lists), "dl")
		os.WriteFile(filepath.Join(dl, "nginx_1.24.0.orig.tar.gz"), []byte("orig"), 0o644)
		return "", nil, true
	case id == "tar":
		dest := args[len(args)-1]
		switch {
		case strings.Contains(args[1], "ModSecurity-nginx"):
			os.MkdirAll(filepath.Join(dest, "ModSecurity-nginx-v1.0.4"), 0o755)
			os.WriteFile(filepath.Join(dest, "ModSecurity-nginx-v1.0.4/config"), nil, 0o644)
		case strings.Contains(args[1], "modsecurity-v"):
			os.MkdirAll(filepath.Join(dest, "modsecurity-v3.0.14"), 0o755)
			os.WriteFile(filepath.Join(dest, "modsecurity-v3.0.14/configure"), nil, 0o644)
		default:
			os.MkdirAll(filepath.Join(dest, "nginx-1.24.0"), 0o755)
			os.WriteFile(filepath.Join(dest, "nginx-1.24.0/configure"), nil, 0o644)
		}
		return "", nil, true
	case id == "make" && m.failMake:
		return fail("src/ngx_http_modsecurity_module.c:42: error: unknown type")
	case id == "make" && contains(args, "modules"):
		// The module lands in objs/ of the nginx tree (found via tar above).
		dirs, _ := filepath.Glob(m.path("/var/lib/whacp-build/*/nginx-1.24.0"))
		for _, d := range dirs {
			os.MkdirAll(filepath.Join(d, "objs"), 0o755)
			content := "ELF"
			if rp, _ := os.ReadFile(filepath.Join(d, "rpath")); len(rp) > 0 {
				content = "RPATH=" + string(rp)
			}
			os.WriteFile(filepath.Join(d, "objs/ngx_http_modsecurity_module.so"), []byte(content), 0o644)
		}
		return "", nil, true
	case id == "make" && contains(args, "install"):
		stage := arg(args, "DESTDIR=")
		libs, _ := filepath.Glob(filepath.Join(stage, "usr/local/lib/whacp/modsecurity/*"))
		if len(libs) == 0 {
			// the prefix was given to configure; find it
			return "", nil, true
		}
		return "", nil, true
	case id == "sh" && contains(args, "./configure"):
		if ld := arg(args, "--with-ld-opt=-Wl,-rpath,"); ld != "" {
			dirs, _ := filepath.Glob(m.path("/var/lib/whacp-build/*/nginx-1.24.0"))
			for _, d := range dirs {
				os.WriteFile(filepath.Join(d, "rpath"), []byte(ld), 0o644)
			}
		}
		if prefix := arg(args, "--prefix="); prefix != "" {
			// libmodsecurity: "make install DESTDIR=" puts it in the stage.
			stages, _ := filepath.Glob(m.path("/var/lib/whacp-build/*"))
			for _, s := range stages {
				lib := filepath.Join(s, "stage", strings.TrimPrefix(prefix, "/"), "lib")
				os.MkdirAll(lib, 0o755)
				os.MkdirAll(filepath.Join(s, "stage", strings.TrimPrefix(prefix, "/"), "include"), 0o755)
				os.WriteFile(filepath.Join(lib, "libmodsecurity.so.3.0.14"), []byte("ELF-lib"), 0o755)
				target := "libmodsecurity.so.3.0.14"
				if m.libEscapes {
					os.WriteFile(m.path("/etc/shadow"), []byte("root:$6$secret"), 0o600)
					target = m.path("/etc/shadow")
				}
				os.Symlink(target, filepath.Join(lib, "libmodsecurity.so.3"))
			}
		}
		return "", nil, true
	}
	return "", nil, false
}

func (m *machine) install(opt InstallOptions) (map[string]any, error) {
	return Install(context.Background(), m.Env, m.src, opt, io.Discard)
}

// state is what must come back after a failed installation.
func (m *machine) state() string {
	var b strings.Builder
	for _, root := range []string{"etc/nginx", "usr/local/lib/whacp", "usr/lib/nginx/modules"} {
		filepath.Walk(filepath.Join(m.Base, root), func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			c, _ := os.ReadFile(p)
			fmt.Fprintf(&b, "%s %v %q\n", strings.TrimPrefix(p, m.Base), info.Mode(), c)
			return nil
		})
	}
	var pk []string
	for n := range m.installed {
		pk = append(pk, n)
	}
	fmt.Fprintf(&b, "packages %d %v\n", len(pk), strings.Join(sorted(pk), ","))
	return b.String()
}

func sorted(s []string) []string {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	return s
}

func lastTx(t *testing.T, m *machine) *txn.Tx {
	t.Helper()
	all := txn.List(m.Env, TxKind)
	if len(all) == 0 {
		t.Fatal("no transaction")
	}
	return all[0]
}

// Distribution package, when it is safe: installed, validated, recorded.
func TestInstallViaPackage(t *testing.T) {
	m := newMachine(t)
	res, err := m.install(InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res["method"] != "package" || !m.installed["libnginx-mod-http-modsecurity"] || m.reloads != 1 {
		t.Fatalf("%v %v", res, m.installed)
	}
	tx := lastTx(t, m)
	if tx.Status != txn.Committed || len(tx.Snapshots) == 0 {
		t.Fatalf("%+v", tx)
	}
	var pk []string
	for _, c := range tx.Changes {
		if c.Op == "package" {
			pk = append(pk, c.Name)
		}
	}
	if strings.Join(sorted(pk), ",") != "libmodsecurity3,libnginx-mod-http-modsecurity" {
		t.Fatalf("recorded packages %v", pk)
	}
	// Repeated: nothing to do, no new transaction.
	res, err = m.install(InstallOptions{})
	if err != nil || res["changed"] != false || len(txn.List(m.Env, TxKind)) != 1 {
		t.Fatalf("second run: %v %v", res, err)
	}
	// A package's connector is upgraded by the package manager, not here.
	if _, err := m.install(InstallOptions{Upgrade: true}); err == nil || !strings.Contains(err.Error(), "package manager") {
		t.Fatalf("%v", err)
	}
}

// The package would replace Nginx (e.g. nginx.org's): built from source
// instead, against the installed version's source package, with the
// library from its package; new versioned files only.
func TestInstallFromSourceWhenThePackageWouldTouchNginx(t *testing.T) {
	m := newMachine(t)
	m.simRemovesNG = true
	res, err := m.install(InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res["method"] != "source" || m.installed["libnginx-mod-http-modsecurity"] {
		t.Fatalf("%v", res)
	}
	mod := res["module"].(string)
	if !strings.HasPrefix(mod, ModuleDir+"/ngx_http_modsecurity_module-1.24.0-") || !m.exists(mod) {
		t.Fatalf("module %s", mod)
	}
	load := m.Read("/etc/nginx/modules-enabled/50-" + loadName)
	if !strings.Contains(load, "load_module "+mod+";") {
		t.Fatalf("load file: %q", load)
	}
	if !m.installed["libmodsecurity-dev"] || !m.installed["gcc"] {
		t.Fatal("build dependencies")
	}
	// The build account and folder are gone again.
	if _, err := m.Env.LookupUser(BuildAccount); err == nil || !m.Runner.Called("userdel --remove "+BuildAccount) {
		t.Fatal("build account left")
	}
	// Sources recorded; the source package came through a deb-src list.
	inst := Installs(m.Env)[0]
	if b := fmt.Sprint(inst["sources"]); !strings.Contains(b, "source package nginx (apt)") || !strings.Contains(b, "ModSecurity-nginx") {
		t.Fatalf("sources: %s", b)
	}
	if inst["can_roll_back"] != true {
		t.Fatalf("%v", inst)
	}
	// The package dry run was consulted and nginx never installed.
	for _, c := range m.Runner.Calls {
		if strings.HasPrefix(c, "apt-get install") && strings.Contains(c, " nginx") && !strings.Contains(c, "libnginx") {
			t.Fatalf("installed nginx: %s", c)
		}
	}
}

// libmodsecurity has no package: it is built too, installed versioned and
// found through the module's RUNPATH.
func TestInstallBuildsLibmodsecurityWhenNoPackage(t *testing.T) {
	m := newMachine(t)
	m.simRemovesNG = true
	m.failInstall["libmodsecurity-dev"] = true
	res, err := m.install(InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	libs, _ := filepath.Glob(m.path(LibRoot + "/3.0.14-*/lib/libmodsecurity.so.3"))
	if len(libs) != 1 {
		t.Fatalf("library: %v", libs)
	}
	if b, _ := os.ReadFile(libs[0]); string(b) != "ELF-lib" {
		t.Fatal("the library is a copy of the built file, not a link")
	}
	if !strings.Contains(fmt.Sprint(Installs(m.Env)[0]["sources"]), "libmodsecurity") || res["method"] != "source" {
		t.Fatal("libmodsecurity source not recorded")
	}
}

// Every failure puts everything back as it was: files, packages, account.
func TestFailuresRollBackEverything(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*machine)
		want  string
	}{
		{"failed build", func(m *machine) { m.failMake = true }, "make"},
		{"failed dependency", func(m *machine) { m.failInstall["libssl-dev"] = true }, "build dependencies"},
		{"library does not resolve", func(m *machine) { m.missingLib = true }, "libyajl.so.2"},
		{"module does not load", func(m *machine) { m.loadErr = "module is not binary compatible" }, "not binary compatible"},
		{"invalid nginx configuration", func(m *machine) { m.badConfig = true }, "nginx -t failed"},
		{"reload fails", func(m *machine) { m.failReload = true }, "could not be reloaded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newMachine(t)
			m.simRemovesNG = true
			before := m.state()
			c.setup(m)
			_, err := m.install(InstallOptions{})
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "rolled back") {
				t.Fatalf("%v", err)
			}
			m.failReload = false
			if after := m.state(); after != before {
				t.Fatalf("not restored:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if _, err := m.Env.LookupUser(BuildAccount); err == nil {
				t.Fatal("build account left")
			}
			if tx := lastTx(t, m); tx.Status != txn.RolledBack || tx.Error == "" {
				t.Fatalf("%+v", tx)
			}
			// Safe to retry once the cause is gone.
			m.failMake, m.missingLib, m.loadErr, m.badConfig = false, false, "", false
			delete(m.failInstall, "libssl-dev")
			if _, err := m.install(InstallOptions{}); err != nil {
				t.Fatalf("retry: %v", err)
			}
		})
	}
}

// The package route fails validation (its module does not load): it is
// rolled back and the source route follows.
func TestPackageThatDoesNotWorkFallsBackToSource(t *testing.T) {
	m := newMachine(t)
	m.badConfig = false
	m.loadErr = ""
	// The package's module does not resolve its library: remove the lib
	// the package would install.
	m.failInstall["libmodsecurity3"] = false
	res, err := func() (map[string]any, error) {
		orig := m.Respond
		m.Respond = func(id string, args []string) (string, error, bool) {
			out, err, ok := orig(id, args)
			if id == "apt-get" && contains(args, "libnginx-mod-http-modsecurity") && contains(args, "install") && !contains(args, "-s") {
				os.Remove(m.path("/usr/lib/x86_64-linux-gnu/libmodsecurity.so.3"))
			}
			return out, err, ok
		}
		return m.install(InstallOptions{})
	}()
	if err != nil {
		t.Fatal(err)
	}
	if res["method"] != "source" || m.installed["libnginx-mod-http-modsecurity"] {
		t.Fatalf("%v %v", res, m.installed)
	}
	all := txn.List(m.Env, TxKind)
	if len(all) != 2 || all[1].Status != txn.RolledBack || all[0].Status != txn.Committed {
		t.Fatalf("%d transactions: %s %s", len(all), all[0].Status, all[1].Status)
	}
}

// Interrupted mid-way (the agent stopped): the next start rolls it back,
// and a new installation then proceeds normally.
func TestInterruptedInstallIsRolledBack(t *testing.T) {
	m := newMachine(t)
	m.simRemovesNG = true
	before := m.state()
	tx, _ := txn.Begin(m.Env, TxKind, io.Discard)
	tx.MkdirAll(ModuleDir, 0o755, agentfs.RootOwner)
	tx.WriteFile("/etc/nginx/modules-enabled/50-"+loadName, []byte("load_module /half.so;\n"), 0o644, agentfs.RootOwner)
	// (the agent stopped here)
	lines := RecoverInstalls(context.Background(), m.Env, io.Discard)
	if len(lines) != 1 || m.state() != before {
		t.Fatalf("%v\n%s", lines, m.state())
	}
	if rec, _ := txn.Load(m.Env, TxKind, tx.ID); rec.Status != txn.RolledBack || !strings.Contains(rec.Error, "interrupted") {
		t.Fatalf("%+v", rec)
	}
	// An installation left running is also recovered by the next install.
	tx2, _ := txn.Begin(m.Env, TxKind, io.Discard)
	tx2.WriteFile("/etc/nginx/modules-enabled/50-"+loadName, []byte("load_module /half.so;\n"), 0o644, agentfs.RootOwner)
	if _, err := m.install(InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if rec, _ := txn.Load(m.Env, TxKind, tx2.ID); rec.Status != txn.RolledBack {
		t.Fatalf("%+v", rec)
	}
}

// Upgrading a working source build keeps the previous module; rolling back
// the upgrade makes it the one in effect again.
func TestUpgradeAndManualRollback(t *testing.T) {
	m := newMachine(t)
	m.simRemovesNG = true
	first, err := m.install(InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	loadV1 := m.Read("/etc/nginx/modules-enabled/50-" + loadName)
	second, err := m.install(InstallOptions{Upgrade: true})
	if err != nil {
		t.Fatal(err)
	}
	if second["module"] == first["module"] || !m.exists(first["module"].(string)) {
		t.Fatalf("the previous module must stay: %v %v", first, second)
	}
	// Only the newest can be rolled back.
	if _, err := RollbackInstall(context.Background(), m.Env, first["transaction"].(string), io.Discard); err == nil || !strings.Contains(err.Error(), "later installation") {
		t.Fatalf("%v", err)
	}
	// A rollback whose result fails nginx -t changes nothing.
	m.badConfig = true
	loadV2 := m.Read("/etc/nginx/modules-enabled/50-" + loadName)
	m.badConfig = false
	res, err := RollbackInstall(context.Background(), m.Env, second["transaction"].(string), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Read("/etc/nginx/modules-enabled/50-" + loadName); got != loadV1 || got == loadV2 {
		t.Fatalf("load file after rollback: %q", got)
	}
	if m.exists(second["module"].(string)) {
		t.Fatal("the rolled-back module was left")
	}
	if rec, _ := txn.Load(m.Env, TxKind, second["transaction"].(string)); rec.Status != txn.RolledBack {
		t.Fatalf("%+v", rec)
	}
	// The first installation is in effect again, and can itself be rolled back.
	var eff map[string]any
	for _, i := range Installs(m.Env) {
		if i["can_roll_back"] == true {
			eff = i
		}
	}
	if eff == nil || eff["id"] != first["transaction"] || res["rolled_back"] != second["transaction"] {
		t.Fatalf("in effect: %v", eff)
	}
}

// A rollback that would break Nginx is itself undone.
func TestRollbackThatBreaksNginxChangesNothing(t *testing.T) {
	m := newMachine(t)
	m.simRemovesNG = true
	res, err := m.install(InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st := Load(m.Env)
	st.Config.Enabled = true
	b, _ := json.Marshal(st)
	os.WriteFile(m.path(statePath(m.Env)), b, 0o600)
	// Without the connector the configuration would fail (ModSecurity on).
	orig := m.Respond
	m.Respond = func(id string, args []string) (string, error, bool) {
		if id == "nginx" && len(args) == 1 && args[0] == "-t" && !m.exists("/etc/nginx/modules-enabled/50-"+loadName) {
			return "nginx: [emerg] unknown directive \"modsecurity\"", errors.New("exit status 1"), true
		}
		return orig(id, args)
	}
	before := m.state()
	if _, err := RollbackInstall(context.Background(), m.Env, res["transaction"].(string), io.Discard); err == nil || !strings.Contains(err.Error(), "turn it off") {
		t.Fatalf("%v", err)
	}
	if m.state() != before {
		t.Fatal("the failed rollback changed something")
	}
}

// Existing working connector: nothing is rebuilt. Present but not loaded
// and loadable: only the load_module line is added.
func TestExistingInstallations(t *testing.T) {
	m := newMachine(t)
	os.WriteFile(m.path("/usr/lib/x86_64-linux-gnu/libmodsecurity.so.3"), []byte("ELF"), 0o644)
	os.MkdirAll(m.path("/opt/modsec"), 0o755)
	os.WriteFile(m.path("/usr/lib/nginx/modules/ngx_http_modsecurity_module.so"), []byte("ELF"), 0o644)
	res, err := m.install(InstallOptions{})
	if err != nil || res["method"] != "enable" {
		t.Fatalf("%v %v", res, err)
	}
	if !strings.Contains(m.Read("/etc/nginx/modules-enabled/50-"+loadName), "/usr/lib/nginx/modules/ngx_http_modsecurity_module.so") {
		t.Fatal("load line")
	}
	calls := len(m.Runner.Calls)
	res, err = m.install(InstallOptions{})
	if err != nil || res["changed"] != false {
		t.Fatalf("%v %v", res, err)
	}
	for _, c := range m.Runner.Calls[calls:] {
		if strings.HasPrefix(c, "apt-get install") || strings.HasPrefix(c, "make") {
			t.Fatalf("rebuilt a working connector: %s", c)
		}
	}
}

// Refusals before any change.
func TestRefusals(t *testing.T) {
	m := newMachine(t)
	m.simRemovesNG = true
	m.compat = false
	if _, err := m.install(InstallOptions{}); err == nil || !strings.Contains(err.Error(), "--with-compat") {
		t.Fatalf("%v", err)
	}
	m.compat = true
	m.nginxOwner = ""
	if _, err := m.install(InstallOptions{Method: "source"}); err == nil || !strings.Contains(err.Error(), "SHA-256 of nginx-1.24.0.tar.gz") {
		t.Fatalf("%v", err)
	}
	// With the checksum of nginx.org's release it builds.
	if _, err := m.install(InstallOptions{Method: "source", NginxSourceSHA256: sum([]byte("nginx-org-tarball"))}); err != nil {
		t.Fatal(err)
	}
	m2 := newMachine(t)
	m2.nginxOwner = ""
	if _, err := m2.install(InstallOptions{Method: "source", NginxSourceSHA256: strings.Repeat("0", 64)}); err == nil || !strings.Contains(err.Error(), "does not match its checksum") {
		t.Fatalf("%v", err)
	}
	// A configuration that is already broken is not touched.
	m3 := newMachine(t)
	orig := m3.Respond
	m3.Respond = func(id string, args []string) (string, error, bool) {
		if id == "nginx" && len(args) == 1 && args[0] == "-T" {
			return "nginx: [emerg] something else", errors.New("exit status 1"), true
		}
		return orig(id, args)
	}
	if _, err := m3.install(InstallOptions{}); err == nil || !strings.Contains(err.Error(), "fix it first") || len(txn.List(m3.Env, TxKind)) != 0 {
		t.Fatalf("%v", err)
	}
}

func TestDebSrc(t *testing.T) {
	if got := DebSrc("deb http://a/ubuntu noble main\n# deb http://b noble main\ndeb-src http://c noble main\n", false); got != "deb-src http://a/ubuntu noble main\n" {
		t.Fatalf("%q", got)
	}
	got := DebSrc("Types: deb\nURIs: http://a/\nSuites: noble\nComponents: main\nSigned-By: /k.gpg\n\nTypes: deb\nURIs: http://off/\nEnabled: no\nSuites: x\n", true)
	if !strings.Contains(got, "Types: deb-src\nURIs: http://a/") || !strings.Contains(got, "Signed-By: /k.gpg") || strings.Contains(got, "off") {
		t.Fatalf("%q", got)
	}
}

// The build account is only WhaCP's own, and what it built is read without
// following links out of the build folder.
func TestBuildAccountAndOutputsAreNotTrusted(t *testing.T) {
	m := newMachine(t)
	m.simRemovesNG = true
	appendPasswd := func(line string) {
		f, _ := os.OpenFile(m.path("/etc/passwd"), os.O_APPEND|os.O_WRONLY, 0o644)
		f.WriteString(line)
		f.Close()
	}
	// An account of that name that is not WhaCP's (e.g. a website's).
	appendPasswd(fmt.Sprintf("%s:x:1700:1700:WhaCP website evil.test:/home/whacp/evil.test:/usr/sbin/nologin\n", BuildAccount))
	before := m.state()
	if _, err := m.install(InstallOptions{}); err == nil || !strings.Contains(err.Error(), "WhaCP did not create") {
		t.Fatalf("%v", err)
	}
	if m.state() != before {
		t.Fatal("not rolled back")
	}

	m2 := newMachine(t)
	m2.simRemovesNG = true
	m2.failInstall["libmodsecurity-dev"] = true // libmodsecurity is built
	m2.libEscapes = true
	if _, err := m2.install(InstallOptions{}); err == nil || !strings.Contains(err.Error(), "outside the build folder") {
		t.Fatalf("%v", err)
	}
	libs, _ := filepath.Glob(m2.path(LibRoot + "/*/lib/*"))
	if len(libs) != 0 {
		t.Fatalf("installed %v", libs)
	}
}
