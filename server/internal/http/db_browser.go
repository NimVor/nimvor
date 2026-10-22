package http

// This file ports the web database browser (DbBrowserController): the
// per-database page (GET /databases/{id}/browse) and the operation endpoint
// the reused /assets/js/dbbrowser.js polls (POST /databases/{id}/browse/api),
// plus the server-wide variants (GET /databases/browse, any database by
// name, all-scope only). Every operation runs in the agent as the
// database's own browser account, which has privileges on that database
// only; the panel checks the user may manage the database and records
// changes in the audit log. The page shell renders the reused dbbrowser.js;
// its bootstrap modal UI (confirm/create dialogs) rides with the Go shell
// work.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// browserOps mirrors the PHP OPS table: operation → [agent action, changes
// data].
var browserOps = map[string]struct {
	action  string
	changes bool
}{
	"tables":     {"db.browse_tables", false},
	"structure":  {"db.browse_structure", false},
	"rows":       {"db.browse_rows", false},
	"row":        {"db.browse_row", false},
	"save":       {"db.browse_save", true},
	"delete":     {"db.browse_delete", true},
	"query":      {"db.query", true},
	"alter":      {"db.browse_alter", true},
	"export_csv": {"db.browse_export_csv", false},
	"import_csv": {"db.browse_import_csv", true},
}

// browserFields is the PHP FIELDS whitelist: the only request fields that
// reach the agent (alter_op travels as "op").
var browserFields = []string{
	"table", "offset", "limit", "order", "desc", "filter", "search", "sql", "key", "keys", "values", "insert",
	"alter_op", "new_name", "column", "columns", "name", "after", "kind", "index_columns", "ref_table",
	"ref_columns", "on_delete", "on_update", "primary", "engine", "dry_run",
	"csv", "delimiter", "header", "mode",
}

// browserNameRe is the server-browser name shape (any database by name,
// all-scope only).
var browserNameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// browserDatabase is the database name the user may browse: a managed
// database they can see, or (all-scope) any database on the server by
// name. Mirrors DbBrowserController::resolve.
type browserDatabase struct {
	Name      string
	ID        *int64
	WebsiteID *int64
}

func (s *Server) browserDatabase(r *http.Request, id string) *browserDatabase {
	user := userFrom(r.Context())
	serverID := s.currentServerID(r)
	if id != "" {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil
		}
		db, err := hosting.NewDatabaseRepository(s.db).Find(r.Context(), serverID, n, user)
		if err != nil || db == nil {
			return nil // out of tenant reads as not found
		}
		wd := &browserDatabase{Name: db.Name, ID: &db.ID}
		if db.WebsiteID.Valid {
			wd.WebsiteID = &db.WebsiteID.Int64
		}
		return wd
	}
	name := r.URL.Query().Get("name")
	if !hosting.IsAll(user) || !browserNameRe.MatchString(name) {
		return nil
	}
	return &browserDatabase{Name: name}
}

// dbBrowser renders the browser page for a managed database. Mirrors
// DbBrowserController::browse (route databases.browser, databases.manage).
func (s *Server) dbBrowser(w http.ResponseWriter, r *http.Request) {
	s.browserPage(w, r, r.PathValue("id"))
}

// dbBrowserServer renders the browser page for any database by name
// (all-scope only). Mirrors DbBrowserController::serverBrowse (route
// databases.browser.server).
func (s *Server) dbBrowserServer(w http.ResponseWriter, r *http.Request) {
	s.browserPage(w, r, "")
}

func (s *Server) browserPage(w http.ResponseWriter, r *http.Request, id string) {
	db := s.browserDatabase(r, id)
	if db == nil {
		s.notFound(w, r)
		return
	}
	back := "/databases"
	if db.WebsiteID != nil {
		back = fmt.Sprintf("/sites/%d/databases", *db.WebsiteID)
	}
	api := "/databases/browse/api?name=" + url.QueryEscape(db.Name)
	if db.ID != nil {
		api = fmt.Sprintf("/databases/%d/browse/api", *db.ID)
	}
	sess := sessionFrom(r.Context())
	s.render(w, "db_browser", map[string]any{
		"User":     userFrom(r.Context()),
		"CSRF":     session.CSRFToken(sess),
		"Database": db.Name,
		"API":      api,
		"Back":     back,
	})
}

// dbBrowserApi dispatches a browser operation to the agent for a managed
// database. Mirrors DbBrowserController::api (route databases.browser.api).
func (s *Server) dbBrowserApi(w http.ResponseWriter, r *http.Request) {
	s.browserApi(w, r, r.PathValue("id"))
}

// dbBrowserApiServer is the server-wide variant (any database by name).
// Mirrors DbBrowserController::serverApi (route databases.browser.server_api).
func (s *Server) dbBrowserApiServer(w http.ResponseWriter, r *http.Request) {
	s.browserApi(w, r, "")
}

func (s *Server) browserApi(w http.ResponseWriter, r *http.Request, id string) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	// CSRF before resolution: as in the PHP middleware order, an expired
	// token never reveals whether the database exists.
	if !session.ValidateCSRF(sess, formToken(r)) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Page expired."},
		})
		return
	}
	db := s.browserDatabase(r, id)
	if db == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Database not found."},
		})
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Bad request."},
		})
		return
	}
	op, _ := body["op"].(string)
	spec, ok := browserOps[op]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Unknown operation."},
		})
		return
	}
	params := browserParams(body)
	params["database"] = db.Name

	res, err := s.agent.Call(r.Context(), spec.action, params, 90*time.Second)
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			if spec.changes {
				ctx := browserAuditContext(op, db.Name, params, nil)
				ctx["error"] = ae.Message
				s.browserAudit(r, op, user, db, ctx)
			}
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
		s.log.Error("db browser", "op", op, "error", err)
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
	if spec.changes && !isDryRun(params) {
		result := audit.Success
		if _, hasErr := data["error"]; hasErr {
			result = audit.Failure
		}
		ctx := browserAuditContext(op, db.Name, params, data)
		if op == "query" && result == audit.Failure {
			if msg, ok := data["error"].(string); ok {
				ctx["error"] = clip(msg, 300)
			}
		}
		s.browserAudit(r, op, user, db, ctx)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

// isDryRun reports whether the alter request is a dry run (the PHP skips
// the audit record for those).
func isDryRun(params map[string]any) bool {
	return params["dry_run"] == true
}

// browserAudit records a changing browser operation (target: the owning
// website, or the server for unmanaged/any-name databases). The result is
// derived by the caller (failure for an agent error, success/failure per
// the reply's error key otherwise).
func (s *Server) browserAudit(r *http.Request, op string, user *identity.User, db *browserDatabase, ctx map[string]any) {
	targetType, targetID := "server", strconv.FormatInt(s.currentServerID(r), 10)
	if db.WebsiteID != nil {
		targetType, targetID = "website", strconv.FormatInt(*db.WebsiteID, 10)
	}
	uid := user.ID
	result := audit.Success
	if _, failed := ctx["error"]; failed {
		result = audit.Failure
	}
	s.audit.Record(r.Context(), audit.Entry{
		UserID: &uid, Username: user.Username,
		Action: "database.browser_" + op, TargetType: targetType, TargetID: targetID,
		Result: result, IP: clientIP(r), UserAgent: r.UserAgent(),
		Details: ctx,
	})
}

// browserParams whitelists + coerces the request fields (the PHP FIELDS
// match: ints, bools, strings; everything else passes through).
func browserParams(body map[string]any) map[string]any {
	out := map[string]any{}
	for _, f := range browserFields {
		v, ok := body[f]
		if !ok || v == nil {
			continue
		}
		name := f
		if f == "alter_op" {
			name = "op"
		}
		switch f {
		case "offset", "limit":
			switch n := v.(type) {
			case float64:
				out[name] = int64(n)
			case int64:
				out[name] = n
			case int:
				out[name] = int64(n)
			}
		case "desc", "insert", "dry_run", "header":
			if b, ok := v.(bool); ok {
				out[name] = b
			}
		case "table", "order", "sql", "search", "csv", "alter_op", "new_name", "name",
			"after", "kind", "ref_table", "on_delete", "on_update", "engine", "delimiter", "mode":
			if s, ok := v.(string); ok {
				out[name] = s
			}
		default:
			out[name] = v
		}
	}
	return out
}

// browserAuditContext mirrors the PHP auditContext: the database, table and
// statement (cut short), never the full row values.
func browserAuditContext(op, dbName string, params, data map[string]any) map[string]any {
	ctx := map[string]any{"database": dbName}
	if t, ok := params["table"].(string); ok {
		ctx["table"] = clip(t, 64)
	}
	switch op {
	case "query":
		if s, ok := params["sql"].(string); ok {
			ctx["sql"] = clip(s, 1000)
		}
	case "save":
		ctx["insert"] = params["insert"] == true
		if v, ok := params["values"].(map[string]any); ok {
			cols := make([]string, 0, len(v))
			for k := range v {
				cols = append(cols, k)
				if len(cols) == 50 {
					break
				}
			}
			ctx["columns"] = cols
		}
		ctx["key"] = params["key"]
	case "delete":
		if k, ok := params["keys"].([]any); ok {
			ctx["rows"] = len(k)
		}
	case "alter":
		if o, ok := params["op"].(string); ok {
			ctx["operation"] = o
		}
		if data != nil {
			if s, ok := data["sql"].(string); ok {
				ctx["sql"] = clip(s, 1000)
			}
		}
	case "import_csv":
		if s, ok := params["csv"].(string); ok {
			ctx["bytes"] = len(s)
		}
		if data != nil {
			ctx["rows"] = data["rows"]
		}
		mode, _ := params["mode"].(string)
		if mode == "" {
			mode = "insert"
		}
		ctx["mode"] = mode
	}
	return ctx
}
