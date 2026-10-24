package http

// This file ports the website file manager (FilesController): the manager
// page, the text-file editor page, the operation endpoint the reused
// filemanager.js polls (list/read/search/write/mkdir/rename/copy/delete/
// chmod/compress/extract + the trash), chunked upload and the chunked
// download. All confinement is enforced by the agent (os.Root); the panel
// checks that the user may access the website. Compress/extract run as
// agent jobs (tracked, pollable at /jobs/{id}); the AV scan of finished
// uploads/extracts rides with the Go shell work, as with the other
// bootstrap UI.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// filesOps mirrors the PHP OPS table: operation → [agent action, mutating].
var filesOps = map[string]struct {
	action   string
	mutating bool
}{
	"list":          {"files.list", false},
	"read":          {"files.read", false},
	"search":        {"files.search", false},
	"write":         {"files.write", true},
	"mkdir":         {"files.mkdir", true},
	"rename":        {"files.rename", true},
	"copy":          {"files.copy", true},
	"delete":        {"files.delete", true},
	"chmod":         {"files.chmod", true},
	"compress":      {"files.compress", true},
	"extract":       {"files.extract", true},
	"trash_list":    {"files.trash_list", false},
	"trash_restore": {"files.trash_restore", true},
	"trash_delete":  {"files.trash_delete", true},
	"trash_empty":   {"files.trash_empty", true},
}

// filesFields is the PHP FIELDS whitelist: the only request fields that
// reach the agent.
var filesFields = []string{"path", "content", "expected_mtime", "from", "to", "paths", "mode", "query", "dest", "overwrite", "permanent", "ids"}

// filesPrintable is the PHP printable-name check for the manager's
// ?path= starting point.
var filesPrintable = regexp.MustCompile(`^[^\x00-\x1f]+$`)

// filesShow renders the file manager page (FilesController::show,
// sites.files, files.manage).
func (s *Server) filesShow(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	if !ok {
		return
	}
	start := ""
	if strings.HasPrefix(site.DocumentRoot, site.Root) {
		start = strings.TrimPrefix(site.DocumentRoot, site.Root)
	}
	start = strings.TrimLeft(start, "/")
	// Opened at a folder (e.g. a WordPress installation's).
	want := strings.Trim(r.URL.Query().Get("path"), "/")
	if want != "" && len(want) <= 400 && !strings.Contains("/"+want+"/", "/../") && filesPrintable.MatchString(want) {
		start = want
	}
	retention, _ := s.settings.Int(r.Context(), "trash_retention_days", 0)
	s.render(w, "files_manager", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sessionFrom(r.Context())),
		"Site":      site,
		"Start":     start,
		"Retention": retention,
	})
}

// filesEdit renders the text-file editor page (FilesController::edit,
// sites.files.edit, files.manage).
func (s *Server) filesEdit(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	path := strings.Trim(r.URL.Query().Get("path"), "/")
	if !ok || path == "" || strings.Contains("/"+path+"/", "/../") {
		s.notFound(w, r)
		return
	}
	folder := ""
	if i := strings.LastIndex(path, "/"); i >= 0 {
		folder = path[:i]
	}
	back := fmt.Sprintf("/sites/%d/files", site.ID)
	if folder != "" {
		back += "?path=" + folder
	}
	s.render(w, "files_editor", map[string]any{
		"User":   user,
		"CSRF":   session.CSRFToken(sessionFrom(r.Context())),
		"Site":   site,
		"Path":   path,
		"Folder": folder,
		"Back":   back,
	})
}

// filesApi dispatches a file operation to the agent (FilesController::api,
// sites.files.api, files.manage). The request is JSON ({op, ...}), as
// filemanager.js sends it; the agent's data reply passes through.
func (s *Server) filesApi(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if !session.ValidateCSRF(sess, formToken(r)) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Page expired."},
		})
		return
	}
	site, ok := s.scopedSite(w, r, user)
	if !ok {
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
	spec, ok := filesOps[op]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Unknown operation."},
		})
		return
	}
	params := map[string]any{"domain": site.Domain}
	for _, f := range filesFields {
		if v, ok := body[f]; ok && v != nil {
			params[f] = v
		}
	}

	res, err := s.agent.Call(r.Context(), spec.action, params)
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
		s.log.Error("files api", "op", op, "error", err)
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
	// Compress and extract run as agent jobs; track them for the /jobs
	// viewer. (The post-extract AV scan path rides with the Go shell work.)
	if jobID, ok := data["job_id"].(string); ok {
		kind := "Extract in " + site.Domain
		if op == "compress" {
			kind = "Compress in " + site.Domain
		}
		if err := hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), jobID, kind, "website", strconv.FormatInt(site.ID, 10), &user.ID); err != nil {
			s.log.Error("track job", "job", jobID, "error", err)
		}
	}
	if spec.mutating {
		details := map[string]any{"domain": site.Domain}
		if p, ok := params["path"]; ok {
			details["path"] = asString(p)
		} else {
			detail := asString(params["from"])
			if to, ok := params["to"]; ok {
				detail += " → " + asString(to)
			}
			if dest, ok := params["dest"]; ok {
				detail += " → " + asString(dest)
			}
			details["path"] = detail
		}
		if p, ok := params["paths"].([]any); ok {
			details["paths"] = first20(p)
		}
		if p, ok := params["ids"].([]any); ok {
			details["ids"] = first20(p)
		}
		if op == "delete" {
			details["permanent"] = params["permanent"] == true
		}
		uid := user.ID
		s.audit.Record(r.Context(), audit.Entry{
			UserID: &uid, Username: user.Username,
			Action: "files." + op, TargetType: "website", TargetID: strconv.FormatInt(site.ID, 10),
			Result: audit.Success, IP: clientIP(r), UserAgent: r.UserAgent(),
			Details: details,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

// filesUpload accepts one chunk of a file (FilesController::upload,
// sites.files.upload, files.manage): a multipart form with the chunk file,
// the target path, the byte offset and final=1 on the last chunk. The AV
// scan of a finished upload rides with the Go shell work.
func (s *Server) filesUpload(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if !session.ValidateCSRF(sess, formToken(r)) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Page expired."},
		})
		return
	}
	site, ok := s.scopedSite(w, r, user)
	bad := func() {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Upload failed."},
		})
	}
	if !ok {
		bad()
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		bad()
		return
	}
	file, _, err := r.FormFile("chunk")
	if err != nil {
		bad()
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		bad()
		return
	}
	final := r.FormValue("final") == "1"
	path := r.FormValue("path")
	offset, _ := strconv.ParseInt(r.FormValue("offset"), 10, 64)

	res, err := s.agent.Call(r.Context(), "files.upload", map[string]any{
		"domain": site.Domain, "path": path, "offset": offset,
		"data": base64.StdEncoding.EncodeToString(data), "final": final,
	})
	if err != nil {
		var ae *agent.Error
		e := map[string]any{"message": "Upload failed."}
		if errors.As(err, &ae) {
			e["message"] = ae.Message
			if ae.Details != "" {
				e["details"] = ae.Details
			}
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": e})
		return
	}
	var out map[string]any
	if err := json.Unmarshal(res, &out); err != nil {
		out = map[string]any{}
	}
	if final {
		uid := user.ID
		s.audit.Record(r.Context(), audit.Entry{
			UserID: &uid, Username: user.Username,
			Action: "files.upload", TargetType: "website", TargetID: strconv.FormatInt(site.ID, 10),
			Result: audit.Success, IP: clientIP(r), UserAgent: r.UserAgent(),
			Details: map[string]any{"domain": site.Domain, "path": path, "size": out["size"]},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

// filesDownload streams a website file (FilesController::download,
// sites.files.download, files.manage) in base64 chunks, as the logs
// download does.
func (s *Server) filesDownload(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	path := r.URL.Query().Get("path")
	if !ok || path == "" {
		s.notFound(w, r)
		return
	}
	first, err := s.fileChunk(r, site.Domain, path, 0)
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			sess.Flash("error", ae.Message)
			http.Redirect(w, r, fmt.Sprintf("/sites/%d/files", site.ID), http.StatusSeeOther)
		} else {
			s.log.Error("files.download", "path", path, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		UserID: &uid, Username: user.Username,
		Action: "files.download", TargetType: "website", TargetID: strconv.FormatInt(site.ID, 10),
		Result: audit.Success, IP: clientIP(r), UserAgent: r.UserAgent(),
		Details: map[string]any{"domain": site.Domain, "path": path},
	})
	name := safeLogFilename(basename(path))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(first.Size, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)
	offset := int64(0)
	for {
		b, _ := base64.StdEncoding.DecodeString(first.Data)
		if _, err := w.Write(b); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		offset += int64(len(b))
		if first.EOF || len(b) == 0 {
			return
		}
		first, err = s.fileChunk(r, site.Domain, path, offset)
		if err != nil {
			// Mid-stream failure: stop; the partial file is what the PHP
			// stream does too (the loop ends on an empty chunk).
			return
		}
	}
}

// fileChunk reads one download chunk from the agent.
func (s *Server) fileChunk(r *http.Request, domain, path string, offset int64) (*fileChunk, error) {
	res, err := s.agent.Call(r.Context(), "files.download", map[string]any{
		"domain": domain, "path": path, "offset": offset,
	})
	if err != nil {
		return nil, err
	}
	var c fileChunk
	if err := json.Unmarshal(res, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// fileChunk is one chunk of files.download (base64 slice).
type fileChunk struct {
	Data string `json:"data"`
	Size int64  `json:"size"`
	EOF  bool   `json:"eof"`
}

// asString is a best-effort string of a decoded JSON value ("").
func asString(v any) string {
	s, _ := v.(string)
	return s
}

// first20 mirrors PHP's array_slice($list, 0, 20) for audit details.
func first20(list []any) []any {
	if len(list) > 20 {
		return list[:20]
	}
	return list
}

// basename is the last path segment ("").
func basename(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
