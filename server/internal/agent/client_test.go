package agent

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeAgent serves the agent protocol on a Unix socket for tests.
func fakeAgent(t *testing.T, handler http.HandlerFunc) (socket, tokenFile string) {
	t.Helper()
	dir := t.TempDir()
	socket = filepath.Join(dir, "agent.sock")
	tokenFile = filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return socket, tokenFile
}

func TestCallSendsAuthAndParsesData(t *testing.T) {
	var gotAuth, gotPath, gotBody string
	socket, token := fakeAgent(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true,"data":{"version":"8.0.4","running":true}}`))
	})
	c := New(socket, token, 5*time.Second)

	data, err := c.Call(context.Background(), "mongo.status", map[string]any{"db": "shop"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotPath != "/v1/actions/mongo.status" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody != `{"db":"shop"}` {
		t.Errorf("body = %q", gotBody)
	}
	var res struct {
		Version string `json:"version"`
		Running bool   `json:"running"`
	}
	if err := json.Unmarshal(data, &res); err != nil || res.Version != "8.0.4" || !res.Running {
		t.Fatalf("data = %s (err=%v)", data, err)
	}
}

func TestCallSurfacesStructuredError(t *testing.T) {
	socket, token := fakeAgent(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		w.Write([]byte(`{"success":false,"error":{"code":"conflict","message":"MongoDB is not installed.","details":"x"}}`))
	})
	c := New(socket, token, 5*time.Second)

	_, err := c.Call(context.Background(), "mongo.backup", map[string]any{"db": "shop"})
	ae, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *agent.Error, got %T: %v", err, err)
	}
	if ae.Code != "conflict" || ae.Status != 409 || ae.Message != "MongoDB is not installed." {
		t.Fatalf("unexpected error: %+v", ae)
	}
}

func TestCallRejectsBadActionName(t *testing.T) {
	c := New("/nonexistent.sock", "/nonexistent", time.Second)
	if _, err := c.Call(context.Background(), "mongo..bad", nil); err == nil {
		t.Error("expected an invalid-action error")
	}
	if _, err := c.Call(context.Background(), "rm -rf", nil); err == nil {
		t.Error("expected an invalid-action error for a shell-looking name")
	}
}
