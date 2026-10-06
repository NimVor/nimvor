//go:build realhost

package modsec

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

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// The real connector and Core Rule Set: every generated file passes the
// real nginx -t, and when Nginx is running a SQL injection is blocked (on),
// passed and logged (detect), and passed with the Core Rule Set removed
// for the website. Machines without the packages have nothing to test.
func TestRealHostModSecurity(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := filesystem.Host()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	e := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(60*time.Second), jm, log)
	if !ModuleLoaded(e) || !FindCRS(e).Available {
		t.Log("the ModSecurity connector or the Core Rule Set is not installed here: nothing to test")
		return
	}
	if out, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
		t.Fatalf("the Nginx configuration is invalid before the test: %s", out)
	}
	r := actions.NewRegistry(true)
	Register(r, e)
	call := func(name, params string) error {
		a, _ := r.Lookup(name)
		_, err := a.Handler(context.Background(), json.RawMessage(params))
		return err
	}
	// Reload: the agent does through systemd; without it (a container)
	// signal a running Nginx directly.
	running := func() bool { return exec.Command("pgrep", "-x", "nginx").Run() == nil }
	reload := func() {
		if _, err := os.Stat("/run/systemd/system"); err != nil && running() {
			exec.Command("nginx", "-s", "reload").Run()
		}
		time.Sleep(700 * time.Millisecond)
	}

	const domain = "whacp-modsec-test.test"
	docroot := "/var/www/whacp-modsec-test"
	os.MkdirAll(docroot, 0o755)
	os.WriteFile(filepath.Join(docroot, "index.html"), []byte("ok\n"), 0o644)
	vhost := filepath.Join(e.M.NginxAvailable, "whacp-"+domain+".conf")
	link := filepath.Join(e.M.NginxEnabled, "whacp-"+domain+".conf")
	os.WriteFile(vhost, []byte("server {\n    listen 127.0.0.1:18089;\n    server_name "+domain+";\n    root "+docroot+";\n"+vhostMarker+"}\n"), 0o644)
	if link != vhost {
		os.Symlink(vhost, link)
	}
	t.Cleanup(func() {
		call("modsec.site", `{"domain":"`+domain+`","mode":"off"}`)
		call("modsec.configure", `{"enabled":false,"crs":{"enabled":true}}`)
		if link != vhost {
			os.Remove(link)
		}
		os.Remove(vhost)
		os.RemoveAll(docroot)
		os.RemoveAll(Dir(e))
		reload()
	})

	if err := call("modsec.configure", `{"enabled":true,"crs":{"enabled":true,"paranoia":1},"custom":{"enabled":true}}`); err != nil {
		t.Fatal(err)
	}
	if err := call("modsec.custom_set", `{"rules":"SecRule ARGS:whacpprobe \"@streq yes\" \"id:10001,phase:2,deny,status:403,log,msg:'WhaCP probe'\"\n"}`); err != nil {
		t.Fatal(err)
	}
	// A rule the parser rejects is refused by nginx -t and rolled back.
	if err := call("modsec.custom_set", `{"rules":"SecRule ARGS \"@rx x\" \"id:10002,deny,nosuchaction\"\n"}`); err == nil {
		t.Fatal("a broken rule was accepted")
	}
	if b, _ := os.ReadFile(filepath.Join(Dir(e), "custom.conf")); !strings.Contains(string(b), "whacpprobe") {
		t.Fatal("custom rules not restored after the failed change")
	}

	get := func(q string) int {
		req, _ := http.NewRequest("GET", "http://127.0.0.1:18089/"+q, nil)
		req.Host = domain
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	sqli := "?id=1%27%20or%201=1--"
	for _, c := range []struct {
		site       string
		sqli, mine int
	}{
		{`"mode":"on"`, 403, 403},
		{`"mode":"detect"`, 200, 200},
		{`"mode":"on","exclude":["crs"]`, 200, 403},
		{`"mode":"on","disabled_rules":[10001]`, 403, 200},
		{`"mode":"off"`, 200, 200},
	} {
		if err := call("modsec.site", `{"domain":"`+domain+`",`+c.site+`}`); err != nil {
			t.Fatalf("%s: %v", c.site, err)
		}
		if !running() {
			continue
		}
		reload()
		if got := get(sqli); got != c.sqli {
			t.Errorf("%s: SQL injection answered %d, want %d", c.site, got, c.sqli)
		}
		if got := get("?whacpprobe=yes"); got != c.mine {
			t.Errorf("%s: custom rule answered %d, want %d", c.site, got, c.mine)
		}
		if got := get(""); got != 200 {
			t.Errorf("%s: a normal request answered %d", c.site, got)
		}
	}
	if running() {
		ev, err := ReadEvents(AuditLog(e), map[string]bool{domain: true}, 50)
		if err != nil || len(ev) == 0 {
			t.Fatalf("no audit events for %s: %v", domain, err)
		}
	} else {
		t.Log("Nginx is not running: the configuration was checked with nginx -t only")
	}
}

// The whole source route on this machine: with the packaged connector
// removed, the installer builds the official connector against this
// Nginx's own source package, installs it versioned, and Nginx loads it;
// rolling back leaves Nginx as it was. It changes the machine (removes the
// connector package), so it only runs when asked.
func TestRealHostBuildConnectorFromSource(t *testing.T) {
	if os.Getenv("WHACP_REALHOST_BUILD") != "1" {
		// Opt-in (it changes the machine): not run is not a skipped check.
		t.Log("not run: set WHACP_REALHOST_BUILD=1 (removes the connector package, builds from source)")
		return
	}
	root := filesystem.Host()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	e := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(30*time.Minute), jm, log)
	ctx := context.Background()
	if out, err := exec.Command("apt-get", "remove", "-y", "-q", "libnginx-mod-http-modsecurity").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	before := Connector(ctx, e, true)
	if before.State == "functional" {
		t.Fatalf("still functional: %+v", before)
	}
	logW := &strings.Builder{}
	res, err := Install(ctx, e, DefaultSources, InstallOptions{Method: "source"}, io.MultiWriter(logW, os.Stderr))
	if err != nil {
		t.Fatalf("%v\n%s", err, logW)
	}
	t.Logf("installed: %v", res)
	after := Connector(ctx, e, true)
	if after.State != "functional" || after.Package != "" || !strings.HasPrefix(after.Modules[0].Path, ModuleDir) {
		t.Fatalf("%+v", after)
	}
	if out, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
		t.Fatalf("nginx -t: %s", out)
	}
	if _, err := e.LookupUser(BuildAccount); err == nil {
		t.Fatal("build account left behind")
	}
	// Roll back: the module goes, Nginx still passes nginx -t.
	if _, err := RollbackInstall(ctx, e, res["transaction"].(string), io.MultiWriter(logW, os.Stderr)); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if c := Connector(ctx, e, false); c.State == "functional" {
		t.Fatalf("still loaded after rollback: %+v", c)
	}
	if out, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
		t.Fatalf("nginx -t after rollback: %s", out)
	}
}

// The pinned Core Rule Set release downloads, matches its checksum and
// unpacks into rules WhaCP can load (nothing is installed).
func TestRealHostCRSRelease(t *testing.T) {
	b, err := download(context.Background(), DefaultCRSSource.Client, DefaultCRSSource.URL)
	if err != nil {
		t.Skipf("no network: %v", err)
	}
	if got := sum(b); got != DefaultCRSSource.SHA256 {
		t.Fatalf("checksum %s", got)
	}
	files, err := CRSFiles(b)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for name := range files {
		if strings.HasSuffix(name, ".conf") {
			n++
		}
	}
	if n < 20 || files["crs-setup.conf"] == nil || files["rules/REQUEST-942-APPLICATION-ATTACK-SQLI.conf"] == nil {
		t.Fatalf("%d rule files", n)
	}
	t.Logf("%d files, %d .conf", len(files), n)
}
