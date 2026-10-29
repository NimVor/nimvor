package phpmgr

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// ionCubeArchive builds what ionCube publishes: ioncube/ioncube_loader_lin_<v>.so
// for every version (plus thread-safe variants and other files), and an
// entry that tries to escape the folder.
func ionCubeArchive(t *testing.T, versions ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	add := func(name string, body []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(body)
	}
	add("ioncube/LICENSE.txt", []byte("licence"))
	add("../../etc/ioncube_loader_lin_8.3.so", []byte("\x7fELF-evil"))
	for _, v := range versions {
		add("ioncube/ioncube_loader_lin_"+v+".so", []byte("\x7fELF"+strings.Repeat("x", 100)+v))
		add("ioncube/ioncube_loader_lin_"+v+"_ts.so", []byte("\x7fELF-ts"+strings.Repeat("y", 100)))
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func serveIonCube(t *testing.T, body []byte, status int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			http.Error(w, "no", status)
			return
		}
		w.Write(body)
	}))
	old := ionCubeSource
	ionCubeSource = IonCubeSource{URL: func(string) string { return srv.URL }, Client: srv.Client()}
	t.Cleanup(func() { ionCubeSource = old; srv.Close() })
}

func ionCubeHost(t *testing.T, banner bool) (*testhost.Host, *actions.Registry) {
	t.Helper()
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "php-fpm8.3" && (len(args) == 4 && args[0] == "-n" && args[1] == "-d" || len(args) == 1 && args[0] == "-v") {
			if banner {
				return "PHP 8.3.6 (fpm-fcgi)\n    with the ionCube PHP Loader v14.4.0, by ionCube Ltd.\n", nil, true
			}
			return "PHP 8.3.6 (fpm-fcgi)\n", nil, true
		}
		return "", nil, false
	}
	return h, r
}

func TestIonCubeInstallDebian(t *testing.T) {
	serveIonCube(t, ionCubeArchive(t, "8.0", "8.1", "8.2", "8.3", "8.4"), http.StatusOK)
	h, r := ionCubeHost(t, true)

	res, err := call(t, r, "php.ioncube_install", map[string]any{"version": "8.3"})
	if err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, h, res); j.Status != jobs.Succeeded {
		t.Fatalf("job %s: %s", j.Status, j.Error)
	}
	so := h.Read("usr/local/lib/nimvor/ioncube/8.3/ioncube_loader_lin_8.3.so")
	if !strings.HasPrefix(so, "\x7fELF") || !strings.HasSuffix(so, "8.3") {
		t.Errorf("loader file: %.20q", so)
	}
	if got := h.Read("etc/php/8.3/mods-available/ioncube.ini"); !strings.Contains(got, "priority=00") || !strings.Contains(got, "zend_extension = /usr/local/lib/nimvor/ioncube/8.3/ioncube_loader_lin_8.3.so") {
		t.Errorf("ini: %q", got)
	}
	if h.Exists("etc/ioncube_loader_lin_8.3.so") || h.Exists("../../etc/ioncube_loader_lin_8.3.so") {
		t.Error("an archive entry outside the folder was used")
	}
	for _, c := range []string{"php-fpm8.3 -n -d zend_extension=/usr/local/lib/nimvor/ioncube/8.3/ioncube_loader_lin_8.3.so -v", "phpenmod -v 8.3 ioncube", "php-fpm8.3 -t"} {
		if !h.Runner.Called(c) {
			t.Errorf("not run: %s (calls %v)", c, h.Runner.Calls)
		}
	}
	// It is listed as installed, vendor-supplied, and can be switched.
	states, _ := call(t, r, "php.extensions", map[string]any{"version": "8.3"})
	var got ExtensionState
	for _, s := range states.([]ExtensionState) {
		if s.Name == "ioncube" {
			got = s
		}
	}
	if !got.Vendor || !got.Installed || got.Package != "" {
		t.Errorf("state = %+v", got)
	}
	if _, err := call(t, r, "php.ioncube_install", map[string]any{"version": "8.3"}); err == nil {
		t.Error("installed twice")
	}
	if _, err := call(t, r, "php.extension_set", map[string]any{"version": "8.3", "name": "ioncube", "enabled": false}); err != nil || !h.Runner.Called("phpdismod -v 8.3 ioncube") {
		t.Errorf("switch off: %v %v", err, h.Runner.Calls)
	}

	// Remove.
	res, err = call(t, r, "php.ioncube_remove", map[string]any{"version": "8.3"})
	if err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, h, res); j.Status != jobs.Succeeded {
		t.Fatalf("remove %s: %s", j.Status, j.Error)
	}
	if h.Exists("usr/local/lib/nimvor/ioncube/8.3/ioncube_loader_lin_8.3.so") || h.Exists("etc/php/8.3/mods-available/ioncube.ini") {
		t.Error("files left after removal")
	}
	if _, err := call(t, r, "php.ioncube_remove", map[string]any{"version": "8.3"}); err == nil {
		t.Error("removed what is not installed")
	}
}

// A PHP version the archive has no loader for is explained, and nothing is
// written.
func TestIonCubeVersionNotCovered(t *testing.T) {
	serveIonCube(t, ionCubeArchive(t, "8.0", "8.1"), http.StatusOK)
	h, r := ionCubeHost(t, true)
	res, err := call(t, r, "php.ioncube_install", map[string]any{"version": "8.3"})
	if err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, h, res)
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "no loader for PHP 8.3") || !strings.Contains(j.Error, "8.0, 8.1") {
		t.Fatalf("job %s: %s", j.Status, j.Error)
	}
	if h.Exists("etc/php/8.3/mods-available/ioncube.ini") || h.Exists("usr/local/lib/nimvor/ioncube") {
		t.Error("something was written")
	}
}

func TestIonCubeDownloadProblems(t *testing.T) {
	serveIonCube(t, nil, http.StatusNotFound)
	h, r := ionCubeHost(t, true)
	res, _ := call(t, r, "php.ioncube_install", map[string]any{"version": "8.3"})
	if j := waitJob(t, h, res); j.Status != jobs.Failed || !strings.Contains(j.Error, "HTTP 404") {
		t.Fatalf("404: %s %s", j.Status, j.Error)
	}
	serveIonCube(t, []byte("this is not an archive"), http.StatusOK)
	res, _ = call(t, r, "php.ioncube_install", map[string]any{"version": "8.3"})
	if j := waitJob(t, h, res); j.Status != jobs.Failed || !strings.Contains(j.Error, "not a compressed archive") {
		t.Fatalf("garbage: %s %s", j.Status, j.Error)
	}
}

// PHP did not load the loader (no ionCube banner): nothing is kept.
func TestIonCubeMustLoad(t *testing.T) {
	serveIonCube(t, ionCubeArchive(t, "8.3"), http.StatusOK)
	h, r := ionCubeHost(t, false)
	res, _ := call(t, r, "php.ioncube_install", map[string]any{"version": "8.3"})
	if j := waitJob(t, h, res); j.Status != jobs.Failed || !strings.Contains(j.Error, "did not load the ionCube") {
		t.Fatalf("%s %s", j.Status, j.Error)
	}
	if h.Exists("etc/php/8.3/mods-available/ioncube.ini") || h.Exists("usr/local/lib/nimvor/ioncube/8.3/ioncube_loader_lin_8.3.so") {
		t.Error("files kept although PHP did not load the loader")
	}
	if h.Runner.Called("phpenmod -v 8.3 ioncube") {
		t.Error("switched on")
	}
}

// PHP-FPM rejects the result: everything is put back.
func TestIonCubeUndoneWhenFPMRejects(t *testing.T) {
	serveIonCube(t, ionCubeArchive(t, "8.3"), http.StatusOK)
	h, r := ionCubeHost(t, true)
	h.Fail["php-fpm8.3 -t"] = "ERROR: failed"
	res, _ := call(t, r, "php.ioncube_install", map[string]any{"version": "8.3"})
	j := waitJob(t, h, res)
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "removed again") {
		t.Fatalf("%s %s", j.Status, j.Error)
	}
	if !h.Runner.Called("phpdismod -v 8.3 ioncube") || h.Exists("etc/php/8.3/mods-available/ioncube.ini") || h.Exists("usr/local/lib/nimvor/ioncube/8.3/ioncube_loader_lin_8.3.so") {
		t.Errorf("not undone: %v", h.Runner.Calls)
	}
}

// RHEL (Remi): the loader is loaded from 00-ioncube.ini in php.d.
func TestIonCubeInstallRHEL(t *testing.T) {
	serveIonCube(t, ionCubeArchive(t, "8.3"), http.StatusOK)
	h, r := ionCubeHost(t, true)
	write(t, h, "etc/os-release", "ID=\"almalinux\"\nVERSION_ID=\"9.6\"\n")
	h.Env.Platform = platform.Detect(h.Env.Root)
	h.Env.M = h.Env.Platform.Managed()
	write(t, h, "etc/opt/remi/php83/php.d/20-opcache.ini", "zend_extension=opcache\n") // PHP 8.3's own php.d
	h.Respond = func(id string, args []string) (string, error, bool) {
		if strings.HasPrefix(id, "php-fpm") && (len(args) == 4 && args[0] == "-n" || len(args) == 1 && args[0] == "-v") {
			return "PHP 8.3.6\n    with the ionCube PHP Loader v14\n", nil, true
		}
		return "", nil, false
	}
	res, err := call(t, r, "php.ioncube_install", map[string]any{"version": "8.3"})
	if err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, h, res); j.Status != jobs.Succeeded {
		t.Fatalf("%s %s", j.Status, j.Error)
	}
	if got := h.Read("etc/opt/remi/php83/php.d/00-ioncube.ini"); !strings.Contains(got, "zend_extension = /usr/local/lib/nimvor/ioncube/8.3/ioncube_loader_lin_8.3.so") {
		t.Errorf("ini: %q", got)
	}
	if !moduleEnabled(h.Env, "8.3", "ioncube") {
		t.Error("not reported enabled")
	}
	if _, err := call(t, r, "php.extension_set", map[string]any{"version": "8.3", "name": "ioncube", "enabled": false}); err != nil {
		t.Fatal(err)
	}
	if moduleEnabled(h.Env, "8.3", "ioncube") {
		t.Error("still enabled after switching off")
	}
}

func TestIonCubeRejectsBadVersion(t *testing.T) {
	_, r := ionCubeHost(t, true)
	for _, v := range []string{"8.3;x", "", "../8.3"} {
		if _, err := call(t, r, "php.ioncube_install", map[string]any{"version": v}); err == nil {
			t.Errorf("accepted %q", v)
		}
	}
}

// The loader loads on its own but, with the server's real configuration, something else is loaded before
// it (ionCube refuses to start then): everything is put back.
func TestIonCubeUndoneWhenNotFirst(t *testing.T) {
	serveIonCube(t, ionCubeArchive(t, "8.3"), http.StatusOK)
	h, r := ionCubeHost(t, true)
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id != "php-fpm8.3" {
			return "", nil, false
		}
		if len(args) == 4 && args[0] == "-n" {
			return "PHP 8.3.6 (fpm-fcgi)\n    with the ionCube PHP Loader v14.4.0, by ionCube Ltd.\n", nil, true
		}
		if len(args) == 1 && args[0] == "-v" {
			return "PHP 8.3.6 (fpm-fcgi)\n", nil, true
		}
		return "", nil, false
	}
	res, _ := call(t, r, "php.ioncube_install", map[string]any{"version": "8.3"})
	j := waitJob(t, h, res)
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "removed again") {
		t.Fatalf("%s %s", j.Status, j.Error)
	}
	if !h.Runner.Called("phpdismod -v 8.3 ioncube") || h.Exists("etc/php/8.3/mods-available/ioncube.ini") {
		t.Errorf("not undone: %v", h.Runner.Calls)
	}
}
