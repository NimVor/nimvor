package security

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

func platformDetect(env *host.Env) platform.Platform { return platform.Detect(env.Root) }

func writeSysctl(t *testing.T, base, key, v string) {
	p := filepath.Join(base, "proc/sys", strings.ReplaceAll(key, ".", "/"))
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(v+"\n"), 0o644)
}

// The recommended kernel values are written to WhaCP's file and loaded
// with sysctl -p (with the capabilities they need); undo puts the earlier
// values back. A router keeps sending redirects.
func TestFixKernel(t *testing.T) {
	h, s := setup(t, "")
	env := s.env
	env.Cfg.ProcDir = "/proc"
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, fixPaths()...)
	for k, v := range map[string]string{"kernel.dmesg_restrict": "0", "fs.suid_dumpable": "2", "net.ipv4.conf.all.send_redirects": "1", "net.ipv4.ip_forward": "1", "kernel.yama.ptrace_scope": "0", "kernel.randomize_va_space": "2"} {
		writeSysctl(t, h.Base, k, v)
	}
	ctx := context.Background()
	changed, err := FixKernel(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(changed, ",") != "kernel.dmesg_restrict,fs.suid_dumpable,kernel.yama.ptrace_scope" {
		t.Fatalf("changed %v", changed)
	}
	b := h.Read(strings.TrimPrefix(SysctlFile, "/"))
	if !strings.Contains(b, "fs.suid_dumpable = 0\nkernel.dmesg_restrict = 1\nkernel.yama.ptrace_scope = 1\n") || strings.Contains(b, "send_redirects") {
		t.Fatalf("file:\n%s", b)
	}
	if !h.Runner.Called("sysctl -p " + SysctlFile) {
		t.Fatalf("calls %v", h.Runner.Calls)
	}
	o := h.Runner.Opts[len(h.Runner.Opts)-1]
	if strings.Join(o.Capabilities, " ") != "CAP_SYS_ADMIN CAP_SYS_PTRACE CAP_NET_ADMIN" || o.As != nil {
		t.Errorf("sysctl opts %+v", o)
	}
	c := sysctlCheck(env)
	if c.Fix != "apply" || strings.Contains(c.Detail, "send_redirects") { // the fake /proc did not change; a router keeps its redirects
		t.Errorf("check %+v", c)
	}
	if err := UndoKernel(ctx, env); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sysctl -w fs.suid_dumpable=2", "sysctl -w kernel.dmesg_restrict=0", "sysctl -w kernel.yama.ptrace_scope=0"} {
		if !h.Runner.Called(want) {
			t.Errorf("undo did not run %q", want)
		}
	}
	if h.Exists(strings.TrimPrefix(SysctlFile, "/")) {
		t.Error("file kept after undo")
	}
	// The kernel refusing a value: nothing kept.
	h.Fail["sysctl -p"] = "sysctl: permission denied"
	if _, err := FixKernel(ctx, env); err == nil || h.Exists(strings.TrimPrefix(SysctlFile, "/")) {
		t.Fatalf("refused settings kept: %v", err)
	}
}

func TestHideProcesses(t *testing.T) {
	h, s := setup(t, "")
	env := s.env
	os.WriteFile(filepath.Join(h.Base, "etc/fstab"), []byte("UUID=abc / ext4 defaults 0 1\n"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "etc/group"), []byte("root:x:0:\nwhacp-proc:x:991:\n"), 0o644)
	ctx := context.Background()
	if err := HideProcesses(ctx, env, true); err != nil {
		t.Fatal(err)
	}
	fstab := h.Read("etc/fstab")
	if fstab != "UUID=abc / ext4 defaults 0 1\n"+fstabMark+"\nproc /proc proc defaults,hidepid=2,gid=991 0 0\n" {
		t.Fatalf("fstab:\n%s", fstab)
	}
	if !h.Runner.Called("mount -o remount,hidepid=2,gid=991 /proc") || !ProcHidden(env) {
		t.Fatalf("calls %v", h.Runner.Calls)
	}
	if err := HideProcesses(ctx, env, true); err != nil || strings.Count(h.Read("etc/fstab"), "/proc") != 1 {
		t.Fatalf("twice: %v\n%s", err, h.Read("etc/fstab"))
	}
	if err := HideProcesses(ctx, env, false); err != nil {
		t.Fatal(err)
	}
	if h.Read("etc/fstab") != "UUID=abc / ext4 defaults 0 1\n" || !h.Runner.Called("mount -o remount,hidepid=0,gid=0 /proc") {
		t.Fatalf("off:\n%s %v", h.Read("etc/fstab"), h.Runner.Calls)
	}
	// Someone else's /proc entry is not replaced.
	os.WriteFile(filepath.Join(h.Base, "etc/fstab"), []byte("proc /proc proc defaults 0 0\n"), 0o644)
	if err := HideProcesses(ctx, env, true); err == nil {
		t.Fatal("replaced an existing /proc entry")
	}
	// The remount failing: fstab is put back.
	os.WriteFile(filepath.Join(h.Base, "etc/fstab"), []byte("UUID=abc / ext4 defaults 0 1\n"), 0o644)
	h.Fail["mount"] = "mount: permission denied"
	if err := HideProcesses(ctx, env, true); err == nil || h.Read("etc/fstab") != "UUID=abc / ext4 defaults 0 1\n" {
		t.Fatalf("kept after a failed remount: %v\n%s", err, h.Read("etc/fstab"))
	}
}

// On Debian, automatic updates are switched in 20auto-upgrades (the
// package is installed first when missing).
func TestAutoUpdatesDebian(t *testing.T) {
	h, s := setup(t, "")
	env := s.env
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, fixPaths()...)
	os.MkdirAll(filepath.Join(h.Base, "etc/apt/apt.conf.d"), 0o755)
	if err := SetAutoUpdates(context.Background(), env, io.Discard, true); err != nil {
		t.Fatal(err)
	}
	b := h.Read("etc/apt/apt.conf.d/20auto-upgrades")
	if !strings.Contains(b, `APT::Periodic::Unattended-Upgrade "1";`) || !AutoUpdatesManaged(env) {
		t.Fatalf("file:\n%s", b)
	}
	if err := SetAutoUpdates(context.Background(), env, io.Discard, false); err != nil {
		t.Fatal(err)
	}
	if b := h.Read("etc/apt/apt.conf.d/20auto-upgrades"); !strings.Contains(b, `Unattended-Upgrade "0"`) || AutoUpdatesManaged(env) {
		t.Fatalf("off:\n%s", b)
	}
}

// Only these privileged command lines are accepted.
func TestPrivilegedArgs(t *testing.T) {
	var r execx.SimRunner
	caps := execx.Opts{Capabilities: []string{"CAP_SYS_ADMIN"}}
	for _, args := range [][]string{
		{"-p", "/etc/sysctl.conf"},
		{"-w", "kernel.core_pattern=|/tmp/x"},
		{"-w", "kernel.modprobe=/tmp/x"},
		{"--system"},
	} {
		if _, err := r.RunWith(context.Background(), "sysctl", caps, args...); err == nil {
			t.Errorf("sysctl %v allowed", args)
		}
	}
	for _, args := range [][]string{
		{"-o", "remount,exec", "/tmp"},
		{"-o", "remount,hidepid=2,suid", "/proc"},
		{"/dev/sdb1", "/mnt"},
		{"-o", "remount,hidepid=2", "/proc/../etc"},
	} {
		if _, err := r.RunWith(context.Background(), "mount", caps, args...); err == nil {
			t.Errorf("mount %v allowed", args)
		}
	}
}

// On RHEL the install timer is started by its fixed name with WhaCP's
// security-only configuration.
func TestAutoUpdatesRHEL(t *testing.T) {
	h, s := setup(t, "")
	os.WriteFile(filepath.Join(h.Base, "etc/os-release"), []byte("ID=\"rocky\"\nID_LIKE=\"rhel centos fedora\"\nVERSION_ID=\"9.6\"\n"), 0o644)
	env := s.env
	env.Platform = platformDetect(env)
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, fixPaths()...)
	if err := SetAutoUpdates(context.Background(), env, io.Discard, true); err != nil {
		t.Fatal(err)
	}
	if !h.Runner.Called("systemctl enable --now -- dnf-automatic-install.timer") {
		t.Fatalf("calls %v", h.Runner.Calls)
	}
	if b := h.Read("etc/whacp/dnf-automatic.conf"); !strings.Contains(b, "upgrade_type = security") || !strings.Contains(b, "reboot = never") {
		t.Errorf("conf:\n%s", b)
	}
	if b := h.Read("etc/systemd/system/dnf-automatic-install.service.d/whacp.conf"); !strings.Contains(b, "ExecStart=\nExecStart=/usr/bin/dnf-automatic /etc/whacp/dnf-automatic.conf --timer --installupdates") {
		t.Errorf("drop-in:\n%s", b)
	}
	if err := SetAutoUpdates(context.Background(), env, io.Discard, false); err != nil {
		t.Fatal(err)
	}
	if !h.Runner.Called("systemctl disable --now -- dnf-automatic-install.timer") || h.Exists("etc/whacp/dnf-automatic.conf") {
		t.Errorf("off: %v", h.Runner.Calls)
	}
}
