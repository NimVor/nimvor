package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAssetsAreCacheable(t *testing.T) {
	s, _, _ := rolesEnvServerAgent(t, false)
	h := s.Handler()
	get1 := httptest.NewRecorder()
	h.ServeHTTP(get1, httptest.NewRequest("GET", "/assets/css/whacp.css", nil))
	tag := get1.Header().Get("ETag")
	if get1.Code != http.StatusOK || tag == "" || get1.Header().Get("Cache-Control") != "public, max-age=3600" || get1.Body.Len() == 0 {
		t.Fatalf("first = %d etag %q cache %q len %d", get1.Code, tag, get1.Header().Get("Cache-Control"), get1.Body.Len())
	}
	req := httptest.NewRequest("GET", "/assets/css/whacp.css", nil)
	req.Header.Set("If-None-Match", tag)
	again := httptest.NewRecorder()
	h.ServeHTTP(again, req)
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Errorf("revalidation = %d with %d bytes", again.Code, again.Body.Len())
	}
	// Pages are never cached.
	page := httptest.NewRecorder()
	h.ServeHTTP(page, httptest.NewRequest("GET", "/login", nil))
	if page.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("page Cache-Control = %q", page.Header().Get("Cache-Control"))
	}
}
