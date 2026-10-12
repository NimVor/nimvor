package pkgs

import "testing"

func TestParseUpdateinfoList(t *testing.T) {
	out := `RHSA-2026:1234 Important/Sec.  nginx-1.24.0-1.el9.x86_64
RHSA-2026:9999 Moderate/Sec.   openssl-3.0.7-2.el9.x86_64
FEDORA-2026-aa bugfix          curl-8.0.1-1.el9.x86_64`
	ids, sev := parseUpdateinfoList(out)
	if ids["nginx"] != "RHSA-2026:1234" || sev["nginx"] != "important" {
		t.Errorf("nginx: id=%q sev=%q", ids["nginx"], sev["nginx"])
	}
	if ids["openssl"] != "RHSA-2026:9999" || sev["openssl"] != "moderate" {
		t.Errorf("openssl: id=%q sev=%q", ids["openssl"], sev["openssl"])
	}
	// A bugfix advisory has no security severity.
	if sev["curl"] != "" {
		t.Errorf("curl severity should be empty, got %q", sev["curl"])
	}
}

func TestParseUpdateinfoCVEs(t *testing.T) {
	out := `CVE-2026-1111 Important/Sec. nginx-1.24.0-1.el9.x86_64
CVE-2026-2222 Important/Sec. nginx-1.24.0-1.el9.x86_64
CVE-2026-3333 Critical/Sec.  openssl-3.0.7-2.el9.x86_64`
	m := parseUpdateinfoCVEs(out)
	if len(m["nginx"]) != 2 || m["nginx"][0] != "CVE-2026-1111" || m["nginx"][1] != "CVE-2026-2222" {
		t.Errorf("nginx cves: %v", m["nginx"])
	}
	if len(m["openssl"]) != 1 || m["openssl"][0] != "CVE-2026-3333" {
		t.Errorf("openssl cves: %v", m["openssl"])
	}
}

func TestNevraName(t *testing.T) {
	cases := map[string]string{
		"nginx-1.24.0-1.el9.x86_64":      "nginx",
		"nginx-core-1.24.0-1.el9.x86_64": "nginx-core",
		"openssl-3.0.7-2.el9.x86_64":     "openssl",
	}
	for in, want := range cases {
		if got := nevraName(in); got != want {
			t.Errorf("nevraName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSeverityOf(t *testing.T) {
	for in, want := range map[string]string{"Critical/Sec.": "critical", "Important/Sec.": "important", "Moderate/Sec.": "moderate", "Low/Sec.": "low", "bugfix": "", "enhancement": ""} {
		if got := severityOf(in); got != want {
			t.Errorf("severityOf(%q) = %q, want %q", in, got, want)
		}
	}
}
