package redismgr

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func TestParseInfo(t *testing.T) {
	info := ParseInfo("# Server\r\nredis_version:7.0.15\r\n\r\n# Keyspace\r\ndb0:keys=3,expires=0,avg_ttl=0\r\n")
	if info["server"]["redis_version"] != "7.0.15" || info["keyspace"]["db0"] != "keys=3,expires=0,avg_ttl=0" {
		t.Fatalf("info = %+v", info)
	}
}

func TestPasswordNeverInArgs(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("redis.configure")
	if _, err := a.Handler(context.Background(), json.RawMessage(`{"maxmemory_mb":512,"policy":"allkeys-lru"}`)); err != nil {
		t.Fatal(err)
	}
	for i, c := range h.Runner.Calls {
		if strings.Contains(c, "super-secret-redis-password") {
			t.Fatalf("password in argv: %s", c)
		}
		if strings.HasPrefix(c, "redis-cli") && !strings.Contains(strings.Join(h.Runner.Opts[i].Env, " "), "REDISCLI_AUTH=super-secret-redis-password") {
			t.Errorf("auth not passed via env for %s", c)
		}
	}
	if !h.Runner.Called("redis-cli --no-auth-warning --raw -h 127.0.0.1 -p 6379 CONFIG SET maxmemory 512mb") || !h.Runner.Called("redis-cli --no-auth-warning --raw -h 127.0.0.1 -p 6379 CONFIG REWRITE") {
		t.Errorf("calls: %v", h.Runner.Calls)
	}
	backups, _ := h.Env.Root.Glob("/var/lib/whacp-agent/config-backups/redis.conf.*")
	if len(backups) != 1 {
		t.Error("redis.conf not backed up before change")
	}
	for _, bad := range []string{`{"maxmemory_mb":1,"policy":"everything"}`, `{"maxmemory_mb":-1,"policy":"noeviction"}`} {
		if _, err := a.Handler(context.Background(), json.RawMessage(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	acl, _ := r.Lookup("redis.acl_set")
	if _, err := acl.Handler(context.Background(), json.RawMessage(`{"user":"default","password":"a-long-password-123","prefix":"x:"}`)); err == nil {
		t.Error("default user may not be redefined")
	}
	if _, err := acl.Handler(context.Background(), json.RawMessage(`{"user":"shop","password":"a-long-password-123","prefix":"shop *"}`)); err == nil {
		t.Error("prefix with space accepted")
	}
}
