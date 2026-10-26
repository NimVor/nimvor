package http

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// TestServerStartsWithoutRouteConflicts guards the start-up panics Go's
// ServeMux raises for ambiguous or duplicate patterns (e.g. POST /ips/{id}/delete
// vs POST /ips/ranges/{id}, a doubly registered GET /roles). New registers
// every route, so constructing the server is the check.
func TestServerStartsWithoutRouteConflicts(t *testing.T) {
	db := routesTestDB(t)
	if _, err := New(testConfig(t), db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
}

// templateTargetGaps are links in the templates that have no registered route.
// Each is a documented migration gap (docs/audits/GO_MIGRATION_FINAL_AUDIT.md,
// F-11): the handlers exist as POST-only routes, but the edit page has no
// forms for them yet. The list must only shrink.
var templateTargetGaps = map[string]bool{
	"GET /users/1/limits":          true,
	"GET /users/1/hosting-options": true,
}

var (
	hrefRe    = regexp.MustCompile(`href="(/[^"#]*)"`)
	formRe    = regexp.MustCompile(`<form[^>]*?action="(/[^"]*)"[^>]*>`)
	tmplRe    = regexp.MustCompile(`\{\{.*?\}\}`)
	tmplCtlRe = regexp.MustCompile(`\{\{-?\s*(if|else|end|range|with|template|define)\b`)
)

// TestTemplateLinksAndFormsHaveRoutes walks every embedded template and asks the
// mux whether the static link / form targets resolve to a registered route, so a
// renamed or never-ported route cannot leave a dead button behind.
func TestTemplateLinksAndFormsHaveRoutes(t *testing.T) {
	db := routesTestDB(t)
	s, err := New(testConfig(t), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("../web/templates/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates found: %v", err)
	}
	check := func(file, method, target string) {
		target = strings.Split(target, "?")[0]
		if strings.Contains(target, "{{.Tab}}") || strings.HasPrefix(target, "/assets/") {
			return // chosen at render time among registered routes / static files
		}
		if ctl := tmplCtlRe.MatchString(target); ctl || strings.ContainsAny(target, " \t\n") {
			return // built with {{if}}/{{range}} at render time: not a static target
		}
		target = tmplRe.ReplaceAllString(target, "1")
		key := method + " " + target
		if templateTargetGaps[key] {
			return
		}
		req := httptest.NewRequest(method, target, nil)
		if _, pattern := s.mux.Handler(req); pattern == "" {
			t.Errorf("%s: %s has no registered route", filepath.Base(file), key)
		}
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, m := range hrefRe.FindAllStringSubmatch(src, -1) {
			check(f, http.MethodGet, m[1])
		}
		for _, m := range formRe.FindAllStringSubmatch(src, -1) {
			if strings.Contains(m[0], `method="post"`) {
				check(f, http.MethodPost, m[1])
			}
		}
	}
}

func routesTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}
