package http

// This suite drives the server-side cache management (MemcachedController +
// RedisController/RedisService): the two index pages, the settings/flush/
// password/user actions (the confirmations, the audits, the one-shot
// secret flash, the install job), the role split and the negative cases.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestMemcachedIndex(t *testing.T) {
	h, _, _ := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/memcached", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/memcached = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"<title>Memcached · WhaCP</title>",
		"Distributed memory object cache",
		"text-bg-success",
		"32.0 MB of 64.0 MB",
		"1.6.31",
		"1d 00:08",
		"1,200",
		"94.7 %",
		"9,000 hits",
		"500 misses",
		"4 of 1024",
		"127.0.0.1:11211",
		`action="/memcached/configure"`,
		`action="/memcached/restart"`,
		`action="/memcached/flush"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "Memcached is not installed") {
		t.Errorf("page shows the not-installed state")
	}
}

func TestMemcachedIndexNotInstalled(t *testing.T) {
	h, sa, _ := cacheEnv(t)
	sa.mu.Lock()
	sa.mcInstalled = false
	sa.mu.Unlock()
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/memcached", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/memcached = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	if !strings.Contains(page, "Memcached is not installed on this server.") {
		t.Errorf("page lacks the not-installed state")
	}
	if !strings.Contains(page, `action="/memcached/install"`) {
		t.Errorf("admin page lacks the install form")
	}
	if strings.Contains(page, "32.0 MB") {
		t.Errorf("stats shown although not installed")
	}
}

func TestMemcachedIndexNoInstallPermission(t *testing.T) {
	h, _, _ := cacheEnv(t)
	ops := loginAsUser(t, h, "ops")
	rr := get(t, h, "/memcached", ops)
	if rr.Code != http.StatusOK {
		t.Fatalf("/memcached (ops) = %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `action="/memcached/install"`) {
		t.Errorf("ops (no packages.manage) sees the install form")
	}
	if strings.Contains(rr.Body.String(), `action="/memcached/restart"`) {
		t.Errorf("ops (no services.manage) sees the restart form")
	}
}

func TestMemcachedIndexAgentDown(t *testing.T) {
	h, sa, _ := cacheEnv(t)
	sa.mu.Lock()
	sa.failActions = map[string]string{"memcached.status": "the agent refused"}
	sa.mu.Unlock()
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/memcached", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/memcached = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "the agent refused") {
		t.Errorf("page lacks the agent error")
	}
}

func TestMemcachedConfigure(t *testing.T) {
	h, sa, db := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/memcached")

	form := url.Values{
		"_token":    {token},
		"memory_mb": {"128"},
		"max_conns": {"2048"},
		"listen":    {"127.0.0.1, ::1"},
		"confirm":   {"1"},
	}
	rr := postForm(h, "/memcached/configure", form, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("configure = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, refreshCookie(admin, rr), "/memcached", "Memcached restarted with the new settings.")

	if got := agentParam(t, sa, "memcached.configure", "memory_mb"); got != float64(128) {
		t.Errorf("memory_mb = %v", got)
	}
	if got := agentParam(t, sa, "memcached.configure", "max_conns"); got != float64(2048) {
		t.Errorf("max_conns = %v", got)
	}
	if got := agentParam(t, sa, "memcached.configure", "confirm_edit_package_config"); got != true {
		t.Errorf("confirm_edit_package_config = %v", got)
	}
	listen, _ := agentParam(t, sa, "memcached.configure", "listen").([]any)
	if len(listen) != 2 || listen[0] != "127.0.0.1" || listen[1] != "::1" {
		t.Errorf("listen = %v", listen)
	}
	var action, targetType, targetID, details string
	if err := db.QueryRow(`SELECT action, target_type, target_id, details FROM audit_logs WHERE action = 'memcached.configure'`).
		Scan(&action, &targetType, &targetID, &details); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if targetType != "server" || targetID != "memcached" {
		t.Errorf("audit target = %s %s", targetType, targetID)
	}
	var det map[string]any
	if err := json.Unmarshal([]byte(details), &det); err != nil || det["memory_mb"] != float64(128) {
		t.Errorf("audit details = %s", details)
	}
}

func TestMemcachedConfigureAgentRefused(t *testing.T) {
	h, sa, _ := cacheEnv(t)
	sa.mu.Lock()
	sa.failActions = map[string]string{"memcached.configure": "Memcached did not answer after the restart."}
	sa.mu.Unlock()
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/memcached")

	rr := postForm(h, "/memcached/configure", url.Values{
		"_token": {token}, "memory_mb": {"64"}, "max_conns": {"1024"},
		"listen": {"127.0.0.1"}, "confirm": {"1"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("configure = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/memcached", "Memcached did not answer after the restart.")
}

func TestMemcachedFlush(t *testing.T) {
	h, sa, db := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/memcached")

	// The wrong word.
	rr := postForm(h, "/memcached/flush", url.Values{"_token": {token}, "confirm": {"yes"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("flush = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/memcached", "Type \u0022flush\u0022 to confirm.")
	if n := len(sa.actions); n != 0 {
		t.Errorf("the agent was called for the wrong confirm: %v", sa.actions)
	}

	// The right word.
	rr = postForm(h, "/memcached/flush", url.Values{"_token": {token}, "confirm": {"flush"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("flush = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/memcached", "All Memcached items were removed.")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'memcached.flush'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("memcached.flush audits = %d", n)
	}
	if got := agentParam(t, sa, "memcached.flush", "confirm"); got != "flush" {
		t.Errorf("confirm = %v", got)
	}
}

func TestMemcachedInstall(t *testing.T) {
	h, sa, db := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/memcached")

	rr := postForm(h, "/memcached/install", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("install = %d", rr.Code)
	}
	cookie := refreshCookie(admin, rr)
	rr = get(t, h, "/memcached", cookie)
	body := rr.Body.String()
	if !strings.Contains(body, "Installing Memcached…") {
		t.Errorf("install flash missing: %s", body)
	}
	if !strings.Contains(body, "Job <code>abcdef0123456789</code> started in the background.") {
		t.Errorf("job infobar missing: %s", body)
	}
	if got := agentParam(t, sa, "packages.install", "name"); got != "memcached" {
		t.Errorf("packages.install name = %v", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'packages.install' AND target_id = 'memcached'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("packages.install audits = %d", n)
	}
}

func TestMemcachedRestart(t *testing.T) {
	h, sa, db := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/memcached")

	rr := postForm(h, "/memcached/restart", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("restart = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/memcached", "Memcached restarted.")
	if got := agentParam(t, sa, "services.control", "unit"); got != "memcached.service" {
		t.Errorf("unit = %v", got)
	}
	if got := agentParam(t, sa, "services.control", "op"); got != "restart" {
		t.Errorf("op = %v", got)
	}
	var targetType, targetID string
	if err := db.QueryRow(`SELECT target_type, target_id FROM audit_logs WHERE action = 'services.restart'`).
		Scan(&targetType, &targetID); err != nil || targetType != "service" || targetID != "memcached.service" {
		t.Errorf("services.restart audit = %s %s: %v", targetType, targetID, err)
	}
}

func TestMemcachedForbidden(t *testing.T) {
	h, _, _ := cacheEnv(t)
	bob := loginAsUser(t, h, "bob")
	if rr := get(t, h, "/memcached", bob); rr.Code != http.StatusForbidden {
		t.Errorf("/memcached (bob) = %d", rr.Code)
	}
	rr := postForm(h, "/memcached/restart", url.Values{"_token": {"x"}}, bob)
	if rr.Code != http.StatusForbidden {
		t.Errorf("restart (bob) = %d", rr.Code)
	}
}

func TestRedisIndex(t *testing.T) {
	h, _, _ := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/redis", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/redis = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"<title>Redis · WhaCP</title>",
		"In-memory cache and queue server",
		`href="/redis/browser"`,
		"7.2.4",
		"1.0 days",
		"5.20M of 1024 MB",
		"<code>allkeys-lru</code>",
		">4</",
		"95.2 %",
		"<code>0</code> 42 keys",
		"<code>1</code> 7 keys",
		"<code>u_bob</code>",
		"<code>u_carol</code>",
		`value="noeviction"`,
		`value="10"`,
		"bob.example (u_bob)",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestRedisIndexAgentDown(t *testing.T) {
	h, sa, _ := cacheEnv(t)
	sa.mu.Lock()
	sa.failActions = map[string]string{"redis.info": "Redis is not reachable."}
	sa.mu.Unlock()
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/redis", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/redis = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"Redis is not reachable.",
		"Install Redis",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, `href="/redis/browser"`) {
		t.Errorf("the browse button is shown although Redis is down")
	}
}

func TestRedisConfigure(t *testing.T) {
	h, sa, db := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/redis")

	rr := postForm(h, "/redis/configure", url.Values{
		"_token": {token}, "maxmemory_mb": {"512"}, "policy": {"noeviction"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("configure = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, refreshCookie(admin, rr), "/redis", "Redis settings saved to redis.conf.")
	if got := agentParam(t, sa, "redis.configure", "maxmemory_mb"); got != float64(512) {
		t.Errorf("maxmemory_mb = %v", got)
	}
	if got := agentParam(t, sa, "redis.configure", "policy"); got != "noeviction" {
		t.Errorf("policy = %v", got)
	}
	var details string
	if err := db.QueryRow(`SELECT details FROM audit_logs WHERE action = 'redis.configure'`).Scan(&details); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	var det map[string]any
	if err := json.Unmarshal([]byte(details), &det); err != nil || det["maxmemory_mb"] != float64(512) || det["policy"] != "noeviction" {
		t.Errorf("audit details = %s", details)
	}
}

func TestRedisConfigureInvalid(t *testing.T) {
	h, _, _ := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/redis")

	rr := postForm(h, "/redis/configure", url.Values{
		"_token": {token}, "maxmemory_mb": {"2000000"}, "policy": {"noeviction"},
	}, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("configure = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Enter 0 (no limit) or a size in MB.") {
		t.Errorf("size error missing: %s", rr.Body.String())
	}
	rr = postForm(h, "/redis/configure", url.Values{
		"_token": {token}, "maxmemory_mb": {"512"}, "policy": {"nope"},
	}, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("configure = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Choose an eviction policy.") {
		t.Errorf("policy error missing")
	}
	// The modal keeps the submitted size.
	if !strings.Contains(rr.Body.String(), `value="512"`) {
		t.Errorf("the submitted size is not prefilled on the re-render")
	}
}

func TestRedisPassword(t *testing.T) {
	h, sa, db := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/redis")

	rr := postForm(h, "/redis/password", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("password = %d: %s", rr.Code, rr.Body.String())
	}
	cookie := refreshCookie(admin, rr)
	rr = get(t, h, "/redis", cookie)
	page := rr.Body.String()
	for _, want := range []string{
		"Redis password set",
		"copy the password now",
		"User: <code>default</code>",
		"Password set. Update applications that connect without a user name.",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	pw, _ := agentParam(t, sa, "redis.password", "password").(string)
	if len(pw) != 32 {
		t.Errorf("generated password length = %d", len(pw))
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'redis.password_set'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("redis.password_set audits = %d", n)
	}
}

func TestRedisPasswordClear(t *testing.T) {
	h, sa, db := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/redis")

	rr := postForm(h, "/redis/password", url.Values{"_token": {token}, "clear": {"1"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("password clear = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/redis", "Password removed. Redis only listens locally, but a password is recommended.")
	if got := agentParam(t, sa, "redis.password", "password"); got != "" {
		t.Errorf("clear sent a password: %v", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'redis.password_clear'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("redis.password_clear audits = %d", n)
	}
}

func TestRedisFlush(t *testing.T) {
	h, sa, db := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/redis")

	rr := postForm(h, "/redis/flush", url.Values{"_token": {token}, "db": {"1"}, "confirm": {"db0"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("flush = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/redis", "Type db1 to confirm.")
	if n := len(sa.actions); n != 0 {
		t.Errorf("the agent was called for the wrong confirm: %v", sa.actions)
	}

	rr = postForm(h, "/redis/flush", url.Values{"_token": {token}, "db": {"1"}, "confirm": {"db1"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("flush = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/redis", "Database 1 flushed.")
	if got := agentParam(t, sa, "redis.flush", "db"); got != float64(1) {
		t.Errorf("db = %v", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'redis.flush'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("redis.flush audits = %d", n)
	}
}

func TestRedisCreateUser(t *testing.T) {
	h, sa, db := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/redis")

	rr := postForm(h, "/redis/users", url.Values{"_token": {token}, "website_id": {"10"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("create user = %d: %s", rr.Code, rr.Body.String())
	}
	cookie := refreshCookie(admin, rr)
	rr = get(t, h, "/redis", cookie)
	page := rr.Body.String()
	for _, want := range []string{
		"Redis user for key prefix u_bob:",
		"User: <code>u_bob</code>",
		"Redis user created. It can only use keys starting with u_bob:",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if got := agentParam(t, sa, "redis.acl_set", "user"); got != "u_bob" {
		t.Errorf("user = %v", got)
	}
	if got := agentParam(t, sa, "redis.acl_set", "prefix"); got != "u_bob:" {
		t.Errorf("prefix = %v", got)
	}
	if pw, _ := agentParam(t, sa, "redis.acl_set", "password").(string); len(pw) != 32 {
		t.Errorf("generated password length = %d", len(pw))
	}
	var targetType, targetID, details string
	if err := db.QueryRow(`SELECT target_type, target_id, details FROM audit_logs WHERE action = 'redis.acl_set'`).
		Scan(&targetType, &targetID, &details); err != nil || targetType != "website" || targetID != "10" {
		t.Errorf("redis.acl_set audit = %s %s: %v", targetType, targetID, err)
	}
	if !strings.Contains(details, `"user":"u_bob"`) || !strings.Contains(details, `"prefix":"u_bob:"`) {
		t.Errorf("audit details = %s", details)
	}
}

func TestRedisCreateUserNotFound(t *testing.T) {
	h, _, _ := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/redis")

	rr := postForm(h, "/redis/users", url.Values{"_token": {token}, "website_id": {"99"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("create user = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/redis", "Website not found.")
}

func TestRedisDeleteUser(t *testing.T) {
	h, sa, db := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/redis")

	// The default user (and a badly shaped name) cannot be removed here.
	rr := postForm(h, "/redis/users/delete", url.Values{"_token": {token}, "user": {"default"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/redis", "That Redis user cannot be removed here.")
	rr = postForm(h, "/redis/users/delete", url.Values{"_token": {token}, "user": {"Bob"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", rr.Code)
	}
	if n := len(sa.actions); n != 0 {
		t.Errorf("the agent was called for the refused users: %v", sa.actions)
	}

	rr = postForm(h, "/redis/users/delete", url.Values{"_token": {token}, "user": {"u_bob"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/redis", "Redis user removed.")
	if got := agentParam(t, sa, "redis.acl_del", "user"); got != "u_bob" {
		t.Errorf("user = %v", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'redis.acl_del'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("redis.acl_del audits = %d", n)
	}
}

func TestRedisForbidden(t *testing.T) {
	h, _, _ := cacheEnv(t)
	bob := loginAsUser(t, h, "bob")
	if rr := get(t, h, "/redis", bob); rr.Code != http.StatusForbidden {
		t.Errorf("/redis (bob) = %d", rr.Code)
	}
	rr := postForm(h, "/redis/flush", url.Values{"_token": {"x"}, "db": {"0"}, "confirm": {"db0"}}, bob)
	if rr.Code != http.StatusForbidden {
		t.Errorf("flush (bob) = %d", rr.Code)
	}
}
