package diskmgr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func write(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseMountsKeepsDataFilesystemsOnce(t *testing.T) {
	got := parseMounts("proc /proc proc rw 0 0\n/dev/vda1 / ext4 rw 0 0\n/dev/vda1 /var/lib/docker ext4 rw 0 0\ntmpfs /run tmpfs rw 0 0\n/dev/sdb1 /mnt/data\\040disk xfs rw 0 0\n")
	if len(got) != 2 || got[0].point != "/" || got[1].point != "/mnt/data disk" || got[1].fstype != "xfs" {
		t.Fatalf("mounts: %+v", got)
	}
}

func TestScanSumsAreasSitesAndListsTheBiggestFiles(t *testing.T) {
	h := testhost.New(t)
	sites := h.Env.Cfg.SitesRoot
	real := func(p string) string { r, _ := h.Env.Root.Path(p); return r }
	write(t, filepath.Join(real(sites), "a.test", "public_html", "big.zip"), 3000)
	write(t, filepath.Join(real(sites), "a.test", "index.php"), 10)
	write(t, filepath.Join(real(sites), "b.test", "dump.sql"), 1000)
	write(t, filepath.Join(real(h.Env.Cfg.BackupDir), "site.tar"), 2000)

	res := Scan(context.Background(), h.Env, 500)
	byKey := map[string]Area{}
	for _, a := range res.Areas {
		byKey[a.Key] = a
	}
	if byKey["sites"].Bytes != 4010 || byKey["sites"].Files != 3 || byKey["backups"].Bytes != 2000 {
		t.Errorf("areas: %+v", byKey)
	}
	if len(res.Sites) != 2 || res.Sites[0].Domain != "a.test" || res.Sites[0].Bytes != 3010 || res.Sites[1].Bytes != 1000 {
		t.Errorf("sites: %+v", res.Sites)
	}
	if len(res.Large) != 3 || res.Large[0].Bytes != 3000 || res.Large[0].Site != "a.test" || !res.Large[0].Deletable || res.Large[2].Bytes != 1000 {
		t.Errorf("large: %+v", res.Large)
	}
	// Small files are not listed.
	for _, f := range res.Large {
		if strings.HasSuffix(f.Path, "index.php") {
			t.Errorf("small file listed: %+v", f)
		}
	}
}

func TestRemoveOnlyTakesRegularFilesFromTheAllowedPlaces(t *testing.T) {
	h := testhost.New(t)
	real := func(p string) string { r, _ := h.Env.Root.Path(p); return r }
	siteFile := filepath.Join(h.Env.Cfg.SitesRoot, "a.test", "public_html", "big.zip")
	write(t, real(siteFile), 3000)
	backup := filepath.Join(h.Env.Cfg.BackupDir, "old.tar")
	write(t, real(backup), 2000)
	mail := filepath.Join(h.Env.M.VmailDir, "a.test", "user", "msg")
	write(t, real(mail), 100)
	topLevel := filepath.Join(h.Env.Cfg.SitesRoot, "loose.bin")
	write(t, real(topLevel), 100)
	outside := filepath.Join(t.TempDir(), "secret")
	write(t, outside, 100)
	link := filepath.Join(h.Env.Cfg.SitesRoot, "a.test", "link")
	os.Symlink(outside, real(link))
	linkedDir := filepath.Join(h.Env.Cfg.SitesRoot, "a.test", "ld")
	os.Symlink(filepath.Dir(outside), real(linkedDir))
	activeLog := "/var/log/app.log"
	oldLog := "/var/log/app.log.1.gz"
	write(t, real(activeLog), 700)
	write(t, real(oldLog), 300)

	res := Remove(h.Env, []string{siteFile, backup, mail, topLevel, link, linkedDir + "/secret", activeLog, oldLog, "relative/x", siteFile + "/../../x", "/etc/passwd"})
	got := map[string]Removed{}
	for _, r := range res {
		got[r.Path] = r
	}
	if got[siteFile].Action != "deleted" || got[siteFile].Bytes != 3000 || got[backup].Action != "deleted" {
		t.Errorf("allowed files: %+v %+v", got[siteFile], got[backup])
	}
	if got[activeLog].Action != "emptied" || got[activeLog].Bytes != 700 || got[oldLog].Action != "deleted" {
		t.Errorf("logs: %+v %+v", got[activeLog], got[oldLog])
	}
	if fi, err := os.Stat(real(activeLog)); err != nil || fi.Size() != 0 {
		t.Errorf("the active log must be emptied, not removed: %v", err)
	}
	for _, p := range []string{mail, topLevel, link, linkedDir + "/secret", "relative/x", siteFile + "/../../x", "/etc/passwd"} {
		if got[p].Error == "" || got[p].Action != "" {
			t.Errorf("%s was not refused: %+v", p, got[p])
		}
	}
	for _, p := range []string{real(mail), real(topLevel), outside} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must still exist: %v", p, err)
		}
	}
}
