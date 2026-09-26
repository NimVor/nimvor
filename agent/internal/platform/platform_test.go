package platform

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestParseOSRelease(t *testing.T) {
	cases := []struct {
		in        string
		family    string
		supported bool
		preview   bool
	}{
		{"ID=ubuntu\nID_LIKE=debian\nVERSION_ID=\"24.04\"\n", "debian", true, false},
		{"ID=ubuntu\nVERSION_ID=\"22.04\"\n", "debian", true, false},
		{"ID=ubuntu\nVERSION_ID=\"26.04\"\n", "debian", true, true},
		{"ID=ubuntu\nVERSION_ID=\"20.04\"\n", "debian", false, false},
		{"ID=debian\nVERSION_ID=\"12\"\n", "debian", false, false},
		{"ID=\"almalinux\"\nID_LIKE=\"rhel centos fedora\"\nVERSION_ID=\"9.4\"\n", "rhel", true, false},
		{"ID=\"rhel\"\nVERSION_ID=\"9.6\"\n", "rhel", true, false},
		{"ID=\"almalinux\"\nVERSION_ID=\"10.0\"\n", "rhel", true, true},
		{"ID=rocky\nVERSION_ID=\"10.0\"\n", "rhel", true, true},
		{"ID=\"rhel\"\nVERSION_ID=\"8.10\"\n", "rhel", false, false},
		{"ID=\"centos\"\nID_LIKE=\"rhel fedora\"\nVERSION_ID=\"10\"\n", "rhel", false, false},
		{"ID=rocky\nID_LIKE=\"rhel centos fedora\"\n", "rhel", false, false},
		{"", "unknown", false, false},
	}
	for _, c := range cases {
		info := ParseOSRelease([]byte(c.in))
		if info.Family != c.family || info.Supported != c.supported || info.Preview != c.preview {
			t.Errorf("ParseOSRelease(%q) = family %q supported %v preview %v", c.in, info.Family, info.Supported, info.Preview)
		}
	}
}

func TestDetectPicksFamilyPaths(t *testing.T) {
	deb := debianFamily{}
	if deb.Paths().NginxSitesEnabled == "" {
		t.Error("debian family must define sites-enabled")
	}
	rh := rhelFamily{}
	if rh.Paths().NginxSitesEnabled != "" {
		t.Error("rhel family does not use sites-enabled")
	}
}

func TestRHELPHPNames(t *testing.T) {
	m := rhelFamily{}.Managed()
	if m.PoolDir("8.3") != "/etc/opt/remi/php83/php-fpm.d" || m.FPMUnit("8.3") != "php83-php-fpm.service" {
		t.Fatalf("pool %s unit %s", m.PoolDir("8.3"), m.FPMUnit("8.3"))
	}
	d := debianFamily{}.Managed()
	if d.PoolDir("8.3") != "/etc/php/8.3/fpm/pool.d" || d.FPMUnit("8.3") != "php8.3-fpm.service" {
		t.Fatalf("pool %s unit %s", d.PoolDir("8.3"), d.FPMUnit("8.3"))
	}
}

// RHEL 10 and its rebuilds ship Valkey instead of Redis.
func TestRHELRedisOrValkey(t *testing.T) {
	for _, c := range []struct{ version, unit, config, pkg string }{
		{"9.4", "redis.service", "/etc/redis/redis.conf", "redis"},
		{"10.0", "valkey.service", "/etc/valkey/valkey.conf", "valkey"},
		{"10", "valkey.service", "/etc/valkey/valkey.conf", "valkey"},
	} {
		r := rhelFamily{info: OSInfo{ID: "almalinux", VersionID: c.version}}
		m := r.Managed()
		pkg, ok := r.PackageName("redis-server")
		if m.RedisUnit != c.unit || m.RedisConfig != c.config || !ok || pkg != c.pkg || m.ServerConfig["redis"] != filepath.Dir(c.config) {
			t.Errorf("%s: unit %s config %s package %s dir %s", c.version, m.RedisUnit, m.RedisConfig, pkg, m.ServerConfig["redis"])
		}
		if !slices.Contains(r.CoreServices(), c.unit) {
			t.Errorf("%s: %s not a core service", c.version, c.unit)
		}
	}
}
