package http

// API v1 middleware (apiPublic, apiGuard) and helpers.

import (
	"net/http"

	"github.com/mchkhawarshahzad/whacp/server/internal/auth"
)

// apiPublic wraps an unauthenticated API handler (no permission check).
func (s *Server) apiPublic(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer apiRecover(w)
		h(w, r)
	}
}

// apiGuard wraps an authenticated API handler. When permission is "" the
// permission check is skipped (me, jobs). write=true requires ability='write'.
func (s *Server) apiGuard(permission string, write bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer apiRecover(w)
		u, tok, se := s.apiAuth.Authenticate(r)
		if se != nil {
			writeAPIError(w, se)
			return
		}
		if write && tok.Ability != "write" {
			writeAPIError(w, &auth.StatusError{Code: http.StatusForbidden, ErrCode: "read_only_token", Message: "This token is read-only."})
			return
		}
		if permission != "" {
			ok, err := s.authorizer().Can(r.Context(), u, permission)
			if err != nil || !ok {
				writeAPIError(w, &auth.StatusError{Code: http.StatusForbidden, ErrCode: "forbidden", Message: "Token does not have this permission."})
				return
			}
		}
		r = auth.WithAPIContext(r, u, tok)
		h(w, r)
	}
}

func apiRecover(w http.ResponseWriter) {
	if rec := recover(); rec != nil {
		writeAPIError(w, &auth.StatusError{Code: http.StatusInternalServerError, ErrCode: "panic", Message: "Internal error."})
	}
}

// writeRaw writes a raw body (used for the OpenAPI yaml document).
func writeRaw(w http.ResponseWriter, status int, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// decodeJSON is a small helper to parse and 400 on malformed bodies.
func decodeJSON(r *http.Request, v any) *auth.StatusError {
	if err := parseJSONBody(r, v); err != nil {
		return &auth.StatusError{Code: http.StatusBadRequest, ErrCode: "bad_json", Message: "Malformed JSON body."}
	}
	return nil
}
