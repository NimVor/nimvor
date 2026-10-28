// Package hardening holds server-wide tweaks that close common paths for
// attacks from a hosting account:
//
//   - Compiler access: the C and C++ compilers and the assembler belong to
//     root and the group nimvor-compiler with mode 0750; only its members
//     (and NimVor's own build account) may compile.
//   - Traceroute: traceroute and tracepath can be limited to root (mode
//     0700), so accounts cannot map the network from the server.
//   - Shell fork bomb protection: a pam_limits file caps the processes of
//     accounts (UID 1000 and up) in their sessions (SSH, cron, su).
//   - SMTP restrictions: an nftables table rejects outgoing connections to
//     SMTP ports (25, 465, 587) except from root, the mail server and the
//     panel, so scripts cannot bypass the mail server to send spam.
//
// Programs are changed on descriptors opened without following links, and
// their earlier owner and mode are recorded so turning a tweak off puts
// them back. Debian and Ubuntu keep the modes through package updates with
// dpkg-statoverride; on RHEL they are applied again after package jobs and
// by the panel's scheduler (Reapply).
package hardening

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// CompilerGroup may run the compilers while they are restricted.
const CompilerGroup = "nimvor-compiler"

// BuildAccount is NimVor's own build account (ModSecurity connector).
const BuildAccount = "nimvor-build"

// Original is a program's owner and mode before NimVor changed it.
type Original struct {
	UID  int    `json:"uid"`
	GID  int    `json:"gid"`
	Mode uint32 `json:"mode"`
}

// State is what NimVor changed.
type State struct {
	Compilers  bool                `json:"compilers_restricted"`
	Traceroute bool                `json:"traceroute_disabled"`
	Originals  map[string]Original `json:"originals"`
}

var mu sync.Mutex

func statePath(env *host.Env) string { return filepath.Join(env.Cfg.StateDir, "hardening.json") }

func load(env *host.Env) State {
	st := State{Originals: map[string]Original{}}
	if b, err := env.Root.ReadFile(statePath(env)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Originals == nil {
		st.Originals = map[string]Original{}
	}
	return st
}

func save(env *host.Env, st State) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	set := env.Changes("hardening")
	set.Write(statePath(env), b, 0o600, agentfs.RootOwner)
	_, err := set.Commit(nil)
	return err
}

var (
	compilerRe   = regexp.MustCompile(`^((cc|c\+\+|gcc|g\+\+|clang|clang\+\+|as|cpp)(-[0-9][0-9.]*)?|[a-z0-9_]+-(linux-gnu|redhat-linux|pc-linux-gnu)-(gcc|g\+\+|as|cpp)(-[0-9][0-9.]*)?)$`)
	tracerouteRe = regexp.MustCompile(`^(traceroute6?(\.db)?|tracepath6?|tcptraceroute)$`)
)

// programs lists the regular files behind the names in /usr/bin and
// /usr/sbin that match re (links resolved; only files under /usr are
// changed), sorted.
func programs(env *host.Env, re *regexp.Regexp) []string {
	seen := map[string]bool{}
	var out []string
	for _, dir := range []string{"/usr/bin", "/usr/sbin"} {
		real, err := env.Root.Path(dir)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(real)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !re.MatchString(e.Name()) {
				continue
			}
			target, err := filepath.EvalSymlinks(filepath.Join(real, e.Name()))
			if err != nil {
				continue
			}
			rootReal, _ := env.Root.Path("/")
			rel, err := filepath.Rel(rootReal, target)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			hostPath := "/" + filepath.ToSlash(rel)
			if !strings.HasPrefix(hostPath, "/usr/") || seen[hostPath] {
				continue
			}
			fi, err := os.Lstat(target)
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
			seen[hostPath] = true
			out = append(out, hostPath)
		}
	}
	sort.Strings(out)
	return out
}

// Program is one program's state.
type Program struct {
	Path  string `json:"path"`
	Mode  string `json:"mode"`
	Owner string `json:"owner"`
}

func describe(env *host.Env, paths []string) []Program {
	out := []Program{}
	for _, p := range paths {
		real, err := env.Root.Path(p)
		var st syscall.Stat_t
		if err != nil || syscall.Lstat(real, &st) != nil {
			continue
		}
		out = append(out, Program{Path: p, Mode: fmt.Sprintf("%o", st.Mode&0o7777), Owner: fmt.Sprintf("%s:%s", name(env, "/etc/passwd", int(st.Uid)), name(env, "/etc/group", int(st.Gid)))})
	}
	return out
}

// name is the account or group name of an ID (the ID when unknown).
func name(env *host.Env, file string, id int) string {
	b, err := env.Root.ReadFile(file)
	if err == nil {
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			f := strings.Split(sc.Text(), ":")
			if len(f) >= 3 && f[2] == strconv.Itoa(id) {
				return f[0]
			}
		}
	}
	return strconv.Itoa(id)
}

// setMode gives a program an owner and mode (root-owned regular files only,
// no links followed) and returns what it had.
func setMode(env *host.Env, p string, uid, gid int, mode uint32) (Original, error) {
	real, err := env.Root.Path(p)
	if err != nil {
		return Original{}, err
	}
	fd, err := syscall.Open(real, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return Original{}, fmt.Errorf("%s: %w (a link is not changed)", p, err)
	}
	defer syscall.Close(fd)
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return Original{}, err
	}
	was := Original{UID: int(st.Uid), GID: int(st.Gid), Mode: st.Mode & 0o7777}
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		return was, fmt.Errorf("%s is not a regular file", p)
	}
	if st.Uid != 0 {
		return was, fmt.Errorf("%s does not belong to root; it is left alone", p)
	}
	if int(st.Uid) != uid || int(st.Gid) != gid {
		if err := syscall.Fchown(fd, uid, gid); err != nil {
			return was, fmt.Errorf("%s: %w", p, err)
		}
	}
	if st.Mode&0o7777 != mode {
		if err := syscall.Fchmod(fd, mode); err != nil {
			return was, fmt.Errorf("%s: %w", p, err)
		}
	}
	return was, nil
}

// statOverride keeps (or drops, group "") a mode through Debian package
// updates.
func statOverride(ctx context.Context, env *host.Env, p, group string, mode uint32) {
	if env.Platform.PackageManager() != "apt" || !env.Runner.Available("dpkg-statoverride") {
		return
	}
	out, _ := env.Runner.Run(ctx, "dpkg-statoverride", "--list", p)
	want := fmt.Sprintf("root %s %o %s", group, mode, p)
	if group != "" && strings.TrimSpace(string(out)) == want {
		return
	}
	if strings.TrimSpace(string(out)) != "" {
		env.Runner.Run(ctx, "dpkg-statoverride", "--remove", p)
	}
	if group != "" {
		env.Runner.Run(ctx, "dpkg-statoverride", "--add", "root", group, fmt.Sprintf("%04o", mode), p)
	}
}

// groupEntry reads a group's ID and members.
func groupEntry(env *host.Env, group string) (gid int, members []string, ok bool) {
	b, err := env.Root.ReadFile("/etc/group")
	if err != nil {
		return 0, nil, false
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) >= 4 && f[0] == group {
			gid, err := strconv.Atoi(f[2])
			if err != nil {
				return 0, nil, false
			}
			for _, m := range strings.Split(f[3], ",") {
				if m = strings.TrimSpace(m); m != "" {
					members = append(members, m)
				}
			}
			sort.Strings(members)
			return gid, members, true
		}
	}
	return 0, nil, false
}

func ensureGroup(ctx context.Context, env *host.Env, group string) (int, error) {
	if gid, _, ok := groupEntry(env, group); ok {
		return gid, nil
	}
	if out, err := env.Runner.Run(ctx, "groupadd", "--system", "--", group); err != nil {
		return 0, fmt.Errorf("groupadd: %s", host.Output(out))
	}
	gid, _, ok := groupEntry(env, group)
	if !ok {
		return 0, fmt.Errorf("the %s group was not found after creating it", group)
	}
	return gid, nil
}

// restrictAll applies owner and mode to programs, recording originals.
func restrictAll(ctx context.Context, env *host.Env, st *State, paths []string, gid int, group string, mode uint32) error {
	var errs []string
	for _, p := range paths {
		was, err := setMode(env, p, 0, gid, mode)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if _, had := st.Originals[p]; !had && !(was.GID == gid && was.Mode == mode) {
			st.Originals[p] = was
		}
		statOverride(ctx, env, p, group, mode)
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// restoreAll puts recorded programs back (or 0755 root:root when none was
// recorded).
func restoreAll(ctx context.Context, env *host.Env, st *State, paths []string) error {
	var errs []string
	for _, p := range paths {
		was, ok := st.Originals[p]
		if !ok {
			was = Original{UID: 0, GID: 0, Mode: 0o755}
		}
		if _, err := setMode(env, p, was.UID, was.GID, was.Mode); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		delete(st.Originals, p)
		statOverride(ctx, env, p, "", 0)
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Compilers.

// CompilerStatus is the compilers' state.
func CompilerStatus(env *host.Env) map[string]any {
	st := load(env)
	_, members, _ := groupEntry(env, CompilerGroup)
	if members == nil {
		members = []string{}
	}
	return map[string]any{"restricted": st.Compilers, "programs": describe(env, programs(env, compilerRe)), "group": CompilerGroup, "members": members}
}

// SetCompilers restricts (or opens) the compilers; users may use them
// while restricted (NimVor's build account always may).
func SetCompilers(ctx context.Context, env *host.Env, restricted bool, users []string) error {
	mu.Lock()
	defer mu.Unlock()
	st := load(env)
	paths := programs(env, compilerRe)
	if !restricted {
		err := restoreAll(ctx, env, &st, paths)
		st.Compilers = false
		if serr := save(env, st); err == nil {
			err = serr
		}
		return err
	}
	gid, err := ensureGroup(ctx, env, CompilerGroup)
	if err != nil {
		return err
	}
	list := []string{}
	seen := map[string]bool{}
	for _, u := range append(users, BuildAccount) {
		if seen[u] {
			continue
		}
		seen[u] = true
		if _, err := env.LookupUser(u); err == nil {
			list = append(list, u)
		}
	}
	sort.Strings(list)
	if out, err := env.Runner.Run(ctx, "gpasswd", "--members", strings.Join(list, ","), CompilerGroup); err != nil {
		return fmt.Errorf("the group's members could not be set: %s", host.Output(out))
	}
	st.Compilers = true
	err = restrictAll(ctx, env, &st, paths, gid, CompilerGroup, 0o750)
	if serr := save(env, st); err == nil {
		err = serr
	}
	return err
}

// AllowBuild lets NimVor's build account compile while compilers are
// restricted (called before a source build).
func AllowBuild(ctx context.Context, env *host.Env) error {
	if !load(env).Compilers {
		return nil
	}
	_, members, ok := groupEntry(env, CompilerGroup)
	if !ok {
		return nil
	}
	for _, m := range members {
		if m == BuildAccount {
			return nil
		}
	}
	if out, err := env.Runner.Run(ctx, "gpasswd", "--add", BuildAccount, CompilerGroup); err != nil {
		return fmt.Errorf("the build account could not be allowed to compile: %s", host.Output(out))
	}
	return nil
}

// Traceroute.

// TracerouteStatus is traceroute's state.
func TracerouteStatus(env *host.Env) map[string]any {
	st := load(env)
	progs := describe(env, programs(env, tracerouteRe))
	enabled := !st.Traceroute
	if st.Traceroute {
		for _, p := range progs {
			if p.Mode != "700" {
				enabled = true // changed since (a package update on RHEL): reapplied by the scheduler
			}
		}
	}
	return map[string]any{"enabled": enabled, "programs": progs}
}

// SetTraceroute enables (0755) or disables (root only, 0700) traceroute.
func SetTraceroute(ctx context.Context, env *host.Env, enabled bool) error {
	mu.Lock()
	defer mu.Unlock()
	st := load(env)
	paths := programs(env, tracerouteRe)
	var err error
	if enabled {
		err = restoreAll(ctx, env, &st, paths)
		st.Traceroute = false
	} else {
		err = restrictAll(ctx, env, &st, paths, 0, "root", 0o700)
		st.Traceroute = true
	}
	if serr := save(env, st); err == nil {
		err = serr
	}
	return err
}

// Reapply restricts programs again after package updates.
func Reapply(ctx context.Context, env *host.Env) error {
	mu.Lock()
	defer mu.Unlock()
	st := load(env)
	var err error
	if st.Compilers {
		if gid, _, ok := groupEntry(env, CompilerGroup); ok {
			err = restrictAll(ctx, env, &st, programs(env, compilerRe), gid, CompilerGroup, 0o750)
		}
	}
	if st.Traceroute {
		if e := restrictAll(ctx, env, &st, programs(env, tracerouteRe), 0, "root", 0o700); err == nil {
			err = e
		}
	}
	if serr := save(env, st); err == nil {
		err = serr
	}
	return err
}

// Fork bomb protection.

// ForkBombFile caps the processes of accounts in their sessions.
const ForkBombFile = "/etc/security/limits.d/90-nimvor-forkbomb.conf"

// Process limits of accounts (UID 1000 and up).
const (
	ForkSoft = 200
	ForkHard = 300
)

func forkBombBody() string {
	return fmt.Sprintf("# Managed by NimVor: Shell Fork Bomb Protection.\n"+
		"# Accounts (UID 1000 and up; not root or system accounts) may run at most\n"+
		"# this many processes in their sessions (SSH, cron, su).\n"+
		"1000: soft nproc %d\n1000: hard nproc %d\n", ForkSoft, ForkHard)
}

// ForkBombEnabled says whether the limits file is NimVor's and in place.
func ForkBombEnabled(env *host.Env) bool {
	b, err := env.Root.ReadFile(ForkBombFile)
	return err == nil && string(b) == forkBombBody()
}

// SetForkBomb writes or removes the limits file.
func SetForkBomb(env *host.Env, enabled bool) error {
	set := env.Changes("forkbomb")
	if enabled {
		set.Mkdir("/etc/security/limits.d", 0o755, agentfs.RootOwner)
		set.Write(ForkBombFile, []byte(forkBombBody()), 0o644, agentfs.RootOwner)
	} else if env.Root.Exists(ForkBombFile) {
		set.Remove(ForkBombFile)
	}
	_, err := set.Commit(nil)
	return err
}

// SMTP restrictions.

// SMTPUnit loads the nftables table at boot.
const (
	SMTPUnit     = "nimvor-smtp-restrict.service"
	smtpUnitPath = "/etc/systemd/system/" + SMTPUnit
)

// SMTPAllowed are the accounts that may connect to remote SMTP servers
// (those that exist): root, the mail server, Mailman and the panel.
var SMTPAllowed = []string{"root", "postfix", "exim", "Debian-exim", "mailman", "list", "nimvor"}

func smtpRules(env *host.Env) (string, []string) {
	var uids, names []string
	for _, u := range SMTPAllowed {
		if acct, err := env.LookupUser(u); err == nil {
			uids = append(uids, strconv.Itoa(acct.UID))
			names = append(names, u)
		}
	}
	if len(uids) == 0 {
		uids, names = []string{"0"}, []string{"root"}
	}
	return "# Managed by NimVor: SMTP restrictions. Outgoing connections to SMTP\n" +
		"# ports are only allowed for: " + strings.Join(names, ", ") + ".\n" +
		"table inet nimvor_smtp\n" +
		"delete table inet nimvor_smtp\n" +
		"table inet nimvor_smtp {\n" +
		"\tchain output {\n" +
		"\t\ttype filter hook output priority 0; policy accept;\n" +
		"\t\toifname \"lo\" accept\n" +
		"\t\tmeta skuid { " + strings.Join(uids, ", ") + " } accept\n" +
		"\t\ttcp dport { 25, 465, 587 } counter reject with tcp reset\n" +
		"\t}\n" +
		"}\n", names
}

func smtpUnit() string {
	return "# Managed by NimVor: SMTP restrictions.\n[Unit]\nDescription=NimVor SMTP restrictions (nftables)\nAfter=network-pre.target\nBefore=network.target\n\n" +
		"[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=/usr/sbin/nft -f " + execx.NftRulesFile + "\nExecStop=/usr/sbin/nft delete table inet nimvor_smtp\n\n[Install]\nWantedBy=multi-user.target\n"
}

// SMTPStatus is the restriction's state.
func SMTPStatus(ctx context.Context, env *host.Env) map[string]any {
	_, names := smtpRules(env)
	available := env.Runner.Available("nft")
	enabled := env.Root.Exists(execx.NftRulesFile) && env.Root.Exists(smtpUnitPath)
	active := false
	if enabled && available {
		_, err := env.Runner.Run(ctx, "nft", "list", "table", "inet", "nimvor_smtp")
		active = err == nil
	}
	return map[string]any{"enabled": enabled, "active": active, "available": available, "allowed": names, "ports": []int{25, 465, 587}}
}

// SetSMTP turns the restriction on (rules checked with nft -c first) or off.
func SetSMTP(ctx context.Context, env *host.Env, enabled bool) error {
	if !env.Runner.Available("nft") {
		return fmt.Errorf("nftables (nft) is not installed; install the nftables package first")
	}
	if !enabled {
		if env.Root.Exists(smtpUnitPath) {
			env.Systemctl(ctx, "disable", SMTPUnit)
			env.Systemctl(ctx, "stop", SMTPUnit)
		}
		env.Runner.Run(ctx, "nft", "delete", "table", "inet", "nimvor_smtp")
		set := env.Changes("smtp-restrict")
		for _, p := range []string{execx.NftRulesFile, smtpUnitPath} {
			if env.Root.Exists(p) {
				set.Remove(p)
			}
		}
		if _, err := set.Commit(nil); err != nil {
			return err
		}
		return env.DaemonReload(ctx)
	}
	rules, _ := smtpRules(env)
	set := env.Changes("smtp-restrict")
	set.Mkdir(filepath.Dir(execx.NftRulesFile), 0o755, agentfs.RootOwner)
	set.Write(execx.NftRulesFile, []byte(rules), 0o644, agentfs.RootOwner)
	set.Write(smtpUnitPath, []byte(smtpUnit()), 0o644, agentfs.RootOwner)
	applied, err := set.Commit(func() error {
		if out, err := env.Runner.Run(ctx, "nft", "-c", "-f", execx.NftRulesFile); err != nil {
			return fmt.Errorf("nft rejected the rules: %s", host.Output(out))
		}
		return nil
	})
	if err != nil {
		return err
	}
	err = env.DaemonReload(ctx)
	if err == nil {
		err = env.Systemctl(ctx, "enable", SMTPUnit)
	}
	if err == nil {
		err = env.Systemctl(ctx, "restart", SMTPUnit)
	}
	if err == nil {
		if out, lerr := env.Runner.Run(ctx, "nft", "list", "table", "inet", "nimvor_smtp"); lerr != nil {
			err = fmt.Errorf("the rules are not loaded: %s", host.Output(out))
		}
	}
	if err != nil {
		applied.Rollback()
		env.DaemonReload(ctx)
		env.Runner.Run(ctx, "nft", "delete", "table", "inet", "nimvor_smtp")
		return err
	}
	return denyNobodySubmit(ctx, env)
}

// denyNobodySubmit keeps the pseudo-user "nobody" from handing mail to
// Postfix's sendmail (authorized_submit_users), which the network rules
// do not cover. What else the setting holds is kept.
func denyNobodySubmit(ctx context.Context, env *host.Env) error {
	if !env.Runner.Available("postconf") {
		return nil
	}
	out, err := env.Runner.Run(ctx, "postconf", "-h", "authorized_submit_users")
	if err != nil {
		return nil // no Postfix configuration to read
	}
	cur := strings.TrimSpace(string(out))
	if NobodyDenied(cur) {
		return nil
	}
	val := "!nobody, " + cur
	if cur == "" {
		val = "!nobody, static:anyone"
	}
	if out, err := env.Runner.Run(ctx, "postconf", "-e", "authorized_submit_users="+val); err != nil {
		return fmt.Errorf("SMTP restrictions are on, but Postfix's authorized_submit_users could not be set: %s", host.Output(out))
	}
	return nil
}

var nobodyDeniedRe = regexp.MustCompile(`(^|[\s,])!\s*nobody\b`)

// NobodyDenied reports whether an authorized_submit_users value refuses
// "nobody".
func NobodyDenied(v string) bool { return nobodyDeniedRe.MatchString(v) }

// AllowPaths lets the agent write this package's files.
func AllowPaths(env *host.Env) {
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, ForkBombFile, "/etc/security/limits.d", execx.NftRulesFile, filepath.Dir(execx.NftRulesFile), smtpUnitPath)
}
