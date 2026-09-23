//go:build realhost

package redismgr

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

// Requires a local redis-server using /etc/redis/redis.conf.
func TestRealHostRedis(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := filesystem.Host()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(30*time.Second), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)
	ctx := context.Background()
	do := func(name, params string) any {
		t.Helper()
		a, _ := r.Lookup(name)
		res, err := a.Handler(ctx, json.RawMessage(params))
		if err != nil {
			t.Fatalf("%s: %+v", name, err)
		}
		return res
	}
	info := do("redis.info", `{}`).(map[string]any)
	if info["info"].(map[string]map[string]string)["server"]["redis_version"] == "" {
		t.Fatal("no version")
	}
	do("redis.configure", `{"maxmemory_mb":64,"policy":"allkeys-lru"}`)
	do("redis.password", `{"password":"a-really-long-redis-pass"}`)
	// The agent now authenticates with the password it reads from redis.conf.
	if got := do("redis.info", `{}`).(map[string]any)["maxmemory_policy"]; got != "allkeys-lru" {
		t.Fatalf("policy = %v", got)
	}
	do("redis.acl_set", `{"user":"shop","password":"another-long-password","prefix":"shop:"}`)
	do("redis.acl_del", `{"user":"shop"}`)
	do("redis.password", `{"password":""}`)
	do("redis.configure", `{"maxmemory_mb":0,"policy":"noeviction"}`)
}
