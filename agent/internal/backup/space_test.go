package backup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// fakeFree makes every filesystem report free(path) bytes available.
func fakeFree(t *testing.T, free func(path string) int64) {
	t.Helper()
	old := statfs
	statfs = func(path string, st *syscall.Statfs_t) error {
		st.Bsize = 1
		st.Bavail = uint64(free(path))
		return nil
	}
	t.Cleanup(func() { statfs = old })
}

func TestSpaceGuardStopsBeforeTheReserve(t *testing.T) {
	free := Reserve + 3*spaceCheckEvery
	fakeFree(t, func(string) int64 { return free })
	var out bytes.Buffer
	g := newSpaceGuard(&out, "/var/backups")
	chunk := make([]byte, 1<<20)
	var err error
	for i := 0; i < 200 && err == nil; i++ {
		_, err = g.Write(chunk)
		free -= int64(len(chunk)) // the disk fills as we write
	}
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("err = %v", err)
	}
	if free < Reserve-spaceCheckEvery {
		t.Fatalf("wrote past the reserve: %d bytes free", free)
	}
}

// A backup that would fill the disk stops and leaves nothing behind.
func TestBackupStopsWhenTheDiskIsFull(t *testing.T) {
	h, s := siteHost(t)
	fakeFree(t, func(string) int64 { return Reserve - 1 })
	cred, _, _ := h.Env.Credential("example")
	var log bytes.Buffer
	_, err := s.CreateSite(context.Background(), SiteRequest{Domain: "example.com", User: "example", Root: "/home/whacp/example.com", Files: true, Databases: []string{"shop"}}, cred, &log)
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(h.Base, "var/backups/whacp/sites/example.com"))
	if len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}

// A restore that needs more room than the website's disk has is refused
// before anything changes (no safety backup, nothing extracted).
func TestRestoreChecksRoomForTheData(t *testing.T) {
	h, s := siteHost(t)
	cred, _, _ := h.Env.Credential("example")
	var log bytes.Buffer
	m, err := s.CreateSite(context.Background(), SiteRequest{Domain: "example.com", User: "example", Root: "/home/whacp/example.com", Files: true, Databases: []string{"shop"}}, cred, &log)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range m.Files {
		if f.Unpacked <= 0 {
			t.Fatalf("unpacked size not recorded: %+v", f)
		}
	}
	site := filepath.Join(h.Base, "home/whacp")
	fakeFree(t, func(p string) int64 {
		if strings.HasPrefix(p, site) {
			return Reserve + 1 // less than the archive unpacks to
		}
		return 1 << 40
	})
	calls := len(h.Runner.Calls)
	_, err = s.Restore(context.Background(), RestoreRequest{Domain: "example.com", User: "example", Root: "/home/whacp/example.com", ID: m.ID, Files: true, Safety: true}, cred, &log)
	if !errors.Is(err, ErrNoSpace) || !strings.Contains(err.Error(), "restored website files") {
		t.Fatalf("err = %v", err)
	}
	if len(h.Runner.Calls) != calls {
		t.Fatalf("commands ran: %v", h.Runner.Calls[calls:])
	}
	if list, _ := s.List("example.com"); len(list) != 1 {
		t.Fatalf("a safety backup was made: %d backups", len(list))
	}
}
