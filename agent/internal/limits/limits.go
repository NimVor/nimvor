// Package limits applies hosting accounts' resource limits with systemd
// and cgroup v2.
//
// Each limited account has a slice, whacp-acct<ID>.slice (a child of
// whacp.slice), with the plan's CPU, memory, process and disk bandwidth
// limits. The account's PHP-FPM master, workers and cron jobs run in it, so
// everything they start is counted and cannot leave (moving a process to
// another cgroup needs delegation, which accounts do not have). Logins
// (SSH, SFTP) are placed by systemd-logind in user-<uid>.slice; the same
// limits are set there with a drop-in (a separate budget).
//
// Unit files are the configuration: they survive reboots, and changes are
// also applied to the running slice at once.
package limits

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/changeset"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// Limits of an account; 0 means no limit.
type Limits struct {
	CPUPercent  int `json:"cpu_percent"`   // 100 = one CPU core
	MemoryMB    int `json:"memory_mb"`     // hard limit; processes are slowed from 90%
	TasksMax    int `json:"tasks_max"`     // processes and threads
	IOReadMBps  int `json:"io_read_mbps"`  // disk reads of the websites' file system
	IOWriteMBps int `json:"io_write_mbps"` // disk writes of the websites' file system
}

// Empty reports whether no limit is set.
func (l Limits) Empty() bool { return l == Limits{} }

// Validate checks ranges.
func (l Limits) Validate() error {
	for _, c := range []struct {
		name     string
		v, lo, h int
	}{
		{"cpu_percent", l.CPUPercent, 10, 12800},
		{"memory_mb", l.MemoryMB, 64, 1 << 21},
		{"tasks_max", l.TasksMax, 16, 1 << 20},
		{"io_read_mbps", l.IOReadMBps, 1, 100000},
		{"io_write_mbps", l.IOWriteMBps, 1, 100000},
	} {
		if c.v != 0 && (c.v < c.lo || c.v > c.h) {
			return actions.InvalidParams(fmt.Sprintf("%s: 0 (no limit) or %d–%d", c.name, c.lo, c.h))
		}
	}
	return nil
}

var accountRe = regexp.MustCompile(`^acct[0-9]{1,9}$`)

// SliceName is an account's slice.
func SliceName(account string) string { return "whacp-" + account + ".slice" }

// ValidSlice reports whether s is an account slice name.
func ValidSlice(s string) bool {
	return strings.HasPrefix(s, "whacp-") && strings.HasSuffix(s, ".slice") && accountRe.MatchString(strings.TrimSuffix(strings.TrimPrefix(s, "whacp-"), ".slice"))
}

// Directives renders the [Slice] settings.
func Directives(l Limits, ioPath string) []string {
	var d []string
	if l.CPUPercent > 0 {
		d = append(d, fmt.Sprintf("CPUQuota=%d%%", l.CPUPercent))
	}
	if l.MemoryMB > 0 {
		d = append(d, fmt.Sprintf("MemoryHigh=%dM", l.MemoryMB*9/10), fmt.Sprintf("MemoryMax=%dM", l.MemoryMB))
	}
	if l.TasksMax > 0 {
		d = append(d, fmt.Sprintf("TasksMax=%d", l.TasksMax))
	}
	if l.IOReadMBps > 0 {
		d = append(d, fmt.Sprintf("IOReadBandwidthMax=%s %dM", ioPath, l.IOReadMBps))
	}
	if l.IOWriteMBps > 0 {
		d = append(d, fmt.Sprintf("IOWriteBandwidthMax=%s %dM", ioPath, l.IOWriteMBps))
	}
	return d
}

// RenderSlice is the account slice unit.
func RenderSlice(account string, l Limits, ioPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Managed by WhaCP: resource limits of hosting account %s (from its plan). Changes here are overwritten.\n[Unit]\nDescription=WhaCP hosting account %s\n\n[Slice]\n", account, account)
	for _, d := range Directives(l, ioPath) {
		b.WriteString(d + "\n")
	}
	return b.String()
}

// RenderUserDropIn limits a system user's logins (user-<uid>.slice).
func RenderUserDropIn(account string, l Limits, ioPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Managed by WhaCP: logins of hosting account %s have its plan's limits.\n[Slice]\n", account)
	for _, d := range Directives(l, ioPath) {
		b.WriteString(d + "\n")
	}
	return b.String()
}

// State maps accounts to their system users.
type State struct {
	Accounts map[string]AccountState `json:"accounts"`
}

// AccountState is one account's applied limits.
type AccountState struct {
	Limits Limits   `json:"limits"`
	Users  []string `json:"users"`
}

func statePath(e *host.Env) string { return filepath.Join(e.Cfg.StateDir, "limits.json") }

// LoadState reads the applied limits.
func LoadState(e *host.Env) State {
	st := State{Accounts: map[string]AccountState{}}
	if b, err := e.Root.ReadFile(statePath(e)); err == nil {
		json.Unmarshal(b, &st)
	}
	if st.Accounts == nil {
		st.Accounts = map[string]AccountState{}
	}
	return st
}

// SliceFor returns the slice a system user's services belong in ("" when
// the user's account has no limits).
func SliceFor(e *host.Env, user string) string { return e.AccountSlice(user) }

func userDropIn(e *host.Env, uid int) string {
	return filepath.Join(e.M.UnitDir, fmt.Sprintf("user-%d.slice.d", uid), "50-whacp-limits.conf")
}

// CronDenyStart and CronDenyEnd frame the users WhaCP adds to cron.deny.
const (
	CronDenyStart = "# BEGIN WhaCP: website accounts use scheduled tasks in the panel"
	CronDenyEnd   = "# END WhaCP"
	// cronDenyStartOld is the line written before every website account
	// was listed (only accounts with resource limits).
	cronDenyStartOld = "# BEGIN WhaCP: accounts with resource limits use scheduled tasks in the panel"
)

// WebsiteAccounts are the accounts whose home is a website folder directly
// under the sites root (UID 1000 and up).
func WebsiteAccounts(e *host.Env) []string {
	b, err := e.Root.ReadFile("/etc/passwd")
	if err != nil {
		return nil
	}
	root := filepath.Clean(e.Cfg.SitesRoot)
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 7 {
			continue
		}
		uid, err := strconv.Atoi(f[2])
		if err != nil || uid < 1000 || uid >= 60000 {
			continue
		}
		if filepath.Dir(filepath.Clean(f[5])) == root {
			out = append(out, f[0])
		}
	}
	sort.Strings(out)
	return out
}

// DenyPersonalCron lists every website account (and extra ones) in
// cron.deny and at.deny: their scheduled tasks are the panel's, which run
// under WhaCP's control (as the account, in its slice when limited); a
// personal crontab or at job would run outside it, unseen.
func DenyPersonalCron(e *host.Env, set *changeset.Set, extra []string) {
	seen := map[string]bool{}
	var users []string
	for _, u := range append(WebsiteAccounts(e), extra...) {
		if !seen[u] {
			seen[u] = true
			users = append(users, u)
		}
	}
	for _, f := range []string{"/etc/cron.deny", "/etc/at.deny"} {
		cur, _ := e.Root.ReadFile(f)
		next := RenderCronDeny(string(cur), users)
		if next != string(cur) {
			if next == "" {
				set.Remove(f)
			} else {
				set.Write(f, []byte(next), 0o644, agentfs.RootOwner)
			}
		}
	}
}

// RenderCronDeny puts WhaCP's block (the limited users) into cron.deny.
func RenderCronDeny(current string, users []string) string {
	var keep []string
	in := false
	for _, line := range strings.Split(strings.TrimRight(current, "\n"), "\n") {
		switch {
		case line == CronDenyStart || line == cronDenyStartOld:
			in = true
		case line == CronDenyEnd && in:
			in = false
		case !in && line != "":
			keep = append(keep, line)
		}
	}
	out := strings.Join(keep, "\n")
	if len(users) > 0 {
		sort.Strings(users)
		if out != "" {
			out += "\n"
		}
		out += CronDenyStart + "\n" + strings.Join(users, "\n") + "\n" + CronDenyEnd
	}
	if out == "" {
		return ""
	}
	return out + "\n"
}

func cgroupV2(e *host.Env) bool {
	return e.Root.Exists("/sys/fs/cgroup/cgroup.controllers")
}

// Usage is an account slice's current use.
type Usage struct {
	Active        bool   `json:"active"`
	MemoryBytes   int64  `json:"memory_bytes"`
	CPUUsageUsec  int64  `json:"cpu_usage_usec"`
	Tasks         int64  `json:"tasks"`
	MemoryHighHit int64  `json:"memory_high_events"`
	MemoryMaxHit  int64  `json:"memory_max_events"`
	OOMKills      int64  `json:"oom_kills"`
	ThrottledUsec int64  `json:"cpu_throttled_usec"`
	Path          string `json:"path"`
}

func readUsage(e *host.Env, account string) Usage {
	dir := filepath.Join("/sys/fs/cgroup/whacp.slice", SliceName(account))
	u := Usage{Path: dir}
	num := func(file string) int64 {
		b, err := e.Root.ReadFile(filepath.Join(dir, file))
		if err != nil {
			return 0
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		return n
	}
	kv := func(file string) map[string]int64 {
		out := map[string]int64{}
		b, err := e.Root.ReadFile(filepath.Join(dir, file))
		if err != nil {
			return out
		}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 {
				out[f[0]], _ = strconv.ParseInt(f[1], 10, 64)
			}
		}
		return out
	}
	if !e.Root.Exists(dir) {
		return u
	}
	u.Active = true
	u.MemoryBytes = num("memory.current")
	u.Tasks = num("pids.current")
	cpu := kv("cpu.stat")
	u.CPUUsageUsec, u.ThrottledUsec = cpu["usage_usec"], cpu["throttled_usec"]
	ev := kv("memory.events")
	u.MemoryHighHit, u.MemoryMaxHit, u.OOMKills = ev["high"], ev["max"], ev["oom_kill"]
	return u
}

// Register adds the limits actions.
func Register(r *actions.Registry, e *host.Env) {
	e.Writer.Policy.Patterns = append(e.Writer.Policy.Patterns,
		filepath.Join(e.M.UnitDir, "whacp.slice"),
		filepath.Join(e.M.UnitDir, "whacp-acct*.slice"),
		filepath.Join(e.M.UnitDir, "user-*.slice.d"),
		filepath.Join(e.M.UnitDir, "user-*.slice.d/50-whacp-limits.conf"),
		"/etc/cron.deny", "/etc/at.deny",
	)

	r.Register(actions.Action{Name: "limits.set", Mutating: true, Description: "Set a hosting account's resource limits (CPU %, memory, processes, disk read/write MB/s; 0 = no limit) for its slice whacp-acct<ID>.slice and its system users' logins; all zero removes them. Applied at once and at every boot. Websites, workers and cron jobs follow when they are applied again.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Account string   `json:"account"`
			Users   []string `json:"users"`
			Limits
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if !accountRe.MatchString(in.Account) {
			return nil, actions.InvalidParams("account: acct followed by the account number")
		}
		if err := in.Limits.Validate(); err != nil {
			return nil, err
		}
		if len(in.Users) > 500 {
			return nil, actions.InvalidParams("at most 500 users")
		}
		uids := map[string]int{}
		for _, u := range in.Users {
			if err := validate.Username("users", u); err != nil {
				return nil, host.Invalid(err)
			}
			acct, err := e.LookupUser(u)
			if err != nil {
				continue // not created yet: its services pick up the slice when they are
			}
			if acct.UID < 1000 {
				return nil, actions.InvalidParams(u + " is a system account")
			}
			uids[u] = acct.UID
		}
		if !in.Limits.Empty() && !cgroupV2(e) && !e.Cfg.SimulateCommands {
			return nil, actions.Conflict("This server does not use the unified cgroup hierarchy (cgroup v2), which WhaCP's resource limits need. Ubuntu 22.04+, Debian 11+ and RHEL/AlmaLinux 9 use it by default.")
		}
		st := LoadState(e)
		prev := st.Accounts[in.Account]
		ioPath := e.Cfg.SitesRoot
		set := e.Changes("limits-" + in.Account)
		slicePath := filepath.Join(e.M.UnitDir, SliceName(in.Account))
		if !e.Root.Exists(filepath.Join(e.M.UnitDir, "whacp.slice")) {
			set.Write(filepath.Join(e.M.UnitDir, "whacp.slice"), []byte("# Managed by WhaCP: parent of the hosting accounts' slices.\n[Unit]\nDescription=WhaCP hosting accounts\n"), 0o644, agentfs.RootOwner)
		}
		// Users no longer in the account lose its login limits.
		for _, u := range prev.Users {
			if _, still := uids[u]; still && !in.Limits.Empty() {
				continue
			}
			if acct, err := e.LookupUser(u); err == nil {
				if e.Root.Exists(userDropIn(e, acct.UID)) {
					set.Remove(userDropIn(e, acct.UID))
				}
			}
		}
		users := []string{}
		if in.Limits.Empty() {
			if e.Root.Exists(slicePath) {
				set.Remove(slicePath)
			}
			delete(st.Accounts, in.Account)
		} else {
			set.Write(slicePath, []byte(RenderSlice(in.Account, in.Limits, ioPath)), 0o644, agentfs.RootOwner)
			for u, uid := range uids {
				set.Mkdir(filepath.Dir(userDropIn(e, uid)), 0o755, agentfs.RootOwner)
				set.Write(userDropIn(e, uid), []byte(RenderUserDropIn(in.Account, in.Limits, ioPath)), 0o644, agentfs.RootOwner)
				users = append(users, u)
			}
			sort.Strings(users)
			st.Accounts[in.Account] = AccountState{Limits: in.Limits, Users: in.Users}
		}
		// Website accounts schedule tasks in the panel (limited ones in the
		// slice); a personal crontab or at job would run outside it.
		var denied []string
		for _, a := range st.Accounts {
			denied = append(denied, a.Users...)
		}
		DenyPersonalCron(e, set, denied)
		b, _ := json.MarshalIndent(st, "", "  ")
		set.Write(statePath(e), b, 0o600, agentfs.RootOwner)
		applied, err := set.Commit(func() error { return e.DaemonReload(ctx) })
		if err != nil {
			return nil, host.FailWithValidation("The resource limits were not applied", err)
		}
		// Live: the unit files take effect at the next start; set the
		// running slices now too.
		var warnings []string
		if !in.Limits.Empty() {
			props := Directives(in.Limits, ioPath)
			targets := []string{SliceName(in.Account)}
			for _, u := range users {
				targets = append(targets, fmt.Sprintf("user-%d.slice", uids[u]))
			}
			for _, t := range targets {
				args := append([]string{"set-property", "--runtime", "--", t}, props...)
				if out, err := e.Runner.Run(ctx, "systemctl", args...); err != nil {
					warnings = append(warnings, t+": "+host.Output(out))
				}
			}
		}
		return map[string]any{"slice": SliceName(in.Account), "users": users, "limits": in.Limits, "changed": applied.Changed, "warnings": warnings}, nil
	}})

	r.Register(actions.Action{Name: "limits.status", Description: "Resource use of hosting accounts' slices (memory, CPU time and throttling, processes, memory pressure and out-of-memory kills) and their limits.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Account string `json:"account"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		st := LoadState(e)
		out := map[string]any{}
		for acct, a := range st.Accounts {
			if in.Account != "" && acct != in.Account {
				continue
			}
			out[acct] = map[string]any{"limits": a.Limits, "users": a.Users, "slice": SliceName(acct), "usage": readUsage(e, acct)}
		}
		return map[string]any{"accounts": out, "cgroup_v2": cgroupV2(e)}, nil
	}})
}
