package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

const mountinfo = `22 1 252:1 / / rw,relatime shared:1 - ext4 /dev/vda1 rw,errors=remount-ro
30 22 0:26 / /proc rw,nosuid shared:12 - proc proc rw
41 22 252:2 / /home\040data rw,relatime shared:30 - xfs /dev/vdb rw,attr2
`

func TestFindMountAndOptions(t *testing.T) {
	for path, want := range map[string]string{"/home/whacp": "/", "/home data/x": "/home data", "/home data": "/home data", "/proc/1": "/proc", "/homework": "/"} {
		m, ok := FindMount(mountinfo, path)
		if !ok || m.Point != want {
			t.Errorf("%s: %+v", path, m)
		}
	}
	m, _ := FindMount(mountinfo, "/home/whacp")
	if m.FSType != "ext4" || m.Source != "/dev/vda1" || !HasOption(m.Options, "errors") || HasOption(m.Options, "usrquota") {
		t.Errorf("%+v", m)
	}
}

func TestAddFstabOptions(t *testing.T) {
	fstab := "# /etc/fstab\nUUID=abc  /          ext4    errors=remount-ro 0       1\n/dev/vdb /home\\040data xfs defaults 0 0\nLABEL=x /srv ext4\n"
	out, ok := AddFstabOptions(fstab, "/", Ext4Options)
	if !ok || out != "# /etc/fstab\nUUID=abc  /          ext4    errors=remount-ro,usrjquota=aquota.user,jqfmt=vfsv1 0       1\n/dev/vdb /home\\040data xfs defaults 0 0\nLABEL=x /srv ext4\n" {
		t.Fatalf("%v\n%s", ok, out)
	}
	again, _ := AddFstabOptions(out, "/", Ext4Options)
	if again != out {
		t.Fatalf("options added twice:\n%s", again)
	}
	if out, _ := AddFstabOptions(fstab, "/home data", XFSOptions); !strings.Contains(out, "/dev/vdb /home\\040data xfs defaults,uquota 0 0\n") {
		t.Fatalf("escaped mount point:\n%s", out)
	}
	if out, _ := AddFstabOptions(fstab, "/srv", XFSOptions); !strings.Contains(out, "LABEL=x /srv ext4\tdefaults,uquota\n") {
		t.Fatalf("no options field:\n%s", out)
	}
	if _, ok := AddFstabOptions(fstab, "/var", XFSOptions); ok {
		t.Fatal("found an entry that is not there")
	}
}

func TestParsers(t *testing.T) {
	rep := `*** Report for user quotas on device /dev/vda1
Block grace time: 7days; Inode grace time: 7days
                        Block limits                File limits
User            used    soft    hard  grace    used  soft  hard  grace
----------------------------------------------------------------------
#0        --  812345       0       0      0   45678     0     0      0
#1500     +-  102400       0  102400      0     321     0     0      0
#1501     --      12       0       0      0       3     0     0      0
`
	got := ParseRepquota(rep)
	if len(got) != 3 || got[1500] != (Usage{UsedKB: 102400, HardKB: 102400}) || got[1501].HardKB != 0 {
		t.Fatalf("%+v", got)
	}
	for out, want := range map[string][2]bool{
		"user quota on / (/dev/vda1) is on\n":                    {true, true},
		"user quota on / (/dev/mapper/r) is on (enforced)\n":     {true, true},
		"user quota on /home (/dev/vdb) is on (accounting)\n":    {false, true},
		"user quota on / (/dev/vda1) is off\n":                   {false, true},
		"quotaon: Cannot find mountpoint for device /dev/vda1\n": {false, false},
	} {
		on, ok := ParseQuotaon(out)
		if on != want[0] || ok != want[1] {
			t.Errorf("%q: %v %v", out, on, ok)
		}
	}
}

// newHost: a test machine with the websites on "/" (fstype), quotaon -p
// answering from *on, and the commands recorded.
func newHost(t *testing.T, fstype string, on *bool) *testhost.Host {
	h := testhost.New(t)
	os.MkdirAll(filepath.Join(h.Base, "proc/self"), 0o755)
	os.MkdirAll(filepath.Join(h.Base, "proc/sys/kernel/random"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "proc/self/mountinfo"), []byte(fmt.Sprintf("22 1 252:1 / / rw,relatime shared:1 - %s /dev/vda1 rw\n", fstype)), 0o644)
	os.WriteFile(filepath.Join(h.Base, "proc/sys/kernel/random/boot_id"), []byte("boot-1\n"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "proc/sys/kernel/osrelease"), []byte("6.8.0-1-generic\n"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "etc/fstab"), []byte("UUID=abc / "+fstype+" defaults 0 1\n"), 0o644)
	f, _ := os.OpenFile(filepath.Join(h.Base, "etc/passwd"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("web1:x:1600:1600::/home/whacp/a.test:/usr/sbin/nologin\ndaemon:x:1:1::/:/usr/sbin/nologin\n")
	f.Close()
	h.Respond = func(id string, args []string) (string, error, bool) {
		switch {
		case id == "quotaon" && args[0] == "-p":
			state := "off"
			if *on {
				state = "on"
			}
			return "user quota on / (/dev/vda1) is " + state + "\n", nil, true
		case id == "quotaon":
			*on = true
		case id == "repquota":
			return "#1600     --  2048  0  4096  0  10 0 0 0\n", nil, true
		}
		return "", nil, false
	}
	return h
}

// Every quota command went through the privileged helper with only what it needs.
func checkCaps(t *testing.T, h *testhost.Host) {
	t.Helper()
	for i, c := range h.Runner.Calls {
		id, _, _ := strings.Cut(c, " ")
		caps := h.Runner.Opts[i].Capabilities
		switch id {
		case "quotacheck":
			if !slices.Equal(caps, []string{"CAP_SYS_ADMIN", "CAP_DAC_READ_SEARCH"}) {
				t.Errorf("%s: %v", c, caps)
			}
		case "setquota", "repquota", "quotaon", "mount":
			if !slices.Equal(caps, SysAdmin) {
				t.Errorf("%s: %v", c, caps)
			}
		default:
			if len(caps) > 0 {
				t.Errorf("%s ran with %v", c, caps)
			}
		}
	}
}

func TestEnableExt4(t *testing.T) {
	on := false
	h := newHost(t, "ext4", &on)
	// The kernel has the quota format module.
	os.MkdirAll(filepath.Join(h.Base, "lib/modules/6.8.0-1-generic"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "lib/modules/6.8.0-1-generic/modules.dep"), []byte("kernel/fs/quota/quota_v2.ko.zst: kernel/fs/quota/quota_tree.ko.zst\n"), 0o644)
	res, err := Enable(context.Background(), h.Env, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res["state"] != "on" {
		t.Fatalf("%v", res)
	}
	var steps []string
	for _, c := range h.Runner.Calls {
		if !strings.HasPrefix(c, "quotaon -p") {
			steps = append(steps, c)
		}
	}
	want := []string{"mount -o remount,usrjquota=aquota.user,jqfmt=vfsv1 /", "quotacheck -u -c -m -F vfsv1 /", "quotaon -u /"}
	if !slices.Equal(steps, want) {
		t.Fatalf("steps:\n%s", strings.Join(steps, "\n"))
	}
	if got := h.Read("/etc/fstab"); got != "UUID=abc / ext4 defaults,usrjquota=aquota.user,jqfmt=vfsv1 0 1\n" {
		t.Fatalf("fstab: %q", got)
	}
	checkCaps(t, h)
	// Again: nothing to do.
	h.Runner.Calls = nil
	if res, _ := Enable(context.Background(), h.Env, io.Discard); res["state"] != "on" || len(h.Runner.Calls) != 1 {
		t.Fatalf("%v %v", res, h.Runner.Calls)
	}
}

func TestEnableExt4FailureKeepsFstab(t *testing.T) {
	on := false
	h := newHost(t, "ext4", &on)
	os.MkdirAll(filepath.Join(h.Base, "sys/module/quota_v2"), 0o755)
	h.Fail["quotacheck"] = "quotacheck: Cannot create new quotafile"
	if _, err := Enable(context.Background(), h.Env, io.Discard); err == nil || !strings.Contains(err.Error(), "Counting") {
		t.Fatalf("%v", err)
	}
	if h.Read("/etc/fstab") != "UUID=abc / ext4 defaults 0 1\n" {
		t.Fatal("fstab changed after a failure")
	}
	if h.Runner.Called("quotaon -u /") {
		t.Fatal("quotas turned on after a failed count")
	}
}

func TestEnableExt4InstallsTheKernelModule(t *testing.T) {
	on := false
	h := newHost(t, "ext4", &on)
	Enable(context.Background(), h.Env, io.Discard)
	found := false
	for _, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "apt-get install") && strings.HasSuffix(c, "linux-modules-extra-6.8.0-1-generic") {
			found = true
		}
	}
	if !found {
		t.Fatalf("module package not installed:\n%s", strings.Join(h.Runner.Calls, "\n"))
	}
}

func TestEnableXFSRootNeedsAReboot(t *testing.T) {
	on := false
	h := newHost(t, "xfs", &on)
	res, err := Enable(context.Background(), h.Env, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res["state"] != "pending_reboot" || res["reboot_required"] != true {
		t.Fatalf("%v", res)
	}
	if !h.Runner.Called("grubby --update-kernel=ALL --args=rootflags=uquota") || h.Runner.Called("mount -o remount,uquota /") {
		t.Fatalf("%v", h.Runner.Calls)
	}
	if !strings.Contains(h.Read("/etc/fstab"), "defaults,uquota") {
		t.Fatal(h.Read("/etc/fstab"))
	}
	s, _ := Current(context.Background(), h.Env)
	if s.State != "pending_reboot" {
		t.Fatalf("%+v", s)
	}
	// After a reboot that did not turn them on: off, with a note.
	os.WriteFile(filepath.Join(h.Base, "proc/sys/kernel/random/boot_id"), []byte("boot-2\n"), 0o644)
	if s, _ := Current(context.Background(), h.Env); s.State != "off" || !strings.Contains(s.Note, "rootflags") {
		t.Fatalf("%+v", s)
	}
	checkCaps(t, h)
}

func call(t *testing.T, h *testhost.Host, name, params string) (map[string]any, error) {
	t.Helper()
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup(name)
	res, err := a.Handler(context.Background(), json.RawMessage(params))
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(res)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m, nil
}

func TestSetAndReport(t *testing.T) {
	on := false
	h := newHost(t, "ext4", &on)
	if _, err := call(t, h, "quota.set", `{"limits":{"web1":1024}}`); err == nil || !strings.Contains(err.Error(), "not on") {
		t.Fatalf("set while off: %v", err)
	}
	on = true
	res, err := call(t, h, "quota.set", `{"limits":{"web1":1048576,"gone":5}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Runner.Called("setquota -u web1 0 1048576 0 0 /") || fmt.Sprint(res["set"]) != "[web1]" || res["failed"].(map[string]any)["gone"] != "no such user" {
		t.Fatalf("%v %v", res, h.Runner.Calls)
	}
	for _, bad := range []string{`{"limits":{"daemon":1}}`, `{"limits":{"root":1}}`, `{"limits":{"web1":-1}}`, `{"limits":{"../x":1}}`, `{"limits":{"web1":1}, "x":1}`} {
		if _, err := call(t, h, "quota.set", bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	rep, err := call(t, h, "quota.report", `{"users":["web1"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if u := rep["users"].(map[string]any)["web1"].(map[string]any); u["used_kb"] != 2048.0 || u["hard_kb"] != 4096.0 {
		t.Fatalf("%v", rep)
	}
	checkCaps(t, h)
}

func TestUnsupportedFileSystem(t *testing.T) {
	on := false
	h := newHost(t, "btrfs", &on)
	if _, err := call(t, h, "quota.enable", `{}`); err == nil || !strings.Contains(err.Error(), "ext4 or XFS") {
		t.Fatalf("%v", err)
	}
	s, _ := call(t, h, "quota.status", `{}`)
	if s["state"] != "unsupported" || s["mount_point"] != "/" {
		t.Fatalf("%v", s)
	}
}
