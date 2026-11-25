package swapmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

const mountExt4 = "36 35 98:0 / / rw,noatime - ext4 /dev/vda1 rw\n"

// swapHost is a test server whose swapon and swapoff keep /proc/swaps in step, as the kernel would.
func swapHost(t *testing.T, fs string, memAvailKB int, swaps string) (*testhost.Host, *actions.Registry) {
	t.Helper()
	minMB = 1
	t.Cleanup(func() { minMB = 256 })
	h := testhost.New(t)
	w := func(p, c string) {
		os.MkdirAll(filepath.Dir(filepath.Join(h.Base, p)), 0o755)
		if err := os.WriteFile(filepath.Join(h.Base, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("proc/self/mountinfo", "36 35 98:0 / / rw,noatime - "+fs+" /dev/vda1 rw\n")
	w("proc/meminfo", fmt.Sprintf("MemTotal:        2097152 kB\nMemAvailable:   %d kB\n", memAvailKB))
	w("proc/swaps", "Filename\t\t\t\tType\t\tSize\t\tUsed\t\tPriority\n"+swaps)
	w("proc/sys/vm/swappiness", "60\n")
	w("etc/fstab", "UUID=abc / ext4 defaults 0 1\n/dev/vda2 /boot ext4 defaults 0 2\n")
	h.Respond = func(id string, args []string) (string, error, bool) {
		switch id {
		case "swapon":
			fi, err := os.Stat(filepath.Join(h.Base, args[0]))
			if err != nil {
				return "swapon: " + args[0] + ": No such file", fmt.Errorf("exit status 255"), true
			}
			b, _ := os.ReadFile(filepath.Join(h.Base, "proc/swaps"))
			os.WriteFile(filepath.Join(h.Base, "proc/swaps"), []byte(string(b)+fmt.Sprintf("%s\tfile\t%d\t0\t-2\n", args[0], fi.Size()/1024)), 0o644)
			return "", nil, true
		case "swapoff":
			b, _ := os.ReadFile(filepath.Join(h.Base, "proc/swaps"))
			var keep []string
			for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
				if !strings.HasPrefix(l, args[0]+"\t") {
					keep = append(keep, l)
				}
			}
			os.WriteFile(filepath.Join(h.Base, "proc/swaps"), []byte(strings.Join(keep, "\n")+"\n"), 0o644)
			return "", nil, true
		}
		return "", nil, false
	}
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	return h, r
}

func call(t *testing.T, r *actions.Registry, name, params string) (any, error) {
	t.Helper()
	a, err := r.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return a.Handler(context.Background(), json.RawMessage(params))
}

func TestStatusReadsSwapMemoryAndTheFileSystem(t *testing.T) {
	_, r := swapHost(t, "ext4", 1500000, "/swap.img\tfile\t2097152\t1024\t-2\n/dev/vda3\tpartition\t1048576\t0\t-3\n")
	res, err := call(t, r, "swap.status", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	s := res.(Status)
	if len(s.Entries) != 2 || !s.Entries[0].Managed || s.Entries[1].Managed {
		t.Fatalf("entries = %+v (only the file is managed)", s.Entries)
	}
	if s.SwapTotal != 3*1024*1024*1024 || s.SwapUsed != 1024*1024 || s.Swappiness != 60 || s.FSType != "ext4" || !s.Supported || s.Target != "/swap.img" {
		t.Fatalf("status = %+v", s)
	}
}

func TestACreateMakesTheFileTurnsItOnAndPersistsIt(t *testing.T) {
	h, r := swapHost(t, "ext4", 1500000, "")
	if _, err := call(t, r, "swap.set", `{"size_mb":4}`); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(h.Base, "swapfile"))
	if err != nil || fi.Mode().Perm() != 0o600 || fi.Size() != 4<<20 {
		t.Fatalf("swap file: %v %v", fi, err)
	}
	for _, c := range []string{"mkswap /swapfile.new", "swapon /swapfile"} {
		if !h.Runner.Called(c) {
			t.Errorf("%s not run: %v", c, h.Runner.Calls)
		}
	}
	for i, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "swapon") && (len(h.Runner.Opts[i].Capabilities) != 1 || h.Runner.Opts[i].Capabilities[0] != "CAP_SYS_ADMIN") {
			t.Errorf("swapon ran with %v", h.Runner.Opts[i].Capabilities)
		}
	}
	fstab := h.Read("etc/fstab")
	if !strings.Contains(fstab, "/swapfile none swap sw 0 0") || !strings.Contains(fstab, "UUID=abc / ext4 defaults 0 1") || !strings.Contains(fstab, "/dev/vda2 /boot ext4") {
		t.Fatalf("fstab = %q", fstab)
	}
	if h.Exists("swapfile.new") || h.Exists("swapfile.old") {
		t.Fatal("temporary files were left behind")
	}
	// The same size again changes nothing.
	n := len(h.Runner.Calls)
	if _, err := call(t, r, "swap.set", `{"size_mb":4}`); err != nil || len(h.Runner.Calls) != n {
		t.Fatalf("a repeat changed something: %v %v", err, h.Runner.Calls[n:])
	}
}

func TestAResizeBuildsBesideTheOldFileAndSwitchesOver(t *testing.T) {
	h, r := swapHost(t, "ext4", 1500000, "/swapfile\tfile\t2048\t0\t-2\n")
	os.WriteFile(filepath.Join(h.Base, "swapfile"), make([]byte, 2<<20), 0o600)
	os.WriteFile(filepath.Join(h.Base, "etc/fstab"), []byte("UUID=abc / ext4 defaults 0 1\n/swapfile none swap sw 0 0\n"), 0o644)
	if _, err := call(t, r, "swap.set", `{"size_mb":6}`); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(h.Base, "swapfile")); fi == nil || fi.Size() != 6<<20 {
		t.Fatalf("size = %v", fi)
	}
	if h.Runner.Index("mkswap /swapfile.new") > h.Runner.Index("swapoff /swapfile") {
		t.Fatalf("the new file must be ready before the old swap is turned off: %v", h.Runner.Calls)
	}
	if strings.Count(h.Read("etc/fstab"), "/swapfile none swap") != 1 {
		t.Fatalf("fstab = %q", h.Read("etc/fstab"))
	}
}

func TestAFailedSwitchPutsThePreviousSwapBack(t *testing.T) {
	h, r := swapHost(t, "ext4", 1500000, "/swapfile\tfile\t2048\t0\t-2\n")
	os.WriteFile(filepath.Join(h.Base, "swapfile"), make([]byte, 2<<20), 0o600)
	fstab := "UUID=abc / ext4 defaults 0 1\n/swapfile none swap sw 0 0\n"
	os.WriteFile(filepath.Join(h.Base, "etc/fstab"), []byte(fstab), 0o644)
	first := true
	inner := h.Respond
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "swapon" && first {
			// The first swapon (the new file) fails; the one that puts the old file back works.
			first = false
			return "swapon: bad file", fmt.Errorf("exit status 255"), true
		}
		return inner(id, args)
	}
	_, err := call(t, r, "swap.set", `{"size_mb":6}`)
	if err == nil || !strings.Contains(err.Error(), "previous swap was put back") {
		t.Fatalf("err = %v", err)
	}
	if fi, _ := os.Stat(filepath.Join(h.Base, "swapfile")); fi == nil || fi.Size() != 2<<20 {
		t.Fatalf("the old file is not back: %v", fi)
	}
	if h.Exists("swapfile.new") || h.Exists("swapfile.old") || h.Read("etc/fstab") != fstab {
		t.Fatal("leftovers or an fstab change after a failure")
	}
	if !strings.Contains(h.Read("proc/swaps"), "/swapfile") {
		t.Fatalf("the old swap was not turned on again: %q", h.Read("proc/swaps"))
	}
}

func TestChangesAreRefusedWhenUnsafe(t *testing.T) {
	// A file system that cannot hold swap files.
	_, r := swapHost(t, "btrfs", 1500000, "")
	if _, err := call(t, r, "swap.set", `{"size_mb":4}`); err == nil {
		t.Fatal("btrfs accepted")
	}
	// Turning the old swap off would not fit in free memory: nothing is touched.
	h, r := swapHost(t, "ext4", 300000, "/swapfile\tfile\t2097152\t1572864\t-2\n")
	os.WriteFile(filepath.Join(h.Base, "swapfile"), make([]byte, 2<<20), 0o600)
	if _, err := call(t, r, "swap.set", `{"size_mb":6}`); err == nil || !strings.Contains(err.Error(), "not enough free memory") {
		t.Fatalf("err = %v", err)
	}
	if h.Runner.Called("swapoff /swapfile") || h.Exists("swapfile.new") {
		t.Fatalf("something was done before the refusal: %v", h.Runner.Calls)
	}
	if _, err := call(t, r, "swap.remove", `{}`); err == nil {
		t.Fatal("removed swap that memory could not take back")
	}
	// Sizes out of range.
	for _, bad := range []string{`{"size_mb":0}`, `{"size_mb":-5}`, `{"size_mb":99999999}`, `{"size_mb":"x"}`} {
		if _, err := call(t, r, "swap.set", bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestRemoveTurnsSwapOffDeletesTheFileAndItsFstabLineOnly(t *testing.T) {
	h, r := swapHost(t, "ext4", 1500000, "/swapfile\tfile\t2048\t0\t-2\n/dev/vda3\tpartition\t1024\t0\t-3\n")
	os.WriteFile(filepath.Join(h.Base, "swapfile"), make([]byte, 2<<20), 0o600)
	os.WriteFile(filepath.Join(h.Base, "etc/fstab"), []byte("UUID=abc / ext4 defaults 0 1\n/swapfile none swap sw 0 0\n/dev/vda3 none swap sw 0 0\n"), 0o644)
	if _, err := call(t, r, "swap.remove", `{}`); err != nil {
		t.Fatal(err)
	}
	if h.Exists("swapfile") || !h.Runner.Called("swapoff /swapfile") {
		t.Fatalf("file or swapoff: %v", h.Runner.Calls)
	}
	f := h.Read("etc/fstab")
	if strings.Contains(f, "/swapfile") || !strings.Contains(f, "/dev/vda3 none swap sw 0 0") || !strings.Contains(f, "UUID=abc") {
		t.Fatalf("fstab = %q", f)
	}
	if h.Runner.Called("swapoff /dev/vda3") {
		t.Fatal("a swap partition was touched")
	}
	if _, err := call(t, r, "swap.remove", `{}`); err == nil {
		t.Fatal("a second remove should say there is nothing to remove")
	}
}

func TestFstabEditsKeepOtherLines(t *testing.T) {
	in := "# comment\nUUID=a / ext4 defaults 0 1\n#/swapfile none swap sw 0 0\n/swapfile none swap sw 0 0\n/swapfile none swap sw 0 0\n"
	out := fstabWith(in, "/swapfile", false)
	if strings.Contains(strings.ReplaceAll(out, "#/swapfile", ""), "/swapfile") || !strings.Contains(out, "# comment") || !strings.Contains(out, "#/swapfile none swap") {
		t.Fatalf("out = %q", out)
	}
	if got := fstabWith("UUID=a / ext4 defaults 0 1\n", "/swap.img", true); got != "UUID=a / ext4 defaults 0 1\n/swap.img none swap sw 0 0\n" {
		t.Fatalf("got %q", got)
	}
	if !fstabHas(in, "/swapfile") || fstabHas("#/swapfile none swap sw 0 0\n", "/swapfile") {
		t.Fatal("fstabHas")
	}
}
