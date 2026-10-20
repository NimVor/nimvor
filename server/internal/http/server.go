// Package http wires the Go panel's HTTP server: routing (stdlib mux),
// middleware (security headers, request logging) and handlers. Routes are
// ported from panel/routes/web.php incrementally; see docs/migration.
package http

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
)

// Server holds the dependencies shared by handlers.
type Server struct {
	cfg config.Config
	db  *sql.DB
	log *slog.Logger
	mux *http.ServeMux
}

// New builds the server and registers the routes implemented so far.
func New(cfg config.Config, db *sql.DB, log *slog.Logger) *Server {
	s := &Server{cfg: cfg, db: db, log: log, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns the fully wrapped http.Handler (middleware applied).
func (s *Server) Handler() http.Handler {
	return s.securityHeaders(s.requestLog(s.mux))
}

func (s *Server) routes() {
	// Unauthenticated liveness probe — parity with the PHP /healthz.
	s.mux.HandleFunc("GET /healthz", s.health)

	// Reused frontend assets (Bootstrap, the 12 JS files, CSS) served as-is.
	if s.cfg.StaticDir != "" {
		fs := http.FileServer(http.Dir(s.cfg.StaticDir))
		s.mux.Handle("GET /assets/", http.StripPrefix("/assets/", fs))
	}
}

// health mirrors HealthController::show — {"status":"ok"} or 503.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	status, code := "ok", http.StatusOK
	var one int
	if err := s.db.QueryRowContext(r.Context(), "SELECT 1").Scan(&one); err != nil || one != 1 {
		status, code = "error", http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]string{"status": status})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
