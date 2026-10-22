package http

// This file ports the Redis key browser (RedisBrowserController): the
// server-wide page (GET /redis/browser) and the per-website page
// (GET /sites/{id}/redis), plus the operation endpoint the reused
// /assets/js/redisbrowser.js polls (POST /redis/browser/api and
// POST /sites/{id}/redis/api). On a website the browser is confined (by
// the agent) to that website's key prefix — the same prefix its Redis ACL
// user is limited to. The page shell renders the reused redisbrowser.js;
// changing operations (edit/delete) are recorded in the audit log.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// redisBrowserOps mirrors the PHP OPS table: operation → [agent action,
// changes data].
var redisBrowserOps = map[string]struct {
	action  string
	changes bool
}{
	"keys":   {"redis.keys", false},
	"get":    {"redis.key_get", false},
	"edit":   {"redis.key_edit", true},
	"delete": {"redis.key_delete", true},
}

// redisBrowserFields is the PHP FIELDS whitelist: the only request fields
// that reach the agent.
var redisBrowserFields = []string{
	"db", "pattern", "cursor", "key", "keys", "op", "type", "field", "value", "score", "index", "ttl", "new_key",
}

// redisPrefixFor is RedisBrowserController::prefixFor: the website's
// keys live under its system user's prefix (lowercased).
func redisPrefixFor(site *hosting.Website) string {
	return strings.ToLower(site.SystemUser) + ":"
}

// redisBrowserSite resolves the website (nil = not found / no access).
func (s *Server) redisBrowserSite(r *http.Request, id string) *hosting.Website {
	if id == "" {
		return nil
	}
	user := userFrom(r.Context())
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil
	}
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), n, user)
	if err != nil || site == nil {
		return nil
	}
	return site
}

// redisBrowserPage renders the browser shell (browse / siteBrowse).
func (s *Server) redisBrowserPage(w http.ResponseWriter, r *http.Request, id string) {
	site := s.redisBrowserSite(r, id)
	if id != "" && site == nil {
		s.notFound(w, r)
		return
	}
	dbs := make([]int, 16)
	for i := range dbs {
		dbs[i] = i
	}
	sess := sessionFrom(r.Context())
	data := map[string]any{
		"User":      userFrom(r.Context()),
		"CSRF":      session.CSRFToken(sess),
		"DBs":       dbs,
		"API":       "/redis/browser/api",
		"Prefix":    "",
		"Fixed":     "0",
		"Back":      "/redis",
		"BackLabel": "Redis",
		"Heading":   "Redis keys",
	}
	if site != nil {
		data["API"] = "/sites/" + strconv.FormatInt(site.ID, 10) + "/redis/api"
		data["Prefix"] = redisPrefixFor(site)
		data["Fixed"] = "1"
		data["Back"] = "/sites/" + strconv.FormatInt(site.ID, 10)
		data["BackLabel"] = site.Domain
		data["Heading"] = "Redis keys of " + site.Domain
	}
	s.render(w, "redis_browser", data)
}

// redisBrowserSitePage mirrors RedisBrowserController::siteBrowse.
func (s *Server) redisBrowserSitePage(w http.ResponseWriter, r *http.Request) {
	s.redisBrowserPage(w, r, r.PathValue("id"))
}

// redisBrowserServerPage mirrors RedisBrowserController::browse.
func (s *Server) redisBrowserServerPage(w http.ResponseWriter, r *http.Request) {
	s.redisBrowserPage(w, r, "")
}

// redisBrowserApi dispatches a browser operation to the agent (api /
// siteApi).
func (s *Server) redisBrowserApi(w http.ResponseWriter, r *http.Request, id string) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	// CSRF before resolution: as in the PHP middleware order, an expired
	// token never reveals whether the website exists.
	if !session.ValidateCSRF(sess, formToken(r)) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Page expired."},
		})
		return
	}
	var site *hosting.Website
	if id != "" {
		site = s.redisBrowserSite(r, id)
		if site == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"error":   map[string]string{"message": "Website not found."},
			})
			return
		}
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Bad request."},
		})
		return
	}
	action, _ := body["action"].(string)
	spec, ok := redisBrowserOps[action]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Unknown operation."},
		})
		return
	}
	params := map[string]any{"prefix": "", "db": int64(0)}
	if site != nil {
		params["prefix"] = redisPrefixFor(site)
	} else if p, _ := body["prefix"].(string); len(p) <= 200 {
		params["prefix"] = p
	}
	for _, f := range redisBrowserFields {
		v, ok := body[f]
		if !ok || v == nil {
			continue
		}
		switch f {
		case "db", "index", "ttl":
			switch n := v.(type) {
			case float64:
				params[f] = int64(n)
			case int64:
				params[f] = n
			case int:
				params[f] = int64(n)
			}
		default:
			params[f] = v
		}
	}
	res, err := s.agent.Call(r.Context(), spec.action, params, 30*time.Second)
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			code := ae.Status
			if code < 400 || code >= 600 {
				code = http.StatusBadGateway
			}
			e := map[string]any{"message": ae.Message}
			if ae.Details != "" {
				e["details"] = ae.Details
			}
			writeJSON(w, code, map[string]any{"success": false, "error": e})
			return
		}
		s.log.Error("redis browser", "op", action, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "The agent could not be reached."},
		})
		return
	}
	var data map[string]any
	if err := json.Unmarshal(res, &data); err != nil {
		data = map[string]any{}
	}
	if spec.changes {
		s.redisBrowserAudit(r, user, action, site, params)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

// redisBrowserSiteApi mirrors RedisBrowserController::siteApi.
func (s *Server) redisBrowserSiteApi(w http.ResponseWriter, r *http.Request) {
	s.redisBrowserApi(w, r, r.PathValue("id"))
}

// redisBrowserServerApi mirrors RedisBrowserController::api.
func (s *Server) redisBrowserServerApi(w http.ResponseWriter, r *http.Request) {
	s.redisBrowserApi(w, r, "")
}

// redisBrowserAudit records a changing browser operation (the PHP always
// records success here — the agent-error path returns before the audit).
func (s *Server) redisBrowserAudit(r *http.Request, user *identity.User, action string, site *hosting.Website, params map[string]any) {
	targetType, targetID := "server", strconv.FormatInt(s.currentServerID(r), 10)
	if site != nil {
		targetType, targetID = "website", strconv.FormatInt(site.ID, 10)
	}
	name := "redis.key_"
	if action == "delete" {
		name = "redis.key_delete"
	} else if op, _ := params["op"].(string); op != "" {
		name = "redis.key_" + op
	} else {
		name = "redis.key_edit"
	}
	keys := []string{}
	if action == "delete" {
		if list, ok := params["keys"].([]any); ok {
			for _, k := range list {
				keys = append(keys, redisAuditKey(k))
			}
		}
	} else if k, ok := params["key"]; ok {
		keys = append(keys, redisAuditKey(k))
	}
	if len(keys) > 20 {
		keys = keys[:20]
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		UserID: &uid, Username: user.Username,
		Action: name, TargetType: targetType, TargetID: targetID,
		Result: audit.Success, IP: clientIP(r), UserAgent: r.UserAgent(),
		Details: map[string]any{"db": params["db"], "keys": keys},
	})
}

// redisAuditKey decodes a base64 key for the audit (truncated, as in the
// PHP — an invalid encoding reads as empty).
func redisAuditKey(v any) string {
	s, _ := v.(string)
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	t := string(b)
	if len(t) > 200 {
		return t[:200]
	}
	return t
}
