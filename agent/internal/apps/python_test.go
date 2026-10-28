package apps

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// Unit tests: pip, the project scaffold and systemd are simulated here (the
// real-host test installs for real). They prove the pinning, the ordering, the
// secret handling and the cleanup.

type pypiFixture struct {
	srv      *httptest.Server
	versions map[string][2]string // package -> {version, requires_python}
}

func newPyPI(t *testing.T) *pypiFixture {
	t.Helper()
	f := &pypiFixture{versions: map[string][2]string{
		"django": {"6.1.2", ">=3.12"}, "gunicorn": {"26.2.0", ">=3.10"}, "whitenoise": {"6.9.0", ">=3.9"},
		"fastapi": {"0.162.0", ">=3.10"}, "uvicorn": {"0.54.0", ">=3.10"},
	}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/pypi/"), "/json")
		v, ok := f.versions[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"info":{"version":%q,"requires_python":%q,"license_expression":"MIT"}}`, v[0], v[1])
	}))
	t.Cleanup(f.srv.Close)
	old := pypiAPI
	pypiAPI = f.srv.URL + "/pypi"
	t.Cleanup(func() { pypiAPI = old })
	return f
}

// withPython makes a system interpreter python3.12 exist in the test root.
func withPython(t *testing.T, h *testhost.Host, minor string) {
	t.Helper()
	dir := filepath.Join(h.Base, "usr/bin")
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, "python"+minor), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func djangoRequest() Request {
	return Request{App: "django", Domain: "py.test", User: "example", Root: "/home/nimvor/shop.test", Path: "django", URL: "https://py.test",
		Port: 3201, ServiceSlug: "py-django", Runtime: "python:3.12", AdminUser: "owner", AdminEmail: "owner@py.test", AdminPassword: "an-admin-password-12"}
}

func fastapiRequest() Request {
	return Request{App: "fastapi", Domain: "py.test", User: "example", Root: "/home/nimvor/shop.test", Path: "api", URL: "https://py.test",
		Port: 3202, ServiceSlug: "py-fastapi", Runtime: "python:3.12"}
}

func TestPythonSpecifiers(t *testing.T) {
	for spec, want := range map[string]map[string]bool{
		">=3.12":         {"3.11": false, "3.12": true, "3.13": true},
		">=3.10,<4":      {"3.9": false, "3.10": true, "3.14": true},
		">=3.9,!=3.9.1":  {"3.9": true, "3.8": false},
		">3.11":          {"3.11": false, "3.12": true},
		"<3.13":          {"3.12": true, "3.13": false},
		"":               {"3.6": true},
		">=3.8, <3.13":   {"3.13": false, "3.12": true},
		">=3.9,<=3.12.*": {"3.12": true},
	} {
		for ver, w := range want {
			got, err := pythonSatisfies(spec, ver)
			if err != nil || got != w {
				t.Errorf("%q on Python %s: %v %v, want %v", spec, ver, got, err, w)
			}
		}
	}
	for _, bad := range []string{"~=3.8", "==3.12.*", "garbage", ">=three"} {
		if _, err := pythonSatisfies(bad, "3.12"); err == nil {
			t.Errorf("%q must be refused, not guessed at", bad)
		}
	}
}

func TestPyPIOnlyYieldsStablePlainVersions(t *testing.T) {
	f := newPyPI(t)
	for _, v := range []string{"6.0rc1", "2.0.dev3", "1.0.post1", "1.0a1", "1", "../1.2", "1.2.3; rm"} {
		f.versions["django"] = [2]string{v, ""}
		if _, err := pypiLatest(context.Background(), f.srv.Client(), "django"); err == nil {
			t.Errorf("%q must not be installed", v)
		}
	}
	f.versions["django"] = [2]string{"5.2.1", ">=3.10"}
	if r, err := pypiLatest(context.Background(), f.srv.Client(), "django"); err != nil || r.Version != "5.2.1" {
		t.Errorf("%v %v", r, err)
	}
	if _, err := pypiLatest(context.Background(), f.srv.Client(), "no-such-package"); err == nil {
		t.Error("an unknown package is an error")
	}
}

func TestDjangoInstall(t *testing.T) {
	h, _ := setup(t)
	f := newPyPI(t)
	withPython(t, h, "3.12")
	health := stubHealth(t, nil)
	cred, _, _ := h.Env.Credential("example")
	var log strings.Builder
	req := djangoRequest()
	res, err := Install(context.Background(), h.Env, Sources{Client: f.srv.Client()}, req, cred, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if res["version"] != "6.1.2" || res["strategy"] != "python-venv" || res["admin_user"] != "owner" {
		t.Errorf("result: %+v", res)
	}
	pk := res["packages"].([]string)
	if strings.Join(pk, " ") != "django==6.1.2 gunicorn==26.2.0 whitenoise==6.9.0" {
		t.Errorf("packages not pinned: %v", pk)
	}
	calls := strings.Join(h.Runner.Calls, "\n")
	// Everything ran as the website account, in order, with pinned packages.
	for i, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "sh -c") {
			if o := h.Runner.Opts[i]; o.As == nil || o.As.UID == 0 {
				t.Errorf("%q did not run as the website account", c)
			}
		}
	}
	order := []string{"-m venv venv", "pip install --disable-pip-version-check --no-input --no-cache-dir 'django==6.1.2' 'gunicorn==26.2.0' 'whitenoise==6.9.0'", "pip freeze", "django-admin startproject config .", "cat > .env", "cat > config/settings.py", "manage.py migrate --noinput", "createsuperuser --noinput --username 'owner' --email 'owner@py.test'"}
	last := -1
	for _, want := range order {
		idx := strings.Index(calls, want)
		if idx < 0 {
			t.Errorf("no step %q in:\n%s", want, calls)
			continue
		}
		if idx < last {
			t.Errorf("step %q ran out of order", want)
		}
		last = idx
	}
	// Secrets: the key reaches only the .env file; the admin password only the
	// environment of the createsuperuser command; neither is in any command
	// line, setting file or log.
	stdins := strings.Join(h.Runner.Stdins, "\n---\n")
	if !strings.Contains(stdins, "DJANGO_SECRET_KEY=") {
		t.Fatal("the secret key was not written")
	}
	key := stdins[strings.Index(stdins, "DJANGO_SECRET_KEY=")+len("DJANGO_SECRET_KEY="):]
	key = strings.SplitN(key, "\n", 2)[0]
	if len(key) < 40 {
		t.Errorf("the secret key is too short: %d characters", len(key))
	}
	settings := h.Runner.Stdins[len(h.Runner.Stdins)-1]
	for _, leak := range []string{key, req.AdminPassword} {
		if strings.Contains(calls, leak) || strings.Contains(log.String(), leak) || strings.Contains(settings, leak) {
			t.Errorf("a secret leaked into a command line, the log or settings: %q", leak[:6])
		}
	}
	foundPw := false
	for _, o := range h.Runner.Opts {
		for _, e := range o.Env {
			if e == "DJANGO_SUPERUSER_PASSWORD="+req.AdminPassword {
				foundPw = true
			}
		}
	}
	if !foundPw {
		t.Error("the administrator password must reach createsuperuser through its environment")
	}
	for _, want := range []string{`ALLOWED_HOSTS = ["py.test"]`, `CSRF_TRUSTED_ORIGINS = ["https://py.test"]`, "DEBUG = False", "SESSION_COOKIE_SECURE = True", "whitenoise.middleware.WhiteNoiseMiddleware", "SECURE_PROXY_SSL_HEADER"} {
		if !strings.Contains(stdins, want) {
			t.Errorf("settings lack %q", want)
		}
	}
	unit := h.Read("etc/systemd/system/nimvor-worker-py-django.service")
	if !strings.Contains(unit, "User=example") {
		t.Errorf("unit:\n%s", unit)
	}
	svc := res["service"].(map[string]any)
	if !strings.Contains(svc["command"].(string), "gunicorn' config.wsgi:application --bind 127.0.0.1:3201") {
		t.Errorf("service command: %v", svc["command"])
	}
	if *health != 1 {
		t.Errorf("health check ran %d times", *health)
	}
}

func TestPythonTooOldIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	h, _ := setup(t)
	f := newPyPI(t)
	withPython(t, h, "3.10")
	stubHealth(t, nil)
	cred, _, _ := h.Env.Credential("example")
	req := djangoRequest()
	req.Runtime = "python:3.10"
	before := len(h.Runner.Calls)
	_, err := Install(context.Background(), h.Env, Sources{Client: f.srv.Client()}, req, cred, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "needs Python 3.12") {
		t.Fatalf("%v", err)
	}
	for _, c := range h.Runner.Calls[before:] {
		if strings.HasPrefix(c, "sh -c") {
			t.Errorf("an unsupported Python still ran %q", c)
		}
	}
}

func TestPackageThatNeedsNewerPythonThanTheWebsiteHas(t *testing.T) {
	h, _ := setup(t)
	f := newPyPI(t)
	f.versions["django"] = [2]string{"7.0.1", ">=3.13"}
	withPython(t, h, "3.12")
	stubHealth(t, nil)
	cred, _, _ := h.Env.Credential("example")
	_, err := Install(context.Background(), h.Env, Sources{Client: f.srv.Client()}, djangoRequest(), cred, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "needs Python >=3.13 but this website runs Python 3.12") {
		t.Fatalf("%v", err)
	}
}

func TestNoPythonSelected(t *testing.T) {
	h, _ := setup(t)
	f := newPyPI(t)
	cred, _, _ := h.Env.Credential("example")
	for _, rt := range []string{"", "system", "node:20.1.0"} {
		req := djangoRequest()
		req.Runtime = rt
		if _, err := Install(context.Background(), h.Env, Sources{Client: f.srv.Client()}, req, cred, io.Discard); err == nil {
			t.Errorf("runtime %q must be refused", rt)
		}
	}
}

func TestFastAPIInstall(t *testing.T) {
	h, _ := setup(t)
	f := newPyPI(t)
	withPython(t, h, "3.12")
	stubHealth(t, nil)
	cred, _, _ := h.Env.Credential("example")
	res, err := Install(context.Background(), h.Env, Sources{Client: f.srv.Client()}, fastapiRequest(), cred, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res["version"] != "0.162.0" || strings.Join(res["packages"].([]string), " ") != "fastapi==0.162.0 uvicorn==0.54.0" {
		t.Errorf("result: %+v", res)
	}
	stdins := strings.Join(h.Runner.Stdins, "\n")
	if !strings.Contains(stdins, "def healthz()") {
		t.Error("the starter application was not written")
	}
	svc := res["service"].(map[string]any)
	if !strings.Contains(svc["command"].(string), "uvicorn' main:app --host 127.0.0.1 --port 3202") {
		t.Errorf("command: %v", svc["command"])
	}
}

func TestPythonFailuresCleanUpAndStop(t *testing.T) {
	cases := map[string]struct {
		fail    string // a substring of the sh script that fails
		stage   string
		service bool
	}{
		"venv":      {"-m venv venv", "virtual environment", false},
		"pip":       {"pip install", "pip could not install", false},
		"scaffold":  {"startproject", "Django project could not be created", false},
		"migrate":   {"manage.py migrate", "migration", false},
		"superuser": {"createsuperuser", "administrator account", false},
		"settings":  {"cat > config/settings.py", "settings could not be written", false},
	}
	for name, c := range cases {
		h, _ := setup(t)
		f := newPyPI(t)
		withPython(t, h, "3.12")
		stubHealth(t, nil)
		prev := h.Runner.Handler
		h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
			if id == "sh" && len(args) > 1 && strings.Contains(args[1], c.fail) {
				return "", fmt.Errorf("simulated failure")
			}
			if prev != nil {
				return prev(id, o, args)
			}
			return "", nil
		}
		cred, _, _ := h.Env.Credential("example")
		var log strings.Builder
		_, err := Install(context.Background(), h.Env, Sources{Client: f.srv.Client()}, djangoRequest(), cred, &log)
		if err == nil || !strings.Contains(err.Error(), c.stage) {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if h.Exists("etc/systemd/system/nimvor-worker-py-django.service") {
			t.Errorf("%s: a service was created after the failure", name)
		}
		removed := false
		for _, call := range h.Runner.Calls {
			removed = removed || strings.HasPrefix(call, "sh -c rm -rf -- 'venv' 'config' 'manage.py'")
		}
		if !removed || !strings.Contains(log.String(), "Cleanup: removed venv, config, manage.py") {
			t.Errorf("%s: only the created names should be removed, and said so:\n%s", name, log.String())
		}
	}
}

func TestPythonUnhealthyRemovesTheService(t *testing.T) {
	h, _ := setup(t)
	f := newPyPI(t)
	withPython(t, h, "3.12")
	stubHealth(t, fmt.Errorf("it did not answer"))
	cred, _, _ := h.Env.Credential("example")
	_, err := Install(context.Background(), h.Env, Sources{Client: f.srv.Client()}, fastapiRequest(), cred, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "not healthy") {
		t.Fatalf("%v", err)
	}
	if h.Exists("etc/systemd/system/nimvor-worker-py-fastapi.service") {
		t.Error("the unhealthy service was left behind")
	}
}

func TestPythonRequestValidation(t *testing.T) {
	h, _ := setup(t)
	if err := (func() error { r := djangoRequest(); return r.validate(h.Env) })(); err != nil {
		t.Fatalf("a valid Django request: %v", err)
	}
	if err := (func() error { r := fastapiRequest(); return r.validate(h.Env) })(); err != nil {
		t.Fatalf("a valid FastAPI request: %v", err)
	}
	for name, mut := range map[string]func(*Request){
		"short password": func(r *Request) { r.AdminPassword = "short" },
		"password nl":    func(r *Request) { r.AdminPassword = "a-long-password-1\nx" },
		"bad user":       func(r *Request) { r.AdminUser = "own er;" },
		"bad email":      func(r *Request) { r.AdminEmail = "nope" },
		"bad runtime":    func(r *Request) { r.Runtime = "python:3.12; rm -rf /" },
		"low port":       func(r *Request) { r.Port = 22 },
	} {
		r := djangoRequest()
		mut(&r)
		if err := r.validate(h.Env); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}

func TestPythonPlan(t *testing.T) {
	h, _ := setup(t)
	withPython(t, h, "3.12")
	cred, _, _ := h.Env.Credential("example")
	old := diskFree
	t.Cleanup(func() { diskFree = old })
	diskFree = func(string) (uint64, error) { return 10 << 30, nil }
	p := BuildPlan(context.Background(), h.Env, djangoRequest(), cred)
	if p.Strategy != "python-venv" || p.License != "BSD-3-Clause" || p.Status != StatusTesting {
		t.Fatalf("plan: %+v", p)
	}
	got := map[string]bool{}
	for _, s := range p.Steps {
		got[s.Action] = true
	}
	for _, want := range []string{"Install with pip", "Create service", "Write files"} {
		if !got[want] {
			t.Errorf("no %q step in %+v", want, p.Steps)
		}
	}
}

func TestPythonInstallNeverReplacesAnExistingService(t *testing.T) {
	h, _ := setup(t)
	f := newPyPI(t)
	withPython(t, h, "3.12")
	stubHealth(t, nil)
	os.MkdirAll(filepath.Join(h.Base, "etc/systemd/system"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "etc/systemd/system/nimvor-worker-py-fastapi.service"), []byte("# taken"), 0o644)
	cred, _, _ := h.Env.Credential("example")
	if _, err := Install(context.Background(), h.Env, Sources{Client: f.srv.Client()}, fastapiRequest(), cred, io.Discard); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("%v", err)
	}
}
