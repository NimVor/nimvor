package pkgs

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// The health check finds what an interrupted or failed package transaction
// left behind, and Repair fixes what can be fixed without removing
// anything:
//
//   - packages whose install script failed (RPM: "Error in POSTIN
//     scriptlet", from dnf's log; Debian: half-configured packages): the
//     package files are there but its setup (users, SELinux labels,
//     alternatives, services) is not. Repair reinstalls them (RPM) or
//     finishes their configuration (dpkg --configure -a).
//   - missing dependencies of installed packages (dnf check, apt-get
//     check): Repair installs them. On Debian only when apt would not
//     remove anything for it.
//   - duplicate package versions (an interrupted upgrade): removing the
//     older ones is a removal, so it only happens when the administrator
//     confirms it (RemoveDuplicates).
//
// Each failed script is reinstalled once (repair.json remembers it): a
// package whose script fails again is reported instead of being
// reinstalled at every start of the agent.

// Problem is one finding of the health check.
type Problem struct {
	// Kind: failed_script, missing_dependency, duplicate, half_configured.
	Kind    string `json:"kind"`
	Package string `json:"package"`
	Detail  string `json:"detail"`
	// Automatic: Repair fixes it without asking (installs only).
	Automatic bool `json:"automatic"`
	// Tried: an automatic repair was already tried and did not fix it.
	Tried bool `json:"tried,omitempty"`

	// The capability to install (missing_dependency) and when the script
	// failed (failed_script, the record of the repair).
	need   string
	failed time.Time
}

// Health is the result of the check.
type Health struct {
	Problems []Problem `json:"problems"`
	// Checked: false when the package manager could not be asked.
	Checked bool `json:"checked"`
}

// Repairable reports whether Repair would do something without asking.
func (h Health) Repairable() bool {
	for _, p := range h.Problems {
		if p.Automatic && !p.Tried {
			return true
		}
	}
	return false
}

// Duplicates reports whether duplicate packages were found.
func (h Health) Duplicates() bool {
	for _, p := range h.Problems {
		if p.Kind == "duplicate" {
			return true
		}
	}
	return false
}

// repairState remembers the reinstalls done for failed scripts.
type repairState struct {
	// package -> time of the script failure that was repaired.
	Reinstalled map[string]time.Time `json:"reinstalled"`
	// The problems the last automatic repair (at the agent's start) saw:
	// the same problems do not start it again.
	AutoSeen string `json:"auto_seen,omitempty"`
}

func repairStatePath(env *host.Env) string {
	return filepath.Join(env.Cfg.StateDir, "pkgs-repair.json")
}

func loadRepairState(env *host.Env) repairState {
	st := repairState{}
	if b, err := env.Root.ReadFile(repairStatePath(env)); err == nil {
		json.Unmarshal(b, &st)
	}
	if st.Reinstalled == nil {
		st.Reinstalled = map[string]time.Time{}
	}
	return st
}

func saveRepairState(env *host.Env, st repairState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	set := env.Changes("package-repair")
	set.Write(repairStatePath(env), b, 0o600, agentfs.RootOwner)
	_, err := set.Apply()
	return err
}

// CheckHealth looks for broken package state; it changes nothing.
func CheckHealth(ctx context.Context, env *host.Env) Health {
	if dnf(env) {
		return checkRPM(ctx, env)
	}
	return checkDpkg(ctx, env)
}

var (
	dnfMissingRe = regexp.MustCompile(`^(\S+) has missing requires of (\S+)(?:\s+(=|>=|<=|>|<)\s+(\S+))?`)
	dnfDupRe     = regexp.MustCompile(`^(\S+) is a duplicate with (\S+)`)
	dnfLogTimeRe = regexp.MustCompile(`^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:Z|[+-]\d\d:?\d\d))\s`)
)

func checkRPM(ctx context.Context, env *host.Env) Health {
	h := Health{Problems: []Problem{}}
	if !env.Runner.Available("dnf") || !env.Runner.Available("rpm") {
		return h
	}
	// dnf check exits 1 when it finds problems; it lists them either way.
	out, err := env.Runner.Run(ctx, "dnf", "-q", "check", "--dependencies", "--duplicates")
	if err != nil && len(strings.TrimSpace(string(out))) == 0 {
		return h
	}
	h.Checked = true
	seen := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if m := dnfMissingRe.FindStringSubmatch(l); m != nil {
			need := m[2]
			if m[3] == "=" {
				// The exact version the installed package needs.
				need = m[2] + "-" + m[4]
			}
			if seen["dep:"+need] {
				continue
			}
			seen["dep:"+need] = true
			// A rich dependency ("(a if b)") is not a package to install.
			auto := !strings.HasPrefix(need, "(") && !strings.HasPrefix(need, "-")
			h.Problems = append(h.Problems, Problem{Kind: "missing_dependency", Package: m[1], Detail: l, Automatic: auto, need: need})
		} else if m := dnfDupRe.FindStringSubmatch(l); m != nil {
			h.Problems = append(h.Problems, Problem{Kind: "duplicate", Package: m[1], Detail: l})
		}
	}
	h.Problems = append(h.Problems, failedScripts(ctx, env)...)
	return h
}

// failedScripts reads dnf's RPM log for install scripts that failed, and
// keeps those of packages that are installed and were not installed again
// since (their install time is not after the failure).
func failedScripts(ctx context.Context, env *host.Env) []Problem {
	logs, _ := env.Root.Glob("/var/log/dnf.rpm.log*")
	// Oldest first: dnf.rpm.log.4 … dnf.rpm.log.1, dnf.rpm.log.
	sort.Slice(logs, func(i, j int) bool { return logAge(logs[i]) > logAge(logs[j]) })
	type failure struct {
		at    time.Time
		stage string
	}
	last := map[string]failure{}
	for _, f := range logs {
		if strings.HasSuffix(f, ".gz") {
			continue
		}
		b, err := env.Root.ReadFile(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(strings.NewReader(string(b)))
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			l := sc.Text()
			m := rpmScriptletRe.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			t := dnfLogTimeRe.FindStringSubmatch(l)
			if t == nil {
				continue
			}
			at, err := parseLogTime(t[1])
			if err != nil {
				continue
			}
			// Removal scripts are not repaired by installing again.
			if m[1] == "PREUN" || m[1] == "POSTUN" || m[1] == "TRIGGERUN" || m[1] == "TRIGGERPOSTUN" {
				continue
			}
			last[m[2]] = failure{at, m[1]}
		}
	}
	if len(last) == 0 {
		return nil
	}
	names := make([]string, 0, len(last))
	for n := range last {
		names = append(names, n)
	}
	sort.Strings(names)
	installedAt := installTimes(ctx, env, names)
	st := loadRepairState(env)
	var res []Problem
	for _, n := range names {
		f := last[n]
		it, ok := installedAt[n]
		if !ok || it.After(f.at) {
			continue // not installed, or installed again since
		}
		p := Problem{Kind: "failed_script", Package: n, Automatic: true, failed: f.at,
			Detail: fmt.Sprintf("its %s script failed on %s; the package is installed without the setup that script does", f.stage, f.at.UTC().Format("2006-01-02 15:04 UTC"))}
		if done, ok := st.Reinstalled[n]; ok && !done.Before(f.at) {
			p.Tried = true
			p.Detail += " (reinstalling it did not fix it)"
		}
		res = append(res, p)
	}
	return res
}

// logAge orders rotated logs: dnf.rpm.log is 0, dnf.rpm.log.N is N.
func logAge(path string) int {
	ext := filepath.Ext(path)
	if n, err := strconv.Atoi(strings.TrimPrefix(ext, ".")); err == nil {
		return n
	}
	return 0
}

func parseLogTime(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15:04:05Z0700", "2006-01-02T15:04:05Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown time format: %s", s)
}

// installTimes returns the latest install time of each installed package.
func installTimes(ctx context.Context, env *host.Env, names []string) map[string]time.Time {
	res := map[string]time.Time{}
	// rpm exits 1 when some are not installed but prints the others.
	b, _ := env.Runner.Run(ctx, "rpm", append([]string{"-q", "--qf", `%{NAME}\t%{INSTALLTIME}\n`, "--"}, names...)...)
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(l, "\t")
		if len(f) != 2 {
			continue
		}
		sec, err := strconv.ParseInt(strings.TrimSpace(f[1]), 10, 64)
		if err != nil {
			continue
		}
		if t := time.Unix(sec, 0); t.After(res[f[0]]) {
			res[f[0]] = t
		}
	}
	return res
}

var dpkgAuditRe = regexp.MustCompile(`^\s(\S+)\s+(.*)$`)

func checkDpkg(ctx context.Context, env *host.Env) Health {
	h := Health{Problems: []Problem{}}
	if !env.Runner.Available("dpkg") || !env.Runner.Available("apt-get") {
		return h
	}
	// dpkg --audit prints the packages that are not fully installed (and
	// exits 1 then on recent dpkg versions).
	out, _ := env.Runner.Run(ctx, "dpkg", "--audit")
	h.Checked = true
	for _, l := range strings.Split(string(out), "\n") {
		if m := dpkgAuditRe.FindStringSubmatch(l); m != nil && validPkgName(m[1]) {
			h.Problems = append(h.Problems, Problem{Kind: "half_configured", Package: m[1], Detail: "not fully installed: " + strings.TrimSpace(m[2]), Automatic: true})
		}
	}
	if out, err := env.Runner.Run(ctx, "apt-get", "-s", "check"); err != nil {
		h.Problems = append(h.Problems, Problem{Kind: "missing_dependency", Package: "", Detail: firstLines(string(out), 6), Automatic: true})
	}
	return h
}

var pkgNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*(:[a-z0-9]+)?$`)

func validPkgName(s string) bool { return pkgNameRe.MatchString(s) }

func firstLines(s string, n int) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	if len(ls) > n {
		ls = ls[:n]
	}
	return strings.Join(ls, "\n")
}

// signature identifies the problems an automatic repair would work on.
func (h Health) signature() string {
	var ks []string
	for _, p := range h.Problems {
		if p.Automatic && !p.Tried {
			ks = append(ks, p.Kind+" "+p.Package+" "+p.need)
		}
	}
	sort.Strings(ks)
	return strings.Join(ks, "\n")
}

// AutoRepairDue reports whether the agent should start a repair by itself:
// there is something to repair without asking, and not exactly the same
// problems an earlier automatic repair could not fix.
func AutoRepairDue(env *host.Env, h Health) bool {
	return h.Repairable() && loadRepairState(env).AutoSeen != h.signature()
}

// MarkAutoRepair records the problems an automatic repair started on.
func MarkAutoRepair(env *host.Env, h Health) {
	st := loadRepairState(env)
	st.AutoSeen = h.signature()
	saveRepairState(env, st)
}

// RepairOptions: what Repair may do beyond installing.
type RepairOptions struct {
	// RemoveDuplicates removes the older versions of duplicate packages
	// (dnf remove --duplicates). Only on the administrator's confirmation.
	RemoveDuplicates bool
}

// maxReinstalls bounds one repair (the rest is done by the next one).
const maxReinstalls = 25

// Repair fixes what CheckHealth found, writing its steps to log, and
// returns the health afterwards. It never removes packages unless
// opts.RemoveDuplicates, and never forces dependencies or skips checks.
func Repair(ctx context.Context, env *host.Env, log io.Writer, opts RepairOptions) (Health, error) {
	h := CheckHealth(ctx, env)
	if !h.Checked {
		return h, fmt.Errorf("the package manager could not be asked about the installed packages")
	}
	if len(h.Problems) == 0 {
		fmt.Fprintln(log, "No package problems found.")
		return h, nil
	}
	fmt.Fprintf(log, "Found %d package problem(s):\n", len(h.Problems))
	for _, p := range h.Problems {
		fmt.Fprintf(log, "  - %s %s: %s\n", p.Kind, p.Package, p.Detail)
	}
	var errs []string
	if dnf(env) {
		errs = repairRPM(ctx, env, log, h, opts)
	} else {
		errs = repairDpkg(ctx, env, log, h)
	}
	after := CheckHealth(ctx, env)
	switch {
	case len(after.Problems) == 0:
		fmt.Fprintln(log, "\nAll package problems are repaired.")
	default:
		fmt.Fprintf(log, "\nStill %d package problem(s):\n", len(after.Problems))
		for _, p := range after.Problems {
			fmt.Fprintf(log, "  - %s %s: %s\n", p.Kind, p.Package, p.Detail)
		}
		if after.Duplicates() && !opts.RemoveDuplicates {
			fmt.Fprintln(log, "Duplicate packages are left by an interrupted update. Removing the older versions removes packages, so it is not done automatically: confirm it with \"Remove duplicates\" on the Packages page (dnf remove --duplicates).")
		}
	}
	if len(errs) > 0 {
		return after, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return after, nil
}

func repairRPM(ctx context.Context, env *host.Env, log io.Writer, h Health, opts RepairOptions) []string {
	var errs []string
	run := func(op string, pkgs []string, args ...string) error {
		o := execx.Opts{Output: log, Timeout: -1, Env: packageEnv}
		return transact(ctx, env, log, op, pkgs, func(ctx context.Context) ([]byte, error) {
			return env.Runner.RunWith(ctx, "dnf", o, args...)
		})
	}
	if opts.RemoveDuplicates && h.Duplicates() {
		fmt.Fprintln(log, "\n==> Removing the older versions of duplicate packages (confirmed by the administrator)")
		if err := run("remove duplicates", nil, "remove", "-y", "-q", "--duplicates"); err != nil {
			errs = append(errs, "removing duplicates: "+err.Error())
		}
	}
	var needs []string
	for _, p := range h.Problems {
		if p.Kind == "missing_dependency" && p.Automatic {
			needs = appendOnce(needs, p.need)
		}
	}
	if len(needs) > 0 {
		fmt.Fprintf(log, "\n==> Installing missing dependencies: %s\n", strings.Join(needs, ", "))
		// Dependencies may come from EPEL/CRB (prepared on failure).
		args := append([]string{"install", "-y", "-q", "--setopt=install_weak_deps=False", "--"}, needs...)
		if err := run("install missing dependencies", needs, args...); err != nil {
			errs = append(errs, "installing missing dependencies: "+err.Error())
		}
	}
	st := loadRepairState(env)
	n := 0
	for _, p := range h.Problems {
		if p.Kind != "failed_script" || p.Tried || !validPkgName(p.Package) {
			continue
		}
		if n++; n > maxReinstalls {
			fmt.Fprintf(log, "More packages to reinstall; the next repair continues.\n")
			break
		}
		// One at a time: a package the repositories no longer offer in
		// its installed version does not stop the others.
		fmt.Fprintf(log, "\n==> Reinstalling %s to run its install script again\n", p.Package)
		err := run("reinstall", []string{p.Package}, "reinstall", "-y", "-q", "--", p.Package)
		st.Reinstalled[p.Package] = time.Now()
		if err != nil {
			errs = append(errs, "reinstalling "+p.Package+": "+err.Error())
		}
	}
	if n > 0 {
		if err := saveRepairState(env, st); err != nil {
			fmt.Fprintf(log, "Could not record the repair: %v\n", err)
		}
	}
	return errs
}

func repairDpkg(ctx context.Context, env *host.Env, log io.Writer, h Health) []string {
	var errs []string
	o := execx.Opts{Output: log, Timeout: -1, Env: packageEnv}
	half, deps := false, false
	for _, p := range h.Problems {
		half = half || p.Kind == "half_configured"
		deps = deps || p.Kind == "missing_dependency"
	}
	if half {
		fmt.Fprintln(log, "\n==> Finishing the configuration of packages (dpkg --configure -a)")
		err := transact(ctx, env, log, "configure packages", nil, func(ctx context.Context) ([]byte, error) {
			return env.Runner.RunWith(ctx, "dpkg", o, "--configure", "-a")
		})
		if err != nil {
			errs = append(errs, "dpkg --configure -a: "+err.Error())
		}
	}
	if deps {
		args := append([]string{"install", "-f", "-y", "-q", "--no-install-recommends", "-o", "Dpkg::Options::=--force-confold"}, aptLockWait...)
		sim, _ := env.Runner.Run(ctx, "apt-get", append([]string{"-s"}, args...)...)
		if rm := ExtraRemovals(string(sim), nil); len(rm) > 0 {
			msg := "fixing the dependencies would remove " + strings.Join(rm, ", ") + "; not done automatically (apt-get install -f)"
			fmt.Fprintln(log, msg)
			errs = append(errs, msg)
		} else {
			fmt.Fprintln(log, "\n==> Installing missing dependencies (apt-get install -f)")
			err := transact(ctx, env, log, "install missing dependencies", nil, func(ctx context.Context) ([]byte, error) {
				return env.Runner.RunWith(ctx, "apt-get", o, args...)
			})
			if err != nil {
				errs = append(errs, "apt-get install -f: "+err.Error())
			}
		}
	}
	return errs
}
