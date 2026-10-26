package http

import (
	"net/http"
	"strings"
	"time"
)

// csp is the exact Content-Security-Policy the PHP SecurityHeaders middleware
// sends. The UI uses no inline scripts; keep it that way.
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'"

// securityHeaders adds the strict CSP and related headers on every response,
// matching app/Http/Middleware/SecurityHeaders.php. HSTS is only sent in
// production (behind TLS).
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		set(h, "Content-Security-Policy", csp)
		set(h, "X-Content-Type-Options", "nosniff")
		set(h, "X-Frame-Options", "DENY")
		set(h, "Referrer-Policy", "same-origin")
		set(h, "Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		set(h, "Cross-Origin-Opener-Policy", "same-origin")
		set(h, "Cross-Origin-Resource-Policy", "same-origin")
		// Pages carry session-specific content: never cache them (static
		// assets under /assets/ are not session-specific).
		if !strings.HasPrefix(r.URL.Path, "/assets/") {
			set(h, "Cache-Control", "no-store")
			set(h, "Pragma", "no-cache")
		}
		if s.cfg.Production {
			set(h, "Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func set(h http.Header, k, v string) {
	if h.Get(k) == "" {
		h.Set(k, v)
	}
}

// requestLog logs one structured line per request (method, path, status, ms).
func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.log.Info("request",
			"method", r.Method, "path", r.URL.Path,
			"status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status = code
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}
