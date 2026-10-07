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
	"os"
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
	r.Register(actions.Action{Name: "av.scan", Mutating: true, Description: "Scan a website for malware with ClamAV, as the website's account (job); optionally quarantine infected files inside the website.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
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
		mu.Lock()
		if running[s.Domain] {
			mu.Unlock()
			return nil, actions.Conflict("A scan of " + s.Domain + " is running.")
		}
		running[s.Domain] = true
		mu.Unlock()
		j := env.Jobs.Start("av.scan", "site:"+s.Domain, func(ctx context.Context, log io.Writer) (any, error) {
			defer func() { mu.Lock(); delete(running, s.Domain); mu.Unlock() }()
			fmt.Fprintf(log, "Scanning %s as %s…\n", s.Root, s.User)
			pr, pw := io.Pipe()
			var res Result
			done := make(chan struct{})
			go func() { res = Parse(pr, real); io.Copy(io.Discard, pr); close(done) }()
			// Exit status 1: infected files found; 2: an error, which may be
			// only files the account cannot read (the scan still completed).
			stderr, runErr := env.Runner.RunWith(ctx, "clamscan", execx.Opts{As: cred, Dir: real, Stdout: pw, Timeout: 6 * time.Hour},
				"--recursive", "--infected", "--stdout", "--exclude-dir=^"+regexp.QuoteMeta(filepath.Join(real, QuarantineDir)), real)
			pw.Close()
			<-done
			if runErr != nil && !execx.ExitCode(runErr, 1) && !(execx.ExitCode(runErr, 2) && res.Signatures > 0) {
				if strings.Contains(string(stderr), "No supported database files") || (execx.ExitCode(runErr, 2) && res.Signatures == 0) {
					return nil, errNoSignatures
				}
				return nil, fmt.Errorf("the scan did not complete: %w: %s", runErr, lastLine(stderr))
			}
			// Without signatures (or a summary) nothing was checked: never "clean".
			if res.Signatures == 0 {
				return nil, errNoSignatures
			}
			fmt.Fprintf(log, "Scanned %d files: %d infected, %d errors.\n", res.Scanned, len(res.Infected), res.Errors)
			if s.Quarantine && len(res.Infected) > 0 {
				quarantine(real, &res, u.UID, u.GID, log)
			}
			return res, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
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
