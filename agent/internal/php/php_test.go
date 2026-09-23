package php

import (
	"path/filepath"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

func TestDiscoverFixture(t *testing.T) {
	abs, _ := filepath.Abs("../../tests/fixtures/ubuntu-host")
	root, _ := filesystem.NewRoot(abs)
	res := Discover(root, platform.Detect(root).Paths())

	if len(res.Versions) != 2 || res.Versions[0].Version != "8.3" || res.Versions[1].Version != "8.4" {
		t.Fatalf("versions = %+v", res.Versions)
	}
	if res.DefaultVersion != "8.3" || !res.Versions[0].IsDefault {
		t.Errorf("default version = %q", res.DefaultVersion)
	}
	if res.Versions[0].FPMService != "php8.3-fpm.service" || res.Versions[0].PoolCount != 2 {
		t.Errorf("8.3 = %+v", res.Versions[0])
	}
	pools := map[string]Pool{}
	for _, p := range res.Pools {
		pools[p.Name] = p
	}
	if len(pools) != 3 {
		t.Fatalf("pools = %+v", res.Pools)
	}
	if p := pools["example"]; p.Listen != "/run/php/php8.3-fpm-example.sock" || p.OpenBasedir != "/var/www/example.com:/tmp" {
		t.Errorf("$pool not expanded or quotes kept: %+v", p)
	}
	if p := pools["www"]; p.MaxChildren != "5" {
		t.Errorf("inline comment not stripped: %q", p.MaxChildren)
	}
	if p := pools["shop"]; p.Version != "8.4" || p.Listen != "127.0.0.1:9084" {
		t.Errorf("shop pool = %+v", p)
	}
}

func TestNormalizeListen(t *testing.T) {
	cases := map[string]string{
		"unix:/run/php/x.sock": "unix:/run/php/x.sock",
		"/run/php/x.sock":      "unix:/run/php/x.sock",
		"9000":                 "tcp:127.0.0.1:9000",
		"127.0.0.1:9000":       "tcp:127.0.0.1:9000",
		"localhost:9000":       "tcp:127.0.0.1:9000",
		"[::1]:9000":           "tcp:127.0.0.1:9000",
		"10.0.0.5:9000":        "tcp:10.0.0.5:9000",
	}
	for in, want := range cases {
		if got := NormalizeListen(in); got != want {
			t.Errorf("NormalizeListen(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVersionFromPath(t *testing.T) {
	cases := map[string]string{
		"/usr/bin/php8.3":                  "8.3",
		"/usr/sbin/php-fpm8.4":             "8.4",
		"/etc/php/8.1/fpm/pool.d/www.conf": "8.1",
		"/opt/remi/php82/root/usr/bin/php": "8.2",
		"/usr/bin/php":                     "",
	}
	for in, want := range cases {
		if got := versionFromPath(in); got != want {
			t.Errorf("versionFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}
