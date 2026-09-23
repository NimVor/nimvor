// Package server exposes agent actions over an authenticated local HTTP API.
//
// Endpoints:
//
//	GET  /v1/health             liveness (authenticated)
//	POST /v1/actions/{name}     execute a named action with a JSON body
//
// Every response uses the Envelope format.
package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
)

// maxBody bounds request bodies (1 MiB).
const maxBody = 1 << 20

// Envelope is the uniform response format.
type Envelope struct {
	Success   bool           `json:"success"`
	Action    string         `json:"action"`
	Message   string         `json:"message"`
	Data      any            `json:"data,omitempty"`
	Error     *actions.Error `json:"error,omitempty"`
	RequestID string         `json:"request_id"`
	Duration  float64        `json:"duration_ms"`
}

// Server is the agent HTTP API.
type Server struct {
	Registry      *actions.Registry
	Token         string
	Log           *slog.Logger
	ActionTimeout time.Duration
	Version       string
	// PeerAllowed, when set, is consulted with the connecting process's UID.
	PeerAllowed func(uid int) bool
	started     time.Time
}

type ctxKey int

const peerKey ctxKey = 1

// Handler builds the HTTP handler.
func (s *Server) Handler() http.Handler {
	if s.started.IsZero() {
		s.started = time.Now()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", s.health)
	mux.HandleFunc("/v1/actions/", s.action)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.write(w, http.StatusNotFound, Envelope{Action: "", Message: "Not found.", Error: &actions.Error{Code: "not_found", Message: "Unknown endpoint."}}, r)
	})
	return s.recoverer(s.authenticate(mux))
}

// ConnContext records peer credentials for Unix socket connections.
func (s *Server) ConnContext(ctx context.Context, c net.Conn) context.Context {
	if uc, ok := c.(*net.UnixConn); ok {
		if uid, err := peerUID(uc); err == nil {
			return context.WithValue(ctx, peerKey, uid)
		}
		return context.WithValue(ctx, peerKey, -1)
	}
	return ctx
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := newRequestID()
		r.Header.Set("X-Request-Id", rid)
		if uid, ok := r.Context().Value(peerKey).(int); ok && s.PeerAllowed != nil && !s.PeerAllowed(uid) {
			s.Log.Warn("rejected connection from unauthorised local user", "uid", uid, "request_id", rid)
			s.deny(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.Token)) != 1 || s.Token == "" {
			s.Log.Warn("rejected request with invalid token", "path", r.URL.Path, "request_id", rid)
			s.deny(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) deny(w http.ResponseWriter, r *http.Request) {
	s.write(w, http.StatusUnauthorized, Envelope{
		Message: "Authentication failed.",
		Error:   &actions.Error{Code: "unauthorized", Message: "Authentication failed."},
	}, r)
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.Log.Error("panic while handling request", "panic", v, "path", r.URL.Path, "request_id", r.Header.Get("X-Request-Id"))
				ie := actions.Internal()
				s.write(w, ie.Status, Envelope{Message: ie.Message, Error: ie}, r)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.methodNotAllowed(w, r, "health")
		return
	}
	s.write(w, http.StatusOK, Envelope{
		Success: true,
		Action:  "health",
		Message: "Agent is healthy",
		Data: map[string]any{
			"status":         "ok",
			"version":        s.Version,
			"uptime_seconds": int64(time.Since(s.started).Seconds()),
		},
	}, r)
}

func (s *Server) action(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/actions/")
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, r, name)
		return
	}
	start := time.Now()
	rid := r.Header.Get("X-Request-Id")
	act, err := s.Registry.Lookup(name)
	if err != nil {
		e := actions.AsError(err)
		s.Log.Info("action refused", "action", name, "code", e.Code, "request_id", rid)
		s.write(w, e.Status, Envelope{Action: name, Message: e.Message, Error: e}, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		e := actions.InvalidParams("Request body is too large or unreadable.")
		s.write(w, e.Status, Envelope{Action: name, Message: e.Message, Error: e}, r)
		return
	}
	if len(body) > 0 && !json.Valid(body) {
		e := actions.InvalidParams("Request body must be valid JSON.")
		s.write(w, e.Status, Envelope{Action: name, Message: e.Message, Error: e}, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.ActionTimeout)
	defer cancel()
	data, err := act.Handler(ctx, json.RawMessage(body))
	elapsed := time.Since(start)
	logAttrs := []any{"action", name, "mutating", act.Mutating, "duration_ms", elapsed.Milliseconds(), "request_id", rid}
	if uid, ok := r.Context().Value(peerKey).(int); ok {
		logAttrs = append(logAttrs, "peer_uid", uid)
	}
	if err != nil {
		e := actions.AsError(err)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			e = &actions.Error{Code: "timeout", Message: "The action did not finish in time.", Status: http.StatusGatewayTimeout}
		}
		// The raw error may contain paths; it is logged, never returned.
		s.Log.Warn("action failed", append(logAttrs, "code", e.Code, "error", err.Error())...)
		s.write(w, e.Status, Envelope{Action: name, Message: e.Message, Error: e, Duration: ms(elapsed)}, r)
		return
	}
	if act.Mutating {
		s.Log.Info("action executed", append(logAttrs, "result", "success")...)
	} else {
		s.Log.Debug("action executed", append(logAttrs, "result", "success")...)
	}
	s.write(w, http.StatusOK, Envelope{Success: true, Action: name, Message: "OK", Data: data, Duration: ms(elapsed)}, r)
}

func (s *Server) methodNotAllowed(w http.ResponseWriter, r *http.Request, action string) {
	e := &actions.Error{Code: "method_not_allowed", Message: "HTTP method not allowed for this endpoint."}
	s.write(w, http.StatusMethodNotAllowed, Envelope{Action: action, Message: e.Message, Error: e}, r)
}

func (s *Server) write(w http.ResponseWriter, status int, env Envelope, r *http.Request) {
	env.RequestID = r.Header.Get("X-Request-Id")
	body, err := json.Marshal(env)
	if err != nil {
		s.Log.Error("encode response", "error", err)
		status = http.StatusInternalServerError
		body = []byte(`{"success":false,"message":"Response encoding failed.","error":{"code":"internal_error","message":"Response encoding failed."}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
