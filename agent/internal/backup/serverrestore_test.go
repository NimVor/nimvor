package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func serverBackup(t *testing.T) (*testhost.Host, *Service, func(string) string, string, string) {
	t.Helper()
	h, s := siteHost(t)
	var log bytes.Buffer
	m, err := s.CreateServer(context.Background(), true, "nightly", nil, true, &log)
	if err != nil {
		t.Fatal(err)
	}
	return h, &s, h.Read, m.ID, h.Base
}

func TestServerRestoreRoundTrip(t *testing.T) {
	_, s, read, id, base := serverBackup(t)
	orig := read("etc/nginx/nginx.conf")
	os.WriteFile(filepath.Join(base, "etc/nginx/nginx.conf"), []byte("# broken by hand\n"), 0o644)
	os.WriteFile(filepath.Join(base, "etc/nginx/extra.conf"), []byte("# added later\n"), 0o644)

	var log bytes.Buffer
	res, err := s.RestoreServer(context.Background(), ServerRestoreRequest{ID: id, Components: []string{"nginx"}, Confirm: true}, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if read("etc/nginx/nginx.conf") != orig {
		t.Error("nginx.conf not restored")
	}
	if _, err := os.Stat(filepath.Join(base, "etc/nginx/extra.conf")); err == nil {
		t.Error("the directory is replaced as a whole")
	}
	if res["safety_backup"] == "" || res["safety_backup"] == nil {
		t.Error("no safety backup")
	}
	if m, _ := filepath.Glob(filepath.Join(base, "etc/nginx.whacp-old-*")); len(m) != 0 {
		t.Errorf("previous copy left behind: %v", m)
	}
	if m, _ := filepath.Glob(filepath.Join(base, "var/lib/whacp-agent/restore/*")); len(m) != 0 {
		t.Errorf("staging left behind: %v", m)
	}
	// The safety backup holds the hand-made state and can itself be restored.
	var log2 bytes.Buffer
	if _, err := s.RestoreServer(context.Background(), ServerRestoreRequest{ID: res["safety_backup"].(string), Components: []string{"nginx"}, Confirm: true}, &log2); err != nil {
		t.Fatal(err)
	}
	if read("etc/nginx/nginx.conf") != "# broken by hand\n" {
		t.Error("safety backup did not restore the previous state")
	}
}

func TestServerRestoreRollsBackInvalidConfig(t *testing.T) {
	host, s, read, id, base := serverBackup(t)
	os.WriteFile(filepath.Join(base, "etc/nginx/nginx.conf"), []byte("# current\n"), 0o644)
	var log bytes.Buffer
	host.Fail["nginx -t"] = "emerg: invalid"
	_, err := s.RestoreServer(context.Background(), ServerRestoreRequest{ID: id, Components: []string{"nginx", "php"}, Confirm: true}, &log)
	if err == nil || !strings.Contains(err.Error(), "previous one was put back") {
		t.Fatalf("err = %v", err)
	}
	if read("etc/nginx/nginx.conf") != "# current\n" {
		t.Error("rollback did not restore the current configuration")
	}
	if m, _ := filepath.Glob(filepath.Join(base, "etc/*.whacp-old-*")); len(m) != 0 {
		t.Errorf("leftovers: %v", m)
	}
}

func TestServerRestoreExtractOnly(t *testing.T) {
	_, s, read, id, base := serverBackup(t)
	os.WriteFile(filepath.Join(base, "etc/nginx/nginx.conf"), []byte("# current\n"), 0o644)
	var log bytes.Buffer
	res, err := s.RestoreServer(context.Background(), ServerRestoreRequest{ID: id, ExtractOnly: true, Confirm: true}, &log)
	if err != nil {
		t.Fatal(err)
	}
	if read("etc/nginx/nginx.conf") != "# current\n" {
		t.Error("extract_only changed the server")
	}
	dir := res["extracted_to"].(string)
	if _, err := os.Stat(filepath.Join(base, dir, "etc/nginx/nginx.conf")); err != nil {
		t.Errorf("not extracted: %v", err)
	}
	if info, _ := os.Stat(filepath.Join(base, dir)); info.Mode().Perm() != 0o700 {
		t.Errorf("staging mode %v", info.Mode())
	}
}

func TestServerRestoreValidation(t *testing.T) {
	for _, r := range []ServerRestoreRequest{
		{ID: "20260901T120000Z-abcdef", Components: []string{"nginx"}},              // not confirmed
		{ID: "20260901T120000Z-abcdef", Components: []string{"ssh"}, Confirm: true}, // not restorable in place
		{ID: "20260901T120000Z-abcdef", Confirm: true},                              // nothing chosen
		{ID: "20260901T120000Z-abcdef", Components: []string{"nginx"}, ExtractOnly: true, Confirm: true},
		{ID: "../x", Components: []string{"nginx"}, Confirm: true},
	} {
		if err := r.validate(); err == nil {
			t.Errorf("accepted %+v", r)
		}
	}
}

func TestExtractRefusesEscapes(t *testing.T) {
	_, s := siteHost(t)
	for name, entries := range map[string][]tar.Header{
		"dotdot":       {{Name: "etc/../../outside", Typeflag: tar.TypeReg}},
		"absolute":     {{Name: "/etc/passwd", Typeflag: tar.TypeReg}},
		"outside etc":  {{Name: "root/.ssh/authorized_keys", Typeflag: tar.TypeReg}},
		"through link": {{Name: "etc/x", Typeflag: tar.TypeSymlink, Linkname: "/"}, {Name: "etc/x/tmp/pwned", Typeflag: tar.TypeReg}},
	} {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, h := range entries {
			h.Mode = 0o644
			tw.WriteHeader(&h)
		}
		tw.Close()
		dest := t.TempDir()
		if _, err := s.extract(&buf, dest); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
