//go:build realhost

package redismgr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"os"
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
)

// Requires a local Redis (or Valkey on RHEL 10) with its default
// configuration file.
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

// Key browser against a real Redis, including binary values, newlines,
// every collection type, prefix confinement and authentication.
func TestRealHostRedisBrowser(t *testing.T) {
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
	call := func(name string, params map[string]any) (any, error) {
		a, _ := r.Lookup(name)
		b, _ := json.Marshal(params)
		return a.Handler(ctx, b)
	}
	must := func(name string, params map[string]any) any {
		t.Helper()
		res, err := call(name, params)
		if err != nil {
			t.Fatalf("%s: %+v", name, err)
		}
		return res
	}
	b := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	// Work in database 9 with a password set, then clean up.
	must("redis.password", map[string]any{"password": "browser-test-password-123"})
	t.Cleanup(func() {
		if _, err := call("redis.flush", map[string]any{"db": 9, "confirm": "db9"}); err != nil {
			t.Errorf("cleanup: %v", err)
		}
		call("redis.password", map[string]any{"password": ""})
	})
	edit := func(p map[string]any) { t.Helper(); p["db"] = 9; must("redis.key_edit", p) }
	edit(map[string]any{"op": "create", "type": "string", "key": b("shop:greeting"), "value": b("hello\nworld\r\n"), "ttl": 3600})
	edit(map[string]any{"op": "create", "type": "string", "key": b("shop:bin"), "value": base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 255})})
	edit(map[string]any{"op": "create", "type": "hash", "key": b("shop:user:1"), "field": b("name"), "value": b("Ada")})
	edit(map[string]any{"op": "hset", "key": b("shop:user:1"), "field": b("email"), "value": b("ada@example.com")})
	edit(map[string]any{"op": "create", "type": "list", "key": b("shop:queue"), "value": b("a")})
	edit(map[string]any{"op": "rpush", "key": b("shop:queue"), "value": b("b")})
	edit(map[string]any{"op": "create", "type": "zset", "key": b("shop:rank"), "value": b("x"), "score": "2.5"})
	edit(map[string]any{"op": "create", "type": "set", "key": b("other:secret"), "value": b("s")})

	res := must("redis.keys", map[string]any{"db": 9, "prefix": "shop:", "pattern": "*"}).(map[string]any)
	keys := res["keys"].([]KeyInfo)
	if len(keys) != 5 || res["cursor"] != "0" {
		t.Fatalf("keys = %+v", res)
	}
	for _, k := range keys {
		if !strings.HasPrefix(k.Key.Text, "shop:") {
			t.Errorf("key outside prefix listed: %+v", k)
		}
	}
	// Glob characters in the prefix are literal.
	res = must("redis.keys", map[string]any{"db": 9, "prefix": "sh*", "pattern": "*"}).(map[string]any)
	if len(res["keys"].([]KeyInfo)) != 0 {
		t.Errorf("prefix glob was not escaped")
	}

	kv := must("redis.key_get", map[string]any{"db": 9, "prefix": "shop:", "key": b("shop:greeting")}).(*KeyValue)
	if kv.Value.Text != "hello\nworld\r\n" || kv.TTL <= 0 {
		t.Errorf("string = %+v", kv)
	}
	kv = must("redis.key_get", map[string]any{"db": 9, "prefix": "shop:", "key": b("shop:bin")}).(*KeyValue)
	raw, _ := base64.StdEncoding.DecodeString(kv.Value.B64)
	if !kv.Value.Binary || string(raw) != string([]byte{0, 1, 2, 255}) {
		t.Errorf("binary = %+v", kv.Value)
	}
	kv = must("redis.key_get", map[string]any{"db": 9, "key": b("shop:user:1")}).(*KeyValue)
	if kv.Type != "hash" || kv.Length != 2 {
		t.Errorf("hash = %+v", kv)
	}
	kv = must("redis.key_get", map[string]any{"db": 9, "key": b("shop:rank")}).(*KeyValue)
	if kv.Type != "zset" || *kv.Elements[0].Score != "2.5" {
		t.Errorf("zset = %+v", kv)
	}
	if _, err := call("redis.key_get", map[string]any{"db": 9, "prefix": "shop:", "key": b("other:secret")}); err == nil {
		t.Error("read outside the prefix")
	}
	if _, err := call("redis.key_edit", map[string]any{"db": 9, "prefix": "shop:", "op": "rename", "key": b("shop:queue"), "new_key": b("other:x")}); err == nil {
		t.Error("renamed outside the prefix")
	}
	edit(map[string]any{"op": "rename", "prefix": "shop:", "key": b("shop:queue"), "new_key": b("shop:jobs")})
	edit(map[string]any{"op": "expire", "key": b("shop:jobs"), "ttl": 0})
	if _, err := call("redis.key_delete", map[string]any{"db": 9, "prefix": "shop:", "keys": []string{b("other:secret")}}); err == nil {
		t.Error("deleted outside the prefix")
	}
	del := must("redis.key_delete", map[string]any{"db": 9, "prefix": "shop:", "keys": []string{b("shop:jobs"), b("shop:bin")}}).(map[string]any)
	if del["deleted"].(int64) != 2 {
		t.Errorf("deleted = %v", del)
	}
}
