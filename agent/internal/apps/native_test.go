package apps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These are unit tests: the privileged operations (running sh as the website
// account, systemd) are simulated. They prove the logic and the ordering, not
// that a real server works; the real-host test does that.

type giteaFixture struct {
	srv        *httptest.Server
	binary     []byte
	sum        string
	version    string
	name       string
	dlSum      string // what the download server publishes (default: the real sum)
	ghSum      string // what the GitHub release publishes
	prerelease bool
	tag        string
}

func newGitea(t *testing.T) *giteaFixture {
	t.Helper()
	f := &giteaFixture{binary: []byte("\x7fELF fake gitea binary"), version: "1.2.3", tag: "v1.2.3"}
	s := sha256.Sum256(f.binary)
	f.sum = hex.EncodeToString(s[:])
	f.dlSum, f.ghSum = f.sum, f.sum
	f.name = "gitea-" + f.version + "-linux-" + giteaArch()
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/go-gitea/gitea/releases/latest":
			fmt.Fprintf(w, `{"tag_name":%q,"draft":false,"prerelease":%v,"assets":[]}`, f.tag, f.prerelease)
		case "/dl/" + f.version + "/" + f.name:
			w.Write(f.binary)
		case "/dl/" + f.version + "/" + f.name + ".sha256":
			fmt.Fprintf(w, "%s  %s\n", f.dlSum, f.name)
		case "/gh/v" + f.version + "/" + f.name + ".sha256":
			fmt.Fprintf(w, "%s  %s\n", f.ghSum, f.name)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	old := githubAPI
	githubAPI = f.srv.URL
	t.Cleanup(func() { githubAPI = old })
	return f
}

func (f *giteaFixture) sources() Sources {
	return Sources{Client: f.srv.Client(), GiteaDL: f.srv.URL + "/dl", GiteaGitHub: f.srv.URL + "/gh"}
}

func giteaRequest() Request {
	return Request{App: "gitea", Domain: "git.test", User: "example", Root: "/home/nimvor/shop.test", Path: "gitea", URL: "https://git.test", Port: 3101, ServiceSlug: "git-gitea"}
}

func stubHealth(t *testing.T, err error) *int {
	t.Helper()
	calls := 0
	old := healthCheck
	healthCheck = func(ctx context.Context, port int, path string, within time.Duration) error {
		calls++
		return err
	}
	t.Cleanup(func() { healthCheck = old })
	return &calls
}

func TestGiteaInstallsVerifiesAndSupervises(t *testing.T) {
	h, _ := setup(t)
	f := newGitea(t)
	health := stubHealth(t, nil)
	cred, _, _ := h.Env.Credential("example")
	var log strings.Builder
	res, err := Install(context.Background(), h.Env, f.sources(), giteaRequest(), cred, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if res["version"] != "1.2.3" || res["strategy"] != "binary-service" || res["port"] != 3101 {
		t.Errorf("result: %+v", res)
	}
	svc := res["service"].(map[string]any)
	if svc["slug"] != "git-gitea" || !strings.Contains(svc["command"].(string), "bin/gitea' web --config") {
		t.Errorf("service: %+v", svc)
	}
	// Everything the install wrote went through sh as the website account.
	shCalls := 0
	for i, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "sh -c") {
			shCalls++
			if o := h.Runner.Opts[i]; o.As == nil || o.As.UID == 0 {
				t.Errorf("%q did not run as the website account", c)
			}
		}
	}
	if shCalls != 3 {
		t.Errorf("%d sh steps, want 3 (folder, program, configuration): %v", shCalls, h.Runner.Calls)
	}
	// The binary reached sh on stdin, byte for byte; the config has no secret
	// and keeps Gitea on loopback with the installer left to the customer.
	stdins := strings.Join(h.Runner.Stdins, "\n---\n")
	if !strings.Contains(stdins, string(f.binary)) {
		t.Error("the verified binary was not what was written")
	}
	for _, want := range []string{"HTTP_ADDR = 127.0.0.1", "HTTP_PORT = 3101", "ROOT_URL = https://git.test/", "DB_TYPE = sqlite3", "INSTALL_LOCK = false", "RUN_USER = example"} {
		if !strings.Contains(stdins, want) {
			t.Errorf("app.ini lacks %q", want)
		}
	}
	for _, secret := range []string{"SECRET_KEY", "INTERNAL_TOKEN", "PASSWD"} {
		if strings.Contains(stdins, secret) || strings.Contains(log.String(), secret) {
			t.Errorf("%s must not appear: NimVor handles no Gitea secret", secret)
		}
	}
	// The worker unit exists and runs as the account, sandboxed.
	unit := h.Read("etc/systemd/system/nimvor-worker-git-gitea.service")
	if !strings.Contains(unit, "User=example") || !strings.Contains(unit, "NoNewPrivileges=yes") {
		t.Errorf("unit:\n%s", unit)
	}
	if *health != 1 {
		t.Errorf("health check ran %d times", *health)
	}
	if !strings.Contains(log.String(), "Checksum verified against the download server and the GitHub release") {
		t.Errorf("log:\n%s", log.String())
	}
}

func TestGiteaRefusesBadChecksums(t *testing.T) {
	cases := map[string]func(*giteaFixture){
		"download does not match":   func(f *giteaFixture) { f.dlSum, f.ghSum = strings.Repeat("0", 64), strings.Repeat("0", 64) },
		"the two sources disagree":  func(f *giteaFixture) { f.ghSum = strings.Repeat("1", 64) },
		"checksum is not a sha256":  func(f *giteaFixture) { f.dlSum = "not-a-hash" },
		"github checksum not found": func(f *giteaFixture) { f.ghSum = "" },
	}
	for name, mut := range cases {
		h, _ := setup(t)
		f := newGitea(t)
		mut(f)
		stubHealth(t, nil)
		cred, _, _ := h.Env.Credential("example")
		before := len(h.Runner.Calls)
		_, err := Install(context.Background(), h.Env, f.sources(), giteaRequest(), cred, io.Discard)
		if err == nil {
			t.Errorf("%s: the install must fail", name)
			continue
		}
		for _, c := range h.Runner.Calls[before:] {
			if strings.HasPrefix(c, "sh -c") {
				t.Errorf("%s: wrote files after a failed verification: %s", name, c)
			}
		}
		if h.Exists("etc/systemd/system/nimvor-worker-git-gitea.service") {
			t.Errorf("%s: created a service", name)
		}
	}
}

func TestGiteaOnlyInstallsAStableNumberedRelease(t *testing.T) {
	for name, set := range map[string]func(*giteaFixture){
		"pre-release": func(f *giteaFixture) { f.prerelease = true },
		"odd tag":     func(f *giteaFixture) { f.tag = "v1.2.3-rc1" },
		"traversal":   func(f *giteaFixture) { f.tag = "v../../x" },
	} {
		h, _ := setup(t)
		f := newGitea(t)
		set(f)
		stubHealth(t, nil)
		cred, _, _ := h.Env.Credential("example")
		if _, err := Install(context.Background(), h.Env, f.sources(), giteaRequest(), cred, io.Discard); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}

func TestGiteaNeedsGit(t *testing.T) {
	h, _ := setup(t)
	f := newGitea(t)
	stubHealth(t, nil)
	h.Runner.Unavailable = map[string]bool{"git": true}
	cred, _, _ := h.Env.Credential("example")
	_, err := Install(context.Background(), h.Env, f.sources(), giteaRequest(), cred, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "git is not installed") {
		t.Fatalf("%v", err)
	}
}

func TestGiteaCleansUpWhenItIsNotHealthy(t *testing.T) {
	h, _ := setup(t)
	f := newGitea(t)
	stubHealth(t, fmt.Errorf("it did not answer"))
	cred, _, _ := h.Env.Credential("example")
	var log strings.Builder
	_, err := Install(context.Background(), h.Env, f.sources(), giteaRequest(), cred, &log)
	if err == nil || !strings.Contains(err.Error(), "not healthy") {
		t.Fatalf("%v", err)
	}
	if h.Exists("etc/systemd/system/nimvor-worker-git-gitea.service") {
		t.Error("the unhealthy service was left behind")
	}
	// Only the names the install created are removed, as the account, and the
	// log says so only because the removal ran.
	var removal string
	for _, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "sh -c rm -rf --") {
			removal = c
		}
	}
	if removal != "sh -c rm -rf -- 'bin' 'custom' 'data' 'repositories' 'log'" {
		t.Errorf("cleanup command: %q", removal)
	}
	if !strings.Contains(log.String(), "Cleanup: removed bin, custom, data, repositories, log.") {
		t.Errorf("log:\n%s", log.String())
	}
}

func TestGiteaCleanupDoesNotClaimWhatFailed(t *testing.T) {
	h, _ := setup(t)
	f := newGitea(t)
	stubHealth(t, fmt.Errorf("it did not answer"))
	h.Runner.Errors = map[string]error{"sh -c rm -rf -- 'bin' 'custom' 'data' 'repositories' 'log'": fmt.Errorf("permission denied")}
	cred, _, _ := h.Env.Credential("example")
	var log strings.Builder
	Install(context.Background(), h.Env, f.sources(), giteaRequest(), cred, &log)
	if strings.Contains(log.String(), "Cleanup: removed") || !strings.Contains(log.String(), "could not remove") {
		t.Errorf("an unverified cleanup must not be reported as done:\n%s", log.String())
	}
}

func TestGiteaFailsWhenTheServiceCannotBeCreated(t *testing.T) {
	h, _ := setup(t)
	f := newGitea(t)
	stubHealth(t, nil)
	h.Fail["systemctl enable"] = "Failed to enable unit"
	cred, _, _ := h.Env.Credential("example")
	_, err := Install(context.Background(), h.Env, f.sources(), giteaRequest(), cred, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "service could not be created") {
		t.Fatalf("%v", err)
	}
}

func TestNativeRequestValidation(t *testing.T) {
	h, _ := setup(t)
	good := giteaRequest()
	if err := good.validate(h.Env); err != nil {
		t.Fatalf("a valid native request: %v", err)
	}
	for name, mut := range map[string]func(*Request){
		"port low":    func(r *Request) { r.Port = 80 },
		"port zero":   func(r *Request) { r.Port = 0 },
		"port high":   func(r *Request) { r.Port = 70000 },
		"slug":        func(r *Request) { r.ServiceSlug = "../x" },
		"slug upper":  func(r *Request) { r.ServiceSlug = "Gitea" },
		"path":        func(r *Request) { r.Path = "../../etc" },
		"logs folder": func(r *Request) { r.Path = "logs" },
		"url":         func(r *Request) { r.URL = "javascript:alert(1)" },
		"root user":   func(r *Request) { r.User = "root" },
		"other root":  func(r *Request) { r.Root = "/etc" },
	} {
		r := giteaRequest()
		mut(&r)
		if err := r.validate(h.Env); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}

func TestGiteaPlanListsServiceCheckAndSteps(t *testing.T) {
	h, _ := setup(t)
	cred, _, _ := h.Env.Credential("example")
	old := diskFree
	t.Cleanup(func() { diskFree = old })
	diskFree = func(string) (uint64, error) { return 10 << 30, nil }
	r := giteaRequest()
	p := BuildPlan(context.Background(), h.Env, r, cred)
	if p.Status != StatusTesting || p.Strategy != "binary-service" || p.License != "MIT" {
		t.Fatalf("plan: %+v", p)
	}
	actions := map[string]bool{}
	for _, s := range p.Steps {
		actions[s.Action] = true
	}
	for _, want := range []string{"Download", "Write files", "Create service"} {
		if !actions[want] {
			t.Errorf("no %q step", want)
		}
	}
	if actions["Create database"] {
		t.Error("Gitea uses SQLite: no database is created")
	}
	for _, want := range []string{"git is installed", "The application's port is free", "The service name is unused"} {
		found := false
		for _, c := range p.Checks {
			found = found || c.Name == want
		}
		if !found {
			t.Errorf("no %q check in %+v", want, p.Checks)
		}
	}
	// A port in use, a service name in use, and no git are each a conflict.
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	busy := r
	fmt.Sscanf(srv.Listener.Addr().String()[strings.LastIndex(srv.Listener.Addr().String(), ":")+1:], "%d", &busy.Port)
	if p := BuildPlan(context.Background(), h.Env, busy, cred); p.OK || !hasError(p.Conflicts, "port is free") {
		t.Errorf("a busy port must be a conflict: %v", p.Conflicts)
	}
	os.MkdirAll(filepath.Join(h.Base, "etc/systemd/system"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "etc/systemd/system/nimvor-worker-git-gitea.service"), []byte("x"), 0o644)
	if p := BuildPlan(context.Background(), h.Env, r, cred); p.OK || !hasError(p.Conflicts, "service name is unused") {
		t.Errorf("a used service name must be a conflict: %v", p.Conflicts)
	}
	os.Remove(filepath.Join(h.Base, "etc/systemd/system/nimvor-worker-git-gitea.service"))
	h.Runner.Unavailable = map[string]bool{"git": true}
	if p := BuildPlan(context.Background(), h.Env, r, cred); p.OK || !hasError(p.Conflicts, "git is installed") {
		t.Errorf("missing git must be a conflict: %v", p.Conflicts)
	}
}

// A real HTTP check against a real listener, without the stub.
func TestWaitHTTPAndPortFree(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	port := 0
	fmt.Sscanf(srv.Listener.Addr().String()[strings.LastIndex(srv.Listener.Addr().String(), ":")+1:], "%d", &port)
	if err := waitHTTP(context.Background(), port, "/", 3*time.Second); err != nil {
		t.Errorf("a listening server is healthy: %v", err)
	}
	if portFree(port) {
		t.Error("a port in use is not free")
	}
	srv.Close()
	if !portFree(port) {
		t.Error("a closed port is free")
	}
	start := time.Now()
	if err := waitHTTP(context.Background(), port, "/", 1500*time.Millisecond); err == nil || time.Since(start) < time.Second {
		t.Errorf("nothing listening must fail after waiting: %v", err)
	}
	// A server error is not healthy.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	defer bad.Close()
	bp := 0
	fmt.Sscanf(bad.Listener.Addr().String()[strings.LastIndex(bad.Listener.Addr().String(), ":")+1:], "%d", &bp)
	if err := waitHTTP(context.Background(), bp, "/", 1500*time.Millisecond); err == nil {
		t.Error("HTTP 502 is not healthy")
	}
}

func TestAnInstallNeverReplacesAnExistingService(t *testing.T) {
	h, _ := setup(t)
	f := newGitea(t)
	stubHealth(t, nil)
	os.MkdirAll(filepath.Join(h.Base, "etc/systemd/system"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "etc/systemd/system/nimvor-worker-git-gitea.service"), []byte("# another website's service"), 0o644)
	cred, _, _ := h.Env.Credential("example")
	before := len(h.Runner.Calls)
	_, err := Install(context.Background(), h.Env, f.sources(), giteaRequest(), cred, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("%v", err)
	}
	for _, c := range h.Runner.Calls[before:] {
		if strings.HasPrefix(c, "sh -c") {
			t.Errorf("wrote files although the service name was taken: %s", c)
		}
	}
	if got := h.Read("etc/systemd/system/nimvor-worker-git-gitea.service"); got != "# another website's service" {
		t.Errorf("the existing service was overwritten: %q", got)
	}
}
