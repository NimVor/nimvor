package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
)

const token = "test-token-0123456789abcdef0123456789"

func newServer() *Server {
	reg := actions.NewRegistry(false)
	reg.Register(actions.Action{Name: "demo.echo", Handler: func(_ context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Msg string `json:"msg"`
		}
		if err := actions.DecodeParams(p, &in); err != nil {
			return nil, err
		}
		return map[string]string{"msg": in.Msg}, nil
	}})
	reg.Register(actions.Action{Name: "demo.fail", Handler: func(context.Context, json.RawMessage) (any, error) {
		return nil, errors.New("open /etc/secret/path: permission denied")
	}})
	reg.Register(actions.Action{Name: "demo.panic", Handler: func(context.Context, json.RawMessage) (any, error) {
		panic("boom")
	}})
	reg.Register(actions.Action{Name: "demo.write", Mutating: true, Handler: func(context.Context, json.RawMessage) (any, error) {
		return nil, nil
	}})
	return &Server{Registry: reg, Token: token, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), ActionTimeout: time.Second, Version: "test"}
}

func do(t *testing.T, h http.Handler, method, path, tok, body string) (int, Envelope) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var env Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON response: %s", rec.Body.String())
	}
	if env.RequestID == "" {
		t.Error("missing request id")
	}
	return rec.Code, env
}

func TestAuthRequired(t *testing.T) {
	h := newServer().Handler()
	for _, tok := range []string{"", "wrong", token + "x"} {
		code, env := do(t, h, "GET", "/v1/health", tok, "")
		if code != http.StatusUnauthorized || env.Success {
			t.Errorf("token %q: code=%d", tok, code)
		}
	}
	code, env := do(t, h, "GET", "/v1/health", token, "")
	if code != 200 || !env.Success || env.Action != "health" {
		t.Fatalf("health: %d %+v", code, env)
	}
}

func TestPeerPolicy(t *testing.T) {
	s := newServer()
	s.PeerAllowed = func(uid int) bool { return uid == 0 }
	h := s.Handler()
	req := httptest.NewRequest("GET", "/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req = req.WithContext(context.WithValue(req.Context(), peerKey, 1234))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("disallowed peer got %d", rec.Code)
	}
}

func TestActions(t *testing.T) {
	h := newServer().Handler()

	code, env := do(t, h, "POST", "/v1/actions/demo.echo", token, `{"msg":"hi"}`)
	if code != 200 || !env.Success || env.Action != "demo.echo" {
		t.Fatalf("echo: %d %+v", code, env)
	}
	code, env = do(t, h, "POST", "/v1/actions/demo.echo", token, `{"msg":"hi","x":1}`)
	if code != 422 || env.Error.Code != "invalid_params" {
		t.Errorf("strict params: %d %+v", code, env.Error)
	}
	code, env = do(t, h, "POST", "/v1/actions/demo.echo", token, `{bad json`)
	if code != 422 {
		t.Errorf("bad json: %d", code)
	}
	code, env = do(t, h, "GET", "/v1/actions/demo.echo", token, "")
	if code != 405 {
		t.Errorf("GET action: %d", code)
	}
	code, env = do(t, h, "POST", "/v1/actions/missing", token, "")
	if code != 404 || env.Error.Code != "unknown_action" {
		t.Errorf("missing: %d %+v", code, env.Error)
	}
	code, env = do(t, h, "POST", "/v1/actions/demo.write", token, "")
	if code != 403 || env.Error.Code != "write_disabled" {
		t.Errorf("write: %d %+v", code, env.Error)
	}
}

func TestInternalErrorsAreNotLeaked(t *testing.T) {
	h := newServer().Handler()
	code, env := do(t, h, "POST", "/v1/actions/demo.fail", token, "")
	if code != 500 || env.Error.Code != "internal_error" {
		t.Fatalf("fail: %d %+v", code, env)
	}
	b, _ := json.Marshal(env)
	if strings.Contains(string(b), "/etc/secret") {
		t.Fatal("internal error detail leaked to client")
	}
	code, env = do(t, h, "POST", "/v1/actions/demo.panic", token, "")
	if code != 500 || env.Success || strings.Contains(env.Message, "boom") {
		t.Fatalf("panic: %d %+v", code, env)
	}
}

func TestBodyLimit(t *testing.T) {
	h := newServer().Handler()
	big := `{"msg":"` + strings.Repeat("a", maxBody) + `"}`
	code, _ := do(t, h, "POST", "/v1/actions/demo.echo", token, big)
	if code != 422 {
		t.Fatalf("oversized body: %d", code)
	}
}
