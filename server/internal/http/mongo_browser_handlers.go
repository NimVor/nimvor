package http

// The server-level MongoDB document browser: GET /mongo/browser (the full
// page, rendered with the reused /assets/js/mongobrowser.js), POST
// /mongo/browse (the JSON operation endpoint), and GET /mongo/export (a
// collection download).
//
// The browser route is gated on mongo.view — an administrator who may look at
// the server's databases but not change them. Because one endpoint carries
// both read and write operations, every mutating operation re-checks
// mongo.manage here and answers 403 in JSON rather than redirecting, which is
// what the PHP does inside MongoController::browse.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// mongoBrowserOps are the operations that change data; they need
// mongo.manage even though the route only asks for mongo.view.
var mongoBrowserOps = map[string]bool{
	"coll_create":   true,
	"coll_drop":     true,
	"coll_empty":    true,
	"coll_rename":   true,
	"delete":        true,
	"insert":        true,
	"update":        true,
	"import":        true,
	"user_create":   true,
	"user_password": true,
	"user_drop":     true,
	"index_create":  true,
	"index_drop":    true,
}

// mongoAdminBrowserPage renders GET /mongo/browser (MongoController::browser).
func (s *Server) mongoAdminBrowserPage(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	svc := s.mongoServerService(r, user)
	var databases []string
	if st, err := svc.Status(r.Context()); err == nil && st.Installed && st.Running {
		if ov, err := svc.Overview(r.Context()); err == nil {
			for _, d := range ov.Databases {
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
	// The admin page reuses the very same template as the per-website
	// browser: every label, the back link and the two endpoints are passed
	// in, and the agent op names are shared, so the only difference is which
	// databases the list offers and what the server-side handlers allow.
	s.render(w, "mongo_browser", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sess),
		"Back":      "/mongo",
		"API":       "/mongo/browse",
		"Export":    "/mongo/export",
		"Databases": databases,
		"Current":   current,
		"CanDelete": s.can(r, "mongo.manage"),
		"Success":   success,
		"Error":     errorMsg,
	})
}

// mongoAdminBrowse dispatches one browser operation to the agent
// (MongoController::browse).
func (s *Server) mongoAdminBrowse(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, formToken(r)) {
		mongoBrowseError(w, http.StatusBadRequest, "Page expired.")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		mongoBrowseError(w, http.StatusBadRequest, "Bad request.")
		return
	}
	op, _ := body["op"].(string)
	if !mongoAdminOpAllowed(op) {
		mongoBrowseError(w, http.StatusBadRequest, "Unknown operation.")
		return
	}
	if mongoBrowserOps[op] && !s.can(r, "mongo.manage") {
		mongoBrowseError(w, http.StatusForbidden, "You may not do this.")
		return
	}
	db := strings.TrimSpace(mongoBrowseStr(body, "db"))
	collection := strings.TrimSpace(mongoBrowseStr(body, "collection"))
	svc := s.mongoServerService(r, userFrom(r.Context()))
	var data map[string]any
	if err := mongoAdminOp(r, op, svc, db, collection, body, &data); err != nil {
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
	if data == nil {
		data = map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

// mongoAdminOpAllowed lists the 25 operations the PHP browser accepts.
func mongoAdminOpAllowed(op string) bool {
	switch op {
	case "collections", "coll_create", "coll_drop", "coll_empty", "coll_rename",
		"documents", "delete", "insert", "update", "aggregate", "explain",
		"import", "schema", "users", "user_create", "user_password", "user_drop",
		"indexes", "collection_stats", "index_create", "index_drop":
		return true
	}
	return false
}

// mongoAdminOp is the server-level version of mongoBrowseOp: the same switch,
// without the per-website prefix and ownership checks.
func mongoAdminOp(r *http.Request, op string, svc *hosting.MongoServerService, db, collection string, body map[string]any, data *map[string]any) error {
	ctx := r.Context()
	switch op {
	case "collections":
		collections, err := svc.Collections(ctx, db)
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
		if err := svc.CreateCollection(ctx, db, collection); err != nil {
			return err
		}
		*data = map[string]any{"created": collection}
		return nil
	case "coll_drop":
		if err := svc.DropCollection(ctx, db, collection); err != nil {
			return err
		}
		*data = map[string]any{"dropped": collection}
		return nil
	case "coll_empty":
		n, err := svc.EmptyCollection(ctx, db, collection)
		if err != nil {
			return err
		}
		*data = map[string]any{"deleted": n}
		return nil
	case "coll_rename":
		name := strings.TrimSpace(mongoBrowseStr(body, "name"))
		if err := svc.RenameCollection(ctx, db, collection, name); err != nil {
			return err
		}
		*data = map[string]any{"renamed": name}
		return nil
	case "documents":
		res, err := svc.Documents(ctx, db, collection,
			mongoBrowseStr(body, "filter"), mongoBrowseStr(body, "sort"),
			mongoBrowseStr(body, "projection"),
			mongoBrowseInt(body, "skip", 0), mongoBrowseInt(body, "limit", 50))
		if err != nil {
			return err
		}
		*data = map[string]any{"documents": res.Documents, "total": res.Total, "skip": res.Skip, "limit": res.Limit}
		return nil
	case "delete":
		n, err := svc.DeleteDocument(ctx, db, collection, mongoBrowseStr(body, "id"))
		if err != nil {
			return err
		}
		*data = map[string]any{"deleted": n}
		return nil
	case "insert":
		inserted, err := svc.InsertDocument(ctx, db, collection, mongoBrowseStr(body, "document"))
		if err != nil {
			return err
		}
		*data = map[string]any{"inserted_id": inserted}
		return nil
	case "update":
		matched, modified, err := svc.UpdateDocument(ctx, db, collection,
			mongoBrowseStr(body, "id"), mongoBrowseStr(body, "document"))
		if err != nil {
			return err
		}
		*data = map[string]any{"matched": matched, "modified": modified}
		return nil
	case "aggregate":
		res, err := svc.Aggregate(ctx, db, collection,
			mongoBrowseStr(body, "pipeline"), mongoBrowseInt(body, "limit", 50))
		if err != nil {
			return err
		}
		*data = map[string]any{"documents": res.Documents, "ms": res.Ms, "limit": res.Limit}
		return nil
	case "explain":
		explain, err := svc.Explain(ctx, db, collection, mongoBrowseStr(body, "pipeline"))
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
		res, err := svc.Import(ctx, db, collection, format, mongoBrowseStr(body, "content"))
		if err != nil {
			return err
		}
		*data = map[string]any{"inserted": res.Inserted, "errors": res.Errors, "total": res.Total}
		return nil
	case "schema":
		res, err := svc.Schema(ctx, db, collection, mongoBrowseInt(body, "sample", 1000))
		if err != nil {
			return err
		}
		*data = map[string]any{"sampled": res.Sampled, "fields": res.Fields}
		return nil
	case "users":
		users, err := svc.DatabaseUsers(ctx, db)
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
		if err := svc.CreateUser(ctx, user, mongoBrowseStr(body, "password"), db, role); err != nil {
			return err
		}
		*data = map[string]any{"user": user}
		return nil
	case "user_password":
		user := strings.TrimSpace(mongoBrowseStr(body, "user"))
		if err := svc.SetPassword(ctx, user, db, mongoBrowseStr(body, "password")); err != nil {
			return err
		}
		*data = map[string]any{"user": user}
		return nil
	case "user_drop":
		user := strings.TrimSpace(mongoBrowseStr(body, "user"))
		if err := svc.DropUser(ctx, user, db); err != nil {
			return err
		}
		*data = map[string]any{"dropped": user}
		return nil
	case "indexes":
		indexes, err := svc.Indexes(ctx, db, collection)
		if err != nil {
			return err
		}
		*data = map[string]any{"indexes": indexes}
		return nil
	case "collection_stats":
		stats, err := svc.CollectionStats(ctx, db, collection)
		if err != nil {
			return err
		}
		*data = map[string]any{}
		if err := json.Unmarshal(stats, data); err != nil {
			return err
		}
		return nil
	case "index_create":
		options := mongoBrowseStr(body, "options")
		if options == "" {
			options = "{}"
		}
		name, err := svc.CreateIndex(ctx, db, collection, mongoBrowseStr(body, "keys"), options)
		if err != nil {
			return err
		}
		*data = map[string]any{"name": name}
		return nil
	case "index_drop":
		name := mongoBrowseStr(body, "name")
		if err := svc.DropIndex(ctx, db, collection, name); err != nil {
			return err
		}
		*data = map[string]any{"dropped": name}
		return nil
	}
	return errMongoUnknown
}

// mongoAdminExport downloads a collection from any database on the server
// (MongoController::export). The download name and content type follow the
// same rules as the per-website browser.
func (s *Server) mongoAdminExport(w http.ResponseWriter, r *http.Request) {
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
	back := fmt.Sprintf("/mongo/browser?db=%s", url.QueryEscape(db))
	out, err := s.mongoServerService(r, userFrom(r.Context())).
		Export(r.Context(), db, collection, format, filter, limit)
	if err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			sessionFrom(r.Context()).Flash("error", ue.What)
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		agentErrorRedirect(w, r, "mongo export", err, back)
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
