package security

import (
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/pkgs"
)

func TestDatabaseSupport(t *testing.T) {
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	for v, want := range map[string]string{
		"10.11.14-MariaDB-0ubuntu0.24.04.1": "pass",
		"10.6.22-MariaDB-0ubuntu0.22.04.1":  "warn", // upstream ended, distribution fixes
		"10.6.22-MariaDB":                   "fail",
		"8.0.43-0ubuntu0.22.04.1":           "warn",
		"8.4.6":                             "pass",
		"5.7.44":                            "fail",
		"11.7.2-MariaDB":                    "warn", // short-term
		"12.0.1-MariaDB":                    "warn",
		"10.1.48-MariaDB":                   "fail",
		"garbage":                           "info",
	} {
		if got, detail := dbSupported(v, now); got != want {
			t.Errorf("%s: %s (%s), want %s", v, got, detail, want)
		}
	}
}

func TestSSHVersion(t *testing.T) {
	if c := sshVersionCheck(nil); c.Status != "pass" {
		t.Fatal(c)
	}
	if c := sshVersionCheck([]pkgs.Upgrade{{Name: "openssh-server", Current: "1:8.9p1-3", New: "1:8.9p1-3ubuntu0.13", Security: true}}); c.Status != "fail" {
		t.Fatal(c)
	}
}

func TestPackageIndexCheck(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	for refreshed, want := range map[time.Time]string{{}: "warn", now.Add(-10 * 24 * time.Hour): "warn", now.Add(-2 * time.Hour): "pass"} {
		if c := indexCheck(refreshed, now); c.Status != want || (want == "warn") != (c.Remedy != "") {
			t.Errorf("%v: %+v", refreshed, c)
		}
	}
}
