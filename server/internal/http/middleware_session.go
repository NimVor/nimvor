package http

import (
	"context"
	"net/http"
)

// sessionCtx loads the sealed-cookie session, resolves the signed-in user, and
// puts both in the request context. It guarantees the session cookie is
// written (if the session changed) before the response body — via a writer
// that flushes on the first header/body write.
func (s *Server) sessionCtx(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := s.sessions.Load(r)
		user, err := s.auth.User(r.Context(), sess)
		if err != nil {
			s.log.Error("resolve user", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		fw := &flushWriter{ResponseWriter: w, flush: func() {
			if err := s.sessions.Save(w, sess); err != nil {
				s.log.Error("save session", "error", err)
			}
		}}
		ctx := context.WithValue(r.Context(), ctxSession, sess)
		ctx = context.WithValue(ctx, ctxUser, user)
		next(fw, r.WithContext(ctx))
		fw.ensureFlushed()
	}
}

// requireAuth redirects unauthenticated browsers to /login (or answers 401 for
// JSON clients). Mirrors the authentication half of the Authenticate
// middleware; per-route permission and feature gating is applied by
// requirePermission / requireFeature as routes are ported.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if userFrom(r.Context()) == nil {
			if wantsJSON(r) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{
					"success": false,
					"error":   map[string]string{"message": "Your session has ended. Sign in again."},
				})
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func wantsJSON(r *http.Request) bool {
	a := r.Header.Get("Accept")
	return a == "application/json" || r.Header.Get("X-Requested-With") == "XMLHttpRequest"
}

// flushWriter runs flush exactly once, immediately before the first
// WriteHeader or Write, so Set-Cookie is emitted with the response headers.
type flushWriter struct {
	http.ResponseWriter
	flush   func()
	flushed bool
}

func (w *flushWriter) doFlush() {
	if !w.flushed {
		w.flushed = true
		w.flush()
	}
}

func (w *flushWriter) WriteHeader(code int) {
	w.doFlush()
	w.ResponseWriter.WriteHeader(code)
}

func (w *flushWriter) Write(b []byte) (int, error) {
	w.doFlush()
	return w.ResponseWriter.Write(b)
}

// ensureFlushed covers a handler that set session state but wrote no response.
func (w *flushWriter) ensureFlushed() { w.doFlush() }
