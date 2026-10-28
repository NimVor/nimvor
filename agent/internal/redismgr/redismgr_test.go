package redismgr

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
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
	backups, _ := h.Env.Root.Glob("/var/lib/nimvor-agent/config-backups/redis.conf.*")
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

// Redis 6.0 (Ubuntu 22.04) has no channel rules in ACLs; 6.2 and later
// limit channels to the website's prefix too.
func TestACLArgsByVersion(t *testing.T) {
	for _, c := range []struct {
		version string
		want    bool
	}{{"6.0.16", false}, {"5.0.7", false}, {"6.2.14", true}, {"7.0.15", true}, {"8.0.2", true}, {"", true}} {
		if got := ChannelACLs(c.version); got != c.want {
			t.Errorf("%q: channels = %v", c.version, got)
		}
	}
	old := strings.Join(ACLArgs("shop", "pw", "shop:", false), " ")
	cur := strings.Join(ACLArgs("shop", "pw", "shop:", true), " ")
	if strings.Contains(old, "channels") || strings.Contains(old, "&") || !strings.Contains(old, "~shop:* +@all -@admin -@dangerous") {
		t.Errorf("6.0 args: %s", old)
	}
	if !strings.Contains(cur, "~shop:* resetchannels &shop:* +@all -@admin -@dangerous") {
		t.Errorf("6.2 args: %s", cur)
	}
}
