package platform

import "testing"

func TestParseOSRelease(t *testing.T) {
	cases := []struct {
		in        string
		family    string
		supported bool
	}{
		{"ID=ubuntu\nID_LIKE=debian\nVERSION_ID=\"24.04\"\n", "debian", true},
		{"ID=ubuntu\nVERSION_ID=\"22.04\"\n", "debian", true},
		{"ID=ubuntu\nVERSION_ID=\"26.04\"\n", "debian", true},
		{"ID=ubuntu\nVERSION_ID=\"20.04\"\n", "debian", false},
		{"ID=debian\nVERSION_ID=\"12\"\n", "debian", false},
		{"ID=\"almalinux\"\nID_LIKE=\"rhel centos fedora\"\nVERSION_ID=\"9.4\"\n", "rhel", true},
		{"ID=\"almalinux\"\nVERSION_ID=\"10.0\"\n", "rhel", true},
		{"ID=\"rhel\"\nVERSION_ID=\"8.10\"\n", "rhel", false},
		{"ID=\"centos\"\nID_LIKE=\"rhel fedora\"\nVERSION_ID=\"9\"\n", "rhel", false},
		{"ID=rocky\nID_LIKE=\"rhel centos fedora\"\n", "rhel", false},
		{"", "unknown", false},
	}
	for _, c := range cases {
		info := ParseOSRelease([]byte(c.in))
		if info.Family != c.family || info.Supported != c.supported {
			t.Errorf("ParseOSRelease(%q) = family %q supported %v", c.in, info.Family, info.Supported)
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
