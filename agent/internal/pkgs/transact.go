package pkgs

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Every package operation goes through transact: it runs the package
// manager, and when that fails it diagnoses the output and recovers where
// that is known to be safe:
//
//   - another package manager holds the lock: wait (backoff), try again;
//   - repository or network errors: wait, refresh the index, try again;
//   - dpkg was interrupted: dpkg --configure -a, try again;
//   - the RPM database is damaged: rpm --rebuilddb, try again;
//   - a package script failed because a lock was busy: wait, try again.
//
// Anything else (a package that does not exist, unsatisfiable
// dependencies, a failed signature check, a package script that fails, a
// full disk) is reported at once with its cause. WhaCP never disables
// signature checks, forces dependencies (--nodeps, --force-all) or removes
// packages to recover. At most maxAttempts runs.

const maxAttempts = 3

// retryDelays are the waits before the second and third attempt (tests
// shorten them).
var retryDelays = []time.Duration{10 * time.Second, 30 * time.Second}

// aptLockWait makes apt wait for the dpkg lock instead of failing at once.
var aptLockWait = []string{"-o", "DPkg::Lock::Timeout=300"}

// packageEnv: needrestart (Ubuntu) only lists services to restart; it must
// not restart services (WhaCP's own agent among them) during a job.
var packageEnv = []string{"NEEDRESTART_MODE=l", "NEEDRESTART_SUSPEND=1", "APT_LISTCHANGES_FRONTEND=none"}

func managerName(env *host.Env) string {
	if dnf(env) {
		return "dnf"
	}
	return "apt"
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// transact runs one package operation with diagnosis and recovery.
func transact(ctx context.Context, env *host.Env, log io.Writer, op string, pkgs []string, run func(context.Context) ([]byte, error)) error {
	e := &Error{Op: op, Packages: pkgs, Manager: managerName(env)}
	repaired := map[Category]bool{}
	for attempt := 1; ; attempt++ {
		e.Attempts = attempt
		out, err := run(ctx)
		if err == nil {
			if attempt > 1 {
				fmt.Fprintf(log, "%s succeeded on attempt %d.\n", strings.ToUpper(op[:1])+op[1:], attempt)
			}
			return nil
		}
		e.Diagnosis = Diagnose(string(out))
		if ctx.Err() != nil {
			fmt.Fprintf(log, "\n%s\n", err)
			return fmt.Errorf("%s: %w", op, err)
		}
		d := e.Diagnosis
		fmt.Fprintf(log, "\nAttempt %d/%d failed: %s [%s].\n", attempt, maxAttempts, d.Summary(), strings.ToUpper(string(d.Category)))
		if attempt >= maxAttempts {
			break
		}
		var step string
		var stepErr error
		switch {
		case d.Category == CatInterrupted && !repaired[CatInterrupted] && !dnf(env):
			repaired[CatInterrupted] = true
			step = "finished the interrupted package configuration (dpkg --configure -a)"
			_, stepErr = env.Runner.RunWith(ctx, "dpkg", execx.Opts{Output: log, Timeout: -1, Env: packageEnv}, "--configure", "-a")
		case d.Category == CatRPMDB && !repaired[CatRPMDB] && dnf(env):
			repaired[CatRPMDB] = true
			step = "rebuilt the RPM database (rpm --rebuilddb)"
			_, stepErr = env.Runner.RunWith(ctx, "rpm", execx.Opts{Output: log, Timeout: -1}, "--rebuilddb")
		case d.Category == CatDependency && dnf(env) && !repaired[CatDependency] && len(nothingProvides(string(out))) > 0:
			// EPEL's packages need libraries from CRB (e.g. opendkim:
			// libmilter, libmemcached); the availability check only saw
			// that the package itself exists.
			repaired[CatDependency] = true
			if prepareRHELRepos(ctx, env, log, []string{"epel-dependencies"}) {
				step = "enabled the repositories EPEL's packages depend on"
			}
		case d.Category == CatRepository:
			if stepErr = sleepCtx(ctx, retryDelays[attempt-1]); stepErr == nil {
				step = "waited and refreshed the package index"
				stepErr = refreshIndex(ctx, env, log)
			}
		case d.transient():
			step = fmt.Sprintf("waited %s", retryDelays[attempt-1])
			stepErr = sleepCtx(ctx, retryDelays[attempt-1])
		}
		if step == "" {
			break // not something to try again
		}
		if stepErr != nil {
			e.Recovery = append(e.Recovery, step+" (failed)")
			fmt.Fprintf(log, "Automatic recovery failed: %s: %v\n", step, stepErr)
			break
		}
		e.Recovery = append(e.Recovery, step)
		fmt.Fprintf(log, "Automatic recovery: %s; trying again.\n", step)
	}
	d := e.Diagnosis
	if d.Category == CatDependency {
		// The consistency of what is installed explains many conflicts.
		if dnf(env) {
			if out, err := env.Runner.Run(ctx, "dnf", "-q", "check"); err != nil || len(strings.TrimSpace(string(out))) > 0 {
				d.Detail += "\n(dnf check)\n" + strings.TrimSpace(string(out))
			}
		} else if out, err := env.Runner.Run(ctx, "apt-get", "-s", "check"); err != nil {
			d.Detail += "\n(apt-get check)\n" + strings.TrimSpace(string(out))
		}
		if dnf(env) {
			d.Detail += whoProvides(ctx, env, nothingProvides(d.Detail))
		}
		e.Diagnosis = d
	}
	fmt.Fprintf(log, "\nDiagnosis: %s [%s]\n", d.Summary(), strings.ToUpper(string(d.Category)))
	if len(e.Recovery) > 0 {
		fmt.Fprintf(log, "Automatic recovery tried: %s\n", strings.Join(e.Recovery, "; "))
	}
	if d.Detail != "" {
		fmt.Fprintf(log, "Relevant output:\n%s\n", d.Detail)
	}
	if h := d.Hint(); h != "" {
		fmt.Fprintf(log, "What to do: %s\n", h)
	}
	return e
}

// refreshIndex downloads fresh repository metadata (no recovery: the
// caller's transact retries).
func refreshIndex(ctx context.Context, env *host.Env, log io.Writer) error {
	opts := execx.Opts{Output: log, Timeout: -1, Env: packageEnv}
	if dnf(env) {
		_, err := env.Runner.RunWith(ctx, "dnf", opts, "-q", "makecache", "--refresh")
		return err
	}
	_, err := env.Runner.RunWith(ctx, "apt-get", opts, append([]string{"update", "-q"}, aptLockWait...)...)
	return err
}

// Unavailable returns the packages the enabled repositories do not offer
// (and that are not installed); ok is false when that cannot be told.
func Unavailable(ctx context.Context, env *host.Env, pkgs []string) ([]string, bool) {
	return unavailable(ctx, env, pkgs)
}

// unavailable returns the packages the enabled repositories do not offer
// (and that are not installed). ok is false when that cannot be told; the
// installation then reports what the package manager says.
func unavailable(ctx context.Context, env *host.Env, pkgs []string) (missing []string, ok bool) {
	have := installed(ctx, env, pkgs, true)
	var query []string
	for _, p := range pkgs {
		if _, inst := have[p]; !inst {
			query = append(query, p)
		}
	}
	if len(query) == 0 {
		return nil, true
	}
	if dnf(env) {
		// dnf list exits 1 ("No matching Packages to list") when none
		// matches, else lists those that do ("name.arch version repo").
		out, err := env.Runner.Run(ctx, "dnf", append([]string{"-q", "list", "--available", "--installed", "--"}, query...)...)
		if err != nil {
			if strings.Contains(string(out), "No matching Packages") {
				return query, true
			}
			return nil, false
		}
		found := map[string]bool{}
		for _, l := range strings.Split(string(out), "\n") {
			if f := strings.Fields(l); len(f) == 3 {
				if i := strings.LastIndex(f[0], "."); i > 0 {
					found[f[0][:i]] = true
				}
			}
		}
		if len(found) == 0 {
			return nil, false
		}
		for _, p := range query {
			if !found[p] {
				missing = append(missing, p)
			}
		}
		return missing, true
	}
	if !env.Runner.Available("apt-cache") {
		return nil, false
	}
	out, err := env.Runner.Run(ctx, "apt-cache", append([]string{"policy", "--"}, query...)...)
	if err != nil {
		return nil, false
	}
	// Missing: no candidate, or "Unable to locate package"; anything else
	// (including output WhaCP does not recognise) counts as available.
	cands := parsePolicy(string(out))
	for _, p := range query {
		c, listed := cands[p]
		if (listed && (c == "" || c == "(none)")) || strings.Contains(string(out), "Unable to locate package "+p) {
			missing = append(missing, p)
		}
	}
	return missing, true
}

var policyHeadRe = regexp.MustCompile(`^(\S+):$`)
var policyCandRe = regexp.MustCompile(`^\s+Candidate:\s*(\S+)`)

// parsePolicy reads `apt-cache policy` (package -> candidate version).
func parsePolicy(out string) map[string]string {
	res := map[string]string{}
	cur := ""
	for _, l := range strings.Split(out, "\n") {
		if m := policyHeadRe.FindStringSubmatch(l); m != nil {
			cur = strings.SplitN(m[1], ":", 2)[0] // "foo:amd64" -> foo
			res[cur] = ""
			continue
		}
		if m := policyCandRe.FindStringSubmatch(l); m != nil && cur != "" {
			res[cur] = m[1]
		}
	}
	return res
}

// ensureAvailable checks that the repositories offer pkgs; on RHEL it
// first enables the repositories missing packages can come from (EPEL
// with CRB, Remi for PHP versions). Packages still missing are reported
// as not available, naming the enabled repositories.
func ensureAvailable(ctx context.Context, env *host.Env, log io.Writer, pkgs []string) error {
	missing, ok := unavailable(ctx, env, pkgs)
	if !ok || len(missing) == 0 {
		return nil
	}
	if dnf(env) && prepareRHELRepos(ctx, env, log, missing) {
		if missing, ok = unavailable(ctx, env, pkgs); !ok || len(missing) == 0 {
			return nil
		}
	}
	repos := strings.Join(enabledRepos(ctx, env), ", ")
	if repos == "" {
		repos = "(none could be listed)"
	}
	e := &Error{Op: "install", Packages: pkgs, Manager: managerName(env), Diagnosis: Diagnosis{Category: CatNotAvailable, Missing: missing, ExitCode: -1,
		Detail: "Enabled repositories: " + repos}}
	fmt.Fprintf(log, "\nDiagnosis: %s [%s]\n%s\nWhat to do: %s\n", e.Diagnosis.Summary(), strings.ToUpper(string(CatNotAvailable)), e.Diagnosis.Detail, e.Diagnosis.Hint())
	return e
}

// enabledRepos lists the enabled repository IDs.
func enabledRepos(ctx context.Context, env *host.Env) []string {
	var ids []string
	if dnf(env) {
		out, err := env.Runner.Run(ctx, "dnf", "-q", "repolist", "--enabled")
		if err != nil {
			return nil
		}
		for _, l := range strings.Split(string(out), "\n") {
			f := strings.Fields(l)
			if len(f) > 0 && f[0] != "repo" {
				ids = append(ids, f[0])
			}
		}
		return ids
	}
	out, err := env.Runner.Run(ctx, "apt-cache", "policy")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		if f := strings.Fields(l); len(f) >= 3 && strings.Contains(f[1], "://") {
			r := f[1] + " " + f[2]
			if !seen[r] {
				seen[r] = true
				ids = append(ids, r)
			}
		}
	}
	return ids
}

var remiPHPRe = regexp.MustCompile(`^php[0-9]{2}-php-`)

// prepareRHELRepos enables, for missing packages, the repositories WhaCP
// installs from on RHEL: EPEL (and CRB, which EPEL needs) for general
// packages, Remi for PHP versions. Only what is missing triggers it, and
// only these well-known repositories; it reports whether it changed
// anything.
func prepareRHELRepos(ctx context.Context, env *host.Env, log io.Writer, missing []string) bool {
	needRemi, needEPEL, needRspamd := false, false, false
	for _, p := range missing {
		switch {
		case remiPHPRe.MatchString(p):
			needRemi = true
		case p == "rspamd" || strings.HasPrefix(p, "rspamd-"):
			// Not in EPEL for EL9 and later: the Rspamd project's own
			// repository (signed; dnf imports its key from rspamd.com).
			needRspamd = true
			needEPEL = true
		default:
			needEPEL = true
		}
	}
	info := env.Platform.Info()
	major := strings.SplitN(info.VersionID, ".", 2)[0]
	if !regexp.MustCompile(`^[0-9]{1,2}$`).MatchString(major) {
		return false
	}
	enabled := map[string]bool{}
	for _, id := range enabledRepos(ctx, env) {
		enabled[strings.ToLower(id)] = true
	}
	inst := installed(ctx, env, []string{"epel-release", "remi-release", "dnf-plugins-core"}, true)
	changed := false
	opts := execx.Opts{Output: log, Timeout: -1}
	install := func(what string, args ...string) bool {
		fmt.Fprintf(log, "Preparing repositories: %s.\n", what)
		err := transact(ctx, env, log, "install "+what, nil, func(ctx context.Context) ([]byte, error) {
			return env.Runner.RunWith(ctx, "dnf", opts, append([]string{"install", "-y", "-q", "--"}, args...)...)
		})
		if err != nil {
			fmt.Fprintf(log, "Could not add %s: %v\n", what, err)
			return false
		}
		changed = true
		return true
	}
	enable := func(repo string) {
		if enabled[repo] {
			return
		}
		if _, ok := inst["dnf-plugins-core"]; !ok && !install("dnf-plugins-core (dnf config-manager)", "dnf-plugins-core") {
			return
		}
		inst["dnf-plugins-core"] = "1"
		fmt.Fprintf(log, "Preparing repositories: enabling %s.\n", repo)
		if out, err := env.Runner.RunWith(ctx, "dnf", opts, "config-manager", "--set-enabled", repo); err != nil {
			fmt.Fprintf(log, "Could not enable %s: %s\n", repo, host.Output(out))
			return
		}
		enabled[repo] = true
		changed = true
	}
	if needEPEL {
		if _, ok := inst["epel-release"]; !ok {
			if info.ID == "rhel" {
				install("the EPEL repository", "https://dl.fedoraproject.org/pub/epel/epel-release-latest-"+major+".noarch.rpm")
			} else {
				install("the EPEL repository (epel-release)", "epel-release")
			}
		}
		// EPEL's packages build on CRB (CodeReady Builder).
		if info.ID == "rhel" {
			if !enabled["codeready-builder-for-rhel-"+major+"-x86_64-rpms"] {
				fmt.Fprintf(log, "Note: EPEL needs CodeReady Builder on RHEL: subscription-manager repos --enable codeready-builder-for-rhel-%s-$(arch)-rpms\n", major)
			}
		} else if !enabled["crb"] && !enabled["powertools"] {
			enable("crb")
		}
	}
	if needRspamd && !enabled["rspamd"] {
		if _, ok := inst["dnf-plugins-core"]; ok || install("dnf-plugins-core (dnf config-manager)", "dnf-plugins-core") {
			inst["dnf-plugins-core"] = "1"
			url := "https://rspamd.com/rpm-stable/centos-" + major + "/rspamd.repo"
			fmt.Fprintf(log, "Preparing repositories: adding the Rspamd project's repository (%s).\n", url)
			if out, err := env.Runner.RunWith(ctx, "dnf", opts, "config-manager", "--add-repo", url); err != nil {
				fmt.Fprintf(log, "Could not add the Rspamd repository: %s\n", host.Output(out))
			} else {
				enabled["rspamd"] = true
				changed = true
			}
		}
	}
	if needRemi {
		if _, ok := inst["remi-release"]; !ok {
			install("Remi's repository for PHP versions (remi-release)", "https://rpms.remirepo.net/enterprise/remi-release-"+major+".rpm")
		}
		enable("remi-safe")
	}
	if changed {
		refreshIndex(ctx, env, log)
	}
	return changed
}

var nothingProvidesRe = regexp.MustCompile(`nothing provides (\S+) needed by (\S+)`)

// nothingProvides lists the capabilities DNF found no package for.
func nothingProvides(out string) []string {
	var caps []string
	for _, m := range nothingProvidesRe.FindAllStringSubmatch(out, -1) {
		caps = appendOnce(caps, m[1])
	}
	return caps
}

// whoProvides names, for capabilities no enabled repository provides, the
// configured but disabled repositories that do (read-only query), so the
// administrator knows which one to enable.
func whoProvides(ctx context.Context, env *host.Env, caps []string) string {
	if len(caps) == 0 {
		return ""
	}
	if len(caps) > 5 {
		caps = caps[:5]
	}
	var b strings.Builder
	b.WriteString("\n(enabled repositories: " + strings.Join(enabledRepos(ctx, env), ", ") + ")")
	for _, c := range caps {
		out, err := env.Runner.Run(ctx, "dnf", append([]string{"-q", "repoquery", "--enablerepo=*", "--setopt=skip_if_unavailable=True", "--qf", "%{name} from %{repoid}", "--whatprovides", "--"}, c)...)
		found := strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", "; ")), " ")
		if err != nil || found == "" {
			found = "no configured repository"
		}
		fmt.Fprintf(&b, "\n%s is provided by: %s", c, found)
	}
	return b.String()
}
