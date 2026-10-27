package security

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// File integrity monitoring (security items 104–106). Two independent
// sources, so there is never a custom checksum database to keep current:
//
//   - Package-owned files are verified with the OS's own tool (dpkg
//     --verify on Debian/Ubuntu, rpm -Va on RHEL). Those tools compare
//     against the checksums the package manager recorded at install time,
//     which are refreshed on every upgrade, so a normal package update is
//     not reported as a change.
//   - NimVor-owned files (the agent binary and the agent configuration under
//     /etc/nimvor) are compared against a baseline manifest that NimVor writes
//     itself, so a change to the agent binary or its configuration is caught
//     even though no package owns them.
//
// Findings are classified (package-owned vs NimVor-owned) and graded, so the
// agent binary being modified is CRITICAL, a security component changing is
// HIGH, and configuration changing outside NimVor is MEDIUM.

// IntegrityFinding is one file that differs from its expected state.
type IntegrityFinding struct {
	Path     string `json:"path"`
	Class    string `json:"class"`             // "nimvor" or "package"
	Issue    string `json:"issue"`             // modified, deleted, permissions, ownership, new
	Severity string `json:"severity"`          // critical, high, medium, low
	Package  string `json:"package,omitempty"` // the owning package, when known
	Detail   string `json:"detail,omitempty"`
}

// IntegrityReport is the result of a scan.
type IntegrityReport struct {
	CheckedAt   time.Time          `json:"checked_at"`
	PackageTool string             `json:"package_tool"`
	HasBaseline bool               `json:"has_baseline"`
	Findings    []IntegrityFinding `json:"findings"`
}

// fileState is a file's recorded fingerprint in the baseline.
type fileState struct {
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
	UID    uint32 `json:"uid"`
	GID    uint32 `json:"gid"`
	Size   int64  `json:"size"`
}

func integrityManifestPath(env *host.Env) string {
	return filepath.Join(env.Cfg.StateDir, "integrity-baseline.json")
}

// nimvorOwnedTargets lists the NimVor-owned files worth baselining: the agent
// binary (no package owns it) and the agent configuration tree. Website and
// service configuration is generated and changes with every customer
// action, so it is not baselined here; package verification covers the
// server software itself.
func nimvorOwnedTargets(env *host.Env) []string {
	var out []string
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			out = append(out, real)
		} else {
			out = append(out, exe)
		}
	}
	// The agent configuration directory.
	confDir := "/etc/nimvor"
	real, err := env.Root.Path(confDir)
	if err == nil {
		filepath.WalkDir(real, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if fi, e := d.Info(); e == nil && fi.Mode().IsRegular() {
				out = append(out, p)
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}

// stateOf fingerprints a file; ok is false when it does not exist.
func stateOf(path string) (fileState, bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return fileState{}, false, nil
	}
	st, _ := fi.Sys().(*syscall.Stat_t)
	s := fileState{Mode: uint32(fi.Mode().Perm()), Size: fi.Size()}
	if st != nil {
		s.UID, s.GID = st.Uid, st.Gid
	}
	// Only regular files are hashed; a symlink where a file is expected is
	// itself reported as a change by the mode comparison.
	if fi.Mode().IsRegular() {
		f, err := os.Open(path)
		if err != nil {
			return s, true, err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return s, true, err
		}
		s.SHA256 = hex.EncodeToString(h.Sum(nil))
	}
	return s, true, nil
}

// WriteIntegrityBaseline records the current state of the NimVor-owned files
// as the baseline. It is called after an install or upgrade, when the files
// are known good.
func WriteIntegrityBaseline(env *host.Env) (int, error) {
	manifest := map[string]fileState{}
	for _, p := range nimvorOwnedTargets(env) {
		s, ok, err := stateOf(p)
		if err != nil || !ok {
			continue
		}
		manifest[p] = s
	}
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return 0, err
	}
	if _, err := env.Writer.WriteFile(integrityManifestPath(env), b, 0o600, agentfs.RootOwner); err != nil {
		return 0, err
	}
	return len(manifest), nil
}

func loadIntegrityBaseline(env *host.Env) (map[string]fileState, error) {
	real, err := env.Root.Path(integrityManifestPath(env))
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(real)
	if err != nil {
		return nil, err
	}
	m := map[string]fileState{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// agentBinaryPath is the resolved path of the running agent, used to grade
// a change to it as CRITICAL.
func agentBinaryPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		return real
	}
	return exe
}

// CheckIntegrity scans NimVor-owned files against the baseline and
// package-owned files with the OS verification tool.
func CheckIntegrity(ctx context.Context, env *host.Env, now time.Time) IntegrityReport {
	r := IntegrityReport{CheckedAt: now}
	binary := agentBinaryPath()

	// NimVor-owned files vs the baseline.
	if base, err := loadIntegrityBaseline(env); err == nil {
		r.HasBaseline = true
		current := map[string]bool{}
		for _, p := range nimvorOwnedTargets(env) {
			current[p] = true
		}
		sev := func(p string) string {
			if p == binary {
				return "critical"
			}
			return "high"
		}
		var paths []string
		for p := range base {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			want := base[p]
			got, ok, _ := stateOf(p)
			switch {
			case !ok:
				r.Findings = append(r.Findings, IntegrityFinding{Path: p, Class: "nimvor", Issue: "deleted", Severity: sev(p), Detail: "a NimVor-owned file is missing"})
			case got.SHA256 != want.SHA256:
				r.Findings = append(r.Findings, IntegrityFinding{Path: p, Class: "nimvor", Issue: "modified", Severity: sev(p), Detail: "checksum changed"})
			case got.Mode != want.Mode:
				r.Findings = append(r.Findings, IntegrityFinding{Path: p, Class: "nimvor", Issue: "permissions", Severity: "high", Detail: fmt.Sprintf("mode %o → %o", want.Mode, got.Mode)})
			case got.UID != want.UID || got.GID != want.GID:
				r.Findings = append(r.Findings, IntegrityFinding{Path: p, Class: "nimvor", Issue: "ownership", Severity: "high", Detail: fmt.Sprintf("owner %d:%d → %d:%d", want.UID, want.GID, got.UID, got.GID)})
			}
		}
		// New NimVor-owned files that appeared after the baseline.
		for p := range current {
			if _, known := base[p]; !known {
				r.Findings = append(r.Findings, IntegrityFinding{Path: p, Class: "nimvor", Issue: "new", Severity: "medium", Detail: "a NimVor-owned file not present at baseline"})
			}
		}
	}

	// Package-owned files with the OS tool.
	r.PackageTool = "dpkg"
	if env.Platform.PackageManager() == "dnf" {
		r.PackageTool = "rpm"
	}
	r.Findings = append(r.Findings, packageVerify(ctx, env, r.PackageTool)...)

	sort.SliceStable(r.Findings, func(i, j int) bool {
		return severityRank(r.Findings[i].Severity) < severityRank(r.Findings[j].Severity)
	})
	return r
}

func severityRank(s string) int {
	switch s {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	default:
		return 3
	}
}

// criticalRoots limits package-verification noise to the files that matter
// for security: the server software's binaries and configuration, SSH, PAM
// and privilege configuration.
var criticalRoots = []string{
	"/etc/nginx", "/etc/ssh", "/etc/pam.d", "/etc/php", "/etc/opt/remi",
	"/etc/sudoers", "/etc/sudoers.d", "/etc/mysql", "/etc/my.cnf.d",
	"/usr/sbin", "/usr/bin", "/bin", "/sbin", "/usr/lib", "/lib",
}

func underCriticalRoot(p string) bool {
	for _, root := range criticalRoots {
		if p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	return false
}

// packageVerifyTimeout bounds one dpkg --verify / rpm -Va run.
const packageVerifyTimeout = 15 * time.Minute

// packageVerify runs the OS verification tool and returns findings for
// changed files under the critical roots. It never fails the scan: a
// non-zero exit is normal (it means some file differs).
func packageVerify(ctx context.Context, env *host.Env, tool string) []IntegrityFinding {
	// Verifying every installed package reads every file they own: minutes on
	// a server with a full package set, far beyond the runner's default
	// timeout (a verification cut short would report nothing as drift).
	var out []byte
	opts := execx.Opts{Timeout: packageVerifyTimeout}
	if tool == "rpm" {
		out, _ = env.Runner.RunWith(ctx, "rpm", opts, "-Va", "--nomtime", "--nordev")
	} else {
		out, _ = env.Runner.RunWith(ctx, "dpkg", opts, "--verify")
	}
	var findings []IntegrityFinding
	const cap = 200
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() && len(findings) < cap {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		flags, path, missing := parseVerifyLine(line)
		if path == "" || !underCriticalRoot(path) {
			continue
		}
		issue, sev := "modified", "medium"
		switch {
		case missing:
			issue = "deleted"
		case strings.ContainsAny(flags, "5"):
			issue = "modified"
		case strings.Contains(flags, "M"):
			issue, sev = "permissions", "medium"
		case strings.ContainsAny(flags, "UG"):
			issue, sev = "ownership", "medium"
		default:
			continue // only mtime/other attributes changed
		}
		// A changed executable or library is more serious than a changed
		// configuration file.
		if strings.HasPrefix(path, "/usr/sbin/") || strings.HasPrefix(path, "/usr/bin/") ||
			strings.HasPrefix(path, "/sbin/") || strings.HasPrefix(path, "/bin/") ||
			strings.HasPrefix(path, "/usr/lib/") || strings.HasPrefix(path, "/lib/") {
			sev = "high"
		}
		findings = append(findings, IntegrityFinding{Path: path, Class: "package", Issue: issue, Severity: sev, Detail: "differs from the installed package (" + strings.TrimSpace(flags) + ")"})
	}
	return findings
}

// parseVerifyLine reads one line of dpkg --verify or rpm -Va output into the
// attribute flags and the file path. Both tools put the flags first, an
// optional file-type character next, and the path last.
func parseVerifyLine(line string) (flags, path string, missing bool) {
	if strings.HasPrefix(strings.TrimSpace(line), "missing") {
		fields := strings.Fields(line)
		return "", fields[len(fields)-1], true
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", "", false
	}
	// The last field is the path; the first is the attribute string.
	return fields[0], fields[len(fields)-1], false
}
