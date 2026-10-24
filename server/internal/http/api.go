package http

// JSON helpers shared by the API v1 handlers. Every response is either
// {"success":true,"data":...} (2xx/202) or {"success":false,"error":{"code",
// "message",...}} (4xx/5xx). Errors from ValidationException, AgentException
// and UserFacingException are mapped to their proper status codes, mirroring
// ApiController::guard() in PHP.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/auth"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
)

// writeAPI writes a success envelope.
func writeAPI(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
}

// writeAPIError writes an error envelope.
func writeAPIError(w http.ResponseWriter, se *auth.StatusError) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(se.Code)
	body := map[string]any{"success": false, "error": map[string]any{"code": se.ErrCode, "message": se.Message}}
	if len(se.Details) > 0 {
		body["error"].(map[string]any)["details"] = se.Details
	}
	_ = json.NewEncoder(w).Encode(body)
}

// apiUserError builds a user-facing error envelope (Validation / UserError / AgentError).
func apiUserError(err error) *auth.StatusError {
	var ve *hosting.ValidationError
	if errors.As(err, &ve) {
		return &auth.StatusError{Code: http.StatusUnprocessableEntity, ErrCode: "validation_failed", Message: "Validation failed.", Details: map[string]any{"fields": ve.Fields}}
	}
	var ue *hosting.UserError
	if errors.As(err, &ue) {
		return &auth.StatusError{Code: http.StatusConflict, ErrCode: "rejected", Message: ue.What, Details: map[string]any{"reason": ue.Why, "action": ue.Fix}}
	}
	var ae *agent.Error
	if errors.As(err, &ae) {
		code := "agent_error"
		if ae.Code != "" {
			code = ae.Code
		}
		details := map[string]any{}
		if ae.Details != "" {
			details["reason"] = ae.Details
		}
		return &auth.StatusError{Code: http.StatusBadGateway, ErrCode: code, Message: ae.Message, Details: details}
	}
	return &auth.StatusError{Code: http.StatusInternalServerError, ErrCode: "internal", Message: "Internal error."}
}

// parseJSONBody decodes the request body into v (limit 1 MiB).
func parseJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	return dec.Decode(v)
}

// pathID parses an integer id from the URL path (mux PathValue).
func pathID(r *http.Request, name string) (int64, bool) {
	raw := r.PathValue(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// apiWebsite maps a Website to the public JSON shape.
func apiWebsite(s *hosting.Website) map[string]any {
	if s == nil {
		return nil
	}
	return map[string]any{
		"id":            s.ID,
		"domain":        s.Domain,
		"aliases":       s.Aliases,
		"kind":          s.Kind,
		"php_version":   s.PhpVersion,
		"document_root": s.DocumentRoot,
		"system_user":   s.SystemUser,
		"https":         s.HTTPS,
		"enabled":       s.Enabled,
		"status":        s.Status,
		"owner_id":      nullableSQL(s.OwnerID),
		"created_at":    s.CreatedAt,
	}
}

// apiBackup maps a hydrated Backup to the public JSON shape.
func apiBackup(b hosting.Backup) map[string]any {
	parts := make([]map[string]any, 0, len(b.Parts))
	for _, p := range b.Parts {
		parts = append(parts, map[string]any{
			"name":     p.Name,
			"kind":     p.Kind,
			"database": p.Database,
			"size":     p.Size,
		})
	}
	return map[string]any{
		"id":         b.ID,
		"backup_id":  b.BackupID,
		"label":      b.Label,
		"status":     b.Status,
		"size":       b.Size,
		"encrypted":  b.Encrypted,
		"parts":      parts,
		"created_at": b.CreatedAt,
		"error":      b.Error,
	}
}

// apiDatabase maps a Database row to the public JSON shape.
func apiDatabase(d hosting.Database) map[string]any {
	return map[string]any{
		"id":         d.ID,
		"name":       d.Name,
		"website_id": nullableSQL(d.WebsiteID),
		"created_at": d.CreatedAt,
	}
}

func nullableSQL(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}

func nullableStr(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}

// isWriteMethod reports whether the request mutates state.
func isWriteMethod(r *http.Request) bool {
	switch strings.ToUpper(r.Method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}
