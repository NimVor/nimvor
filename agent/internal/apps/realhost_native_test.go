//go:build realhost

package apps

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
	"github.com/NimVor/nimvor/agent/internal/workers"
)

// realNative builds an agent environment on the real host: real systemd, real
// sh as an unprivileged account, real downloads. It needs root, systemd and
// NIMVOR_NETWORK=1.
type realNative struct {
	env              *host.Env
	reg              *actions.Registry
	sites, home, dir string
	user             string
	uid              string
}

func newRealNative(t *testing.T, tag, user, uid string) *realNative {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if os.Getenv("NIMVOR_NETWORK") != "1" {
		t.Skip("needs NIMVOR_NETWORK=1")
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		t.Fatal("systemd is not running: native applications are systemd services")
	}
	n := &realNative{sites: "/srv/nimvor-rt-" + tag, user: user, uid: uid}
	n.home = filepath.Join(n.sites, tag+".test")
	state := "/var/lib/nimvor-rt-" + tag
	cleanup := func() {
		exec.Command("pkill", "-KILL", "-u", user).Run()
		exec.Command("userdel", "-f", user).Run()
	}
	cleanup()
	os.RemoveAll(n.sites)
	if out, err := exec.Command("useradd", "--home-dir", n.home, "--no-create-home", "--shell", "/usr/sbin/nologin", "--user-group", "--uid", uid, user).CombinedOutput(); err != nil {
		t.Fatalf("useradd: %s", out)
	}
	os.MkdirAll(filepath.Join(n.home, "public_html"), 0o750)
	os.Chmod(n.sites, 0o755)
	exec.Command("chown", "-R", user+":"+user, n.home).Run()
	os.Chmod(n.home, 0o710)
	os.MkdirAll(state, 0o700)
	t.Cleanup(func() {
		cleanup()
		os.RemoveAll(n.sites)
		os.RemoveAll(state)
	})
	cfg := config.Default()
	cfg.SitesRoot = n.sites
	cfg.StateDir = state
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 2, 20*time.Minute, log)
	root := filesystem.Host()
	n.env = host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(5*time.Minute), jm, log)
	n.reg = actions.NewRegistry(true)
	Register(n.reg, n.env)
	return n
}

// install runs apps.install through the registered action, as the panel's
// request does, and waits for the job.
func (n *realNative) install(t *testing.T, req Request) (jobs.Job, error) {
	t.Helper()
	a, err := n.reg.Lookup("apps.install")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(req)
	res, err := a.Handler(context.Background(), b)
	if err != nil {
		return jobs.Job{}, err
	}
	id := res.(map[string]any)["job_id"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	j, err := n.env.Jobs.Wait(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return j, nil
}

func (n *realNative) jobLog(t *testing.T, id string) string {
	t.Helper()
	text, _, _ := n.env.Jobs.Log(id, 0)
	return text
}

// A real installation of Gitea: the newest stable release is downloaded from
// dl.gitea.com, verified against two published checksums, installed as the
// website account, run as a systemd service and checked over HTTP.
func TestRealHostGiteaInstall(t *testing.T) {
	n := newRealNative(t, "gitea", "rtgitea", "2893")
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git is not installed on this host")
	}
	const slug, port = "rt-gitea", 3917
	t.Cleanup(func() {
		workers.Remove(context.Background(), n.env, slug)
		exec.Command("systemctl", "daemon-reload").Run()
	})
	req := Request{App: "gitea", Domain: "gitea.test", User: n.user, Root: n.home, Path: "gitea", URL: "https://gitea.test", Port: port, ServiceSlug: slug, AllowTesting: true}

	// A customer without the testing allowance cannot install it.
	denied := req
	denied.AllowTesting = false
	if _, err := n.install(t, denied); err == nil || !strings.Contains(err.Error(), "status is testing") {
		t.Fatalf("a testing application must be refused without the allowance: %v", err)
	}

	j, err := n.install(t, req)
	if err != nil {
		t.Fatalf("apps.install: %v", err)
	}
	log := n.jobLog(t, j.ID)
	if j.Status != "succeeded" {
		t.Fatalf("the install did not succeed: %s\n%s", j.Error, log)
	}
	res, _ := j.Result.(map[string]any)
	version, _ := res["version"].(string)
	if !versionTag.MatchString(version) {
		t.Errorf("no version recorded: %v", res["version"])
	}
	t.Logf("installed Gitea %s", version)
	for _, want := range []string{"Checksum verified against the download server and the GitHub release", "Gitea answers on its port"} {
		if !strings.Contains(log, want) {
			t.Errorf("the job log lacks %q:\n%s", want, log)
		}
	}

	// It really runs, as the website account, and answers.
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get("http://127.0.0.1:3917/")
	if err != nil {
		t.Fatalf("Gitea does not answer: %v", err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Gitea") {
		t.Errorf("the install page: HTTP %d, %d bytes", resp.StatusCode, len(body))
	}
	if out, err := exec.Command("systemctl", "is-active", workers.Unit(slug)).CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "active" {
		t.Errorf("the service is not active: %s", out)
	}
	ps, _ := exec.Command("ps", "-u", n.user, "-o", "comm=").CombinedOutput()
	if !strings.Contains(string(ps), "gitea") {
		t.Errorf("no gitea process runs as %s:\n%s", n.user, ps)
	}
	// It listens on loopback only.
	ss, _ := exec.Command("ss", "-ltn", "sport = :"+"3917").CombinedOutput()
	if !strings.Contains(string(ss), "127.0.0.1:3917") || strings.Contains(string(ss), "0.0.0.0:3917") || strings.Contains(string(ss), "*:3917") {
		t.Errorf("Gitea must listen on 127.0.0.1 only:\n%s", ss)
	}

	// Ownership and permissions: the account's, closed to everyone else.
	target := filepath.Join(n.home, "gitea")
	for path, want := range map[string]os.FileMode{"bin/gitea": 0o750, "custom/conf/app.ini": 0o600} {
		fi, err := os.Stat(filepath.Join(target, path))
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s mode %o, want %o", path, fi.Mode().Perm(), want)
		}
	}
	owner, _ := exec.Command("stat", "-c", "%U", filepath.Join(target, "bin", "gitea")).CombinedOutput()
	if strings.TrimSpace(string(owner)) != n.user {
		t.Errorf("the binary belongs to %s, not %s", owner, n.user)
	}
	if out, err := exec.Command("setpriv", "--reuid=65534", "--regid=65534", "--clear-groups", "cat", filepath.Join(target, "custom/conf/app.ini")).CombinedOutput(); err == nil {
		t.Errorf("another account read app.ini:\n%s", out)
	}

	// A second installation into the same folder is refused: it is not empty.
	if _, err := n.install(t, req); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("a repeat must be refused: %v", err)
	}
	// The service name is taken, so the plan says so.
	cred, _, _ := n.env.Credential(n.user)
	plan := BuildPlan(context.Background(), n.env, Request{App: "gitea", Domain: "gitea.test", User: n.user, Root: n.home, Path: "gitea2", URL: "https://gitea.test", Port: 3918, ServiceSlug: slug}, cred)
	if plan.OK {
		t.Errorf("the plan must flag the used service name: %+v", plan.Conflicts)
	}
}
