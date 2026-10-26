package http

// The 18 REST v1 endpoints of ApiController (PHP Api/ApiController.php, 288
// LOC). They reuse the same hosting services as the web UI, so validation,
// plan limits, tenant scope and auditing are identical. Responses follow the
// {success,data} / {success,false,error} envelope.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/auth"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/notify"
)

// apiOpenapi serves a stubbed OpenAPI document describing the API v1 surface.
// The full OpenAPI spec is served inline below (apiOpenapiYaml const) and
// we keep a minimal self-describing document embedded here to avoid extra
// build plumbing.
func (s *Server) apiOpenapi(w http.ResponseWriter, r *http.Request) {
	writeRaw(w, http.StatusOK, "application/yaml; charset=utf-8", []byte(apiOpenapiYaml))
}

const apiOpenapiYaml = "openapi: 3.1.0\ninfo:\n  title: WhaCP API\n  version: \"1\"\npaths:\n  /api/v1/me: {get: {summary: Token identity}}\n"

// apiMe returns the token's user + token metadata.
func (s *Server) apiMe(w http.ResponseWriter, r *http.Request) {
	u := auth.APIUserFrom(r.Context())
	t := auth.APITokenFrom(r.Context())
	writeAPI(w, http.StatusOK, map[string]any{
		"id": u.ID, "username": u.Username, "role": u.RoleName, "scope": u.RoleScope,
		"token":     map[string]any{"name": t.Name, "ability": t.Ability, "expires_at": nullableStr(t.ExpiresAt)},
		"server_id": s.currentServerID(r),
	})
}

// apiServerInfo returns system.info + system.metrics.
func (s *Server) apiServerInfo(w http.ResponseWriter, r *http.Request) {
	infoRaw, err := s.agent.Call(r.Context(), "system.info", nil)
	if err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	metricsRaw, err := s.agent.Call(r.Context(), "system.metrics", nil)
	if err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	writeAPI(w, http.StatusOK, map[string]any{
		"info":    unwrapEnvelopeAny(infoRaw),
		"metrics": unwrapEnvelopeAny(metricsRaw),
	})
}

// apiListWebsites lists websites visible to the token's user.
func (s *Server) apiListWebsites(w http.ResponseWriter, r *http.Request) {
	u := auth.APIUserFrom(r.Context())
	sites, err := hosting.NewWebsiteRepository(s.db).All(r.Context(), s.currentServerID(r), u)
	if err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	out := make([]any, 0, len(sites))
	for i := range sites {
		out = append(out, apiWebsite(&sites[i]))
	}
	writeAPI(w, http.StatusOK, out)
}

// apiShowWebsite returns one website (404 outside the token's scope).
func (s *Server) apiShowWebsite(w http.ResponseWriter, r *http.Request) {
	u := auth.APIUserFrom(r.Context())
	id, ok := pathID(r, "id")
	if !ok {
		writeAPIError(w, notFoundAPI())
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).Find(r.Context(), s.currentServerID(r), id, u)
	if err != nil {
		writeAPIError(w, notFoundAPI())
		return
	}
	writeAPI(w, http.StatusOK, apiWebsite(site))
}

// apiCreateWebsite creates a website from JSON input.
func (s *Server) apiCreateWebsite(w http.ResponseWriter, r *http.Request) {
	u := auth.APIUserFrom(r.Context())
	var in map[string]any
	if se := decodeJSON(r, &in); se != nil {
		writeAPIError(w, se)
		return
	}
	strs := map[string]string{}
	for k, v := range in {
		if s, ok := v.(string); ok {
			strs[k] = s
		}
	}
	if aliases, ok := in["aliases"].([]any); ok {
		parts := make([]string, 0, len(aliases))
		for _, a := range aliases {
			if s, ok := a.(string); ok {
				parts = append(parts, s)
			}
		}
		strs["aliases"] = strings.Join(parts, "\n")
	}
	id, err := s.websiteService(r, u).Create(r.Context(), strs, u, nil)
	if err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).Find(r.Context(), s.currentServerID(r), id, nil)
	if err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	writeAPI(w, http.StatusCreated, apiWebsite(site))
}

// apiDeleteWebsite deletes a website.
func (s *Server) apiDeleteWebsite(w http.ResponseWriter, r *http.Request) {
	u := auth.APIUserFrom(r.Context())
	id, ok := pathID(r, "id")
	if !ok {
		writeAPIError(w, notFoundAPI())
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).Find(r.Context(), s.currentServerID(r), id, u)
	if err != nil || site == nil {
		writeAPIError(w, notFoundAPI())
		return
	}
	removeFiles := r.URL.Query().Get("remove_files") == "1"
	removeUser := r.URL.Query().Get("remove_user") == "1"
	warnings, err := s.websiteService(r, u).Delete(r.Context(), site, removeFiles, removeUser)
	if err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	writeAPI(w, http.StatusOK, map[string]any{"deleted": site.ID, "warnings": warnings})
}

func (s *Server) apiEnableWebsite(w http.ResponseWriter, r *http.Request) {
	s.apiSetEnabled(w, r, true)
}
func (s *Server) apiDisableWebsite(w http.ResponseWriter, r *http.Request) {
	s.apiSetEnabled(w, r, false)
}

func (s *Server) apiSetEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	u := auth.APIUserFrom(r.Context())
	id, ok := pathID(r, "id")
	if !ok {
		writeAPIError(w, notFoundAPI())
		return
	}
	repo := hosting.NewWebsiteRepository(s.db)
	site, err := repo.Find(r.Context(), s.currentServerID(r), id, u)
	if err != nil || site == nil {
		writeAPIError(w, notFoundAPI())
		return
	}
	if err := s.websiteService(r, u).SetEnabled(r.Context(), site, enabled); err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	fresh, _ := repo.Find(r.Context(), s.currentServerID(r), id, nil)
	writeAPI(w, http.StatusOK, apiWebsite(fresh))
}

// apiIssueSsl issues/renews a Let's Encrypt certificate.
func (s *Server) apiIssueSsl(w http.ResponseWriter, r *http.Request) {
	u := auth.APIUserFrom(r.Context())
	id, ok := pathID(r, "id")
	if !ok {
		writeAPIError(w, notFoundAPI())
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).Find(r.Context(), s.currentServerID(r), id, u)
	if err != nil || site == nil {
		writeAPIError(w, notFoundAPI())
		return
	}
	var in struct {
		Email          string `json:"email"`
		IncludeAliases *bool  `json:"include_aliases"`
		Staging        bool   `json:"staging"`
	}
	_ = parseJSONBody(r, &in)
	includeAliases := true
	if in.IncludeAliases != nil {
		includeAliases = *in.IncludeAliases
	}
	if err := s.sslService(r, u).Issue(r.Context(), site, in.Email, includeAliases, in.Staging); err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	fresh, _ := hosting.NewWebsiteRepository(s.db).Find(r.Context(), s.currentServerID(r), id, nil)
	writeAPI(w, http.StatusOK, apiWebsite(fresh))
}

// apiListBackups lists a website's backups (refreshes running rows first).
func (s *Server) apiListBackups(w http.ResponseWriter, r *http.Request) {
	u := auth.APIUserFrom(r.Context())
	id, ok := pathID(r, "id")
	if !ok {
		writeAPIError(w, notFoundAPI())
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).Find(r.Context(), s.currentServerID(r), id, u)
	if err != nil || site == nil {
		writeAPIError(w, notFoundAPI())
		return
	}
	svc := s.backupsService(r, u)
	_ = svc.Refresh(r.Context())
	rows, err := svc.ListSite(r.Context(), site.ID)
	if err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	out := make([]any, 0, len(rows))
	for _, b := range rows {
		out = append(out, apiBackup(b))
	}
	writeAPI(w, http.StatusOK, out)
}

// apiCreateBackup starts a website backup.
func (s *Server) apiCreateBackup(w http.ResponseWriter, r *http.Request) {
	u := auth.APIUserFrom(r.Context())
	id, ok := pathID(r, "id")
	if !ok {
		writeAPIError(w, notFoundAPI())
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).Find(r.Context(), s.currentServerID(r), id, u)
	if err != nil || site == nil {
		writeAPIError(w, notFoundAPI())
		return
	}
	var in struct {
		Files     *bool  `json:"files"`
		Databases *bool  `json:"databases"`
		Encrypt   *bool  `json:"encrypt"`
		Label     string `json:"label"`
	}
	_ = parseJSONBody(r, &in)
	opts := hosting.CreateSiteOptions{Label: in.Label, Files: true, Databases: true, Encrypt: true}
	if in.Files != nil {
		opts.Files = *in.Files
	}
	if in.Databases != nil {
		opts.Databases = *in.Databases
	}
	if in.Encrypt != nil {
		opts.Encrypt = *in.Encrypt
	}
	job, err := s.backupsService(r, u).CreateSite(r.Context(), site, opts, actorID(u))
	if err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), job, "Backup "+site.Domain, "website", strconv.FormatInt(site.ID, 10), userIDPtr(u))
	writeAPI(w, http.StatusAccepted, map[string]any{"job_id": job})
}

// apiDeployWebsite triggers a deployment.
func (s *Server) apiDeployWebsite(w http.ResponseWriter, r *http.Request) {
	u := auth.APIUserFrom(r.Context())
	id, ok := pathID(r, "id")
	if !ok {
		writeAPIError(w, notFoundAPI())
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).Find(r.Context(), s.currentServerID(r), id, u)
	if err != nil || site == nil {
		writeAPIError(w, notFoundAPI())
		return
	}
	uid := userIDPtr(u)
	job, err := s.gitService(r, u).Deploy(r.Context(), site, "api", uid)
	if err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), job, "Deploy "+site.Domain, "website", strconv.FormatInt(site.ID, 10), uid)
	writeAPI(w, http.StatusAccepted, map[string]any{"job_id": job})
}

// apiListDatabases returns databases visible to the token's user.
func (s *Server) apiListDatabases(w http.ResponseWriter, r *http.Request) {
	u := auth.APIUserFrom(r.Context())
	rows, err := hosting.NewDatabaseRepository(s.db).All(r.Context(), s.currentServerID(r), u, nil)
	if err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	out := make([]any, 0, len(rows))
	for _, d := range rows {
		out = append(out, apiDatabase(d))
	}
	writeAPI(w, http.StatusOK, out)
}

// apiCreateDatabase creates a database (with optional user).
func (s *Server) apiCreateDatabase(w http.ResponseWriter, r *http.Request) {
	u := auth.APIUserFrom(r.Context())
	var in struct {
		Name      string `json:"name"`
		Username  string `json:"username"`
		Password  string `json:"password"`
		WebsiteID *int64 `json:"website_id"`
	}
	if se := decodeJSON(r, &in); se != nil {
		writeAPIError(w, se)
		return
	}
	dbSvc := s.databaseService(r, u)
	if in.Username != "" {
		_, dbName, user, pw, err := dbSvc.CreateWithUser(r.Context(), s.currentServerID(r), u, in.Name, in.Username, in.WebsiteID)
		if err != nil {
			writeAPIError(w, apiUserError(err))
			return
		}
		password := in.Password
		if password == "" {
			password = pw
		}
		writeAPI(w, http.StatusCreated, map[string]any{"database": dbName, "username": user, "password": password})
		return
	}
	id, err := dbSvc.CreateDatabase(r.Context(), s.currentServerID(r), u, in.Name, in.WebsiteID)
	if err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	writeAPI(w, http.StatusCreated, map[string]any{"id": id, "name": in.Name})
}

// apiJob polls a background job.
func (s *Server) apiJob(w http.ResponseWriter, r *http.Request) {
	u := auth.APIUserFrom(r.Context())
	id := r.PathValue("id")
	if !hosting.JobIDRe.MatchString(id) {
		writeAPIError(w, notFoundAPI())
		return
	}
	tracker := hosting.NewJobTracker(s.db)
	job, err := tracker.Find(r.Context(), s.currentServerID(r), id)
	if err != nil || job == nil {
		writeAPIError(w, notFoundAPI())
		return
	}
	vis, err := tracker.VisibleTo(r.Context(), job, u)
	if err != nil || !vis {
		writeAPIError(w, notFoundAPI())
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	raw, err := tracker.Poll(r.Context(), s.agent, s.currentServerID(r), id, offset)
	if err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	out := map[string]any{"kind": job.Kind}
	var full map[string]any
	if err := json.Unmarshal(raw, &full); err == nil {
		if data, ok := full["data"].(map[string]any); ok {
			for k, v := range data {
				out[k] = v
			}
		}
	}
	writeAPI(w, http.StatusOK, out)
}

// apiListNotifications lists notifications for the token's user.
func (s *Server) apiListNotifications(w http.ResponseWriter, r *http.Request) {
	u := auth.APIUserFrom(r.Context())
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n >= 1 && n <= 200 {
		limit = n
	}
	items, err := notify.NewRepository(s.db).List(r.Context(), u, limit)
	if err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	out := make([]any, 0, len(items))
	for _, n := range items {
		var domain any
		if n.Domain.Valid {
			domain = n.Domain.String
		}
		out = append(out, map[string]any{
			"id": n.ID, "server_id": s.currentServerID(r), "website_id": nil,
			"domain": domain, "kind": n.Kind, "severity": n.Severity,
			"title": n.Title, "body": n.Body, "job_id": nil,
			"read": n.IsRead, "created_at": n.CreatedAt,
		})
	}
	writeAPI(w, http.StatusOK, out)
}

// apiReadNotifications marks notifications read (all for this user).
func (s *Server) apiReadNotifications(w http.ResponseWriter, r *http.Request) {
	u := auth.APIUserFrom(r.Context())
	var in struct {
		IDs []int64 `json:"ids"`
		All bool    `json:"all"`
	}
	_ = parseJSONBody(r, &in)
	marked, err := notify.NewRepository(s.db).MarkAllRead(r.Context(), u)
	if err != nil {
		writeAPIError(w, apiUserError(err))
		return
	}
	writeAPI(w, http.StatusOK, map[string]any{"marked": marked, "unread": 0})
}

// ---- helpers ----

func notFoundAPI() *auth.StatusError {
	return &auth.StatusError{Code: http.StatusNotFound, ErrCode: "not_found", Message: "Not found, or not visible to this token."}
}

func unwrapEnvelopeAny(raw json.RawMessage) any {
	var env struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && env.Success && len(env.Data) > 0 {
		var out any
		if err := json.Unmarshal(env.Data, &out); err == nil {
			return out
		}
	}
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}
