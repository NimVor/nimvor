package migrate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

type tarEntry struct {
	name, body, link string
	typ              byte
	mode             int64
}

func writeTarGz(t *testing.T, path string, entries []tarEntry) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: e.name, Typeflag: typ, Mode: mode, Linkname: e.link}
		if typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	zw.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

const cpmoveDump = "-- MySQL dump\nCREATE TABLE `a` (id int);\nINSERT INTO a VALUES (1);\nCREATE TABLE `b` (id int);\n-- Dump completed on 2026-01-01  1:00:00\n"

func cpmoveEntries() []tarEntry {
	return []tarEntry{
		{name: "cpmove-acme/", typ: tar.TypeDir, mode: 0o755},
		{name: "cpmove-acme/cp/acme", body: "DNS=acme-shop.com\nIP=203.0.113.9\n"},
		{name: "cpmove-acme/userdata/main", body: "main_domain: acme-shop.com\nsub_domains:\n  - blog.acme-shop.com\n"},
		{name: "cpmove-acme/userdata/acme-shop.com", body: "documentroot: /home/acme/public_html\nhomedir: /home/acme\nphpversion: ea-php74\n"},
		{name: "cpmove-acme/userdata/blog.acme-shop.com", body: "documentroot: /home/acme/public_html/blog\nhomedir: /home/acme\n"},
		{name: "cpmove-acme/userdata/acme-shop.com_SSL", body: "documentroot: /home/acme/ssl-ignored\n"},
		{name: "cpmove-acme/homedir/public_html/index.php", body: "<?php echo 1;"},
		{name: "cpmove-acme/homedir/public_html/blog/index.php", body: "<?php echo 2;"},
		{name: "cpmove-acme/homedir/etc/acme-shop.com/passwd", body: "info:x:1:1::/home/acme/mail/acme-shop.com/info:/bin/false\n"},
		{name: "cpmove-acme/homedir/mail/acme-shop.com/info/cur/1.msg", body: "Subject: hi\n"},
		{name: "cpmove-acme/mysql/acme_wp.sql", body: cpmoveDump},
		{name: "cpmove-acme/mysql/acme_wp.create", body: "CREATE DATABASE acme_wp;"},
		{name: "cpmove-acme/mysql/mysql.sql", body: "grants"},
		{name: "cpmove-acme/va/acme-shop.com", body: "support: info@acme-shop.com, help@example.org\n"},
		{name: "cpmove-acme/proftpdpasswd", body: "deploy@acme-shop.com:x:1:1::/home/acme/public_html:/bin/false\n"},
		{name: "cpmove-acme/cron/acme", body: "*/5 * * * * php /home/acme/public_html/cron.php\n"},
		{name: "cpmove-acme/dnszones/acme-shop.com.db", body: "$ORIGIN acme-shop.com.\n@ 3600 IN MX 10 mail.acme-shop.com.\n"},
	}
}

func TestACpmoveArchiveIsUnpackedAndDescribed(t *testing.T) {
	h := testhost.New(t)
	real, _ := h.Env.Root.Path("/root/cpmove-acme.tar.gz")
	writeTarGz(t, real, cpmoveEntries())
	list := ListArchives(h.Env)
	if len(list) != 1 || list[0].Path != "/root/cpmove-acme.tar.gz" {
		t.Fatalf("list = %+v", list)
	}
	var log strings.Builder
	id, err := ExtractArchive(context.Background(), h.Env, "/root/cpmove-acme.tar.gz", &log)
	if err != nil {
		t.Fatal(err)
	}
	d, err := DiscoverArchive(h.Env, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Accounts) != 1 {
		t.Fatalf("accounts = %+v", d.Accounts)
	}
	a := d.Accounts[0]
	dir, _ := archiveDir(h.Env, id)
	if a.ID != "acme" || len(a.Domains) != 2 || a.Domains[0].Name != "acme-shop.com" || a.Domains[0].Kind != "main" || a.Domains[1].Kind != "sub" {
		t.Fatalf("domains = %+v", a.Domains)
	}
	if want := filepath.Join(dir, "cpmove-acme", "homedir", "public_html"); a.Domains[0].DocRoot != want {
		t.Errorf("docroot = %q, want %q", a.Domains[0].DocRoot, want)
	}
	if a.PHP != "7.4" {
		t.Errorf("php = %q", a.PHP)
	}
	if len(a.Databases) != 1 || a.Databases[0].Name != "acme_wp" {
		t.Errorf("databases = %+v", a.Databases)
	}
	if len(a.Email) != 1 || a.Email[0].Address != "info@acme-shop.com" || !strings.HasPrefix(a.Email[0].Maildir, dir) || a.Email[0].SizeKB < 1 {
		t.Errorf("mail = %+v", a.Email)
	}
	if len(a.Forwarders) != 1 || a.Forwarders[0].Source != "support@acme-shop.com" {
		t.Errorf("forwarders = %+v", a.Forwarders)
	}
	if len(a.FTP) != 1 || !strings.HasPrefix(a.FTP[0].Home, dir) {
		t.Errorf("ftp = %+v", a.FTP)
	}
	if len(a.Cron) != 1 {
		t.Errorf("cron = %+v", a.Cron)
	}
	if z, err := ArchiveZone(h.Env, id, "acme-shop.com"); err != nil || !strings.Contains(z, "MX 10") {
		t.Errorf("zone = %q %v", z, err)
	}
	if _, err := ArchiveZone(h.Env, id, "other.com"); err == nil {
		t.Error("a zone that is not in the backup was returned")
	}

	// The engine's two questions about a database are answered from the SQL file.
	p, err := newArchivePuller(h.Env, id)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := p.Exec(context.Background(), "sh -c "+Shq("mysqldump --single-transaction -- 'acme_wp' | gzip -c"), &out); err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(&out)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	if string(body) != cpmoveDump {
		t.Errorf("dump = %q", body)
	}
	if n, err := p.Exec(context.Background(), "sh -c "+Shq("mysql -N -e "+Shq("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'acme_wp'")), nil); err != nil || strings.TrimSpace(n) != "2" {
		t.Errorf("tables = %q %v", n, err)
	}
	for _, bad := range []string{"mysqldump -- 'nosuch' | gzip -c", "rm -rf /", "mysqldump -- '../x' | gzip -c"} {
		if _, err := p.Exec(context.Background(), "sh -c "+Shq(bad), io.Discard); err == nil {
			t.Errorf("%q was answered", bad)
		}
	}
	if _, err := p.Rsync(context.Background(), []string{"-a"}, "/etc", "/tmp/x", io.Discard); err == nil {
		t.Error("a folder outside the backup was copied")
	}

	if err := ForgetArchive(h.Env, id); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverArchive(h.Env, id, nil); err == nil {
		t.Error("the unpacked copy is still there")
	}
}

func TestAHostileArchiveIsNeverUnpackedOutsideItsFolder(t *testing.T) {
	h := testhost.New(t)
	cases := map[string][]tarEntry{
		"dotdot":   {{name: "cpmove-x/../../evil", body: "x"}},
		"absolute": {{name: "/etc/evil", body: "x"}},
		"through a link": {{name: "cpmove-x/l", typ: tar.TypeSymlink, link: "."}, {name: "cpmove-x/l/ok", body: "fine"},
			{name: "cpmove-x/m", typ: tar.TypeSymlink, link: "../../.."}, {name: "cpmove-x/m/evil", body: "x"}},
	}
	for name, entries := range cases {
		real, _ := h.Env.Root.Path("/root/cpmove-" + strings.ReplaceAll(name, " ", "") + ".tar.gz")
		writeTarGz(t, real, entries)
		id, err := ExtractArchive(context.Background(), h.Env, "/root/cpmove-"+strings.ReplaceAll(name, " ", "")+".tar.gz", nil)
		if name == "through a link" {
			// The link to ".." is skipped, so the write through it fails the confinement check or lands inside.
			if err == nil {
				dir, _ := archiveDir(h.Env, id)
				if _, e := os.Stat(filepath.Join(filepath.Dir(dir), "evil")); e == nil {
					t.Error("a file was written outside the folder")
				}
				if fi, e := os.Lstat(filepath.Join(dir, "cpmove-x", "m")); e == nil && fi.Mode()&os.ModeSymlink != 0 {
					t.Error("a link that leaves the folder was recreated")
				}
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: unpacked (%s)", name, id)
		}
	}
	root, _ := archivesRoot(h.Env)
	if ents, _ := os.ReadDir(root); len(ents) > 1 {
		for _, e := range ents {
			if _, err := os.Stat(filepath.Join(root, e.Name(), "evil")); err == nil {
				t.Errorf("evil file in %s", e.Name())
			}
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil")); err == nil {
		t.Error("evil file next to the archives folder")
	}
}

func TestSetIDBitsAndSpecialEntriesAreDropped(t *testing.T) {
	h := testhost.New(t)
	real, _ := h.Env.Root.Path("/root/cpmove-s.tar.gz")
	writeTarGz(t, real, []tarEntry{
		{name: "cpmove-s/cp/s", body: "x"}, {name: "cpmove-s/homedir/bin", body: "x", mode: 0o4755},
		{name: "cpmove-s/homedir/hl", typ: tar.TypeLink, link: "cpmove-s/homedir/bin"},
		{name: "cpmove-s/homedir/fifo", typ: tar.TypeFifo},
		{name: "cpmove-s/homedir/abs", typ: tar.TypeSymlink, link: "/etc/passwd"},
	})
	id, err := ExtractArchive(context.Background(), h.Env, "/root/cpmove-s.tar.gz", nil)
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := archiveDir(h.Env, id)
	fi, err := os.Stat(filepath.Join(dir, "cpmove-s", "homedir", "bin"))
	if err != nil || fi.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		t.Errorf("mode = %v %v", fi, err)
	}
	for _, n := range []string{"hl", "fifo", "abs"} {
		if _, err := os.Lstat(filepath.Join(dir, "cpmove-s", "homedir", n)); err == nil {
			t.Errorf("%s was recreated", n)
		}
	}
}

func TestArchivePathsAndSourcesAreValidated(t *testing.T) {
	h := testhost.New(t)
	real, _ := h.Env.Root.Path("/root/cpmove-v.tar.gz")
	writeTarGz(t, real, []tarEntry{{name: "other/file", body: "x"}})
	for _, p := range []string{"cpmove-v.tar.gz", "/etc/cpmove-v.tar.gz", "/root/../etc/cpmove-v.tar.gz", "/root/notes.txt", "/root/cpmove-missing.tar.gz", "/root/cpmove-v.tar.gz; rm"} {
		if _, err := ExtractArchive(context.Background(), h.Env, p, nil); err == nil {
			t.Errorf("%q was accepted", p)
		}
	}
	link, _ := h.Env.Root.Path("/root/cpmove-link.tar.gz")
	os.Symlink(real, link)
	if _, err := ExtractArchive(context.Background(), h.Env, "/root/cpmove-link.tar.gz", nil); err == nil {
		t.Error("a link was accepted as an archive")
	}
	// Something that is not a cPanel backup unpacks but cannot be described.
	id, err := ExtractArchive(context.Background(), h.Env, "/root/cpmove-v.tar.gz", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverArchive(h.Env, id, nil); err == nil || !strings.Contains(err.Error(), "not a cPanel account backup") {
		t.Errorf("err = %v", err)
	}
	for _, s := range []Source{{Archive: "../x"}, {Archive: "0123456789abcdef", Host: "x.example.com"}, {Archive: "0123456789abcdef", Password: "p"}} {
		if s.Validate(true) == nil {
			t.Errorf("%+v was accepted", s)
		}
	}
	ok := Source{Archive: "0123456789abcdef"}
	if err := ok.Validate(true); err != nil {
		t.Error(err)
	}
	if _, err := Lookup("cpmove"); err != nil {
		t.Error(err)
	}
}
