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
