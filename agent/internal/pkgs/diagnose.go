package pkgs

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Category is what kind of package manager failure an output shows.
type Category string

const (
	// The package manager could not switch to its download user or run a
	// maintainer script in its own SELinux domain: a sandbox around it.
	CatPrivilege Category = "privilege_transition"
	// Another package manager holds the lock.
	CatLocked Category = "locked"
	// A package does not exist in the enabled repositories.
	CatNotAvailable Category = "not_available"
	// Repository metadata or downloads failed (network, mirror, proxy).
	CatRepository Category = "repository"
	// A signature or key check failed (never bypassed).
	CatSignature Category = "signature"
	// The packages' dependencies cannot be satisfied.
	CatDependency Category = "dependency"
	// A package's own install or removal script failed.
	CatScriptlet Category = "package_script"
	// dpkg was interrupted earlier and must finish configuring.
	CatInterrupted Category = "interrupted"
	// The RPM database is damaged.
	CatRPMDB    Category = "rpm_database"
	CatDiskFull Category = "disk_full"
	CatUnknown  Category = "unknown"
)

// Diagnosis describes a failed package manager run.
type Diagnosis struct {
	Category Category `json:"category"`
	// Package and Stage (e.g. "PREIN", "post-installation") when a
	// package's script failed; ExitCode its exit status (-1: unknown).
	Package  string `json:"package,omitempty"`
	Stage    string `json:"stage,omitempty"`
	ExitCode int    `json:"exit_code"`
	// Packages that are not available (CatNotAvailable).
	Missing []string `json:"missing,omitempty"`
	// Detail: the output lines that show the error.
	Detail string `json:"detail,omitempty"`
}

var (
	rpmScriptletRe  = regexp.MustCompile(`Error in (PRETRANS|PREIN|POSTIN|PREUN|POSTUN|POSTTRANS|TRIGGERIN|TRIGGERUN|TRIGGERPOSTUN) scriptlet in rpm package (\S+)`)
	rpmScriptExitRe = regexp.MustCompile(`%(pretrans|prein|post|preun|postun|posttrans|triggerprein|triggerin|triggerun|triggerpostun)\(([^)]+)\) scriptlet failed, (?:exit status|signal) (\d+)`)
	dpkgScriptRe    = regexp.MustCompile(`(?:installed|new) (\S+) package (pre-installation|post-installation|pre-removal|post-removal) script subprocess returned error exit status (\d+)`)
	aptLocateRe     = regexp.MustCompile(`E: Unable to locate package (\S+)`)
	aptNoCandRe     = regexp.MustCompile(`E: Package '([^']+)' has no installation candidate`)
	dnfNoMatchRe    = regexp.MustCompile(`(?:No match for argument|Unable to find a match): (\S+)`)
	errorLineRe     = regexp.MustCompile(`(?i)^(E:|Error|error:|dpkg: error|\s*Problem|\s*- |.*(scriptlet|failed|denied|not permitted|cannot|could not|unable to|no space)).*`)
)

// Diagnose classifies a package manager's output. The most specific
// recognised cause wins: a script failure explains a "transaction failed".
func Diagnose(out string) Diagnosis {
	d := Diagnosis{Category: CatUnknown, ExitCode: -1}
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(out, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("seteuid", "setresuid", "Failed to set new user ids", "setegid", "setgroups"):
		d.Category = CatPrivilege
	case rpmScriptletRe.MatchString(out) || rpmScriptExitRe.MatchString(out) || dpkgScriptRe.MatchString(out):
		d.Category = CatScriptlet
		if m := rpmScriptletRe.FindStringSubmatch(out); m != nil {
			d.Stage, d.Package = m[1], m[2]
		}
		if m := rpmScriptExitRe.FindStringSubmatch(out); m != nil {
			if d.Stage == "" {
				d.Stage = strings.ToUpper(m[1])
				if d.Stage == "POST" {
					d.Stage = "POSTIN"
				}
			}
			if d.Package == "" {
				d.Package = m[2]
			}
			d.ExitCode, _ = strconv.Atoi(m[3])
		}
		if m := dpkgScriptRe.FindStringSubmatch(out); m != nil && d.Package == "" {
			d.Package, d.Stage = m[1], m[2]
			d.ExitCode, _ = strconv.Atoi(m[3])
		}
	case has("dpkg was interrupted", "dpkg --configure -a"):
		d.Category = CatInterrupted
	case has("Could not get lock", "Unable to acquire the dpkg frontend lock", "is another process using it", "Waiting for process with pid", "lock held by", "Could not open lock file"):
		d.Category = CatLocked
	case has("rpmdb open failed", "DB_RUNRECOVERY", "Error: rpmdb", "rpmdb: ", "error: db5 error", "sqlite failure", "BDB0", "Thread died in Berkeley DB"):
		d.Category = CatRPMDB
	case has("No space left on device", "You don't have enough free space", "needs more space on the", "Disk Requirements"):
		d.Category = CatDiskFull
	case has("NO_PUBKEY", "GPG check FAILED", "GPG error", "is not signed", "signatures couldn't be verified", "public key for", "BADSIG", "The repository", "is no longer signed"):
		if has("NO_PUBKEY", "GPG check FAILED", "BADSIG", "public key for", "signatures couldn't be verified", "is not signed") {
			d.Category = CatSignature
		} else {
			d.Category = CatRepository
		}
	case aptLocateRe.MatchString(out) || aptNoCandRe.MatchString(out) || dnfNoMatchRe.MatchString(out):
		d.Category = CatNotAvailable
		for _, re := range []*regexp.Regexp{aptLocateRe, aptNoCandRe, dnfNoMatchRe} {
			for _, m := range re.FindAllStringSubmatch(out, -1) {
				d.Missing = appendOnce(d.Missing, m[1])
			}
		}
	case has("Unmet dependencies", "held broken packages", "nothing provides", "conflicting requests", "conflicts with", "Depends:", "but it is not installable", "Problem: "):
		d.Category = CatDependency
	case has("Failed to fetch", "Temporary failure resolving", "Could not resolve", "Failed to download metadata", "Cannot download repomd.xml", "Curl error", "Cannot retrieve", "Connection timed out", "Connection refused", "Errors during downloading metadata", "Hash Sum mismatch", "is not valid yet", "Some index files failed to download", "Failed to download packages", "Cannot download", "Method http has died", "Sub-process http returned an error code", "Sub-process https returned an error code"):
		d.Category = CatRepository
	}
	d.Detail = errorLines(out)
	return d
}

func appendOnce(l []string, s string) []string {
	for _, x := range l {
		if x == s {
			return l
		}
	}
	return append(l, s)
}

// errorLines keeps the lines that show the error (at most 25), else the
// output's end.
func errorLines(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var keep []string
	for _, l := range lines {
		if errorLineRe.MatchString(l) && strings.TrimSpace(l) != "" {
			keep = append(keep, strings.TrimRight(l, " \r"))
		}
	}
	if len(keep) == 0 {
		start := len(lines) - 15
		if start < 0 {
			start = 0
		}
		keep = lines[start:]
	}
	if len(keep) > 25 {
		keep = keep[len(keep)-25:]
	}
	return strings.Join(keep, "\n")
}

// Summary is one sentence for the user.
func (d Diagnosis) Summary() string {
	switch d.Category {
	case CatPrivilege:
		return "the package manager was prevented from switching to its download user or running a package script in its own security context (a sandbox around it)"
	case CatLocked:
		return "another package manager is running and holds the package lock"
	case CatNotAvailable:
		if len(d.Missing) > 0 {
			return strings.Join(d.Missing, ", ") + " is not available from the enabled repositories"
		}
		return "a package is not available from the enabled repositories"
	case CatRepository:
		return "the package repositories could not be reached or returned invalid data"
	case CatSignature:
		return "a package or repository signature could not be verified (NimVor never installs unverified packages)"
	case CatDependency:
		return "the packages' dependencies cannot be satisfied"
	case CatScriptlet:
		s := "the package's own installation script failed"
		if d.Package != "" {
			s = d.Package + ": its " + strings.ToLower(d.Stage) + " script failed"
			if d.Stage == "" {
				s = d.Package + ": its installation script failed"
			}
		}
		if d.ExitCode >= 0 {
			s += fmt.Sprintf(" (exit status %d)", d.ExitCode)
		}
		return s
	case CatInterrupted:
		return "an earlier package installation was interrupted and must finish first"
	case CatRPMDB:
		return "the RPM package database is damaged"
	case CatDiskFull:
		return "there is not enough free disk space"
	}
	return "the package manager failed"
}

// Hint is what an administrator can do when NimVor could not recover.
func (d Diagnosis) Hint() string {
	switch d.Category {
	case CatPrivilege:
		return "Update NimVor (the agent runs package transactions outside its sandbox since this version) and try again."
	case CatLocked:
		return "Wait for the other package manager (e.g. automatic updates) to finish, then try again."
	case CatNotAvailable:
		return "This component cannot be installed from the repositories configured on this server."
	case CatRepository:
		return "Check the server's network, DNS and proxy, and the repository configuration (apt-get update / dnf makecache)."
	case CatSignature:
		return "Import the repository's signing key or fix the repository configuration; do not disable signature checks."
	case CatDependency:
		return "Another repository or a held package conflicts; the details above name the packages."
	case CatScriptlet:
		return "The package's script output above shows why; on SELinux systems also check: ausearch -m AVC -ts recent."
	case CatInterrupted:
		return "Run: dpkg --configure -a"
	case CatRPMDB:
		return "Run: rpm --rebuilddb"
	case CatDiskFull:
		return "Free disk space (e.g. apt-get clean / dnf clean all, old logs and backups) and try again."
	}
	return ""
}

// transient: worth waiting and trying again as is.
func (d Diagnosis) transient() bool {
	switch d.Category {
	case CatLocked, CatRepository:
		return true
	case CatScriptlet:
		// e.g. useradd: cannot lock /etc/passwd; try again later.
		return strings.Contains(d.Detail, "try again later") || strings.Contains(d.Detail, "cannot lock")
	}
	return false
}

// Error is a package operation that failed after NimVor's recovery.
type Error struct {
	Op        string    `json:"op"`
	Packages  []string  `json:"packages"`
	Manager   string    `json:"manager"`
	Attempts  int       `json:"attempts"`
	Recovery  []string  `json:"recovery,omitempty"`
	Diagnosis Diagnosis `json:"diagnosis"`
}

func (e *Error) Error() string {
	what := e.Op
	if len(e.Packages) > 0 {
		what += " " + strings.Join(e.Packages, ", ")
	}
	s := fmt.Sprintf("%s failed: %s [%s]", what, e.Diagnosis.Summary(), strings.ToUpper(string(e.Diagnosis.Category)))
	if h := e.Diagnosis.Hint(); h != "" {
		s += ". " + h
	}
	return s
}
