//go:build realhost

package dbmgr

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
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

// Requires root and a running MariaDB/MySQL: anonymous accounts are removed,
// named ones are left.
func TestRealHostDropAnonymousUsers(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := filesystem.Host()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(60*time.Second), jm, log)
	ctx := context.Background()
	c := Client{Env: env}
	if _, err := c.Query(ctx, "SELECT 1;"); err != nil {
		t.Skipf("no database server: %v", err)
	}
	if _, err := c.Query(ctx, "CREATE USER ''@'localhost'; CREATE USER 'whacpanonkeep'@'localhost';"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Query(ctx, "DROP USER IF EXISTS ''@'localhost'; DROP USER IF EXISTS 'whacpanonkeep'@'localhost';")
	})
	r := actions.NewRegistry(true)
	Register(r, env)
	a, _ := r.Lookup("db.drop_anonymous_users")
	res, err := a.Handler(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(res); string(b) == `{"removed":[]}` {
		t.Fatalf("nothing removed: %s", b)
	}
	rows, _ := c.Query(ctx, "SELECT COUNT(*) FROM mysql.user WHERE User = ''; SELECT COUNT(*) FROM mysql.user WHERE User = 'whacpanonkeep';")
	if len(rows) != 2 || rows[0][0] != "0" || rows[1][0] != "1" {
		t.Fatalf("after: %v", rows)
	}
}
