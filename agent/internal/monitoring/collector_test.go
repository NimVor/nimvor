package monitoring

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSystemInfoHostnameFollowsEtcHostname(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/hostname"), []byte("vc100.example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadSystemInfo(t.TempDir(), root).Hostname; got != "vc100.example.com" {
		t.Errorf("hostname = %q, want the one in /etc/hostname", got)
	}
	// No file: the running name.
	if got := ReadSystemInfo(t.TempDir(), t.TempDir()).Hostname; got == "" {
		t.Error("no fallback hostname")
	}
}

func TestReadTimezonePrefersTheLocaltimeLinkOverAStaleEtcTimezone(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	// timedatectl set-timezone moved the link; /etc/timezone still has the old name.
	if err := os.WriteFile(filepath.Join(root, "etc/timezone"), []byte("Europe/Berlin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../usr/share/zoneinfo/Asia/Karachi", filepath.Join(root, "etc/localtime")); err != nil {
		t.Fatal(err)
	}
	if got := readTimezone(root); got != "Asia/Karachi" {
		t.Errorf("readTimezone = %q, want Asia/Karachi", got)
	}
	_ = os.Remove(filepath.Join(root, "etc/localtime"))
	if got := readTimezone(root); got != "Europe/Berlin" {
		t.Errorf("without the link, readTimezone = %q, want Europe/Berlin", got)
	}
}
