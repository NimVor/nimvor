package sites

import (
	"strings"
	"testing"
)

func phpSpec(p PHPSettings) Spec {
	return Spec{Domain: "a.test", User: "atest", Kind: "php", PHPVersion: "8.3", PHP: p}
}

// The defaults render the pool existing websites have, line for line.
func TestPoolDefaultsUnchanged(t *testing.T) {
	s := phpSpec(PHPSettings{})
	if err := s.Normalize("/home/nimvor", "/var/lib/nimvor-agent"); err != nil {
		t.Fatal(err)
	}
	got := RenderPool(s, Layout{Socket: "/run/php/a.sock", LogDir: "/l", TmpDir: "/t", WebUser: "www-data"})
	want := "pm = ondemand\npm.max_children = 5\npm.process_idle_timeout = 10s\npm.max_requests = 500\nchdir = /\ncatch_workers_output = yes\nslowlog = /l/php-slow.log\nrequest_slowlog_timeout = 10s\n"
	if !strings.Contains(got, want) || strings.Contains(got, "request_terminate_timeout") {
		t.Fatalf("pool:\n%s", got)
	}
}

func TestPoolProcessManager(t *testing.T) {
	s := phpSpec(PHPSettings{PM: "dynamic", MaxChildren: 20, RequestTimeoutSec: 120, SlowlogSec: 5, MaxRequests: 1000})
	if err := s.Normalize("/home/nimvor", "/var/lib/nimvor-agent"); err != nil {
		t.Fatal(err)
	}
	got := RenderPool(s, Layout{LogDir: "/l"})
	for _, w := range []string{"pm = dynamic\npm.max_children = 20\npm.start_servers = 2\npm.min_spare_servers = 1\npm.max_spare_servers = 3\npm.max_requests = 1000\nrequest_terminate_timeout = 120s\n", "request_slowlog_timeout = 5s\n"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
	if strings.Contains(got, "process_idle_timeout") {
		t.Error("idle timeout on a dynamic pool")
	}
	s = phpSpec(PHPSettings{PM: "static", MaxChildren: 8, StartServers: 4})
	if err := s.Normalize("/home/nimvor", "/var/lib/nimvor-agent"); err != nil {
		t.Fatal(err)
	}
	if got := RenderPool(s, Layout{LogDir: "/l"}); !strings.Contains(got, "pm = static\npm.max_children = 8\npm.max_requests = 500\n") || strings.Contains(got, "start_servers") {
		t.Errorf("static:\n%s", got)
	}
	for _, bad := range []PHPSettings{
		{PM: "adaptive"},
		{PM: "dynamic", MaxChildren: 4, MinSpare: 3, StartServers: 2, MaxSpare: 4},
		{PM: "dynamic", MaxChildren: 4, MaxSpare: 9},
		{RequestTimeoutSec: -1}, {MaxRequests: 200000}, {IdleTimeoutSec: 4000}, {SlowlogSec: 9999},
	} {
		s := phpSpec(bad)
		if err := s.Normalize("/home/nimvor", "/var/lib/nimvor-agent"); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
