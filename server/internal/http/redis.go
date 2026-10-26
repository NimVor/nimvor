package http

// This file ports the server-side Redis management (RedisController +
// RedisService): the index page (GET /redis — redis.manage), the memory +
// eviction settings (POST /redis/configure), the default-user password
// (POST /redis/password), the database flush (POST /redis/flush), the
// per-website ACL users (POST /redis/users, POST /redis/users/delete).
// Generated passwords are shown once (the secret flash) and never stored.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// redisPolicies mirrors RedisService::POLICIES.
var redisPolicies = [][2]string{
	{"noeviction", "No eviction (writes fail when full)"},
	{"allkeys-lru", "Evict least recently used keys (cache)"},
	{"allkeys-lfu", "Evict least frequently used keys (cache)"},
	{"volatile-lru", "Evict least recently used keys with an expiry"},
	{"volatile-ttl", "Evict keys closest to expiry"},
	{"allkeys-random", "Evict random keys"},
	{"volatile-lfu", "Evict least frequently used keys with an expiry"},
	{"volatile-random", "Evict random keys with an expiry"},
}

var redisUserRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

// redisKeyRow is one keyspace row of the view.
type redisKeyRow struct {
	DB    string
	Value string
}

// redisSiteRow is one website of the create-user modal.
type redisSiteRow struct {
	ID         int64
	Domain     string
	SystemUser string
}

// redisView is the view's $info-derived values.
type redisView struct {
	Version    string
	UptimeDays string
	UsedHuman  string
	HasLimit   bool
	MaxmemMB   string
	Policy     string
	Clients    string
	HitRate    string
	Keyspace   []redisKeyRow
	Users      []string
	Websites   []redisSiteRow
}

// redisPassword generates a 32-character password (Crypto::password(32)):
// the same unambiguous alphabet as the database passwords.
func redisPassword() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789-_.!@#%+="
	buf := make([]byte, 32)
	for i := range buf {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		buf[i] = alphabet[n.Int64()]
	}
	return string(buf), nil
}

// redisInfoOf calls redis.info (RedisService::info).
func (s *Server) redisInfoOf(ctx context.Context) (map[string]any, string) {
	res, err := s.agent.Call(ctx, "redis.info", map[string]any{}, 30*time.Second)
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			return nil, ae.Message
		}
		s.log.Error("redis info", "error", err)
		return nil, "The WhaCP agent could not be reached."
	}
	var out map[string]any
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, "The WhaCP agent answered with an unreadable state."
	}
	return out, ""
}

func (s *Server) redisViewOf(info map[string]any) *redisView {
	v := &redisView{}
	if info == nil {
		return v
	}
	inner, _ := info["info"].(map[string]any)
	if inner != nil {
		if server, ok := inner["server"].(map[string]any); ok {
			v.Version, _ = server["redis_version"].(string)
			uptime := intOf(server["uptime_in_seconds"])
			v.UptimeDays = strconv.FormatFloat(float64(uptime)/86400, 'f', 1, 64) + " days"
		}
		if mem, ok := inner["memory"].(map[string]any); ok {
			v.UsedHuman, _ = mem["used_memory_human"].(string)
		}
		if clients, ok := inner["clients"].(map[string]any); ok {
			v.Clients = strconv.Itoa(intOf(clients["connected_clients"]))
		}
		if ks, ok := inner["keyspace"].(map[string]any); ok {
			dbs := make([]string, 0, len(ks))
			for db := range ks {
				dbs = append(dbs, db)
			}
			sort.Strings(dbs)
			for _, db := range dbs {
				val, _ := ks[db].(string)
				v.Keyspace = append(v.Keyspace, redisKeyRow{DB: db, Value: val})
			}
		}
		if stats, ok := inner["stats"].(map[string]any); ok {
			hits, misses := intOf(stats["keyspace_hits"]), intOf(stats["keyspace_misses"])
			if hits+misses > 0 {
				v.HitRate = strconv.FormatFloat(float64(hits)/float64(hits+misses)*100, 'f', 1, 64) + " %"
			}
		}
	}
	if mm, _ := info["maxmemory"].(string); mm != "" {
		if n, err := strconv.ParseInt(mm, 10, 64); err == nil && n > 0 {
			v.HasLimit = true
			v.MaxmemMB = strconv.FormatInt(n/1048576, 10)
		}
	}
	v.Policy, _ = info["maxmemory_policy"].(string)
	if users, ok := info["acl_users"].([]any); ok {
		for _, u := range users {
			if s2, ok := u.(string); ok && s2 != "default" {
				v.Users = append(v.Users, s2)
			}
		}
	}
	return v
}

// redisIndexData assembles the page's data (the index and the
// re-render-on-error share it). On a normal load the settings modal is
// prefilled from the live info; on a validation re-render it keeps the
// submitted values.
func (s *Server) redisIndexData(r *http.Request, errMessage string, submitted bool, oldMaxMemoryMB int, oldPolicy string) map[string]any {
	user := userFrom(r.Context())
	ctx := r.Context()
	sess := sessionFrom(r.Context())
	info, agentMsg := s.redisInfoOf(ctx)
	v := s.redisViewOf(info)
	websites, err := hosting.NewWebsiteRepository(s.db).All(ctx, s.currentServerID(r), user)
	if err == nil {
		rows := make([]redisSiteRow, 0, len(websites))
		for _, w := range websites {
			rows = append(rows, redisSiteRow{ID: w.ID, Domain: w.Domain, SystemUser: w.SystemUser})
		}
		v.Websites = rows
	}
	maxmemMB := 0
	if v.HasLimit {
		maxmemMB = intOf(v.MaxmemMB)
	}
	policy := v.Policy
	if submitted {
		maxmemMB = oldMaxMemoryMB
		policy = oldPolicy
	}
	var secret *secretFlash
	if raw, _ := sess.PullFlash("secret"); raw != "" {
		var sf secretFlash
		if json.Unmarshal([]byte(raw), &sf) == nil {
			secret = &sf
		}
	}
	success, _ := sess.PullFlash("success")
	// An action that redirects back with an error (wrong confirmation, refused
	// user, unknown website) flashes it; without this pull those failures were
	// never shown. A validation error rendered in place takes precedence.
	if flashed, _ := sess.PullFlash("error"); errMessage == "" {
		errMessage = flashed
	}
	return map[string]any{
		"User":          user,
		"CSRF":          session.CSRFToken(sess),
		"AgentError":    agentMsg,
		"V":             v,
		"Secret":        secret,
		"Success":       success,
		"Error":         errMessage,
		"MaxmemMB":      maxmemMB,
		"Policy":        policy,
		"PolicyOptions": redisPolicies,
	}
}

// redisIndex renders the server's Redis page (index — redis.manage).
func (s *Server) redisIndex(w http.ResponseWriter, r *http.Request) {
	s.render(w, "redis_index", s.redisIndexData(r, "", false, 0, ""))
}

// redisRenderError re-renders the page with the validation error (the PHP's
// backWithErrors): the settings modal keeps the entered values.
func (s *Server) redisRenderError(w http.ResponseWriter, r *http.Request, msg string, maxMemoryMB int, policy string) {
	s.render(w, "redis_index", s.redisIndexData(r, msg, true, maxMemoryMB, policy))
}

// redisRedirectWith sends the page's flash + redirect (the PHP's act()).
func (s *Server) redisRedirectWith(w http.ResponseWriter, r *http.Request, ok bool, msg string) {
	sess := sessionFrom(r.Context())
	if ok {
		sess.Flash("success", msg)
	} else {
		sess.Flash("error", msg)
	}
	http.Redirect(w, r, "/redis", http.StatusSeeOther)
}

// redisAgentMessage returns the agent's message (or a generic one).
func redisAgentMessage(err error) string {
	var ae *agent.Error
	if errors.As(err, &ae) {
		return ae.Message
	}
	return "The WhaCP agent could not be reached."
}

// redisAudit records a Redis change (target: the service "redis", or the
// website for the ACL user).
func (s *Server) redisAudit(r *http.Request, user *identity.User, action, targetType, targetID string, details map[string]any) {
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		UserID: &uid, Username: user.Username,
		Action: action, TargetType: targetType, TargetID: targetID,
		Result: audit.Success, IP: clientIP(r), UserAgent: r.UserAgent(),
		Details: details,
	})
}

// redisConfigure saves the memory + eviction settings (configure —
// redis.manage). Validation failures re-render the page with the error.
func (s *Server) redisConfigure(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		s.redisRenderError(w, r, "Bad request.", 0, "")
		return
	}
	maxMemoryMB := intOf(r.FormValue("maxmemory_mb"))
	policy := r.FormValue("policy")
	if maxMemoryMB < 0 || maxMemoryMB > 1048576 {
		s.redisRenderError(w, r, "Enter 0 (no limit) or a size in MB.", maxMemoryMB, policy)
		return
	}
	valid := false
	for _, p := range redisPolicies {
		if p[0] == policy {
			valid = true
			break
		}
	}
	if !valid {
		s.redisRenderError(w, r, "Choose an eviction policy.", maxMemoryMB, policy)
		return
	}
	params := map[string]any{"maxmemory_mb": maxMemoryMB, "policy": policy}
	if _, err := s.agent.Call(ctx, "redis.configure", params, 60*time.Second); err != nil {
		s.redisRenderError(w, r, redisAgentMessage(err), maxMemoryMB, policy)
		return
	}
	s.redisAudit(r, user, "redis.configure", "service", "redis", params)
	s.redisRedirectWith(w, r, true, "Redis settings saved to redis.conf.")
}

// redisPassword sets or clears the default user's password (password —
// redis.manage). A generated password is flashed once (the secret).
func (s *Server) redisPassword(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		s.redisRedirectWith(w, r, false, "Bad request.")
		return
	}
	clear := r.FormValue("clear") == "1"
	pw := ""
	if !clear {
		var err error
		pw, err = redisPassword()
		if err != nil {
			s.redisRedirectWith(w, r, false, "Password generation failed.")
			return
		}
	}
	if _, err := s.agent.Call(ctx, "redis.password", map[string]any{"password": pw}, 30*time.Second); err != nil {
		s.redisRedirectWith(w, r, false, redisAgentMessage(err))
		return
	}
	action, msg := "redis.password_set", "Password set. Update applications that connect without a user name."
	if clear {
		action, msg = "redis.password_clear", "Password removed. Redis only listens locally, but a password is recommended."
	}
	s.redisAudit(r, user, action, "service", "redis", nil)
	if !clear {
		s.flashSecret(sessionFrom(r.Context()), "Redis password set", "default", pw)
	}
	s.redisRedirectWith(w, r, true, msg)
}

// redisFlush flushes one database (flush — redis.manage).
func (s *Server) redisFlush(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		s.redisRedirectWith(w, r, false, "Bad request.")
		return
	}
	db := intOf(r.FormValue("db"))
	confirm := r.FormValue("confirm")
	if db < 0 || db > 255 || confirm != "db"+strconv.Itoa(db) {
		s.redisRedirectWith(w, r, false, "Type db"+strconv.Itoa(db)+" to confirm.")
		return
	}
	if _, err := s.agent.Call(ctx, "redis.flush", map[string]any{"db": db, "confirm": confirm}, 120*time.Second); err != nil {
		s.redisRedirectWith(w, r, false, redisAgentMessage(err))
		return
	}
	s.redisAudit(r, user, "redis.flush", "service", "redis", map[string]any{"db": db})
	s.redisRedirectWith(w, r, true, "Database "+strconv.Itoa(db)+" flushed.")
}

// redisCreateUser creates (or resets) a website's ACL user
// (users.store — redis.manage).
func (s *Server) redisCreateUser(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		s.redisRedirectWith(w, r, false, "Bad request.")
		return
	}
	websiteID := int64(intOf(r.FormValue("website_id")))
	// As in the PHP, the pick is not scope-limited: any website of the
	// server (the page is already redis.manage).
	site, err := hosting.NewWebsiteRepository(s.db).Find(ctx, s.currentServerID(r), websiteID, nil)
	if err != nil || site == nil {
		s.redisRedirectWith(w, r, false, "Website not found.")
		return
	}
	username := strings.ToLower(site.SystemUser)
	prefix := username + ":"
	pw, err := redisPassword()
	if err != nil {
		s.redisRedirectWith(w, r, false, "Password generation failed.")
		return
	}
	if _, err := s.agent.Call(ctx, "redis.acl_set", map[string]any{"user": username, "password": pw, "prefix": prefix}, 30*time.Second); err != nil {
		s.redisRedirectWith(w, r, false, redisAgentMessage(err))
		return
	}
	s.redisAudit(r, user, "redis.acl_set", "website", strconv.FormatInt(websiteID, 10), map[string]any{"user": username, "prefix": prefix})
	s.flashSecret(sessionFrom(r.Context()), "Redis user for key prefix "+prefix, username, pw)
	s.redisRedirectWith(w, r, true, "Redis user created. It can only use keys starting with "+prefix)
}

// redisDeleteUser removes a website's ACL user (users.delete —
// redis.manage).
func (s *Server) redisDeleteUser(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		s.redisRedirectWith(w, r, false, "Bad request.")
		return
	}
	username := r.FormValue("user")
	if !redisUserRe.MatchString(username) || username == "default" {
		s.redisRedirectWith(w, r, false, "That Redis user cannot be removed here.")
		return
	}
	if _, err := s.agent.Call(ctx, "redis.acl_del", map[string]any{"user": username}, 30*time.Second); err != nil {
		s.redisRedirectWith(w, r, false, redisAgentMessage(err))
		return
	}
	s.redisAudit(r, user, "redis.acl_del", "service", "redis", map[string]any{"user": username})
	s.redisRedirectWith(w, r, true, "Redis user removed.")
}
