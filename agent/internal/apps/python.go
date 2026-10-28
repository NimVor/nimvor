package apps

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/runtimemgr"
	"github.com/NimVor/nimvor/agent/internal/workers"
)

// Python applications (Django, FastAPI) get a virtual environment of their
// own under the website's folder, packages pinned to the release that was
// current at install time (and recorded in requirements.lock), and a worker
// service behind the website's reverse proxy. pip, the project scaffold and the
// migrations all run as the website account.

// pypiAPI is the PyPI JSON API; tests point it at a local server.
var pypiAPI = "https://pypi.org/pypi"

func init() {
	nativeApps["django"] = nativeApp{manifest: djangoManifest(), install: installDjango}
	nativeApps["fastapi"] = nativeApp{manifest: fastapiManifest(), install: installFastAPI}
}

func pythonManifest(id, name, desc, pkg, health string, min string) Manifest {
	return Manifest{
		Schema: ManifestSchema, ID: id, Name: name, Kind: KindApplication, Category: "frameworks",
		Description: desc, Status: StatusTesting,
		Runtime:  RuntimeSpec{Type: "python", Min: min},
		Database: DatabaseSpec{Type: "sqlite"},
		Strategy: "python-venv",
		// pip verifies packages over TLS from PyPI; the exact versions that
		// were installed are written to requirements.lock.
		Source:  SourceSpec{Kind: "pypi", Package: pkg, Integrity: "https-only", IntegrityNote: "PyPI packages are fetched over TLS by pip and pinned to exact versions; PyPI offers no checksum this installer can compare in advance"},
		Service: &ServiceSpec{Type: "worker", Proxy: true},
		Health:  HealthSpec{Type: "http", Path: health},
		Backup:  "files", Upgrade: "venv-rebuild", Uninstall: "files",
		Compat: Compat{Arch: []string{"amd64", "arm64"}, MinDiskMB: 400},
	}
}

func djangoManifest() Manifest {
	m := pythonManifest("django", "Django", "Python web framework: a production-ready project with the admin site, served by Gunicorn.", "django", "/admin/login/", "3.12")
	m.License = LicenseSpec{SPDX: "BSD-3-Clause", URL: "https://github.com/django/django/blob/main/LICENSE"}
	m.Secrets = []string{"DJANGO_SECRET_KEY", "ADMIN_PASSWORD"}
	m.Verified = "realhost:TestRealHostPythonDjango" // .github/workflows/app-native.yml
	return m
}

func fastapiManifest() Manifest {
	m := pythonManifest("fastapi", "FastAPI", "Python API framework: a starter application with a health endpoint, served by Uvicorn.", "fastapi", "/healthz", "3.10")
	m.License = LicenseSpec{SPDX: "MIT", URL: "https://github.com/fastapi/fastapi/blob/master/LICENSE"}
	m.Verified = "realhost:TestRealHostPythonFastAPI" // .github/workflows/app-native.yml
	return m
}

// pyVersionRe accepts only plain numeric releases: a pre-release, development
// or post release (1.0rc1, 2.0.dev3) is never installed.
var pyVersionRe = regexp.MustCompile(`^[0-9]{1,4}(\.[0-9]{1,4}){1,3}$`)

// pypiRelease is a package's newest release as PyPI reports it.
type pypiRelease struct {
	Version        string
	RequiresPython string
	License        string
}

// pypiLatest reads the newest release of a package.
func pypiLatest(ctx context.Context, client *http.Client, pkg string) (pypiRelease, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, pypiAPI+"/"+pkg+"/json", nil)
	req.Header.Set("User-Agent", userAgent)
	res, err := client.Do(req)
	if err != nil {
		return pypiRelease{}, fmt.Errorf("%s could not be looked up on PyPI: %v", pkg, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return pypiRelease{}, fmt.Errorf("%s could not be looked up on PyPI: HTTP %d", pkg, res.StatusCode)
	}
	var d struct {
		Info struct {
			Version        string `json:"version"`
			RequiresPython string `json:"requires_python"`
			License        string `json:"license_expression"`
		} `json:"info"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&d); err != nil {
		return pypiRelease{}, fmt.Errorf("%s could not be read from PyPI: %v", pkg, err)
	}
	if !pyVersionRe.MatchString(d.Info.Version) {
		return pypiRelease{}, fmt.Errorf("the latest %s release (%q) is not a plain stable version; nothing was installed", pkg, d.Info.Version)
	}
	return pypiRelease{Version: d.Info.Version, RequiresPython: d.Info.RequiresPython, License: d.Info.License}, nil
}

// pythonSatisfies reports whether a Python "major.minor" meets a PEP 440
// requires_python specifier made of >=, >, <=, < and != clauses. Anything else
// is refused rather than guessed at.
func pythonSatisfies(spec, version string) (bool, error) {
	if strings.TrimSpace(spec) == "" {
		return true, nil
	}
	for _, clause := range strings.Split(spec, ",") {
		clause = strings.TrimSpace(clause)
		var op string
		for _, o := range []string{">=", "<=", "!=", ">", "<"} {
			if strings.HasPrefix(clause, o) {
				op = o
				break
			}
		}
		if op == "" {
			return false, fmt.Errorf("the Python requirement %q is not understood", spec)
		}
		want := strings.TrimSpace(strings.TrimPrefix(clause, op))
		if op == "!=" {
			continue // a single excluded patch release never decides this
		}
		if !regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,2}(\.\*)?$`).MatchString(want) {
			return false, fmt.Errorf("the Python requirement %q is not understood", spec)
		}
		c := compareVersion(version, strings.TrimSuffix(want, ".*"))
		ok := false
		switch op {
		case ">=":
			ok = c >= 0
		case ">":
			ok = c > 0
		case "<=":
			ok = c <= 0
		case "<":
			ok = c < 0
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// pythonOf resolves the website's selected runtime ("python:3.12.7") to its
// interpreter and "major.minor".
func pythonOf(env *host.Env, runtime string) (interp, minor string, err error) {
	kind, ver, ok := strings.Cut(runtime, ":")
	if !ok || kind != "python" {
		return "", "", fmt.Errorf("this website has no Python version selected; choose one under Runtime first")
	}
	dir, err := runtimemgr.BinDir(env, runtime)
	if err != nil {
		return "", "", err
	}
	interp = filepath.Join(dir, "python3")
	if env.Root.Exists(filepath.Join(dir, "python"+ver)) {
		interp = filepath.Join(dir, "python"+ver)
	}
	parts := strings.SplitN(ver, ".", 3)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("the Python runtime %q has no minor version", runtime)
	}
	return interp, parts[0] + "." + parts[1], nil
}

func randomSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pyPackage is a package pinned for installation.
type pyPackage struct {
	name    string
	release pypiRelease
}

func (p pyPackage) pin() string { return p.name + "==" + p.release.Version }

// resolvePackages pins the newest stable release of each package and checks
// the first (the application itself) against the interpreter.
func resolvePackages(ctx context.Context, client *http.Client, minor, app string, names []string, log io.Writer) ([]pyPackage, error) {
	var out []pyPackage
	for i, n := range names {
		rel, err := pypiLatest(ctx, client, n)
		if err != nil {
			return nil, err
		}
		if ok, err := pythonSatisfies(rel.RequiresPython, minor); err != nil {
			return nil, fmt.Errorf("%s %s: %v", n, rel.Version, err)
		} else if !ok {
			if i == 0 {
				return nil, fmt.Errorf("%s %s needs Python %s but this website runs Python %s. Choose a newer Python for the website under Runtime and try again", app, rel.Version, rel.RequiresPython, minor)
			}
			return nil, fmt.Errorf("%s %s needs Python %s but this website runs Python %s", n, rel.Version, rel.RequiresPython, minor)
		}
		fmt.Fprintf(log, "%s %s.\n", n, rel.Version)
		out = append(out, pyPackage{name: n, release: rel})
	}
	return out, nil
}

// pyEnvironment creates the virtual environment and installs the pinned
// packages as the website account, then freezes what was installed.
func pyEnvironment(ctx context.Context, env *host.Env, cred *execx.Credential, target, interp string, pins []pyPackage, log io.Writer) error {
	if out, err := asUser(ctx, env, cred, target, nil, log, "umask 027 && exec "+shQuote(interp)+" -m venv venv"); err != nil {
		if strings.Contains(string(out), "ensurepip") || strings.Contains(string(out), "venv") {
			return fmt.Errorf("the virtual environment could not be created: the python3-venv package is probably missing (install it under Packages)")
		}
		return fmt.Errorf("the virtual environment could not be created")
	}
	args := make([]string, len(pins))
	for i, p := range pins {
		args[i] = shQuote(p.pin())
	}
	fmt.Fprintln(log, "Installing packages with pip…")
	if _, err := asUser(ctx, env, cred, target, nil, log, "umask 027 && exec venv/bin/pip install --disable-pip-version-check --no-input --no-cache-dir "+strings.Join(args, " ")); err != nil {
		return fmt.Errorf("pip could not install the packages")
	}
	// What was really installed, dependencies included, for reproducibility.
	if _, err := asUser(ctx, env, cred, target, nil, nil, "umask 027 && venv/bin/pip freeze --disable-pip-version-check > requirements.lock"); err != nil {
		fmt.Fprintf(log, "Note: the list of installed packages (requirements.lock) could not be written: %v\n", err)
	}
	return nil
}

// pyService creates the worker and waits for the application to answer.
func pyService(ctx context.Context, env *host.Env, r Request, target, command, healthPath string, extraEnv map[string]string, log io.Writer) (workers.Spec, error) {
	e := map[string]string{"PYTHONUNBUFFERED": "1", "HOME": target, "USER": r.User}
	for k, v := range extraEnv {
		e[k] = v
	}
	spec := workers.Spec{
		Slug: r.ServiceSlug, Name: nativeApps[r.App].manifest.Name, Domain: r.Domain, User: r.User, Root: r.Root, Directory: r.Path,
		Command: command, Env: e, Restart: "always", Procs: 1, Enabled: true,
	}
	if _, err := workers.Apply(ctx, env, spec); err != nil {
		return spec, fmt.Errorf("the service could not be created: %v", err)
	}
	fmt.Fprintln(log, "Created and started the service.")
	if err := healthCheck(ctx, r.Port, healthPath, 90*time.Second); err != nil {
		_, _ = workers.Remove(context.Background(), env, r.ServiceSlug)
		return spec, fmt.Errorf("the application started but is not healthy: %v. Its service was removed again", err)
	}
	fmt.Fprintln(log, "The application answers on its port.")
	return spec, nil
}

// pyPrepare is the part both Python applications share: the interpreter, the
// pinned packages and a clean folder.
func pyPrepare(ctx context.Context, env *host.Env, src Sources, r Request, log io.Writer, names []string) (root, target, interp string, pins []pyPackage, err error) {
	root, err = env.Root.Path(r.Root)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("the website folder could not be resolved")
	}
	target = filepath.Join(root, r.Path)
	interp, minor, err := pythonOf(env, r.Runtime)
	if err != nil {
		return "", "", "", nil, err
	}
	fmt.Fprintf(log, "Python %s.\n", minor)
	m := nativeApps[r.App].manifest
	if !m.Runtime.Supports(minor) {
		return "", "", "", nil, fmt.Errorf("%s needs Python %s or newer; this website runs Python %s", m.Name, m.Runtime.Min, minor)
	}
	pins, err = resolvePackages(ctx, src.Client, minor, m.Name, names, log)
	return root, target, interp, pins, err
}

// pyFolder creates the application folder as the account and returns a
// cleanup that removes only what the install made.
func pyFolder(ctx context.Context, env *host.Env, cred *execx.Credential, root, target string, r Request, created []string, log io.Writer) (func(error) error, error) {
	existed := env.Root.Exists(target)
	fail := func(err error) error {
		cleanup(context.Background(), env, cred, target, created, log)
		if !existed {
			if _, rerr := asUser(context.Background(), env, cred, root, nil, nil, "rmdir -- "+shQuote(r.Path)); rerr != nil {
				fmt.Fprintf(log, "Cleanup: the folder %s could not be removed: %v.\n", r.Path, rerr)
			}
		}
		return err
	}
	if _, err := asUser(ctx, env, cred, root, nil, log, "umask 027 && mkdir -p -- "+shQuote(r.Path)); err != nil {
		return nil, fmt.Errorf("the folder %s could not be created", r.Path)
	}
	return fail, nil
}

func installFastAPI(ctx context.Context, env *host.Env, src Sources, r Request, cred *execx.Credential, log io.Writer) (map[string]any, error) {
	root, target, interp, pins, err := pyPrepare(ctx, env, src, r, log, []string{"fastapi", "uvicorn"})
	if err != nil {
		return nil, err
	}
	fail, err := pyFolder(ctx, env, cred, root, target, r, []string{"venv", "main.py", "requirements.lock"}, log)
	if err != nil {
		return nil, err
	}
	if err := pyEnvironment(ctx, env, cred, target, interp, pins, log); err != nil {
		return nil, fail(err)
	}
	if _, err := asUser(ctx, env, cred, target, strings.NewReader(fastapiMain), log, "umask 027 && cat > main.py"); err != nil {
		return nil, fail(fmt.Errorf("the application file could not be written"))
	}
	cmd := shQuote(filepath.Join(target, "venv/bin/uvicorn")) + " main:app --host 127.0.0.1 --port " + strconv.Itoa(r.Port) + " --proxy-headers --forwarded-allow-ips 127.0.0.1"
	spec, err := pyService(ctx, env, r, target, cmd, "/healthz", nil, log)
	if err != nil {
		return nil, fail(err)
	}
	return map[string]any{
		"app": "fastapi", "version": pins[0].release.Version, "path": r.Path, "strategy": "python-venv", "port": r.Port,
		"staged": true, "finish_url": strings.TrimRight(r.URL, "/") + "/docs", "service": serviceResult(spec),
		"packages": pinNames(pins),
	}, nil
}

func pinNames(pins []pyPackage) []string {
	out := make([]string, len(pins))
	for i, p := range pins {
		out[i] = p.pin()
	}
	return out
}

const fastapiMain = `from fastapi import FastAPI

app = FastAPI(title="FastAPI on NimVor")


@app.get("/")
def index():
    return {"message": "Your FastAPI application is running. Edit main.py to build your API."}


@app.get("/healthz")
def healthz():
    return {"status": "ok"}
`

func installDjango(ctx context.Context, env *host.Env, src Sources, r Request, cred *execx.Credential, log io.Writer) (map[string]any, error) {
	root, target, interp, pins, err := pyPrepare(ctx, env, src, r, log, []string{"django", "gunicorn", "whitenoise"})
	if err != nil {
		return nil, err
	}
	fail, err := pyFolder(ctx, env, cred, root, target, r, []string{"venv", "config", "manage.py", "data", "staticfiles", ".env", "requirements.lock"}, log)
	if err != nil {
		return nil, err
	}
	if err := pyEnvironment(ctx, env, cred, target, interp, pins, log); err != nil {
		return nil, fail(err)
	}
	if _, err := asUser(ctx, env, cred, target, nil, log, "umask 027 && venv/bin/django-admin startproject config . && mkdir -p data staticfiles"); err != nil {
		return nil, fail(fmt.Errorf("the Django project could not be created"))
	}
	// The secret key lives in a file only the account can read; the settings
	// read it, so it is in no command line, environment or job log.
	key, err := randomSecret(48)
	if err != nil {
		return nil, fail(err)
	}
	if _, err := asUser(ctx, env, cred, target, strings.NewReader("DJANGO_SECRET_KEY="+key+"\n"), nil, "umask 077 && cat > .env"); err != nil {
		return nil, fail(fmt.Errorf("the secret key could not be stored"))
	}
	if _, err := asUser(ctx, env, cred, target, strings.NewReader(djangoSettings(r)), log, "umask 027 && cat > config/settings.py"); err != nil {
		return nil, fail(fmt.Errorf("the Django settings could not be written"))
	}
	fmt.Fprintln(log, "Wrote the production settings and the secret key (only the website's account can read it).")
	if _, err := asUser(ctx, env, cred, target, nil, log, "umask 027 && venv/bin/python manage.py migrate --noinput && venv/bin/python manage.py collectstatic --noinput"); err != nil {
		return nil, fail(fmt.Errorf("Django's database migration or static files step failed"))
	}
	// The administrator: the password goes to Django through the environment
	// of that one command, never an argument, and is not logged.
	o := execx.Opts{As: cred, Dir: target, Timeout: 5 * time.Minute, Env: []string{"DJANGO_SUPERUSER_PASSWORD=" + r.AdminPassword}}
	script := "umask 027 && venv/bin/python manage.py createsuperuser --noinput --username " + shQuote(r.AdminUser) + " --email " + shQuote(r.AdminEmail)
	if _, err := env.Runner.RunWith(ctx, "sh", o, "-c", script); err != nil {
		return nil, fail(fmt.Errorf("the administrator account could not be created"))
	}
	fmt.Fprintf(log, "Created the administrator %s.\n", r.AdminUser)
	cmd := shQuote(filepath.Join(target, "venv/bin/gunicorn")) + " config.wsgi:application --bind 127.0.0.1:" + strconv.Itoa(r.Port) + " --workers 2 --access-logfile -"
	spec, err := pyService(ctx, env, r, target, cmd, "/admin/login/", map[string]string{"DJANGO_SETTINGS_MODULE": "config.settings"}, log)
	if err != nil {
		return nil, fail(err)
	}
	return map[string]any{
		"app": "django", "version": pins[0].release.Version, "path": r.Path, "strategy": "python-venv", "port": r.Port,
		"admin_user": r.AdminUser, "staged": true, "finish_url": strings.TrimRight(r.URL, "/") + "/admin/", "service": serviceResult(spec),
		"packages": pinNames(pins),
	}, nil
}

// pyString is s as a Python string literal (JSON strings are valid Python).
func pyString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// djangoSettings is the project's production settings. Every value that varies
// is a validated domain or URL written as a Python string literal.
func djangoSettings(r Request) string {
	https := strings.HasPrefix(r.URL, "https://")
	return `"""Production settings written by NimVor. Edit freely; NimVor does not rewrite this file."""
import os
from pathlib import Path

BASE_DIR = Path(__file__).resolve().parent.parent


def _read_env_file():
    values = {}
    path = BASE_DIR / ".env"
    if path.exists():
        for line in path.read_text().splitlines():
            if "=" in line and not line.startswith("#"):
                key, _, value = line.partition("=")
                values[key.strip()] = value.strip()
    return values


_env = _read_env_file()
SECRET_KEY = os.environ.get("DJANGO_SECRET_KEY") or _env["DJANGO_SECRET_KEY"]
DEBUG = False
ALLOWED_HOSTS = [` + pyString(r.Domain) + `]
CSRF_TRUSTED_ORIGINS = [` + pyString(r.URL) + `]
SECURE_PROXY_SSL_HEADER = ("HTTP_X_FORWARDED_PROTO", "https")
SESSION_COOKIE_SECURE = ` + pyBool(https) + `
CSRF_COOKIE_SECURE = ` + pyBool(https) + `

INSTALLED_APPS = [
    "django.contrib.admin",
    "django.contrib.auth",
    "django.contrib.contenttypes",
    "django.contrib.sessions",
    "django.contrib.messages",
    "django.contrib.staticfiles",
]

MIDDLEWARE = [
    "django.middleware.security.SecurityMiddleware",
    "whitenoise.middleware.WhiteNoiseMiddleware",
    "django.contrib.sessions.middleware.SessionMiddleware",
    "django.middleware.common.CommonMiddleware",
    "django.middleware.csrf.CsrfViewMiddleware",
    "django.contrib.auth.middleware.AuthenticationMiddleware",
    "django.contrib.messages.middleware.MessageMiddleware",
    "django.middleware.clickjacking.XFrameOptionsMiddleware",
]

ROOT_URLCONF = "config.urls"

TEMPLATES = [
    {
        "BACKEND": "django.template.backends.django.DjangoTemplates",
        "DIRS": [],
        "APP_DIRS": True,
        "OPTIONS": {
            "context_processors": [
                "django.template.context_processors.request",
                "django.contrib.auth.context_processors.auth",
                "django.contrib.messages.context_processors.messages",
            ],
        },
    },
]

WSGI_APPLICATION = "config.wsgi.application"

DATABASES = {
    "default": {
        "ENGINE": "django.db.backends.sqlite3",
        "NAME": BASE_DIR / "data" / "db.sqlite3",
    }
}

AUTH_PASSWORD_VALIDATORS = [
    {"NAME": "django.contrib.auth.password_validation.UserAttributeSimilarityValidator"},
    {"NAME": "django.contrib.auth.password_validation.MinimumLengthValidator"},
    {"NAME": "django.contrib.auth.password_validation.CommonPasswordValidator"},
    {"NAME": "django.contrib.auth.password_validation.NumericPasswordValidator"},
]

LANGUAGE_CODE = "en-us"
TIME_ZONE = "UTC"
USE_I18N = True
USE_TZ = True

STATIC_URL = "static/"
STATIC_ROOT = BASE_DIR / "staticfiles"
DEFAULT_AUTO_FIELD = "django.db.models.BigAutoField"
`
}

func pyBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}
