//go:build realhost

package statsmgr

import (
	"context"
	"fmt"
	"io"
	"log/slog"
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

// Requires root and the awstats package. Runs the real update through the
// agent's command runner, as the website's account, on a log nginx created
// (root-owned, in the account's logs folder).
func TestRealHostStatsUpdate(t *testing.T) {
	if os.Geteuid() == 0 && program(&host.Env{Root: filesystem.Host()}) == "" {
		// Not a missing test prerequisite when the distribution does not
		// package AWStats at all (EPEL 10): WhaCP reports the component
		// as not available there.
		if _, err := exec.LookPath("dnf"); err == nil && exec.Command("dnf", "-q", "list", "awstats").Run() != nil {
			t.Log("AWStats is not packaged for this distribution; nothing to test")
			return
		}
	}
	if os.Geteuid() != 0 || program(&host.Env{Root: filesystem.Host()}) == "" {
		t.Skip("needs root and AWStats")
	}
	sitesRoot, _ := os.MkdirTemp("/tmp", "whacp-stats-")
	os.Chmod(sitesRoot, 0o755)
	t.Cleanup(func() { os.RemoveAll(sitesRoot) })
	exec.Command("useradd", "-M", "-U", "-s", "/usr/sbin/nologin", "-u", "1781", "whacpst").Run()
	t.Cleanup(func() { exec.Command("userdel", "whacpst").Run() })
	root := filepath.Join(sitesRoot, "st.test")
	os.MkdirAll(filepath.Join(root, "logs"), 0o750)
	exec.Command("chown", "-R", "whacpst:whacpst", root).Run()
	now := time.Now().UTC()
	var lines strings.Builder
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&lines, "203.0.113.%d - - [%s] \"GET /page%d HTTP/1.1\" 200 512 \"-\" \"Mozilla/5.0 (X11; Linux x86_64) Chrome/120.0\"\n", i+1, now.Add(-time.Duration(i)*time.Minute).Format("02/Jan/2006:15:04:05 -0700"), i)
	}
	// As "nginx -t" creates it under the agent's umask (0027): root:root 0640.
	os.WriteFile(filepath.Join(root, "logs/access.log"), []byte(lines.String()), 0o640)
	os.Chmod(filepath.Join(root, "logs/access.log"), 0o640)

	cfg := config.Default()
	cfg.SitesRoot = sitesRoot
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	fsys := filesystem.Host()
	env := host.New(cfg, fsys, platform.Detect(fsys), execx.NewSystemRunner(time.Minute), jm, log)
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, cfg.StateDir+"/**")
	r := actions.NewRegistry(true)
	Register(r, env)
	site := `"domain":"st.test","user":"whacpst","root":"` + root + `"`
	if _, err := call(t, r, "stats.enable", `{`+site+`,"enabled":true}`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { call(t, r, "stats.enable", `{`+site+`,"enabled":false}`) })

	res, err := call(t, r, "stats.update", `{`+site+`}`)
	if err != nil {
		t.Fatal(err)
	}
	id := res.(map[string]any)["job_id"].(string)
	j, _ := jm.Wait(context.Background(), id)
	out, _, _ := jm.Log(id, 0)
	if j.Status != "succeeded" {
		t.Fatalf("update %s: %s\n%s", j.Status, j.Error, out)
	}
	res, err = call(t, r, "stats.data", fmt.Sprintf(`{%s,"month":"%s"}`, site, now.Format("200601")))
	if err != nil || !strings.Contains(res.(map[string]any)["content"].(string), "BEGIN_SIDER") {
		t.Fatalf("no data after the update: %v %v\n%s", res, err, out)
	}
}
