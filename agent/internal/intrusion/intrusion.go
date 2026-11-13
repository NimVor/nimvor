// Package intrusion is NimVor's anti-intrusion protection for accounts that
// run other people's code: the web server's and database's system accounts
// and the website accounts.
//
// Two independent parts, each chosen per account:
//
//   - Log: the kernel audit system (auditd) records every program the
//     account starts. NimVor writes one rules file (key "nimvor-exec") with
//     the UIDs chosen; the agent reads the audit log and shows what ran, and
//     which attempts were refused. Nothing is blocked.
//   - Protection: a blacklist. The programs on the administrator's list
//     (network scanners, tunnelling tools, debuggers, …) get a POSIX ACL
//     entry that denies execution to the accounts under protection; every
//     other program keeps working, so protecting an account cannot break a
//     website that does not use a listed program. Turning protection off (or
//     removing a program from the list) removes exactly the entries NimVor
//     added. Package updates replace program files and so drop the entries:
//     they are applied again after package jobs (Reapply) and when the
//     agent starts.
//
// A blacklist raises the bar: a determined attacker can still bring and run
// a program of their own in a place they can write. It is a layer next to
// the sandboxes, the firewall and the web application firewall, not a
// replacement.
package intrusion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/users"
)

// Files and names.
const (
	// RulesFile is the audit rules file NimVor owns.
	RulesFile = "/etc/audit/rules.d/nimvor-exec.rules"
	// AuditLog is where auditd writes.
	AuditLog = "/var/log/audit/audit.log"
	// Key tags NimVor's rules in the audit log.
	Key = "nimvor-exec"

	maxOps      = 200
	maxAccounts = 2000
	maxList     = 200
)

// Setting is what is switched on for one account.
type Setting struct {
	Log     bool `json:"log"`
	Protect bool `json:"protect"`
}

// Op is one entry of the operation log.
type Op struct {
	Time string `json:"time"`
	Text string `json:"text"`
}

// State is what the agent keeps (intrusion.json in the state directory).
type State struct {
	Enabled   bool               `json:"enabled"`
	Accounts  map[string]Setting `json:"accounts"`
	Blacklist []string           `json:"blacklist"`
	// Applied: program → the accounts NimVor added a deny entry for.
	Applied map[string][]string `json:"applied"`
	Ops     []Op                `json:"ops"`
}

var mu sync.Mutex

func statePath(env *host.Env) string { return filepath.Join(env.Cfg.StateDir, "intrusion.json") }

func load(env *host.Env) State {
	st := State{Accounts: map[string]Setting{}, Applied: map[string][]string{}}
	if b, err := env.Root.ReadFile(statePath(env)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Accounts == nil {
		st.Accounts = map[string]Setting{}
	}
	if st.Applied == nil {
		st.Applied = map[string][]string{}
	}
	return st
}

func save(env *host.Env, st State) error {
	if len(st.Ops) > maxOps {
		st.Ops = st.Ops[len(st.Ops)-maxOps:]
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	set := env.Changes("intrusion")
	set.Write(statePath(env), b, 0o600, agentfs.RootOwner)
	_, err := set.Commit(nil)
	return err
}

func (s *State) op(text string) {
	s.Ops = append(s.Ops, Op{Time: time.Now().UTC().Format(time.RFC3339), Text: text})
}

// Account is one account that can be put under monitoring or protection.
type Account struct {
	Name     string `json:"name"`
	UID      int    `json:"uid"`
	Kind     string `json:"kind"` // "system" or "website"
	CanLogin bool   `json:"can_login"`
	Remark   string `json:"remark"`
	Log      bool   `json:"log"`
	Protect  bool   `json:"protect"`
	Total    int    `json:"total"`
	Today    int    `json:"today"`
	Blocked  int    `json:"blocked"`
}

// candidates are the accounts that may be chosen: the system's service
// accounts and the website accounts (not root: its programs are the
// administrator's).
func candidates(env *host.Env) ([]users.User, error) {
	all, err := env.Accounts()
	if err != nil {
		return nil, err
	}
	var out []users.User
	for _, u := range all {
		if u.UID == 0 {
			continue
		}
		if u.System || isWebsite(env, u) {
			out = append(out, u)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}

func isWebsite(env *host.Env, u users.User) bool {
	return env.Cfg.SitesRoot != "" && strings.HasPrefix(u.Home, strings.TrimSuffix(env.Cfg.SitesRoot, "/")+"/")
}

func kindOf(env *host.Env, u users.User) string {
	if isWebsite(env, u) {
		return "website"
	}
	return "system"
}

// programDirs are where a blacklisted program may live (after links are
// followed): the system's own program directories.
var programDirs = []string{"/usr/", "/bin/", "/sbin/", "/opt/"}

var userRe = regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,31}\$?$`)

// ValidateProgram resolves a program (a path, or a name searched in the
// system's program directories) to the regular file it ends at; the file
// must be in a system program directory.
func ValidateProgram(env *host.Env, p string) (string, error) {
	if p == "" || len(p) > 300 || strings.ContainsAny(p, "\x00\n\r\t ") {
		return "", errors.New("not a valid program")
	}
	candidates := []string{p}
	if !strings.Contains(p, "/") {
		candidates = nil
		for _, d := range []string{"/usr/local/bin", "/usr/local/sbin", "/usr/bin", "/usr/sbin", "/bin", "/sbin"} {
			candidates = append(candidates, d+"/"+p)
		}
	} else if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return "", errors.New("give the program's name or its full path")
	}
	var lastErr error = errors.New("the program was not found")
	for _, c := range candidates {
		real, err := env.Root.Resolve(c)
		if err != nil {
			lastErr = errors.New("the program was not found")
			continue
		}
		fi, err := env.Root.Lstat(real)
		if err != nil || !fi.Mode().IsRegular() {
			lastErr = errors.New("not a program file")
			continue
		}
		ok := false
		for _, d := range programDirs {
			if strings.HasPrefix(real, d) {
				ok = true
			}
		}
		if !ok {
			return "", errors.New("only programs in the system's program directories (/usr, /bin, /sbin, /opt) can be listed")
		}
		if fi.Mode()&0o111 == 0 {
			lastErr = errors.New("not an executable file")
			continue
		}
		return real, nil
	}
	return "", lastErr
}

// Suggested are programs sites rarely need and attackers often bring:
// scanners, tunnelling and reverse-shell helpers, packet capture, debuggers
// and password crackers. Only those present on the server are offered.
var Suggested = []string{
	"nc", "ncat", "netcat", "socat", "nmap", "masscan", "telnet", "tcpdump", "tshark",
	"strace", "ltrace", "gdb", "hydra", "john", "sqlmap", "nikto", "proxychains", "proxychains4",
}

// SuggestedPresent returns the resolved paths of the suggested programs
// that exist here.
func SuggestedPresent(env *host.Env) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range Suggested {
		if p, err := ValidateProgram(env, n); err == nil && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Configure sets the master switch and the accounts' switches, then makes
// the host match.
func Configure(ctx context.Context, env *host.Env, enabled bool, settings map[string]Setting) error {
	if len(settings) > maxAccounts {
		return fmt.Errorf("at most %d accounts", maxAccounts)
	}
	cands, err := candidates(env)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, u := range cands {
		known[u.Name] = true
	}
	clean := map[string]Setting{}
	for name, s := range settings {
		if !userRe.MatchString(name) || !known[name] {
			return fmt.Errorf("%s cannot be put under protection (it is root, missing, or an account that is not a service or website account)", name)
		}
		if s.Log || s.Protect {
			clean[name] = s
		}
	}
	mu.Lock()
	defer mu.Unlock()
	st := load(env)
	before := st.Enabled
	st.Enabled = enabled
	if before != enabled {
		if enabled {
			st.op("Turned the anti-intrusion master switch on")
		} else {
			st.op("Turned the anti-intrusion master switch off")
		}
	}
	for name, s := range clean {
		old := st.Accounts[name]
		if old.Log != s.Log {
			st.op(onOff(s.Log, "Logging", name))
		}
		if old.Protect != s.Protect {
			st.op(onOff(s.Protect, "Protection", name))
		}
	}
	for name, old := range st.Accounts {
		if _, kept := clean[name]; !kept {
			if old.Log {
				st.op(onOff(false, "Logging", name))
			}
			if old.Protect {
				st.op(onOff(false, "Protection", name))
			}
		}
	}
	st.Accounts = clean
	return reconcileLocked(ctx, env, &st)
}

func onOff(on bool, what, name string) string {
	if on {
		return "Turned " + what + " on for " + name
	}
	return "Turned " + what + " off for " + name
}

// SetBlacklist replaces the list of programs and makes the host match.
func SetBlacklist(ctx context.Context, env *host.Env, programs []string) error {
	if len(programs) > maxList {
		return fmt.Errorf("at most %d programs", maxList)
	}
	seen := map[string]bool{}
	var list []string
	for _, p := range programs {
		real, err := ValidateProgram(env, p)
		if err != nil {
			return fmt.Errorf("%s: %v", p, err)
		}
		if !seen[real] {
			seen[real] = true
			list = append(list, real)
		}
	}
	sort.Strings(list)
	mu.Lock()
	defer mu.Unlock()
	st := load(env)
	old := map[string]bool{}
	for _, p := range st.Blacklist {
		old[p] = true
	}
	for _, p := range list {
		if !old[p] {
			st.op("Added " + p + " to the blacklist")
		}
		delete(old, p)
	}
	for p := range old {
		st.op("Removed " + p + " from the blacklist")
	}
	st.Blacklist = list
	return reconcileLocked(ctx, env, &st)
}

// Reconcile makes the host match the saved state (the agent calls it when
// it starts and after package jobs, which replace program files).
func Reconcile(ctx context.Context, env *host.Env) error {
	mu.Lock()
	defer mu.Unlock()
	st := load(env)
	if !st.Enabled && len(st.Applied) == 0 && !env.Root.Exists(RulesFile) {
		return nil
	}
	return reconcileLocked(ctx, env, &st)
}

// reconcileLocked applies the audit rules and the ACL entries and saves.
func reconcileLocked(ctx context.Context, env *host.Env, st *State) error {
	cands, err := candidates(env)
	if err != nil {
		return err
	}
	uids := map[string]int{}
	for _, u := range cands {
		uids[u.Name] = u.UID
	}
	var errs []string
	if err := reconcileAudit(ctx, env, st, uids); err != nil {
		errs = append(errs, err.Error())
	}
	if err := reconcileACL(ctx, env, st, uids); err != nil {
		errs = append(errs, err.Error())
	}
	if err := save(env, *st); err != nil {
		errs = append(errs, "the settings were not saved: "+err.Error())
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// rulesBody is the audit rules file for these UIDs: every successful or
// refused execve of the account (arch b64 covers the 64-bit programs of
// the websites and services).
func rulesBody(uids []int) string {
	var b strings.Builder
	b.WriteString("# Managed by NimVor: Security → Anti-intrusion (logging). Do not edit.\n")
	for _, uid := range uids {
		fmt.Fprintf(&b, "-a always,exit -F arch=b64 -S execve -F uid=%d -k %s\n", uid, Key)
	}
	return b.String()
}

func reconcileAudit(ctx context.Context, env *host.Env, st *State, uids map[string]int) error {
	var list []int
	if st.Enabled {
		for name, s := range st.Accounts {
			if s.Log {
				if uid, ok := uids[name]; ok {
					list = append(list, uid)
				}
			}
		}
	}
	sort.Ints(list)
	have := env.Root.Exists(RulesFile)
	if len(list) == 0 && !have {
		return nil
	}
	set := env.Changes("intrusion-audit")
	if len(list) > 0 {
		if !env.Runner.Available("augenrules") {
			return errors.New("logging needs the audit system (auditd); install it first")
		}
		want := rulesBody(list)
		if cur, err := env.Root.ReadFile(RulesFile); err == nil && string(cur) == want {
			return nil
		}
		set.Mkdir(filepath.Dir(RulesFile), 0o750, agentfs.RootOwner)
		set.Write(RulesFile, []byte(want), 0o640, agentfs.RootOwner)
	} else {
		set.Remove(RulesFile)
	}
	if _, err := set.Commit(nil); err != nil {
		return fmt.Errorf("the audit rules were not written: %v", err)
	}
	if env.Runner.Available("augenrules") {
		if out, err := env.Runner.Run(ctx, "augenrules", "--load"); err != nil {
			return fmt.Errorf("the audit rules were written but not loaded: %s", host.Output(out))
		}
	}
	return nil
}

// desiredACL: program → accounts to deny.
func desiredACL(st *State, uids map[string]int) map[string][]string {
	out := map[string][]string{}
	if !st.Enabled {
		return out
	}
	var names []string
	for name, s := range st.Accounts {
		if _, ok := uids[name]; ok && s.Protect {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, p := range st.Blacklist {
		if len(names) > 0 {
			out[p] = names
		}
	}
	return out
}

func reconcileACL(ctx context.Context, env *host.Env, st *State, uids map[string]int) error {
	want := desiredACL(st, uids)
	if len(want) == 0 && len(st.Applied) == 0 {
		return nil
	}
	if !env.Runner.Available("setfacl") {
		if len(want) > 0 {
			return errors.New("protection needs the ACL tools (setfacl); install the acl package first")
		}
		st.Applied = map[string][]string{}
		return nil
	}
	var errs []string
	applied := map[string][]string{}
	// Entries NimVor added that are no longer wanted.
	for p, names := range st.Applied {
		keep := map[string]bool{}
		for _, n := range want[p] {
			keep[n] = true
		}
		for _, n := range names {
			if keep[n] {
				continue
			}
			if !env.Root.Exists(p) {
				continue
			}
			if out, err := env.Runner.Run(ctx, "setfacl", "-x", "u:"+n, p); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %s", p, host.Output(out)))
				applied[p] = append(applied[p], n) // still there
			}
		}
	}
	// Entries wanted (set again every time: a package update drops them).
	for p, names := range want {
		for _, n := range names {
			if out, err := env.Runner.Run(ctx, "setfacl", "-m", "u:"+n+":---", p); err != nil {
				errs = append(errs, fmt.Sprintf("%s for %s: %s", p, n, host.Output(out)))
				continue
			}
			applied[p] = appendUnique(applied[p], n)
		}
	}
	for p := range applied {
		sort.Strings(applied[p])
	}
	st.Applied = applied
	if len(errs) > 0 {
		return errors.New("some programs were not changed: " + strings.Join(errs, "; "))
	}
	return nil
}

func appendUnique(l []string, s string) []string {
	for _, x := range l {
		if x == s {
			return l
		}
	}
	return append(l, s)
}

// AllowPaths lets the agent write this package's files.
func AllowPaths(env *host.Env) {
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, RulesFile, filepath.Dir(RulesFile))
}
