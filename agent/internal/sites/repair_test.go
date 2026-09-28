package sites

import (
	"os"
	"path/filepath"
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
