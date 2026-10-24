package http

// This suite drives the Redis key browser (RedisBrowserController): the
// server-wide + per-website pages (the fixed prefix), the operation
// endpoint (CSRF, the field whitelist, the audit of changing ops, the
// agent errors), the role split and the 404s.

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestRedisServerPage(t *testing.T) {
	h, _, _, _ := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/redis/browser", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/redis/browser = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		`data-api="/redis/browser/api"`,
		`data-prefix=""`,
		`data-fixed="0"`,
		`data-rb-prefix`,
		"<h1>Redis keys</h1>",
		"<option value=\"0\">0</option>",
		"<option value=\"15\">15</option>",
		"<a href=\"/redis\">← Redis</a>",
		"/assets/js/redisbrowser.js",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "Keys are limited to this prefix") {
		t.Errorf("server-wide page shows the fixed-prefix text")
	}
}

func TestRedisSitePage(t *testing.T) {
	h, _, _, _ := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/10/redis", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/redis = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		`data-api="/sites/10/redis/api"`,
		`data-prefix="u_bob:"`,
		`data-fixed="1"`,
		"Keys are limited to this prefix",
		"<h1>Redis keys of bob.example</h1>",
		`<a href="/sites/10">← bob.example</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, `data-rb-prefix`) {
		t.Errorf("site page shows the free prefix input")
	}
}

func TestRedisPageForbidden(t *testing.T) {
	h, _, _, _ := cacheEnv(t)
	bob := loginAsUser(t, h, "bob")
	if rr := get(t, h, "/sites/10/redis", bob); rr.Code != http.StatusForbidden {
		t.Errorf("/sites/10/redis (bob) = %d: %s", rr.Code, rr.Body.String())
	}
	if rr := get(t, h, "/redis/browser", bob); rr.Code != http.StatusForbidden {
		t.Errorf("/redis/browser (bob) = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRedisPageNotFound(t *testing.T) {
	h, _, _, _ := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	if rr := get(t, h, "/sites/99/redis", admin); rr.Code != http.StatusNotFound {
		t.Errorf("/sites/99/redis = %d: %s", rr.Code, rr.Body.String())
	}
	if rr := get(t, h, "/sites/x/redis", admin); rr.Code != http.StatusNotFound {
		t.Errorf("/sites/x/redis = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRedisSiteApiKeys(t *testing.T) {
	h, _, sa, db := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/sites/10/redis")

	rr := postRedis(t, h, "/sites/10/redis/api", map[string]any{"action": "keys", "db": 0, "pattern": "*"}, admin, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("api keys = %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Keys []struct {
				Key struct {
					Text string `json:"text"`
				} `json:"key"`
				Type string `json:"type"`
				TTL  int64  `json:"ttl"`
			} `json:"keys"`
			Cursor string `json:"cursor"`
			DbSize int64  `json:"db_size"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || !body.Success {
		t.Fatalf("keys reply = %s", rr.Body.String())
	}
	if len(body.Data.Keys) != 2 || body.Data.Keys[0].Key.Text != "u_bob:session:1" || body.Data.Keys[0].TTL != 300 {
		t.Fatalf("keys = %+v", body.Data.Keys)
	}
	if got := agentParam(t, sa, "redis.keys", "prefix"); got != "u_bob:" {
		t.Errorf("redis.keys prefix = %v (the site prefix must be forced)", got)
	}
	if got := agentParam(t, sa, "redis.keys", "db"); got != 0 {
		t.Errorf("redis.keys db = %v", got)
	}
	if got := agentParam(t, sa, "redis.keys", "pattern"); got != "*" {
		t.Errorf("redis.keys pattern = %v", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action LIKE 'redis.key_%'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("read-only op audited: %d rows", n)
	}
}

func TestRedisSiteApiEditAudited(t *testing.T) {
	h, _, sa, db := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/sites/10/redis")

	rr := postRedis(t, h, "/sites/10/redis/api", map[string]any{
		"action": "edit", "op": "set", "db": 1,
		"key": b64("u_bob:session:1"), "value": b64("hello"), "ttl": 300,
	}, admin, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("api edit = %d: %s", rr.Code, rr.Body.String())
	}
	if got := agentParam(t, sa, "redis.key_edit", "prefix"); got != "u_bob:" {
		t.Errorf("redis.key_edit prefix = %v", got)
	}
	if got := agentParam(t, sa, "redis.key_edit", "ttl"); got != int64(300) {
		t.Errorf("redis.key_edit ttl = %v (int, not string)", got)
	}
	var action, targetType, targetID, keys string
	if err := db.QueryRow(`SELECT action, target_type, target_id, details FROM audit_logs WHERE action = 'redis.key_set'`).
		Scan(&action, &targetType, &targetID, &keys); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if targetType != "website" || targetID != "10" {
		t.Errorf("audit target = %s %s", targetType, targetID)
	}
	var det struct {
		DB   any   `json:"db"`
		Keys []any `json:"keys"`
	}
	if err := json.Unmarshal([]byte(keys), &det); err != nil || len(det.Keys) != 1 {
		t.Fatalf("audit details = %s", keys)
	}
	if s, _ := det.Keys[0].(string); s != "u_bob:session:1" {
		t.Errorf("audit keys = %v (the decoded key, truncated)", det.Keys)
	}
}

func TestRedisSiteApiDeleteAudited(t *testing.T) {
	h, _, _, db := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/sites/10/redis")

	rr := postRedis(t, h, "/sites/10/redis/api", map[string]any{
		"action": "delete", "keys": []any{b64("u_bob:a"), b64("u_bob:b")},
	}, admin, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("api delete = %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Deleted int `json:"deleted"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || !body.Success || body.Data.Deleted != 2 {
		t.Fatalf("delete reply = %s", rr.Body.String())
	}
	var targetType, targetID, details string
	if err := db.QueryRow(`SELECT target_type, target_id, details FROM audit_logs WHERE action = 'redis.key_delete'`).
		Scan(&targetType, &targetID, &details); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if targetType != "website" || targetID != "10" {
		t.Errorf("audit target = %s %s", targetType, targetID)
	}
	var det struct {
		Keys []string `json:"keys"`
	}
	if err := json.Unmarshal([]byte(details), &det); err != nil || len(det.Keys) != 2 || det.Keys[0] != "u_bob:a" {
		t.Fatalf("audit details = %s", details)
	}
}

func TestRedisServerApiAuditTarget(t *testing.T) {
	h, _, sa, db := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/redis/browser")

	rr := postRedis(t, h, "/redis/browser/api", map[string]any{
		"action": "edit", "op": "expire", "prefix": "u_bob:",
		"key": b64("u_bob:session:1"), "ttl": 60,
	}, admin, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("api edit = %d: %s", rr.Code, rr.Body.String())
	}
	if got := agentParam(t, sa, "redis.key_edit", "prefix"); got != "u_bob:" {
		t.Errorf("redis.key_edit prefix = %v", got)
	}
	var targetType, targetID string
	if err := db.QueryRow(`SELECT target_type, target_id FROM audit_logs WHERE action = 'redis.key_expire'`).
		Scan(&targetType, &targetID); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if targetType != "server" || targetID != "1" {
		t.Errorf("audit target = %s %s (server-wide)", targetType, targetID)
	}
}

func TestRedisApiUnknownOp(t *testing.T) {
	h, _, sa, _ := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/sites/10/redis")

	rr := postRedis(t, h, "/sites/10/redis/api", map[string]any{"action": "drop"}, admin, token)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("api drop = %d: %s", rr.Code, rr.Body.String())
	}
	if n := len(sa.actions); n != 0 {
		t.Errorf("unknown op reached the agent: %v", sa.actions)
	}
}

func TestRedisApiBadCSRF(t *testing.T) {
	h, _, sa, _ := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")

	rr := postRedis(t, h, "/sites/10/redis/api", map[string]any{"action": "keys"}, admin, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("api without token = %d: %s", rr.Code, rr.Body.String())
	}
	if n := len(sa.actions); n != 0 {
		t.Errorf("the agent was called despite the bad CSRF: %v", sa.actions)
	}
}

func TestRedisSiteApiNotFound(t *testing.T) {
	h, _, _, _ := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/sites/10/redis")

	rr := postRedis(t, h, "/sites/99/redis/api", map[string]any{"action": "keys"}, admin, token)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("api /sites/99 = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRedisApiAgentDown(t *testing.T) {
	h, _, sa, _ := cacheEnv(t)
	sa.mu.Lock()
	sa.failActions = map[string]string{"redis.keys": "Redis is not running on this server."}
	sa.mu.Unlock()
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/sites/10/redis")

	rr := postRedis(t, h, "/sites/10/redis/api", map[string]any{"action": "keys"}, admin, token)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("api keys = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Redis is not running on this server.") {
		t.Errorf("agent message missing: %s", rr.Body.String())
	}
}

func TestRedisServerApiLongPrefixIgnored(t *testing.T) {
	h, _, sa, _ := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/redis/browser")

	long := strings.Repeat("x", 300)
	rr := postRedis(t, h, "/redis/browser/api", map[string]any{"action": "keys", "prefix": long}, admin, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("api keys = %d: %s", rr.Code, rr.Body.String())
	}
	if got := agentParam(t, sa, "redis.keys", "prefix"); got != "" {
		t.Errorf("prefix > 200 chars must be dropped, got %v", got)
	}
}
