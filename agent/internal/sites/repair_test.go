package sites

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRepairPermissions(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root (chown)")
	}
	base := t.TempDir()
	site := filepath.Join(base, "shop.test")
	pub := filepath.Join(site, "public_html")
	os.MkdirAll(filepath.Join(pub, "uploads"), 0o755)
	os.MkdirAll(filepath.Join(site, "logs"), 0o750)
	write := func(p string, mode uint32, uid, gid int) {
		os.WriteFile(p, []byte("x"), 0o600)
		os.Lchown(p, uid, gid)
		syscall.Chmod(p, mode) // after chown, which clears set-ID bits
	}
	const u, g, web = 5001, 5001, 33
	for _, d := range []string{site, pub, filepath.Join(pub, "uploads"), filepath.Join(site, "logs")} {
		os.Lchown(d, u, g)
	}
	os.Chmod(site, 0o710)
	os.Lchown(site, u, web)
	write(filepath.Join(pub, "index.php"), 0o644, u, web)          // fine
	write(filepath.Join(pub, "wp-config.php"), 0o600, u, g)        // private on purpose: fine
	write(filepath.Join(pub, "group.txt"), 0o664, u, web)          // group-writable: fine
	write(filepath.Join(pub, "rootfile.php"), 0o644, 0, 0)         // owner and group
	write(filepath.Join(pub, "open.php"), 0o666, u, g)             // world-writable
	write(filepath.Join(pub, "suid"), 0o4755, u, g)                // set-user-ID
	write(filepath.Join(pub, "locked"), 0o044, u, g)               // owner cannot read
	write(filepath.Join(site, "logs", "x.log"), 0o640, 1234, 1234) // other account's
	os.Chmod(filepath.Join(pub, "uploads"), 0o777)
	secret := filepath.Join(t.TempDir(), "secret")
	write(secret, 0o640, 0, 0)
	os.Symlink(secret, filepath.Join(pub, "link"))
	os.Link(secret, filepath.Join(pub, "hard"))

	fd, _ := syscall.Open(base, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	defer syscall.Close(fd)
	rules := permRules{uid: u, gid: g, webGID: web, docRel: "public_html"}
	dry, err := RepairPermissions(fd, "shop.test", rules, true)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Issues != 6 || dry.Fixed != 0 || dry.Counts[ProbOwner] != 2 || dry.Counts[ProbGroup] != 2 || dry.Counts[ProbWorldWrite] != 2 || dry.Counts[ProbSetID] != 1 || dry.Counts[ProbUnreadable] != 1 || dry.SkipN != 2 {
		t.Fatalf("dry run: %+v", dry)
	}
	var st syscall.Stat_t
	syscall.Stat(filepath.Join(pub, "rootfile.php"), &st)
	if st.Uid != 0 {
		t.Fatal("dry run changed a file")
	}
	rep, err := RepairPermissions(fd, "shop.test", rules, false)
	if err != nil || rep.Fixed != 6 {
		t.Fatalf("repair: %+v %v", rep, err)
	}
	check := func(p string, uid, gid int, mode uint32) {
		t.Helper()
		var st syscall.Stat_t
		syscall.Lstat(p, &st)
		if int(st.Uid) != uid || int(st.Gid) != gid || st.Mode&0o7777 != mode {
			t.Errorf("%s: %d:%d %o, want %d:%d %o", filepath.Base(p), st.Uid, st.Gid, st.Mode&0o7777, uid, gid, mode)
		}
	}
	check(filepath.Join(pub, "rootfile.php"), u, web, 0o644)
	check(filepath.Join(pub, "open.php"), u, g, 0o664)
	check(filepath.Join(pub, "suid"), u, g, 0o755)
	check(filepath.Join(pub, "locked"), u, g, 0o444)
	check(filepath.Join(pub, "uploads"), u, g, 0o775)
	check(filepath.Join(site, "logs", "x.log"), u, g, 0o640)
	check(filepath.Join(pub, "wp-config.php"), u, g, 0o600)
	check(filepath.Join(pub, "group.txt"), u, web, 0o664)
	check(site, u, web, 0o710)
	check(secret, 0, 0, 0o640)
	if again, _ := RepairPermissions(fd, "shop.test", rules, true); again.Issues != 0 {
		t.Fatalf("issues left: %+v", again)
	}
}

// Log files in logs/ are made by the web server and PHP-FPM as root (or the web server's account): who owns them
// is not a problem, so a new website does not need a repair at once; other flaws, and other owners, still are.
func TestLogsOwnedByServicesAreNotProblems(t *testing.T) {
	r := permRules{uid: 1001, gid: 1001, webGID: 33, webUID: 33, docRel: "public_html"}
	logStat := func(uid, gid, mode uint32) syscall.Stat_t { return syscall.Stat_t{Uid: uid, Gid: gid, Mode: mode} }
	for _, c := range []struct {
		rel  string
		st   syscall.Stat_t
		want []string
	}{
		{"logs/access.log", logStat(0, 4, 0o640), nil},
		{"logs/error.log", logStat(0, 0, 0o640), nil},
		{"logs/php-slow.log", logStat(0, 0, 0o600), nil},
		{"logs/access.log", logStat(33, 33, 0o640), nil},
		{"logs", logStat(0, 0, 0o750), nil},
		{"logs/access.log", logStat(0, 0, 0o666), []string{ProbWorldWrite}},
		{"logs/access.log", logStat(1002, 1002, 0o640), []string{ProbOwner, ProbGroup}},
		{"public_html/index.php", logStat(0, 0, 0o640), []string{ProbOwner, ProbGroup}},
		{"logsx/a.log", logStat(0, 0, 0o640), []string{ProbOwner, ProbGroup}},
	} {
		_, _, _, got := r.want(c.rel, c.rel == "logs", c.st)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s (uid %d): problems %v, want %v", c.rel, c.st.Uid, got, c.want)
		}
	}
}

// A site unpacked with owner-only modes (every file 0600, folders 0700) answers 403 for every page: the web server
// can neither read the files nor enter the folders. The repair gives the document root what the web server needs,
// and leaves what is private on purpose.
func TestOwnerOnlyModesInTheDocumentRootAreRepaired(t *testing.T) {
	r := permRules{uid: 1001, gid: 1001, webGID: 33, webUID: 33, docRel: "public_html"}
	st := func(mode uint32) syscall.Stat_t { return syscall.Stat_t{Uid: 1001, Gid: 33, Mode: mode} }
	type c struct {
		rel     string
		dir     bool
		mode    uint32
		want    uint32
		problem bool
	}
	for _, tc := range []c{
		{"public_html/index.php", false, 0o600, 0o644, true},
		{"public_html/css/site.css", false, 0o600, 0o644, true},
		{"public_html/img/logo.png", false, 0o400, 0o444, true},
		{"public_html/bin/run.sh", false, 0o700, 0o755, true},
		{"public_html/index.php", false, 0o640, 0o640, false}, // the web server's group reads it
		{"public_html/index.php", false, 0o604, 0o604, false},
		{"public_html/.well-known/acme-challenge/x", false, 0o600, 0o644, true},
		{"public_html/.htaccess", false, 0o600, 0o644, true},
		{"public_html/css", true, 0o700, 0o755, true},
		{"public_html", true, 0o700, 0o755, true},
		{"", true, 0o700, 0o755, true}, // the website folder: on the way to the document root
		{"public_html/css", true, 0o750, 0o750, false},
		// Private on purpose: left as they are.
		{"public_html/wp-config.php", false, 0o600, 0o600, false},
		{"public_html/sub/config.php", false, 0o600, 0o600, false},
		{"public_html/.env", false, 0o600, 0o600, false},
		{"public_html/.env.production", false, 0o600, 0o600, false},
		{"public_html/.git/config", false, 0o600, 0o600, false},
		{"public_html/.git", true, 0o700, 0o700, false},
		{"public_html/keys/server.key", false, 0o600, 0o600, false},
		{"public_html/backup/dump.sql", false, 0o600, 0o600, false},
		{"public_html/.user.ini", false, 0o600, 0o600, false},
		// Outside the document root nothing is asked of the web server.
		{"logs/x.log", false, 0o600, 0o600, false},
		{"tmp", true, 0o700, 0o700, false},
		{"private/notes.txt", false, 0o600, 0o600, false},
	} {
		_, _, mode, problems := r.want(tc.rel, tc.dir, st(tc.mode))
		got := false
		for _, p := range problems {
			got = got || p == ProbWebRead
		}
		if got != tc.problem || mode&0o7777 != tc.want {
			t.Errorf("%s (dir %v) %o: problems %v mode %o, want problem %v mode %o", tc.rel, tc.dir, tc.mode, problems, mode&0o7777, tc.problem, tc.want)
		}
	}
	// A document root that is the website folder itself.
	root := permRules{uid: 1001, gid: 1001, webGID: 33, webUID: 33, docRel: ""}
	_, _, mode, problems := root.want("index.html", false, st(0o600))
	if len(problems) != 1 || mode&0o7777 != 0o644 {
		t.Errorf("index.html in a website whose document root is its folder: %v %o", problems, mode&0o7777)
	}
}
