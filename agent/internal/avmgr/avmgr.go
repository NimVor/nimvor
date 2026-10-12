// Package avmgr scans websites for malware with ClamAV.
//
// clamscan runs as the website's own account: it reads only what that
// account can read, and a crafted file that attacks the scanner gains
// nothing beyond the website. Quarantine moves infected files into the
// website's .whacp-quarantine folder (mode 0000), confined to the website
// folder: a path reported by the scan is never followed out of it.
package avmgr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/pkgs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// QuarantineDir is the folder, inside the website, infected files are moved to.
const QuarantineDir = ".whacp-quarantine"

// FreshclamUnits update the signatures (Debian, RHEL).
var FreshclamUnits = []string{"clamav-freshclam.service", "clamav-freshclam"}

// Finding is an infected file.
type Finding struct {
	Path        string `json:"path"` // relative to the website folder
	Signature   string `json:"signature"`
	Quarantined bool   `json:"quarantined"`
}

// Result of a scan.
type Result struct {
	Scanned  int64     `json:"scanned"`
	Infected []Finding `json:"infected"`
	Errors   int64     `json:"errors"` // files that could not be read
	// Signatures loaded; 0 when the scan did not run (no signature database).
	Signatures int64 `json:"signatures"`
}

var (
	foundRe   = regexp.MustCompile(`^(/.+): (.+) FOUND$`)
	scannedRe = regexp.MustCompile(`^Scanned files: ([0-9]+)$`)
	errorsRe  = regexp.MustCompile(`^Total errors: ([0-9]+)$`)
	knownRe   = regexp.MustCompile(`^Known viruses: ([0-9]+)$`)
)

// Parse reads clamscan's output (--infected: only infected files, then the summary).
func Parse(out io.Reader, root string) Result {
	res := Result{Infected: []Finding{}}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	prefix := filepath.Clean(root) + "/"
	for sc.Scan() {
		line := sc.Text()
		if m := foundRe.FindStringSubmatch(line); m != nil {
			if rel, ok := strings.CutPrefix(filepath.Clean(m[1]), prefix); ok && len(res.Infected) < 10000 {
				res.Infected = append(res.Infected, Finding{Path: rel, Signature: m[2]})
			}
		} else if m := scannedRe.FindStringSubmatch(line); m != nil {
			res.Scanned, _ = strconv.ParseInt(m[1], 10, 64)
		} else if m := errorsRe.FindStringSubmatch(line); m != nil {
			res.Errors, _ = strconv.ParseInt(m[1], 10, 64)
		} else if m := knownRe.FindStringSubmatch(line); m != nil {
			res.Signatures, _ = strconv.ParseInt(m[1], 10, 64)
		}
	}
	return res
}

// site identifies a website to scan.
type site struct {
	Domain     string `json:"domain"`
	User       string `json:"user"`
	Root       string `json:"root"`
	Quarantine bool   `json:"quarantine"`
	// Paths, when set, are specific files or folders (relative to the site
	// root) to scan instead of the whole website — used to scan just the
	// files that were uploaded or extracted. Each is validated to stay
	// inside the site root.
	Paths []string `json:"paths"`
}

// scanTargets resolves s.Paths to real, in-root paths to hand clamscan. It
// rejects traversal, absolute paths and the quarantine folder. With no
// paths it scans the whole site root.
func scanTargets(env *host.Env, real, siteRoot string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return []string{real}, nil
	}
	if len(paths) > 1000 {
		return nil, actions.InvalidParams("paths: too many (max 1000)")
	}
	seen := map[string]bool{}
	targets := make([]string, 0, len(paths))
	for _, rel := range paths {
		rel = strings.TrimPrefix(strings.TrimSpace(rel), "/")
		if rel == "" || rel == "." {
			return nil, actions.InvalidParams("paths: empty path")
		}
		abs := filepath.Join(siteRoot, rel)
		if err := validate.Within("path", siteRoot, abs); err != nil {
			return nil, host.Invalid(err)
		}
		if first := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]; first == QuarantineDir {
			continue // never rescan the quarantine folder
		}
		rp, err := env.Root.Path(abs)
		if err != nil {
			return nil, err
		}
		// The resolved real path must stay within the site's real root even
		// through symlinks (no escaping via a symlinked component).
		if rp != real && !strings.HasPrefix(rp, real+string(os.PathSeparator)) {
			return nil, actions.InvalidParams("path: outside the website")
		}
		if seen[rp] {
			continue
		}
		seen[rp] = true
		targets = append(targets, rp)
	}
	if len(targets) == 0 {
		return nil, actions.InvalidParams("paths: nothing to scan")
	}
	return targets, nil
}

func installed(env *host.Env) bool { return env.Runner.Available("clamscan") }

// running scans, one per website at a time.
var (
	mu      sync.Mutex
	running = map[string]bool{}
)

// Register adds the ClamAV actions.
func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "av.status", Description: "ClamAV: installed, version and signature date, signature updates running.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		out := map[string]any{"installed": installed(env), "version": "", "freshclam": false}
		if !installed(env) {
			return out, nil
		}
		// "ClamAV 1.4.3/27781/2026-09-27": engine / signatures / their date.
		// Read from the package and the signature database's header:
		// clamscan itself only runs as an unprivileged account.
		engine := ""
		for _, v := range pkgs.InstalledNative(ctx, env, []string{"clamav"}) {
			engine = strings.SplitN(strings.SplitN(v, "+", 2)[0], "-", 2)[0]
		}
		out["version"] = "ClamAV " + engine
		if ver, date, ok := Signatures(env); ok {
			out["version"] = fmt.Sprintf("ClamAV %s/%d/%s", engine, ver, date)
		}
		for _, u := range FreshclamUnits {
			if env.UnitActive(ctx, u) {
				out["freshclam"] = true
			}
		}
		// No signature database yet: the updater's last messages say why
		// (still downloading, mirror refused, no network…).
		if strings.Count(out["version"].(string), "/") < 2 {
			out["updater_log"] = env.JournalTail(ctx, FreshclamUnits[0], 12)
		}
		return out, nil
	}})
	r.Register(actions.Action{Name: "av.update", Mutating: true, Description: "Update the ClamAV signatures now (restarts the signature updater, which downloads them).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		if !installed(env) {
			return nil, actions.Conflict("ClamAV is not installed.")
		}
		for _, u := range FreshclamUnits {
			if err := env.Systemctl(ctx, "restart", u); err == nil {
				env.Systemctl(ctx, "enable", u)
				return map[string]any{"restarted": u}, nil
			}
		}
		return nil, actions.Failed("The signature updater (clamav-freshclam) could not be started.", "")
	}})
	r.Register(actions.Action{Name: "av.scan", Mutating: true, Description: "Scan a website for malware with ClamAV, as the website's account (job); optionally quarantine infected files inside the website. With paths, scans only those files or folders (relative to the website), e.g. just-uploaded or just-extracted files.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var s site
		if err := host.Decode(p, &s); err != nil {
			return nil, err
		}
		if err := validate.Domain("domain", s.Domain); err != nil {
			return nil, host.Invalid(err)
		}
		if s.Root == "" {
			s.Root = filepath.Join(env.Cfg.SitesRoot, s.Domain)
		}
		if err := validate.Within("root", env.Cfg.SitesRoot, s.Root); err != nil {
			return nil, host.Invalid(err)
		}
		if !installed(env) {
			return nil, actions.Conflict("ClamAV is not installed. Install it under Server → Packages (clamav).")
		}
		cred, u, err := env.Credential(s.User)
		if err != nil {
			return nil, actions.InvalidParams("user: " + err.Error())
		}
		real, err := env.Root.Path(s.Root)
		if err != nil {
			return nil, err
		}
		if info, err := os.Lstat(real); err != nil || !info.IsDir() {
			return nil, actions.NotFound("The website folder does not exist.")
		}
		targets, err := scanTargets(env, real, s.Root, s.Paths)
		if err != nil {
			return nil, err
		}
		// A whole-site scan is exclusive per website; targeted scans (an
		// upload or an extraction) may run concurrently — they cover
		// different files and do not conflict.
		fullScan := len(s.Paths) == 0
		if fullScan {
			mu.Lock()
			if running[s.Domain] {
				mu.Unlock()
				return nil, actions.Conflict("A scan of " + s.Domain + " is running.")
			}
			running[s.Domain] = true
			mu.Unlock()
		}
		j := env.Jobs.Start("av.scan", "site:"+s.Domain, func(ctx context.Context, log io.Writer) (any, error) {
			if fullScan {
				defer func() { mu.Lock(); delete(running, s.Domain); mu.Unlock() }()
			}
			if fullScan {
				fmt.Fprintf(log, "Scanning %s as %s…\n", s.Root, s.User)
			} else {
				fmt.Fprintf(log, "Scanning %d path(s) of %s as %s…\n", len(targets), s.Domain, s.User)
			}
			res, err := runClamscan(ctx, env, real, cred, targets, true, 6*time.Hour)
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(log, "Scanned %d files: %d infected, %d errors.\n", res.Scanned, len(res.Infected), res.Errors)
			if s.Quarantine && len(res.Infected) > 0 {
				quarantine(real, &res, u.UID, u.GID, log)
			}
			return res, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "av.quarantine_list", Description: "List a website's quarantined files (moved there by a malware scan): where each came from, its size and when it was quarantined.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		real, _, _, err := openSite(env, p)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": quarantineList(real)}, nil
	}})
	r.Register(actions.Action{Name: "av.release", Mutating: true, Description: "Release a file from a website's quarantine back to where it came from, after re-scanning it. Refuses if the file is still infected or its original location is now occupied.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			site
			Path string `json:"path"` // quarantine-relative, e.g. "20260101-120000/public_html/x.php"
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		real, cred, _, err := openSite(env, mustJSON(in.site))
		if err != nil {
			return nil, err
		}
		return release(ctx, env, real, cred, in.Path)
	}})
}

// QuarantineEntry is one file sitting in a website's quarantine.
type QuarantineEntry struct {
	QuarantinePath string `json:"quarantine_path"` // relative to the quarantine folder
	Path           string `json:"path"`            // where it came from (site-relative)
	Size           int64  `json:"size"`
	QuarantinedAt  string `json:"quarantined_at"` // RFC3339, from the batch folder name
}

// quarantineList reports the regular files under the website's quarantine
// folder. Symlinks and other non-regular entries are ignored and never
// descended into, so the listing cannot be steered out of the website.
func quarantineList(real string) []QuarantineEntry {
	items := []QuarantineEntry{}
	base := filepath.Join(real, QuarantineDir)
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(items) >= 10000 {
			return filepath.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		qrel, err := filepath.Rel(base, p)
		if err != nil {
			return nil
		}
		qrel = filepath.ToSlash(qrel)
		stamp, orig, _ := strings.Cut(qrel, "/")
		at := ""
		if t, e := time.Parse("20060102-150405", stamp); e == nil {
			at = t.UTC().Format(time.RFC3339)
		}
		items = append(items, QuarantineEntry{QuarantinePath: qrel, Path: orig, Size: info.Size(), QuarantinedAt: at})
		return nil
	})
	return items
}

// release re-scans a quarantined file and, when it is clean and its original
// location is free, moves it back there. Every path stays inside the website
// (os.Root), a symlink is never followed, and a still-infected file is left
// in quarantine (mode 0) and refused.
func release(ctx context.Context, env *host.Env, real string, cred *execx.Credential, qpath string) (any, error) {
	clean := path.Clean(filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(qpath), "/")))
	if clean == "." || clean == "" || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || strings.HasPrefix(clean, "/") {
		return nil, actions.InvalidParams("path: invalid quarantine path")
	}
	stamp, orig, ok := strings.Cut(clean, "/")
	if !ok || orig == "" || stamp == "" {
		return nil, actions.InvalidParams("path: a quarantine path is <batch>/<original path>")
	}
	if strings.SplitN(orig, "/", 2)[0] == QuarantineDir {
		return nil, actions.InvalidParams("path: cannot restore into the quarantine")
	}
	if !installed(env) {
		return nil, actions.Conflict("ClamAV is not installed, so the file cannot be re-scanned before release.")
	}
	root, err := os.OpenRoot(real)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	qrel := QuarantineDir + "/" + clean
	// The original location must be free; never overwrite.
	if _, err := root.Lstat(orig); err == nil {
		return nil, actions.Conflict("A file already exists where this one came from; move it first.")
	}
	// Make the quarantined file readable by the account for the re-scan.
	f, err := root.OpenFile(qrel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, actions.NotFound("The quarantined file was not found.")
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, actions.InvalidParams("path: not a regular file")
	}
	f.Chmod(0o600)
	f.Close()
	res, err := runClamscan(ctx, env, real, cred, []string{filepath.Join(real, qrel)}, false, 30*time.Minute)
	if err != nil {
		reChmod0(root, qrel)
		return nil, err
	}
	if len(res.Infected) > 0 {
		reChmod0(root, qrel)
		sig := res.Infected[0].Signature
		return nil, actions.Conflict("The file is still infected (" + sig + ") and was not released.")
	}
	if err := renameIn(root, qrel, orig); err != nil {
		reChmod0(root, qrel)
		return nil, actions.Failed("The file could not be restored.", err.Error())
	}
	if f, err := root.OpenFile(orig, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0); err == nil {
		f.Chmod(0o644)
		f.Close()
	}
	return map[string]any{"released": true, "path": orig}, nil
}

// reChmod0 puts a quarantined file's restrictive mode back after a refused
// release, without following a symlink.
func reChmod0(root *os.Root, qrel string) {
	if f, err := root.OpenFile(qrel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0); err == nil {
		f.Chmod(0)
		f.Close()
	}
}

// openSite validates a site payload and returns its real root and the
// website account's credential. Used by the quarantine actions, which take
// the same {domain, user, root} shape as a scan.
func openSite(env *host.Env, p json.RawMessage) (real string, cred *execx.Credential, uid int, err error) {
	var s site
	if err = host.Decode(p, &s); err != nil {
		return "", nil, 0, err
	}
	if err = validate.Domain("domain", s.Domain); err != nil {
		return "", nil, 0, host.Invalid(err)
	}
	if s.Root == "" {
		s.Root = filepath.Join(env.Cfg.SitesRoot, s.Domain)
	}
	if err = validate.Within("root", env.Cfg.SitesRoot, s.Root); err != nil {
		return "", nil, 0, host.Invalid(err)
	}
	c, u, err := env.Credential(s.User)
	if err != nil {
		return "", nil, 0, actions.InvalidParams("user: " + err.Error())
	}
	real, err = env.Root.Path(s.Root)
	if err != nil {
		return "", nil, 0, err
	}
	if info, err := os.Lstat(real); err != nil || !info.IsDir() {
		return "", nil, 0, actions.NotFound("The website folder does not exist.")
	}
	return real, c, u.UID, nil
}

// mustJSON re-encodes a value for a nested Decode; the site struct has just
// been decoded from JSON, so it always round-trips.
func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// runClamscan scans targets as cred, parsing infected files and the summary.
// It returns errNoSignatures when the signature database is missing (so a
// scan is never reported "clean" without having checked anything).
func runClamscan(ctx context.Context, env *host.Env, real string, cred *execx.Credential, targets []string, excludeQuarantine bool, timeout time.Duration) (Result, error) {
	pr, pw := io.Pipe()
	var res Result
	done := make(chan struct{})
	go func() { res = Parse(pr, real); io.Copy(io.Discard, pr); close(done) }()
	args := []string{"--recursive", "--infected", "--stdout"}
	if excludeQuarantine {
		args = append(args, "--exclude-dir=^"+regexp.QuoteMeta(filepath.Join(real, QuarantineDir)))
	}
	args = append(args, targets...)
	// Exit status 1: infected files found; 2: an error, which may be only
	// files the account cannot read (the scan still completed).
	stderr, runErr := env.Runner.RunWith(ctx, "clamscan", execx.Opts{As: cred, Dir: real, Stdout: pw, Timeout: timeout}, args...)
	pw.Close()
	<-done
	if runErr != nil && !execx.ExitCode(runErr, 1) && !(execx.ExitCode(runErr, 2) && res.Signatures > 0) {
		if strings.Contains(string(stderr), "No supported database files") || (execx.ExitCode(runErr, 2) && res.Signatures == 0) {
			return res, errNoSignatures
		}
		return res, fmt.Errorf("the scan did not complete: %w: %s", runErr, lastLine(stderr))
	}
	if res.Signatures == 0 {
		return res, errNoSignatures
	}
	return res, nil
}

var errNoSignatures = fmt.Errorf("ClamAV has no virus signatures yet: update the signatures (Security → Malware scan) and scan again")

// lastLine is the last non-empty line of a command's error output.
func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// quarantine moves infected files into the website's quarantine folder
// (timestamped, the same relative path, mode 0000), confined to the
// website folder: symlinks and anything not a regular file are left.
func quarantine(real string, res *Result, uid, gid int, log io.Writer) {
	root, err := os.OpenRoot(real)
	if err != nil {
		fmt.Fprintf(log, "Quarantine not possible: %v\n", err)
		return
	}
	defer root.Close()
	stamp := time.Now().UTC().Format("20060102-150405")
	base := filepath.Join(QuarantineDir, stamp)
	for i := range res.Infected {
		rel := res.Infected[i].Path
		if strings.HasPrefix(rel, QuarantineDir+"/") || rel == "" {
			continue
		}
		info, err := root.Lstat(rel)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		dest := filepath.Join(base, rel)
		if err := mkdirAll(root, filepath.Dir(dest), uid, gid); err != nil {
			fmt.Fprintf(log, "%s: %v\n", rel, err)
			continue
		}
		if err := renameIn(root, rel, dest); err != nil {
			fmt.Fprintf(log, "%s: %v\n", rel, err)
			continue
		}
		if f, err := root.OpenFile(dest, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0); err == nil {
			f.Chmod(0)
			f.Close()
		}
		res.Infected[i].Quarantined = true
		fmt.Fprintf(log, "Quarantined %s\n", rel)
	}
}

// renameIn renames within root: both folders are opened through root and
// only plain names are renamed between them (renameat), so neither end
// can lead out of the website; a symlink is moved itself, not followed.
func renameIn(root *os.Root, from, to string) error {
	src, err := root.Open(filepath.Dir(from))
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := root.Open(filepath.Dir(to))
	if err != nil {
		return err
	}
	defer dst.Close()
	return syscall.Renameat(int(src.Fd()), filepath.Base(from), int(dst.Fd()), filepath.Base(to))
}

// mkdirAll creates dir and its parents inside root (0700, the website's account).
func mkdirAll(root *os.Root, dir string, uid, gid int) error {
	cur := ""
	for _, part := range strings.Split(filepath.Clean(dir), "/") {
		cur = filepath.Join(cur, part)
		info, err := root.Lstat(cur)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("%s is not a folder", cur)
			}
			continue
		}
		if err := root.Mkdir(cur, 0o700); err != nil {
			return err
		}
		// Not followed: the website's account could swap the new folder for a link.
		if d, err := root.OpenFile(cur, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0); err == nil {
			d.Chown(uid, gid)
			d.Close()
		}
	}
	return nil
}

// DatabaseDir holds the signatures (Debian and RHEL alike).
const DatabaseDir = "/var/lib/clamav"

// Signatures reads the daily signature database's header ("ClamAV-VDB:
// 07 Oct 2026 08-24 -0400:28146:355728:90:…"): its version and date.
func Signatures(env *host.Env) (int, string, bool) {
	best, date := 0, ""
	for _, name := range []string{"daily.cld", "daily.cvd"} {
		real, err := env.Root.Path(DatabaseDir + "/" + name)
		if err != nil {
			continue
		}
		f, err := os.Open(real)
		if err != nil {
			continue
		}
		head := make([]byte, 512)
		n, _ := io.ReadFull(f, head)
		f.Close()
		v, d, ok := ParseHeader(string(head[:n]))
		if ok && v > best {
			best, date = v, d
		}
	}
	return best, date, best > 0
}

// ParseHeader reads a ClamAV database header: version and date (YYYY-MM-DD).
func ParseHeader(h string) (int, string, bool) {
	f := strings.Split(strings.TrimRight(h, "\x00 "), ":")
	if len(f) < 4 || f[0] != "ClamAV-VDB" {
		return 0, "", false
	}
	v, err := strconv.Atoi(f[2])
	if err != nil || v <= 0 {
		return 0, "", false
	}
	date := ""
	if t, err := time.Parse("02 Jan 2006 15-04 -0700", strings.TrimSpace(f[1])); err == nil {
		date = t.UTC().Format("2006-01-02")
	}
	return v, date, true
}
