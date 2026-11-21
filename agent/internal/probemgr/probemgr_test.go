package probemgr

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func serve(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	_, port, _ := net.SplitHostPort(u.Host)
	old := portFor
	portFor = func(string) string { return port }
	t.Cleanup(func() { portFor = old })
}

func TestProbeStatusAndHostHeader(t *testing.T) {
	var gotHost string
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		http.Redirect(w, r, "https://example.com/", http.StatusMovedPermanently)
	})
	r := Probe(context.Background(), Site{Domain: "example.com"}, 3*time.Second)
	if r.Status != 301 || r.Error != "" || gotHost != "example.com" {
		t.Fatalf("got %+v host=%q (redirect must not be followed)", r, gotHost)
	}
}

func TestProbeRefusedAndForeignAddress(t *testing.T) {
	old := portFor
	portFor = func(string) string { return "1" }
	defer func() { portFor = old }()
	if r := Probe(context.Background(), Site{Domain: "example.com"}, 2*time.Second); r.Status != 0 || r.Error != "refused" {
		t.Fatalf("got %+v", r)
	}
	if r := Probe(context.Background(), Site{Domain: "example.com", IPv4: "203.0.113.9"}, time.Second); r.Error != "other" {
		t.Fatalf("a foreign address must not be asked: %+v", r)
	}
}

func TestProbeTimeout(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) { time.Sleep(600 * time.Millisecond) })
	if r := Probe(context.Background(), Site{Domain: "example.com"}, 150*time.Millisecond); r.Error != "timeout" {
		t.Fatalf("got %+v", r)
	}
}

func TestProbeAll(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	rs := ProbeAll(context.Background(), []Site{{Domain: "a.example"}, {Domain: "b.example"}}, 2*time.Second)
	if len(rs) != 2 || rs[0].Status != 503 || rs[1].Domain != "b.example" {
		t.Fatalf("got %+v", rs)
	}
}
