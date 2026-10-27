// Package quota enforces hosting plans' disk space with Linux user quotas
// on the file system that holds the websites.
//
// Setting and reading other users' quotas, turning quotas on and
// remounting need CAP_SYS_ADMIN, which the agent does not hold: those few
// commands (setquota, repquota, quotaon, quotacheck, mount) run as root in
// a transient unit started by PID 1 whose capability bounding set is only
// what they need (execx.Opts.Capabilities).
//
// Turning quotas on is an administrator's one-time action (quota.enable):
//   - ext4: the file system is remounted with journaled user quotas, usage
//     is counted (quotacheck), quotas are turned on, and /etc/fstab gets the
//     same options so they apply after a reboot;
//   - XFS: quota accounting can only start at mount time, so /etc/fstab
//     (and for the root file system the kernel's rootflags) get "uquota"
//     and quotas start after the next reboot.
//
// Other file systems are not supported.
package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// SysAdmin is what setquota, repquota, quotaon and mount need.
var SysAdmin = []string{"CAP_SYS_ADMIN"}

// Mount is the file system holding a path.
type Mount struct {
	Point   string `json:"mount_point"`
	Source  string `json:"device"`
	FSType  string `json:"fstype"`
	Options string `json:"options"` // mount and super block options
}

func unescape(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}

// FindMount returns the mount (from /proc/self/mountinfo) that holds path:
// the longest mount point containing it, the last one mounted there.
func FindMount(mountinfo, path string) (Mount, bool) {
	var best Mount
	found := false
	for _, line := range strings.Split(mountinfo, "\n") {
		pre, post, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		f, g := strings.Fields(pre), strings.Fields(post)
		if len(f) < 6 || len(g) < 3 {
			continue
		}
		mp := unescape(f[4])
		if !(path == mp || mp == "/" || strings.HasPrefix(path, mp+"/")) {
			continue
		}
		if found && len(mp) < len(best.Point) {
			continue
		}
		best = Mount{Point: mp, Source: unescape(g[1]), FSType: g[0], Options: f[5] + "," + g[2]}
		found = true
	}
	return best, found
}

// HasOption reports whether a comma-separated option list has key (with
// or without a value).
func HasOption(opts, key string) bool {
	for _, o := range strings.Split(opts, ",") {
		if o == key || strings.HasPrefix(o, key+"=") {
			return true
		}
	}
	return false
}

// Options NimVor turns quotas on with.
var (
	Ext4Options = []string{"usrjquota=aquota.user", "jqfmt=vfsv1"}
	XFSOptions  = []string{"uquota"}
)

// AddFstabOptions adds opts (those whose key is missing) to the options of
// the /etc/fstab entry for mountPoint, keeping every other byte. It reports
// whether the entry was found.
func AddFstabOptions(fstab, mountPoint string, opts []string) (string, bool) {
	lines := strings.SplitAfter(fstab, "\n")
	found := false
	for i, line := range lines {
		body := strings.TrimRight(line, "\n")
		if t := strings.TrimSpace(body); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		// Field positions in the line, so spacing is kept.
		type span struct{ start, end int }
		var fields []span
		inField := false
		for j, c := range body {
			ws := c == ' ' || c == '\t'
			switch {
			case !ws && !inField:
				fields = append(fields, span{j, j})
				inField = true
			case ws && inField:
				fields[len(fields)-1].end = j
				inField = false
			}
		}
		if inField {
			fields[len(fields)-1].end = len(body)
		}
		if len(fields) < 3 || unescape(body[fields[1].start:fields[1].end]) != mountPoint {
			continue
		}
		found = true
		cur := "defaults"
		if len(fields) >= 4 {
			cur = body[fields[3].start:fields[3].end]
		}
		next := cur
		for _, o := range opts {
			key, _, _ := strings.Cut(o, "=")
			if !HasOption(next, key) {
				next += "," + o
			}
		}
		if len(fields) >= 4 {
			body = body[:fields[3].start] + next + body[fields[3].end:]
		} else {
			body += "\t" + next
		}
		lines[i] = body + strings.TrimPrefix(line, strings.TrimRight(line, "\n"))
	}
	return strings.Join(lines, ""), found
}

// Usage is one user's quota.
type Usage struct {
	UsedKB int64 `json:"used_kb"`
	HardKB int64 `json:"hard_kb"` // 0: no limit
}

// ParseRepquota reads "repquota -u -n -p" output: uid → usage (1 KiB blocks).
func ParseRepquota(out string) map[int]Usage {
	res := map[int]Usage{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || !strings.HasPrefix(f[0], "#") || len(f[1]) != 2 {
			continue
		}
		uid, err := strconv.Atoi(f[0][1:])
		used, err2 := strconv.ParseInt(f[2], 10, 64)
		hard, err3 := strconv.ParseInt(f[4], 10, 64)
		if err != nil || err2 != nil || err3 != nil {
			continue
		}
		res[uid] = Usage{UsedKB: used, HardKB: hard}
	}
	return res
}

// ParseQuotaon reads "quotaon -p -u": whether user quotas are on
// (accounting alone, XFS's uqnoenforce, does not count).
func ParseQuotaon(out string) (on bool, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "user quota") {
			continue
		}
		switch {
		case strings.Contains(line, " is on") && !strings.Contains(line, "accounting"):
			return true, true
		case strings.Contains(line, " is on"), strings.Contains(line, " is off"):
			return false, true
		}
	}
	return false, false
}

// Status of quotas on the websites' file system.
type Status struct {
	Path string `json:"path"`
	Mount
	// State: on, off, pending_reboot (turned on for the next boot) or
	// unsupported (file system type).
	State string `json:"state"`
	Tools bool   `json:"tools"`
	Note  string `json:"note,omitempty"`
}

type state struct {
	PendingBoot string `json:"pending_boot,omitempty"` // boot id when a reboot was asked for
	MountPoint  string `json:"mount_point,omitempty"`
}

func statePath(e *host.Env) string { return filepath.Join(e.Cfg.StateDir, "quota.json") }

func loadState(e *host.Env) state {
	var st state
	if b, err := e.Root.ReadFile(statePath(e)); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

func bootID(e *host.Env) string {
	b, _ := e.Root.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(b))
}

func supported(fstype string) bool { return fstype == "ext4" || fstype == "xfs" }

func mountOf(e *host.Env) (Mount, error) {
	path := e.Cfg.SitesRoot
	if e.Cfg.RootDir == "" || e.Cfg.RootDir == "/" {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			path = real // e.g. /home as a link to /var/home
		}
	}
	info, err := e.Root.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return Mount{}, err
	}
	m, ok := FindMount(string(info), path)
	if !ok {
		return Mount{}, fmt.Errorf("no mount holds %s", path)
	}
	return m, nil
}

func run(ctx context.Context, e *host.Env, log io.Writer, caps []string, id string, args ...string) (string, error) {
	out, err := e.Runner.RunWith(ctx, id, execx.Opts{Capabilities: caps, Output: log, Timeout: -1}, args...)
	return string(out), err
}

// Current reports the quota state of the websites' file system.
func Current(ctx context.Context, e *host.Env) (Status, error) {
	m, err := mountOf(e)
	if err != nil {
		return Status{}, err
	}
	s := Status{Path: e.Cfg.SitesRoot, Mount: m, Tools: e.Runner.Available("setquota") && e.Runner.Available("quotaon")}
	if !supported(m.FSType) {
		s.State = "unsupported"
		s.Note = "Disk quotas need ext4 or XFS; " + m.Point + " is " + m.FSType + "."
		return s, nil
	}
	s.State = "off"
	if s.Tools {
		out, _ := run(ctx, e, nil, SysAdmin, "quotaon", "-p", "-u", m.Point)
		if on, ok := ParseQuotaon(out); ok && on {
			s.State = "on"
			return s, nil
		}
	}
	if st := loadState(e); st.PendingBoot != "" && st.MountPoint == m.Point {
		if st.PendingBoot == bootID(e) {
			s.State = "pending_reboot"
			s.Note = "Quotas start after the next reboot."
		} else {
			s.Note = "Quotas were set up for the next boot but are still off; check that the kernel started with rootflags=uquota (root file system) or that /etc/fstab has uquota."
		}
	}
	return s, nil
}

// quotaModule reports whether the kernel can use the vfsv1 quota format
// (ext4's journaled quota files): loaded, built in or installed.
func quotaModule(e *host.Env) bool {
	if e.Root.Exists("/sys/module/quota_v2") {
		return true
	}
	rel, _ := e.Root.ReadFile("/proc/sys/kernel/osrelease")
	dir := filepath.Join("/lib/modules", strings.TrimSpace(string(rel)))
	if b, err := e.Root.ReadFile(filepath.Join(dir, "modules.builtin")); err == nil && strings.Contains(string(b), "/quota_v2.ko") {
		return true
	}
	if b, err := e.Root.ReadFile(filepath.Join(dir, "modules.dep")); err == nil && strings.Contains(string(b), "/quota_v2.ko") {
		return true
	}
	return false
}

// Enable turns user quotas on for the websites' file system (see the
// package comment). It logs each step.
func Enable(ctx context.Context, e *host.Env, log io.Writer) (map[string]any, error) {
	s, err := Current(ctx, e)
	if err != nil {
		return nil, err
	}
	switch s.State {
	case "on":
		fmt.Fprintf(log, "User quotas are already on for %s.\n", s.Point)
		return map[string]any{"state": "on", "mount_point": s.Point}, nil
	case "unsupported":
		return nil, actions.Conflict(s.Note)
	case "pending_reboot":
		return map[string]any{"state": s.State, "mount_point": s.Point, "reboot_required": true}, nil
	}
	if !s.Tools {
		fmt.Fprintln(log, "Installing the quota tools…")
		if err := pkgs.Install(ctx, e, log, []string{"quota"}); err != nil {
			return nil, host.FailWithValidation("The quota tools could not be installed", err)
		}
	}
	fstab, _ := e.Root.ReadFile("/etc/fstab")
	if s.FSType == "xfs" {
		return enableXFS(ctx, e, log, s, string(fstab))
	}
	next, found := AddFstabOptions(string(fstab), s.Point, Ext4Options)
	if !found {
		return nil, actions.Conflict("/etc/fstab has no entry for " + s.Point + ", so quotas would not return after a reboot. Add one, or add " + strings.Join(Ext4Options, ",") + " to its options yourself.")
	}
	if !quotaModule(e) {
		if e.Platform.PackageManager() == "dnf" {
			return nil, actions.Conflict("The kernel has no quota_v2 module (needed for ext4 quota files). Install the kernel-modules package of the running kernel and try again.")
		}
		rel, _ := e.Root.ReadFile("/proc/sys/kernel/osrelease")
		p := "linux-modules-extra-" + strings.TrimSpace(string(rel))
		fmt.Fprintf(log, "The running kernel needs its quota modules: installing %s…\n", p)
		if err := pkgs.InstallNative(ctx, e, log, []string{p}); err != nil {
			return nil, host.FailWithValidation("The kernel's quota module is missing and "+p+" could not be installed", err)
		}
	}
	fmt.Fprintf(log, "Remounting %s with user quotas…\n", s.Point)
	if out, err := run(ctx, e, log, SysAdmin, "mount", "-o", "remount,"+strings.Join(Ext4Options, ","), s.Point); err != nil {
		return nil, actions.Failed("The file system could not be remounted with quotas; nothing was changed", out)
	}
	fmt.Fprintf(log, "Counting the files of every user on %s (this can take a while)…\n", s.Point)
	if out, err := run(ctx, e, log, []string{"CAP_SYS_ADMIN", "CAP_DAC_READ_SEARCH"}, "quotacheck", "-u", "-c", "-m", "-F", "vfsv1", s.Point); err != nil {
		return nil, actions.Failed("Counting disk usage failed; /etc/fstab was not changed, so quotas stay off after a reboot", out)
	}
	if out, err := run(ctx, e, log, SysAdmin, "quotaon", "-u", s.Point); err != nil {
		return nil, actions.Failed("Quotas could not be turned on; /etc/fstab was not changed", out)
	}
	set := e.Changes("quota-fstab")
	set.Write("/etc/fstab", []byte(next), 0o644, agentfs.RootOwner)
	if _, err := set.Commit(func() error { return nil }); err != nil {
		return nil, host.FailWithValidation("Quotas are on, but /etc/fstab could not be updated (they would be off after a reboot)", err)
	}
	fmt.Fprintf(log, "User quotas are on for %s, also after a reboot.\n", s.Point)
	return map[string]any{"state": "on", "mount_point": s.Point}, nil
}

func enableXFS(ctx context.Context, e *host.Env, log io.Writer, s Status, fstab string) (map[string]any, error) {
	next, found := AddFstabOptions(fstab, s.Point, XFSOptions)
	if !found {
		return nil, actions.Conflict("/etc/fstab has no entry for " + s.Point + ". Add uquota to its mount options and reboot.")
	}
	if s.Point == "/" {
		// The root file system is mounted before /etc/fstab is read.
		if !e.Runner.Available("grubby") {
			return nil, actions.Conflict("XFS quotas on the root file system need the kernel option rootflags=uquota. Add it to the boot loader configuration and reboot.")
		}
		fmt.Fprintln(log, "Adding rootflags=uquota to every installed kernel…")
		if out, err := e.Runner.RunWith(ctx, "grubby", execx.Opts{Output: log}, "--update-kernel=ALL", "--args=rootflags=uquota"); err != nil {
			return nil, actions.Failed("The kernel option could not be added", string(out))
		}
	}
	set := e.Changes("quota-fstab")
	if next != fstab {
		set.Write("/etc/fstab", []byte(next), 0o644, agentfs.RootOwner)
	}
	b, _ := json.Marshal(state{PendingBoot: bootID(e), MountPoint: s.Point})
	set.Write(statePath(e), b, 0o600, agentfs.RootOwner)
	if _, err := set.Commit(func() error { return nil }); err != nil {
		return nil, host.FailWithValidation("/etc/fstab could not be updated", err)
	}
	fmt.Fprintf(log, "XFS starts counting user quotas when it is mounted: reboot the server to turn them on for %s.\n", s.Point)
	return map[string]any{"state": "pending_reboot", "mount_point": s.Point, "reboot_required": true}, nil
}

// lookup validates website system users (no system accounts).
func lookup(e *host.Env, names []string) (map[string]int, error) {
	uids := map[string]int{}
	for _, u := range names {
		if err := validate.Username("users", u); err != nil {
			return nil, host.Invalid(err)
		}
		acct, err := e.LookupUser(u)
		if err != nil {
			continue // not created (yet)
		}
		if acct.UID < 1000 {
			return nil, actions.InvalidParams(u + " is a system account")
		}
		uids[u] = acct.UID
	}
	return uids, nil
}

func requireOn(ctx context.Context, e *host.Env) (Status, error) {
	s, err := Current(ctx, e)
	if err != nil {
		return s, err
	}
	if s.State != "on" {
		return s, actions.Conflict("Disk quotas are not on for " + s.Path + " (" + s.State + "). Turn them on in Server → Resource limits.")
	}
	return s, nil
}

// MaxKB is the largest limit accepted (4 PiB).
const MaxKB = 1 << 42

// Register adds the quota actions.
func Register(r *actions.Registry, e *host.Env) {

	r.Register(actions.Action{Name: "quota.status", Description: "Whether disk quotas are on for the file system holding the websites (ext4 or XFS), with its mount point, device and type.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := host.Decode(raw, &struct{}{}); err != nil {
			return nil, err
		}
		return Current(ctx, e)
	}})

	r.Register(actions.Action{Name: "quota.enable", Mutating: true, Description: "Start a background job turning user quotas on for the websites' file system: ext4 is remounted with journaled quotas, usage is counted and /etc/fstab updated; XFS gets uquota in /etc/fstab (and rootflags=uquota for the root file system) and needs a reboot. Installs the quota tools when missing.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := host.Decode(raw, &struct{}{}); err != nil {
			return nil, err
		}
		s, err := Current(ctx, e)
		if err != nil {
			return nil, err
		}
		if s.State == "unsupported" {
			return nil, actions.Conflict(s.Note)
		}
		j := e.Jobs.Start("quota.enable", "fs:"+s.Point, func(ctx context.Context, log io.Writer) (any, error) {
			return Enable(ctx, e, log)
		})
		return map[string]any{"job_id": j.ID, "status": j.Status}, nil
	}})

	r.Register(actions.Action{Name: "quota.report", Timeout: 2 * time.Minute, Description: "Disk use and limit (KiB) of website system users on the websites' file system, from the quota records (read-only).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Users []string `json:"users"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if len(in.Users) > 5000 {
			return nil, actions.InvalidParams("users: at most 5000")
		}
		uids, err := lookup(e, in.Users)
		if err != nil {
			return nil, err
		}
		s, err := requireOn(ctx, e)
		if err != nil {
			return nil, err
		}
		out, err := run(ctx, e, nil, SysAdmin, "repquota", "-u", "-n", "-p", s.Point)
		if err != nil {
			return nil, actions.Failed("The quota report could not be read", out)
		}
		byUID := ParseRepquota(out)
		users := map[string]Usage{}
		for u, uid := range uids {
			users[u] = byUID[uid] // no entry: nothing used, no limit
		}
		return map[string]any{"mount_point": s.Point, "users": users}, nil
	}})

	r.Register(actions.Action{Name: "quota.set", Mutating: true, Timeout: 2 * time.Minute, Description: "Set website system users' disk space limits (KiB, hard limit; 0 removes the limit) on the websites' file system. Writing beyond the limit fails with \"Disk quota exceeded\".", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Limits map[string]int64 `json:"limits"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if len(in.Limits) > 5000 {
			return nil, actions.InvalidParams("limits: at most 5000 users")
		}
		names := make([]string, 0, len(in.Limits))
		for u, kb := range in.Limits {
			if kb < 0 || kb > MaxKB {
				return nil, actions.InvalidParams(fmt.Sprintf("limits: %s must be 0 (no limit) to %d KiB", u, int64(MaxKB)))
			}
			names = append(names, u)
		}
		sort.Strings(names)
		uids, err := lookup(e, names)
		if err != nil {
			return nil, err
		}
		s, err := requireOn(ctx, e)
		if err != nil {
			return nil, err
		}
		set, failed := []string{}, map[string]string{}
		for _, u := range names {
			if _, ok := uids[u]; !ok {
				failed[u] = "no such user"
				continue
			}
			kb := strconv.FormatInt(in.Limits[u], 10)
			if out, err := run(ctx, e, nil, SysAdmin, "setquota", "-u", u, "0", kb, "0", "0", s.Point); err != nil {
				failed[u] = host.Output([]byte(out))
				continue
			}
			set = append(set, u)
		}
		e.Log.Info("disk quotas set", "users", len(set), "failed", len(failed))
		return map[string]any{"set": set, "failed": failed, "mount_point": s.Point}, nil
	}})
}
