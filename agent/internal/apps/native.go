package apps

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/workers"
)

// Native applications are not unpacked into a PHP web root: they are programs
// (a Go binary, a Python application) that NimVor installs under the website's
// folder, supervises as a worker service running as the website's account, and
// publishes through the website's reverse proxy. The website is therefore a
// proxy website whose port the application listens on.
//
// Everything a native installation writes is written *as the website account*
// (through `sh` run as that account), never by the agent as root: the folder
// belongs to the customer, who could otherwise plant a symbolic link between a
// check and a write.

// nativeApp is one natively deployed application.
type nativeApp struct {
	manifest Manifest
	install  func(ctx context.Context, env *host.Env, src Sources, r Request, cred *execx.Credential, log io.Writer) (map[string]any, error)
}

// nativeApps are the applications with a native strategy, by ID.
var nativeApps = map[string]nativeApp{}

func init() {
	nativeApps["gitea"] = nativeApp{manifest: giteaManifest(), install: installGitea}
}

// nativeManifests are the manifests of the native applications.
func nativeManifests() []Manifest {
	out := make([]Manifest, 0, len(nativeApps))
	for _, n := range nativeApps {
		out = append(out, n.manifest)
	}
	return out
}

// healthCheck waits for an application to answer on its loopback port. A
// variable so tests can stand in for the application.
var healthCheck = waitHTTP

// waitHTTP polls http://127.0.0.1:<port><path> until any HTTP response with a
// status below 500 arrives or the time is up.
func waitHTTP(ctx context.Context, port int, path string, within time.Duration) error {
	deadline := time.Now().Add(within)
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var last error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
		res, err := client.Do(req)
		if err == nil {
			res.Body.Close()
			if res.StatusCode < 500 {
				return nil
			}
			last = fmt.Errorf("HTTP %d", res.StatusCode)
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("it did not answer in %s (%v)", within.Round(time.Second), last)
}

// portFree reports whether nothing listens on the loopback port.
func portFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// asUser runs a fixed script through sh as the website account in dir.
func asUser(ctx context.Context, env *host.Env, cred *execx.Credential, dir string, stdin io.Reader, log io.Writer, script string) ([]byte, error) {
	o := execx.Opts{As: cred, Dir: dir, Output: log, Timeout: 20 * time.Minute}
	if stdin != nil {
		o.Stdin = stdin
	}
	return env.Runner.RunWith(ctx, "sh", o, "-c", script)
}

// cleanup removes only what an installation created (fixed names under the
// folder), as the website account, and says truthfully what happened.
func cleanup(ctx context.Context, env *host.Env, cred *execx.Credential, dir string, names []string, log io.Writer) {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = shQuote(n)
	}
	if _, err := asUser(ctx, env, cred, dir, nil, nil, "rm -rf -- "+strings.Join(quoted, " ")); err != nil {
		fmt.Fprintf(log, "Cleanup: could not remove %s: %v. Remove them by hand before trying again.\n", strings.Join(names, ", "), err)
		return
	}
	fmt.Fprintf(log, "Cleanup: removed %s.\n", strings.Join(names, ", "))
}

// serviceResult is what a native install tells the panel about the worker it
// created, so the panel can list it with the website's other workers. It
// never holds a secret.
func serviceResult(s workers.Spec) map[string]any {
	return map[string]any{
		"slug": s.Slug, "name": s.Name, "directory": s.Directory, "command": s.Command, "env": s.Env,
		"restart": s.Restart, "procs": 1, "memory_mb": s.MemoryMB, "runtime": s.Runtime,
	}
}

// ---------------------------------------------------------------------------
// Gitea: a single Go binary.

func giteaManifest() Manifest {
	return Manifest{
		Schema: ManifestSchema, ID: "gitea", Name: "Gitea", Kind: KindApplication, Category: "developer-tools",
		Description: "Self-hosted Git service: repositories, issues, pull requests and CI hooks.",
		Status:      StatusTesting,
		Runtime:     RuntimeSpec{Type: "binary", OSPackages: []string{"git"}},
		Database:    DatabaseSpec{Type: "sqlite"},
		Strategy:    "binary-service",
		Source: SourceSpec{
			Kind: "github-release", GitHub: "go-gitea/gitea", AssetRe: `^gitea-[0-9.]+-linux-(amd64|arm64)$`,
			// The binary comes from dl.gitea.com; its SHA-256 must match both
			// the file beside it there and the one attached to the GitHub release.
			Integrity: "sha256-sidecar",
		},
		Service: &ServiceSpec{Type: "worker", Proxy: true},
		Health:  HealthSpec{Type: "http", Path: "/"},
		Backup:  "files", Upgrade: "binary-replace", Uninstall: "files",
		License: LicenseSpec{SPDX: "MIT", URL: "https://github.com/go-gitea/gitea/blob/main/LICENSE"},
		Compat:  Compat{Arch: []string{"amd64", "arm64"}, MinDiskMB: 500, MinRAMMB: 256},
	}
}

var (
	versionTag = regexp.MustCompile(`^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$`)
	hex64      = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// giteaArch is the Gitea download name of this server's CPU, or "".
func giteaArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	}
	return ""
}

// latestStable is the newest stable release of a GitHub project, without the
// leading "v".
func latestStable(ctx context.Context, client *http.Client, repo, name string) (string, error) {
	rels, err := githubReleases(ctx, client, AppDef{Name: name, GitHub: repo}, "/releases/latest")
	if err != nil {
		return "", err
	}
	if len(rels) == 0 || rels[0].Draft || rels[0].Prerelease || rels[0].TagName == "" {
		return "", fmt.Errorf("no stable %s release was found", name)
	}
	v := strings.TrimPrefix(rels[0].TagName, "v")
	if !versionTag.MatchString(v) {
		return "", fmt.Errorf("the latest %s release (%q) is not a plain version number", name, rels[0].TagName)
	}
	return v, nil
}

// checksumFrom reads "<sha256>  <name>" from url and returns the hash.
func checksumFrom(ctx context.Context, client *http.Client, url, name string) (string, error) {
	text, err := fetchText(ctx, client, url)
	if err != nil {
		return "", err
	}
	f := strings.Fields(text)
	if len(f) < 1 || !hex64.MatchString(strings.ToLower(f[0])) {
		return "", fmt.Errorf("the checksum file for %s is not a SHA-256", name)
	}
	if len(f) > 1 && strings.TrimPrefix(f[1], "*") != name {
		return "", fmt.Errorf("the checksum file names %s, not %s", f[1], name)
	}
	return strings.ToLower(f[0]), nil
}

// giteaConfig is app.ini: loopback only, SQLite, the web installer left to the
// customer to finish (it asks for the first administrator, so NimVor never
// handles an administrator password) and no secrets of its own.
func giteaConfig(r Request, target string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "APP_NAME = Gitea\nRUN_USER = %s\nRUN_MODE = prod\nWORK_PATH = %s\n\n", r.User, target)
	fmt.Fprintf(&b, "[server]\nPROTOCOL = http\nHTTP_ADDR = 127.0.0.1\nHTTP_PORT = %d\nDOMAIN = %s\nROOT_URL = %s/\nDISABLE_SSH = true\nSTART_SSH_SERVER = false\n\n", r.Port, r.Domain, r.URL)
	fmt.Fprintf(&b, "[database]\nDB_TYPE = sqlite3\nPATH = %s/data/gitea.db\n\n", target)
	fmt.Fprintf(&b, "[repository]\nROOT = %s/repositories\n\n", target)
	fmt.Fprintf(&b, "[security]\nINSTALL_LOCK = false\n\n")
	fmt.Fprintf(&b, "[service]\nDISABLE_REGISTRATION = false\n\n")
	fmt.Fprintf(&b, "[log]\nMODE = file\nROOT_PATH = %s/log\n\n", target)
	fmt.Fprintf(&b, "[session]\nPROVIDER = file\nPROVIDER_CONFIG = %s/data/sessions\n", target)
	return b.String()
}

func installGitea(ctx context.Context, env *host.Env, src Sources, r Request, cred *execx.Credential, log io.Writer) (map[string]any, error) {
	root, err := env.Root.Path(r.Root)
	if err != nil {
		return nil, fmt.Errorf("the website folder could not be resolved")
	}
	target := filepath.Join(root, r.Path)
	arch := giteaArch()
	if arch == "" {
		return nil, fmt.Errorf("Gitea is not available for this server's CPU (%s)", runtime.GOARCH)
	}
	if !env.Runner.Available("git") {
		return nil, fmt.Errorf("git is not installed on this server; Gitea needs it. Install git first")
	}

	// 1. The version: the newest stable release, recorded below.
	fmt.Fprintln(log, "Looking up the newest stable Gitea release…")
	version, err := latestStable(ctx, src.Client, "go-gitea/gitea", "Gitea")
	if err != nil {
		return nil, err
	}
	name := "gitea-" + version + "-linux-" + arch
	fmt.Fprintf(log, "Gitea %s.\n", version)

	// 2. Two independent published checksums, which must agree.
	dl := strings.TrimRight(src.GiteaDL, "/") + "/" + version + "/" + name
	gh := strings.TrimRight(src.GiteaGitHub, "/") + "/v" + version + "/" + name + ".sha256"
	sumA, err := checksumFrom(ctx, src.Client, dl+".sha256", name)
	if err != nil {
		return nil, fmt.Errorf("the Gitea checksum from the download server could not be read: %v", err)
	}
	sumB, err := checksumFrom(ctx, src.Client, gh, name)
	if err != nil {
		return nil, fmt.Errorf("the Gitea checksum from the GitHub release could not be read: %v", err)
	}
	if sumA != sumB {
		return nil, fmt.Errorf("the two published Gitea checksums disagree; nothing was installed")
	}

	// 3. Download and verify.
	fmt.Fprintln(log, "Downloading Gitea…")
	file, got, err := downloadSum(ctx, env, src.Client, dl, "sha256", log)
	if err != nil {
		return nil, err
	}
	defer os.Remove(file)
	if got != sumA {
		return nil, fmt.Errorf("the Gitea download does not match its published checksum; nothing was installed")
	}
	fmt.Fprintln(log, "Checksum verified against the download server and the GitHub release.")

	// 4. Write the files as the website account.
	existed := env.Root.Exists(target)
	created := []string{"bin", "custom", "data", "repositories", "log"}
	fail := func(err error) (map[string]any, error) {
		cleanup(context.Background(), env, cred, target, created, log)
		if !existed {
			// The folder was made by this install: remove it too (only if empty).
			if _, rerr := asUser(context.Background(), env, cred, root, nil, nil, "rmdir -- "+shQuote(r.Path)); rerr != nil {
				fmt.Fprintf(log, "Cleanup: the folder %s could not be removed: %v.\n", r.Path, rerr)
			}
		}
		return nil, err
	}
	if _, err := asUser(ctx, env, cred, root, nil, log, "umask 027 && mkdir -p -- "+shQuote(r.Path)); err != nil {
		return nil, fmt.Errorf("the folder %s could not be created", r.Path)
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := asUser(ctx, env, cred, target, f, log, "umask 027 && mkdir -p bin custom/conf data repositories log && cat > bin/gitea.new && chmod 750 bin/gitea.new && mv -f bin/gitea.new bin/gitea"); err != nil {
		return fail(fmt.Errorf("the Gitea files could not be written"))
	}
	if _, err := asUser(ctx, env, cred, target, strings.NewReader(giteaConfig(r, target)), log, "umask 077 && cat > custom/conf/app.ini"); err != nil {
		return fail(fmt.Errorf("the Gitea configuration could not be written"))
	}
	fmt.Fprintln(log, "Wrote the program and its configuration.")

	// 5. The supervised service, as an ordinary NimVor worker.
	spec := workers.Spec{
		Slug: r.ServiceSlug, Name: "Gitea", Domain: r.Domain, User: r.User, Root: r.Root, Directory: r.Path,
		Command: shQuote(filepath.Join(target, "bin", "gitea")) + " web --config " + shQuote(filepath.Join(target, "custom", "conf", "app.ini")),
		Env:     map[string]string{"GITEA_WORK_DIR": target, "HOME": target, "USER": r.User},
		Restart: "always", Procs: 1, Enabled: true,
	}
	if _, err := workers.Apply(ctx, env, spec); err != nil {
		return fail(fmt.Errorf("the Gitea service could not be created: %v", err))
	}
	fmt.Fprintln(log, "Created and started the Gitea service.")

	// 6. It must answer before the installation counts as done.
	if err := healthCheck(ctx, r.Port, "/", 90*time.Second); err != nil {
		_, _ = workers.Remove(context.Background(), env, r.ServiceSlug)
		return fail(fmt.Errorf("Gitea started but is not healthy: %v. Its service was removed again", err))
	}
	fmt.Fprintln(log, "Gitea answers on its port.")
	return map[string]any{
		"app": "gitea", "version": version, "path": r.Path, "strategy": "binary-service", "port": r.Port,
		"staged": true, "finish_url": strings.TrimRight(r.URL, "/") + "/",
		"service": serviceResult(spec),
	}, nil
}
