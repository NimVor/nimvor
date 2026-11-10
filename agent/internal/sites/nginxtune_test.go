package sites

import (
	"strings"
	"testing"
)

func tunedSpec(t *testing.T, kind string, n *NginxTuning) Spec {
	t.Helper()
	s := Spec{Domain: "shop.test", User: "shop", CreateUser: true, Kind: kind, PHPVersion: "8.3", Enabled: true, Nginx: n}
	if kind == "proxy" {
		s.ProxyPass = "http://127.0.0.1:3000"
	}
	if err := s.Normalize(t.TempDir(), t.TempDir()); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	return s
}

func TestNginxTuningIsRenderedIntoTheServerBlock(t *testing.T) {
	on := true
	s := tunedSpec(t, "php", &NginxTuning{ReadTimeoutSeconds: 120, SendTimeoutSeconds: 90, KeepaliveSeconds: 30, FastcgiBufferKB: 32, Gzip: &on, GzipLevel: 5, RateLimitKBps: 512, Extra: []string{"client_body_buffer_size 256k;", "fastcgi_buffers 16 16k"}})
	out := RenderNginx(s, Layout{Socket: "/run/x.sock", LogDir: "/var/log/x"})
	for _, want := range []string{"fastcgi_read_timeout 120s;", "send_timeout 90s;", "keepalive_timeout 30s;", "fastcgi_buffer_size 32k;", "gzip on;", "gzip_comp_level 5;", "limit_rate 512k;", "client_body_buffer_size 256k;", "fastcgi_buffers 16 16k;"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	p := tunedSpec(t, "proxy", &NginxTuning{ReadTimeoutSeconds: 45})
	if !strings.Contains(RenderNginx(p, Layout{LogDir: "/var/log/x"}), "proxy_read_timeout 45s;") {
		t.Error("proxy read timeout not rendered")
	}
	plain := RenderNginx(tunedSpec(t, "php", nil), Layout{Socket: "/run/x.sock", LogDir: "/var/log/x"})
	if strings.Contains(plain, "gzip") || strings.Contains(plain, "limit_rate") {
		t.Errorf("an untuned site must render nothing extra:\n%s", plain)
	}
}

func TestNginxTuningRefusesAnythingUnsafe(t *testing.T) {
	off := false
	cases := map[string]struct {
		kind string
		n    *NginxTuning
	}{
		"out of range":     {"php", &NginxTuning{ReadTimeoutSeconds: 5}},
		"buffer on static": {"static", &NginxTuning{FastcgiBufferKB: 8}},
		"level with off":   {"static", &NginxTuning{Gzip: &off, GzipLevel: 4}},
		"root override":    {"static", &NginxTuning{Extra: []string{"root /etc"}}},
		"include":          {"static", &NginxTuning{Extra: []string{"include /etc/passwd"}}},
		"brace":            {"static", &NginxTuning{Extra: []string{"tcp_nodelay on; } location / {"}}},
		"variable":         {"static", &NginxTuning{Extra: []string{"gzip_types $host"}}},
		"duplicate":        {"static", &NginxTuning{Extra: []string{"tcp_nodelay on", "tcp_nodelay off"}}},
		"set by own field": {"static", &NginxTuning{SendTimeoutSeconds: 30, Extra: []string{"send_timeout 60s"}}},
		"no value":         {"static", &NginxTuning{Extra: []string{"tcp_nodelay"}}},
		"too many":         {"static", &NginxTuning{Extra: make([]string, 21)}},
	}
	for name, c := range cases {
		s := Spec{Domain: "shop.test", User: "shop", CreateUser: true, Kind: c.kind, PHPVersion: "8.3", Enabled: true, Nginx: c.n}
		if err := s.Normalize(t.TempDir(), t.TempDir()); err == nil {
			t.Errorf("%s: expected a refusal", name)
		}
	}
}
