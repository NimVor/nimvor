package http

import (
	"database/sql"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func writeTestKey(t *testing.T, path string) {
	t.Helper()
	var key [32]byte
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestKeyRoutesDoNotPanic hits a fixed list of routes with a bare request
// and asserts none of them 5xx or panic. It is a coarse regression net for
// nil-pointer dereferences and handler wiring mistakes; individual handler
// behaviour is covered by their own focused tests.
func TestKeyRoutesDoNotPanic(t *testing.T) {
	s := testServer(t)
	h := s.Handler()

	cases := []struct {
		method, path string
	}{
		{"GET", "/healthz"},
		{"GET", "/login"},
		{"GET", "/"},
		{"GET", "/api/v1/openapi.yaml"},
		{"GET", "/assets/js/whacp.js"},
		{"GET", "/api/v1/me"},
		{"GET", "/api/v1/server"},
		{"GET", "/api/v1/websites"},
		{"GET", "/api/v1/notifications"},
		{"GET", "/robots.txt"},
		{"GET", "/favicon.ico"},
		{"POST", "/api/v1/notifications/read"},
		{"POST", "/login"},
		{"POST", "/logout"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic: %v", r)
				}
			}()
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))
			if rr.Code >= http.StatusInternalServerError {
				t.Errorf("status %d (body: %.200s)", rr.Code, rr.Body.String())
			}
		})
	}
}

// TestHealthzBadDB returns a non-200 when the database path cannot be
// opened as a SQLite file. We point cfg.DBPath at a directory (which SQLite
// cannot open as a DB), ensuring /healthz surfaces the failure rather than
// returning 200.
func TestHealthzBadDB(t *testing.T) {
	dir := t.TempDir()
	kf := filepath.Join(dir, "panel.key")
	writeTestKey(t, kf)
	// cfg.DBPath = dir itself — a directory cannot be a SQLite database.
	cfg := config.Config{
		KeyFile: kf,
		DBPath:  dir,
		LogFile: filepath.Join(dir, "panel.log"),
	}

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		return // fail-fast Open error is acceptable
	}
	defer db.Close()

	srv, err := New(cfg, db, testLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/healthz", nil))
	if rr.Code == http.StatusOK {
		t.Errorf("/healthz returned 200 against a DB path that is a directory")
	}
}

var _ = sql.LevelDefault // keep database/sql import used (in case future tests need it)

// TestConcurrentHealthz fires parallel /healthz requests; under `go test
// -race` this surfaces data races introduced by the handler chain.
func TestConcurrentHealthz(t *testing.T) {
	s := testServer(t)
	h := s.Handler()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				rr := httptest.NewRecorder()
				h.ServeHTTP(rr, httptest.NewRequest("GET", "/healthz", nil))
				if rr.Code != http.StatusOK {
					t.Errorf("/healthz = %d", rr.Code)
					return
				}
			}
		}()
	}
	wg.Wait()
}
