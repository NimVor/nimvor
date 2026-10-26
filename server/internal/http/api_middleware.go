package http

// API v1 middleware (apiPublic, apiGuard) and helpers.

import (
	"fmt"
	"net/http"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/auth"
)

// apiPublic wraps an unauthenticated API handler (no permission check).
func (s *Server) apiPublic(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer apiRecover(w)
		h(w, r)
	}
}

// apiGuard wraps an authenticated API handler. name is the route name used for
// plan-feature gating; when permission is "" the permission check is skipped
// (me, jobs). write=true requires ability='write'. Order follows
// Middleware\ApiAuthenticate: token, rate limit, ability, feature, permission.
func (s *Server) apiGuard(name, permission string, write bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer apiRecover(w)
		u, tok, se := s.apiAuth.Authenticate(r)
		if se != nil {
			if se.Code == http.StatusUnauthorized {
				s.audit.Record(r.Context(), audit.Entry{Action: "api.auth_failed", TargetType: "route", TargetID: r.URL.Path,
					Result: audit.Denied, IP: clientIP(r), Details: map[string]any{"ip": clientIP(r)}})
			}
			writeAPIError(w, se)
			return
		}
		ok, err := s.apiAuth.Hit(r.Context(), tok.ID)
		if err != nil {
			s.log.Error("api rate limit", "error", err)
		} else if !ok {
			writeAPIError(w, &auth.StatusError{Code: http.StatusTooManyRequests, ErrCode: "rate_limited",
				Message: fmt.Sprintf("Too many requests; at most %d per minute per token.", auth.APIRatePerMinute)})
			return
		}
		if write && tok.Ability != "write" {
			writeAPIError(w, &auth.StatusError{Code: http.StatusForbidden, ErrCode: "read_only_token", Message: "This token is read-only."})
			return
		}
		if name != "" {
			if allowed, err := s.authorizer().RouteAllowed(r.Context(), u, name); err != nil || !allowed {
				s.audit.Record(r.Context(), audit.Entry{UserID: &u.ID, Username: u.Username + " (api)", Action: "security.access_denied",
					TargetType: "route", TargetID: r.URL.Path, Result: audit.Denied, IP: clientIP(r),
					Details: map[string]any{"feature": "off", "route": name, "via": "api"}})
				writeAPIError(w, &auth.StatusError{Code: http.StatusForbidden, ErrCode: "forbidden", Message: "This feature is not included in the account's hosting plan."})
				return
			}
		}
		if permission != "" {
			ok, err := s.authorizer().Can(r.Context(), u, permission)
			if err != nil || !ok {
				s.audit.Record(r.Context(), audit.Entry{UserID: &u.ID, Username: u.Username + " (api)", Action: "security.access_denied",
					TargetType: "route", TargetID: r.URL.Path, Result: audit.Denied, IP: clientIP(r),
					Details: map[string]any{"permission": permission, "via": "api"}})
				writeAPIError(w, &auth.StatusError{Code: http.StatusForbidden, ErrCode: "forbidden", Message: "The token's user lacks the permission " + permission + "."})
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
