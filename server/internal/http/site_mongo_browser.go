package http

// This file ports the site MongoDB database browser (the browser / browse /
// export actions of SiteMongoController): the full-page tool
// (GET /sites/{id}/mongo/browser) rendered with the reused
// /assets/js/mongobrowser.js, the JSON operation endpoint it polls
// (POST /sites/{id}/mongo/browse), and the collection export download
// (GET /sites/{id}/mongo/export). Like every other action of this area,
// every operation is confined to the website's own (\"<system_user>_\")
// databases: the service refuses any other name, so a crafted request can
// never reach another account's MongoDB data.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// mongoBrowserPage renders the browser page for this website's own
// (prefixed) databases. Mirrors SiteMongoController::browser.
func (s *Server) mongoBrowserPage(w http.ResponseWriter, r *http.Request) {
	site := s.mongoSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.mongoFlashNotFound(w, r)
		return
	}
	svc := s.mongoService(r, userFrom(r.Context()))
	// The page still loads when the agent cannot be reached; the error
	// surfaces when the browser runs.
	var databases []string
	if st, err := svc.Status(r.Context()); err == nil && st.Running {
		if o, err := svc.Overview(r.Context(), site); err == nil {
			for _, d := range o.Databases {
				databases = append(databases, d.Name)
			}
		}
	}
	if databases == nil {
		databases = []string{}
	}
	current := strings.TrimSpace(r.URL.Query().Get("db"))
	if current != "" && !mongoInList(databases, current) {
		current = ""
	}
	sess := sessionFrom(r.Context())
	success, _ := sess.PullFlash("success")
	errorMsg, _ := sess.PullFlash("error")
	s.render(w, "mongo_browser", map[string]any{
		"User":      userFrom(r.Context()),
		"CSRF":      session.CSRFToken(sess),
		"Back":      fmt.Sprintf("/sites/%d/mongo", site.ID),
		"API":       fmt.Sprintf("/sites/%d/mongo/browse", site.ID),
		"Export":    fmt.Sprintf("/sites/%d/mongo/export", site.ID),
		"Databases": databases,
		"Current":   current,
		"CanDelete": true,
		"Success":   success,
		"Error":     errorMsg,
	})
}

// mongoInList reports whether s is one of the list's values.
func mongoInList(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// mongoBrowseAPI dispatches one browser operation to the agent, confined
// to this website's databases. Mirrors SiteMongoController::browse.
func (s *Server) mongoBrowseAPI(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	// CSRF before resolution: an expired token never reveals whether the
	// website exists.
	if !session.ValidateCSRF(sess, formToken(r)) {
		mongoBrowseError(w, http.StatusBadRequest, "Page expired.")
		return
	}
	site := s.mongoSiteOf(r, r.PathValue("id"))
	if site == nil {
		mongoBrowseError(w, http.StatusNotFound, "Website not found.")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		mongoBrowseError(w, http.StatusBadRequest, "Bad request.")
		return
	}
	op, _ := body["op"].(string)
	db := strings.TrimSpace(mongoBrowseStr(body, "db"))
	collection := strings.TrimSpace(mongoBrowseStr(body, "collection"))
	svc := s.mongoService(r, userFrom(r.Context()))
	var data map[string]any
	err := mongoBrowseOp(r, op, svc, site, db, collection, body, &data)
	if err != nil {
		if errors.Is(err, errMongoUnknown) {
			mongoBrowseError(w, http.StatusBadRequest, "Unknown operation.")
			return
		}
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			mongoBrowseError(w, http.StatusForbidden, ue.What)
			return
		}
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
		s.log.Error("mongo browser", "op", op, "error", err)
		mongoBrowseError(w, http.StatusBadGateway, "The agent could not be reached.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

// mongoBrowseOp mirrors the PHP switch: one case per operation, the
// response assembled from the service's result (never the raw agent
// reply).
func mongoBrowseOp(r *http.Request, op string, svc *hosting.SiteMongoService, site *hosting.Website, db, collection string, body map[string]any, data *map[string]any) error {
	switch op {
	case "collections":
		collections, err := svc.Collections(r.Context(), site, db)
		if err != nil {
			return err
		}
		out := []map[string]any{}
		for _, c := range collections {
			out = append(out, map[string]any{"name": c.Name, "count": c.Count})
		}
		*data = map[string]any{"collections": out}
		return nil
	case "coll_create":
		if err := svc.CreateCollection(r.Context(), site, db, collection); err != nil {
			return err
		}
		*data = map[string]any{"created": collection}
		return nil
	case "coll_drop":
		if err := svc.DropCollection(r.Context(), site, db, collection); err != nil {
			return err
		}
		*data = map[string]any{"dropped": collection}
		return nil
	case "coll_empty":
		n, err := svc.EmptyCollection(r.Context(), site, db, collection)
		if err != nil {
			return err
		}
		*data = map[string]any{"deleted": n}
		return nil
	case "coll_rename":
		name := strings.TrimSpace(mongoBrowseStr(body, "name"))
		if err := svc.RenameCollection(r.Context(), site, db, collection, name); err != nil {
			return err
		}
		*data = map[string]any{"renamed": name}
		return nil
	case "documents":
		filter := mongoBrowseStr(body, "filter")
		sort := mongoBrowseStr(body, "sort")
		projection := mongoBrowseStr(body, "projection")
		skip := mongoBrowseInt(body, "skip", 0)
		limit := mongoBrowseInt(body, "limit", 50)
		res, err := svc.Documents(r.Context(), site, db, collection, filter, sort, projection, skip, limit)
		if err != nil {
			return err
		}
		*data = map[string]any{"documents": res.Documents, "total": res.Total, "skip": res.Skip, "limit": res.Limit}
		return nil
	case "delete":
		id := mongoBrowseStr(body, "id")
		n, err := svc.DeleteDocument(r.Context(), site, db, collection, id)
		if err != nil {
			return err
		}
		*data = map[string]any{"deleted": n}
		return nil
	case "insert":
		inserted, err := svc.InsertDocument(r.Context(), site, db, collection, mongoBrowseStr(body, "document"))
		if err != nil {
			return err
		}
		*data = map[string]any{"inserted_id": inserted}
		return nil
	case "update":
		id := mongoBrowseStr(body, "id")
		matched, modified, err := svc.UpdateDocument(r.Context(), site, db, collection, id, mongoBrowseStr(body, "document"))
		if err != nil {
			return err
		}
		*data = map[string]any{"matched": matched, "modified": modified}
		return nil
	case "aggregate":
		pipeline := mongoBrowseStr(body, "pipeline")
		limit := mongoBrowseInt(body, "limit", 50)
		res, err := svc.Aggregate(r.Context(), site, db, collection, pipeline, limit)
		if err != nil {
			return err
		}
		*data = map[string]any{"documents": res.Documents, "ms": res.Ms, "limit": res.Limit}
		return nil
	case "explain":
		explain, err := svc.Explain(r.Context(), site, db, collection, mongoBrowseStr(body, "pipeline"))
		if err != nil {
			return err
		}
		*data = map[string]any{"explain": explain}
		return nil
	case "import":
		format := mongoBrowseStr(body, "format")
		if format != "jsonl" {
			format = "json"
		}
		content := mongoBrowseStr(body, "content")
		res, err := svc.Import(r.Context(), site, db, collection, format, content)
		if err != nil {
			return err
		}
		*data = map[string]any{"inserted": res.Inserted, "errors": res.Errors, "total": res.Total}
		return nil
	case "schema":
		sample := mongoBrowseInt(body, "sample", 1000)
		res, err := svc.Schema(r.Context(), site, db, collection, sample)
		if err != nil {
			return err
		}
		*data = map[string]any{"sampled": res.Sampled, "fields": res.Fields}
		return nil
	case "users":
		users, err := svc.DatabaseUsers(r.Context(), site, db)
		if err != nil {
			return err
		}
		*data = map[string]any{"users": users}
		return nil
	case "user_create":
		user := strings.TrimSpace(mongoBrowseStr(body, "user"))
		role := mongoBrowseStr(body, "role")
		if role == "" {
			role = "readWrite"
		}
		created, err := svc.CreateUser(r.Context(), site, user, mongoBrowseStr(body, "password"), db, role)
		if err != nil {
			return err
		}
		*data = map[string]any{"user": created}
		return nil
	case "user_password":
		user := strings.TrimSpace(mongoBrowseStr(body, "user"))
		if err := svc.SetPassword(r.Context(), site, user, db, mongoBrowseStr(body, "password")); err != nil {
			return err
		}
		*data = map[string]any{"user": user}
		return nil
	case "user_drop":
		user := strings.TrimSpace(mongoBrowseStr(body, "user"))
		if err := svc.DropUser(r.Context(), site, user, db); err != nil {
			return err
		}
		*data = map[string]any{"dropped": user}
		return nil
	case "indexes":
		indexes, err := svc.Indexes(r.Context(), site, db, collection)
		if err != nil {
			return err
		}
		*data = map[string]any{"indexes": indexes}
		return nil
	case "collection_stats":
		stats, err := svc.CollectionStats(r.Context(), site, db, collection)
		if err != nil {
			return err
		}
		*data = map[string]any{}
		if err := json.Unmarshal(stats, data); err != nil {
			return err
		}
		return nil
	case "index_create":
		keys := mongoBrowseStr(body, "keys")
		options := mongoBrowseStr(body, "options")
		if options == "" {
			options = "{}"
		}
		name, err := svc.CreateIndex(r.Context(), site, db, collection, keys, options)
		if err != nil {
			return err
		}
		*data = map[string]any{"name": name}
		return nil
	case "index_drop":
		name := mongoBrowseStr(body, "name")
		if err := svc.DropIndex(r.Context(), site, db, collection, name); err != nil {
			return err
		}
		*data = map[string]any{"dropped": name}
		return nil
	default:
		return errMongoUnknown
	}
}

// errMongoUnknown marks the "Unknown operation." reply (a 400, not an
// agent error).
var errMongoUnknown = errors.New("unknown operation")

// mongoBrowseError writes the JSON error reply.
func mongoBrowseError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]any{
		"success": false,
		"error":   map[string]any{"message": message},
	})
}

// mongoBrowseStr is a string body field (” when absent).
func mongoBrowseStr(body map[string]any, key string) string {
	s, _ := body[key].(string)
	return s
}

// mongoBrowseInt is an int body field (PHP's (int) cast: floats and
// numeric strings included; $def when absent or non-numeric).
func mongoBrowseInt(body map[string]any, key string, def int) int {
	switch v := body[key].(type) {
	case float64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
		return def
	default:
		return def
	}
}

// mongoExportFilename is the download name (Controller::downloadExport):
// only safe characters survive, the result is trimmed of dots/underscores
// and falls back to "export".
func mongoExportFilename(db, collection string) string {
	base := mongoExportSafeRe.ReplaceAllString(db+"."+collection, "_")
	name := strings.Trim(base, "_.")
	if name == "" {
		name = "export"
	}
	return name
}

var mongoExportSafeRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// mongoExport downloads a collection of this website's own database as a
// JSON array or JSONL file. Mirrors SiteMongoController::export.
func (s *Server) mongoExport(w http.ResponseWriter, r *http.Request) {
	site := s.mongoSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.mongoFlashNotFound(w, r)
		return
	}
	db := strings.TrimSpace(r.URL.Query().Get("db"))
	collection := strings.TrimSpace(r.URL.Query().Get("collection"))
	format := r.URL.Query().Get("format")
	if format != "jsonl" {
		format = "json"
	}
	filter := r.URL.Query().Get("filter")
	limit := 2000
	if n, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit"))); err == nil {
		limit = n
	}
	back := fmt.Sprintf("/sites/%d/mongo/browser?db=%s", site.ID, url.QueryEscape(db))
	out, err := s.mongoService(r, userFrom(r.Context())).Export(r.Context(), site, db, collection, format, filter, limit)
	if err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			sessionFrom(r.Context()).Flash("error", ue.What)
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		s.agentErrorRedirect(w, r, "mongo export", err, back)
		return
	}
	ext, contentType := "json", "application/json"
	if out.Format == "jsonl" {
		ext, contentType = "jsonl", "application/x-ndjson"
	}
	w.Header().Set("Content-Type", contentType+"; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+mongoExportFilename(db, collection)+"."+ext+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, out.Content)
}
