package security

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/pkgs"
)

// Fixes the security overview applies on request, each with an undo.
//
// Kernel settings: the recommended values the audit checks go into
// SysctlFile and are applied with sysctl (as root with only the
// capabilities the settings need). The values in effect before are kept
// and put back by the undo. send_redirects is left alone on a server that
// routes for others (ip_forward on).
//
// Process visibility: /proc is mounted with hidepid=2 and gid=ProcGroup,
// in /etc/fstab (kept across reboots) and at once by a remount. Members of
// ProcGroup (polkit's account, monitoring tools an administrator adds)
// still see every process.

// SysctlFile holds the kernel settings WhaCP applied.
const SysctlFile = execx.SysctlFile

// ProcGroup may see every process while hidepid is on.
const ProcGroup = "whacp-proc"

// sysctlCaps are the capabilities writing the audited settings needs:
// kernel.* and fs.* (CAP_SYS_ADMIN), Yama (CAP_SYS_PTRACE), net.*
// (CAP_NET_ADMIN).
var sysctlCaps = []string{"CAP_SYS_ADMIN", "CAP_SYS_PTRACE", "CAP_NET_ADMIN"}

type fixState struct {
	// Kernel values before WhaCP changed them (key -> value).
	KernelBefore map[string]string `json:"kernel_before"`
}

func fixStatePath(env *host.Env) string {
	return filepath.Join(env.Cfg.StateDir, "security-fixes.json")
}

func loadFixState(env *host.Env) fixState {
	st := fixState{}
	if b, err := env.Root.ReadFile(fixStatePath(env)); err == nil {
		json.Unmarshal(b, &st)
	}
	if st.KernelBefore == nil {
		st.KernelBefore = map[string]string{}
	}
	return st
}

func readSysctl(env *host.Env, key string) (string, bool) {
	b, err := env.Root.ReadFile(filepath.Join(env.Cfg.ProcDir, "sys", strings.ReplaceAll(key, ".", "/")))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

var sysctlLineRe = regexp.MustCompile(`^\s*([a-z0-9_.]+)\s*=\s*(\S+)\s*$`)

func parseSysctlFile(b []byte) map[string]string {
	out := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		if m := sysctlLineRe.FindStringSubmatch(l); m != nil {
			out[m[1]] = m[2]
		}
	}
	return out
}

// FixKernel applies the recommended kernel settings that are not in
// effect, and returns the keys it changed.
func FixKernel(ctx context.Context, env *host.Env) ([]string, error) {
	cur, _ := env.Root.ReadFile(SysctlFile)
	want := parseSysctlFile(cur)
	st := loadFixState(env)
	forwarding, _ := readSysctl(env, "net.ipv4.ip_forward")
	var changed []string
	for _, s := range kernelSettings {
		v, ok := readSysctl(env, s.key)
		if !ok || s.want(v) {
			continue
		}
		if s.key == "net.ipv4.conf.all.send_redirects" && forwarding == "1" {
			continue // this server routes for others
		}
		value := s.value
		if s.key == "kernel.unprivileged_bpf_disabled" {
			value = "2" // 1 cannot be undone without a restart
		}
		if _, had := st.KernelBefore[s.key]; !had {
			st.KernelBefore[s.key] = v
		}
		want[s.key] = value
		changed = append(changed, s.key)
	}
	if len(changed) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# Managed by WhaCP (Security → Overview → Kernel security settings).\n# Undo it there; editing this file by hand is overwritten.\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", k, want[k])
	}
	state, _ := json.MarshalIndent(st, "", "  ")
	set := env.Changes("kernel-hardening")
	set.Write(fixStatePath(env), state, 0o600, agentfs.RootOwner)
	if !env.Root.Exists(filepath.Dir(SysctlFile)) {
		set.Mkdir(filepath.Dir(SysctlFile), 0o755, agentfs.RootOwner)
	}
	set.Write(SysctlFile, []byte(b.String()), 0o644, agentfs.RootOwner)
	applied, err := set.Apply()
	if err != nil {
		return nil, actions.Failed("The kernel settings were not saved.", err.Error())
	}
	if out, err := env.Runner.RunWith(ctx, "sysctl", execx.Opts{Capabilities: sysctlCaps}, "-p", SysctlFile); err != nil {
		applied.Rollback()
		restoreKernel(ctx, env, st.KernelBefore, changed)
		return nil, actions.Failed("The kernel did not accept the settings; nothing was changed.", host.Output(out))
	}
	return changed, nil
}

func restoreKernel(ctx context.Context, env *host.Env, before map[string]string, keys []string) []string {
	var failed []string
	for _, k := range keys {
		v, ok := before[k]
		if !ok {
			continue
		}
		if _, err := env.Runner.RunWith(ctx, "sysctl", execx.Opts{Capabilities: sysctlCaps}, "-w", k+"="+v); err != nil {
			failed = append(failed, k)
		}
	}
	return failed
}

// UndoKernel puts the earlier values back and removes SysctlFile.
func UndoKernel(ctx context.Context, env *host.Env) error {
	st := loadFixState(env)
	keys := make([]string, 0, len(st.KernelBefore))
	for k := range st.KernelBefore {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	failed := restoreKernel(ctx, env, st.KernelBefore, keys)
	set := env.Changes("kernel-hardening")
	if env.Root.Exists(SysctlFile) {
		set.Remove(SysctlFile)
	}
	st.KernelBefore = map[string]string{}
	state, _ := json.MarshalIndent(st, "", "  ")
	set.Write(fixStatePath(env), state, 0o600, agentfs.RootOwner)
	if _, err := set.Apply(); err != nil {
		return actions.Failed("The kernel settings file was not removed.", err.Error())
	}
	if len(failed) > 0 {
		return actions.Failed("Some earlier values could not be put back; they return at the next restart.", strings.Join(failed, ", "))
	}
	return nil
}

// KernelFixed reports whether WhaCP's kernel settings are in place.
func KernelFixed(env *host.Env) bool { return env.Root.Exists(SysctlFile) }

// Process visibility.

const fstabMark = "# WhaCP: process visibility (Security → Overview)"

func groupID(env *host.Env, name string) (int, bool) {
	b, err := env.Root.ReadFile("/etc/group")
	if err != nil {
		return 0, false
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(l, ":")
		if len(f) >= 3 && f[0] == name {
			if id, err := strconv.Atoi(f[2]); err == nil {
				return id, true
			}
		}
	}
	return 0, false
}

// procFstab returns fstab without WhaCP's /proc line, and whether another
// /proc entry (someone else's) is there.
func procFstab(b []byte) (rest []string, foreign bool) {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if l == fstabMark {
			i++ // and the entry after it
			continue
		}
		if f := strings.Fields(l); len(f) >= 3 && !strings.HasPrefix(f[0], "#") && f[1] == "/proc" {
			foreign = true
		}
		rest = append(rest, l)
	}
	if len(rest) == 1 && rest[0] == "" {
		rest = nil
	}
	return rest, foreign
}

// HideProcesses turns hidepid on (each account sees only its own
// processes) or off.
func HideProcesses(ctx context.Context, env *host.Env, on bool) error {
	cur, err := env.Root.ReadFile("/etc/fstab")
	if err != nil && !os.IsNotExist(err) {
		return actions.Failed("/etc/fstab could not be read.", err.Error())
	}
	rest, foreign := procFstab(cur)
	if on && foreign {
		return actions.Conflict("/etc/fstab already has its own /proc entry; add hidepid=2 to it by hand.")
	}
	gid := 0
	if on {
		if _, ok := groupID(env, ProcGroup); !ok {
			if out, err := env.Runner.Run(ctx, "groupadd", "--system", "--", ProcGroup); err != nil {
				return actions.Failed("The group "+ProcGroup+" could not be created.", host.Output(out))
			}
		}
		gid, _ = groupID(env, ProcGroup)
		if gid == 0 {
			return actions.Failed("The group "+ProcGroup+" was not found after creating it.", "")
		}
		// polkit decides who may manage services and log in; it must
		// still see the processes it asks about.
		for _, u := range []string{"polkitd", "polkit"} {
			if _, err := env.LookupUser(u); err == nil {
				env.Runner.Run(ctx, "gpasswd", "-a", u, ProcGroup)
			}
		}
	}
	next := rest
	opts := "hidepid=0,gid=0"
	if on {
		opts = fmt.Sprintf("hidepid=2,gid=%d", gid)
		next = append(next, fstabMark, "proc /proc proc defaults,"+opts+" 0 0")
	}
	body := strings.Join(next, "\n")
	if body != "" {
		body += "\n"
	}
	set := env.Changes("proc-hidepid")
	set.Write("/etc/fstab", []byte(body), 0o644, agentfs.Keep)
	applied, err := set.Apply()
	if err != nil {
		return actions.Failed("/etc/fstab was not changed.", err.Error())
	}
	if out, err := env.Runner.RunWith(ctx, "mount", execx.Opts{Capabilities: []string{"CAP_SYS_ADMIN"}}, "-o", "remount,"+opts, "/proc"); err != nil {
		applied.Rollback()
		return actions.Failed("/proc could not be mounted again with the new setting; nothing was changed.", host.Output(out))
	}
	return nil
}

// ProcHidden reports whether WhaCP's /proc entry is in /etc/fstab.
func ProcHidden(env *host.Env) bool {
	b, _ := env.Root.ReadFile("/etc/fstab")
	return strings.Contains(string(b), fstabMark)
}

// Automatic security updates.
//
// Debian/Ubuntu: unattended-upgrades (installs only the security archive
// by default), turned on in 20auto-upgrades. RHEL: dnf-automatic's install
// timer, with WhaCP's own configuration (security updates only, no
// reboot) through a drop-in, so the package's automatic.conf stays as it
// is.

const (
	aptAutoFile   = "/etc/apt/apt.conf.d/20auto-upgrades"
	dnfAutoConf   = "/etc/whacp/dnf-automatic.conf"
	dnfAutoUnit   = "dnf-automatic-install.service"
	dnfAutoTimer  = "dnf-automatic-install.timer"
	dnfAutoDropIn = "/etc/systemd/system/" + dnfAutoUnit + ".d/whacp.conf"
	autoMark      = "Managed by WhaCP (Security → Overview → Automatic security updates)"
)

func fixPaths() []string {
	return []string{SysctlFile, filepath.Dir(SysctlFile), aptAutoFile, dnfAutoConf, filepath.Dir(dnfAutoConf), filepath.Dir(dnfAutoDropIn), dnfAutoDropIn}
}

// AutoUpdatesManaged reports whether WhaCP turned automatic updates on.
func AutoUpdatesManaged(env *host.Env) bool {
	f := aptAutoFile
	if env.Platform.PackageManager() == "dnf" {
		f = dnfAutoDropIn
	}
	b, _ := env.Root.ReadFile(f)
	return strings.Contains(string(b), autoMark)
}

// SetAutoUpdates turns automatic security updates on (installing the
// package when needed; log receives the package manager's output) or off.
func SetAutoUpdates(ctx context.Context, env *host.Env, log io.Writer, on bool) error {
	set := env.Changes("auto-updates")
	if env.Platform.PackageManager() != "dnf" {
		if on && len(pkgs.InstalledNative(ctx, env, []string{"unattended-upgrades"})) == 0 {
			if err := pkgs.InstallNative(ctx, env, log, []string{"unattended-upgrades"}); err != nil {
				return err
			}
		}
		v := "0"
		head := ""
		if on {
			v, head = "1", "// "+autoMark+".\n"
		}
		set.Write(aptAutoFile, []byte(head+"APT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \""+v+"\";\n"), 0o644, agentfs.RootOwner)
		if _, err := set.Apply(); err != nil {
			return fmt.Errorf("%s was not written: %w", aptAutoFile, err)
		}
		fmt.Fprintf(log, "Automatic security updates are %s (%s).\n", map[bool]string{true: "on", false: "off"}[on], aptAutoFile)
		return nil
	}
	// The timer by its fixed name (env.Systemctl takes services only).
	timer := func(verb ...string) error {
		out, err := env.Runner.Run(ctx, "systemctl", append(verb, "--", dnfAutoTimer)...)
		if err != nil {
			return fmt.Errorf("systemctl %s %s failed: %s", strings.Join(verb, " "), dnfAutoTimer, host.Output(out))
		}
		return nil
	}
	if !on {
		timer("disable", "--now")
		for _, f := range []string{dnfAutoDropIn, dnfAutoConf} {
			if env.Root.Exists(f) {
				set.Remove(f)
			}
		}
		if _, err := set.Apply(); err != nil {
			return err
		}
		fmt.Fprintln(log, "Automatic security updates are off.")
		return env.DaemonReload(ctx)
	}
	if len(pkgs.InstalledNative(ctx, env, []string{"dnf-automatic"})) == 0 {
		if err := pkgs.InstallNative(ctx, env, log, []string{"dnf-automatic"}); err != nil {
			return err
		}
	}
	conf := "# " + autoMark + ".\n[commands]\nupgrade_type = security\nrandom_sleep = 0\nnetwork_online_timeout = 60\ndownload_updates = yes\napply_updates = yes\nreboot = never\n\n[emitters]\nemit_via = stdio\n\n[base]\ndebuglevel = 1\n"
	dropIn := "# " + autoMark + ".\n[Service]\nExecStart=\nExecStart=/usr/bin/dnf-automatic " + dnfAutoConf + " --timer --installupdates\n"
	set.Mkdir(filepath.Dir(dnfAutoConf), 0o755, agentfs.RootOwner)
	set.Write(dnfAutoConf, []byte(conf), 0o644, agentfs.RootOwner)
	set.Mkdir(filepath.Dir(dnfAutoDropIn), 0o755, agentfs.RootOwner)
	set.Write(dnfAutoDropIn, []byte(dropIn), 0o644, agentfs.RootOwner)
	applied, err := set.Apply()
	if err != nil {
		return err
	}
	err = env.DaemonReload(ctx)
	if err == nil {
		err = timer("enable", "--now")
	}
	if err != nil {
		applied.Rollback()
		env.DaemonReload(ctx)
		return err
	}
	fmt.Fprintf(log, "Automatic security updates are on: %s installs security updates daily (%s).\n", dnfAutoTimer, dnfAutoConf)
	return nil
}
