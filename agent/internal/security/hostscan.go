package security

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/NimVor/nimvor/agent/internal/host"
)

// The host scan looks for what an intruder or a careless change leaves
// behind: set-user-ID programs and file capabilities that no package
// installed, world-writable system files, and scheduled tasks or services
// no package or NimVor created. It only reports; nothing is changed. It
// walks the file system, so it runs in the background at most once a day
// and the audit shows its latest result.

// HostScan is a scan's result.
type HostScan struct {
	Time      int64     `json:"time"`
	Seconds   float64   `json:"seconds"`
	Entries   int       `json:"entries"`
	Truncated bool      `json:"truncated"`
	Setuid    []Finding `json:"setuid"`
	Caps      []Finding `json:"caps"`
	Writable  []Finding `json:"writable"`
	Persist   []Finding `json:"persist"`
	Owned     bool      `json:"owned"` // package file lists were read
}

// Finding is one file of a scan.
type Finding struct {
	Path   string `json:"path"`
	Detail string `json:"detail"`
	Recent bool   `json:"recent,omitempty"` // changed in the last 7 days
}

// scanRoots are walked; within them, skipDirs are not.
var (
	scanRoots = []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/opt", "/etc", "/var", "/srv", "/home", "/root", "/tmp", "/boot"}
	skipDirs  = map[string]bool{"/var/lib/docker": true, "/var/lib/containers": true, "/var/cache": true, "/var/lib/lxcfs": true, "/var/log/journal": true}
	// writableRoots: world-writable entries here are reported (the others,
	// e.g. websites' own files, are their owners' business).
	writableRoots = []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/opt", "/boot", "/root", "/srv", "/var/lib", "/var/spool/cron", "/var/www"}
	// untrustedRoots: set-user-ID files here are always reported.
	untrustedRoots = []string{"/home", "/tmp", "/var/tmp", "/dev/shm", "/srv", "/var/www"}
	maxScanEntries = 3000000
	maxFindings    = 200
)

func under(p string, roots []string) bool {
	for _, r := range roots {
		if p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}

// OwnedFiles lists the files installed by packages (dpkg's lists, or rpm's
// database).
func OwnedFiles(ctx context.Context, env *host.Env) (map[string]bool, error) {
	owned := map[string]bool{}
	if env.Platform.PackageManager() == "dnf" {
		// rpm expands the \n escape: the agent refuses arguments with real
		// line breaks.
		out, err := env.Runner.Run(ctx, "rpm", "-qa", "--qf", `[%{FILENAMES}\n]`)
		if err != nil {
			return nil, fmt.Errorf("rpm: %s", host.Output(out))
		}
		for _, l := range strings.Split(string(out), "\n") {
			if l != "" {
				owned[l] = true
			}
		}
		return owned, nil
	}
	dir, err := env.Root.Path("/var/lib/dpkg/info")
	if err != nil {
		return nil, err
	}
	lists, _ := filepath.Glob(filepath.Join(dir, "*.list"))
	if len(lists) == 0 {
		return nil, errors.New("no package file lists")
	}
	for _, f := range lists {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			owned[sc.Text()] = true
		}
		fh.Close()
	}
	return owned, nil
}

// isOwned also accepts the /usr-merged spelling of a path (/bin/su is
// listed, /usr/bin/su is found, or the reverse).
func isOwned(owned map[string]bool, p string) bool {
	if owned[p] {
		return true
	}
	for _, pair := range [][2]string{{"/usr/bin/", "/bin/"}, {"/usr/sbin/", "/sbin/"}, {"/usr/lib/", "/lib/"}, {"/usr/lib64/", "/lib64/"}} {
		if strings.HasPrefix(p, pair[0]) && owned[pair[1]+p[len(pair[0]):]] {
			return true
		}
		if strings.HasPrefix(p, pair[1]) && owned[pair[0]+p[len(pair[1]):]] {
			return true
		}
	}
	return false
}

// Scan walks the file system. owned may be nil (then set-user-ID and
// capability files are reported only in untrusted places).
func Scan(ctx context.Context, env *host.Env, owned map[string]bool, now time.Time) HostScan {
	start := time.Now()
	res := HostScan{Time: now.Unix(), Owned: owned != nil}
	recent := func(fi fs.FileInfo) bool { return now.Sub(fi.ModTime()) < 7*24*time.Hour }
	add := func(list *[]Finding, f Finding) {
		if len(*list) < maxFindings {
			*list = append(*list, f)
		}
	}
	sitesRoot := filepath.Clean(env.Cfg.SitesRoot)
	seenReal := map[string]bool{}
	for _, root := range scanRoots {
		real, err := env.Root.Path(root)
		if err != nil {
			continue
		}
		if fi, err := os.Lstat(real); err != nil || !fi.IsDir() {
			continue // e.g. /bin -> usr/bin
		}
		if seenReal[real] {
			continue
		}
		seenReal[real] = true
		filepath.WalkDir(real, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if ctx.Err() != nil || res.Entries >= maxScanEntries {
				res.Truncated = true
				return filepath.SkipAll
			}
			res.Entries++
			logical := root + strings.TrimPrefix(p, real)
			if d.IsDir() && skipDirs[logical] {
				return filepath.SkipDir
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			mode := fi.Mode()
			if mode.IsRegular() && mode&(fs.ModeSetuid|fs.ModeSetgid) != 0 {
				untrusted := under(logical, untrustedRoots) || under(logical, []string{sitesRoot})
				if untrusted || (owned != nil && !isOwned(owned, logical)) {
					kind := "set-user-ID"
					if mode&fs.ModeSetuid == 0 {
						kind = "set-group-ID"
					}
					why := "not installed by a package"
					if untrusted {
						why = "in a place users can write to"
					}
					add(&res.Setuid, Finding{Path: logical, Detail: kind + ", " + why, Recent: recent(fi)})
				}
			}
			if mode.IsRegular() && mode&0o111 != 0 && owned != nil && !isOwned(owned, logical) {
				if n, err := syscall.Getxattr(p, "security.capability", nil); err == nil && n > 0 {
					add(&res.Caps, Finding{Path: logical, Detail: "has file capabilities, not installed by a package", Recent: recent(fi)})
				}
			}
			// Sockets need write permission to be connected to (the
			// database server's mysql.sock): who may use the service is
			// up to the service, not the file mode.
			if mode.Perm()&0o002 != 0 && mode&fs.ModeSocket == 0 && under(logical, writableRoots) {
				if d.IsDir() && mode&fs.ModeSticky != 0 {
					return nil // e.g. a shared temporary folder
				}
				what := "file"
				if d.IsDir() {
					what = "directory (without the sticky bit)"
				}
				add(&res.Writable, Finding{Path: logical, Detail: "world-writable " + what, Recent: recent(fi)})
			}
			return nil
		})
	}
	for _, l := range [][]Finding{res.Setuid, res.Caps, res.Writable} {
		sort.Slice(l, func(i, j int) bool { return l[i].Path < l[j].Path })
	}
	res.Persist = persistence(env, owned, now)
	res.Seconds = time.Since(start).Seconds()
	return res
}

// persistence lists scheduled tasks, services and preloaded libraries no
// package or NimVor created.
func persistence(env *host.Env, owned map[string]bool, now time.Time) []Finding {
	var out []Finding
	check := func(logical, what string) {
		real, err := env.Root.Path(logical)
		if err != nil {
			return
		}
		fi, err := os.Lstat(real)
		if err != nil || !fi.Mode().IsRegular() {
			return
		}
		if owned != nil && isOwned(owned, logical) {
			return
		}
		base := filepath.Base(logical)
		if strings.HasPrefix(base, "nimvor") || strings.HasPrefix(base, "nim-") {
			return
		}
		if b, err := os.ReadFile(real); err == nil && len(b) > 0 {
			head := string(b[:min(len(b), 512)])
			if strings.Contains(head, "Managed by NimVor") {
				return
			}
		}
		out = append(out, Finding{Path: logical, Detail: what, Recent: now.Sub(fi.ModTime()) < 7*24*time.Hour})
	}
	globs := []struct{ pattern, what string }{
		{"/etc/cron.d/*", "system cron job"},
		{"/etc/cron.hourly/*", "hourly cron script"},
		{"/etc/cron.daily/*", "daily cron script"},
		{"/etc/cron.weekly/*", "weekly cron script"},
		{"/etc/cron.monthly/*", "monthly cron script"},
		{"/var/spool/cron/crontabs/*", "personal crontab"},
		{"/var/spool/cron/*", "personal crontab"},
		{"/etc/systemd/system/*.service", "systemd service"},
		{"/etc/systemd/system/*.timer", "systemd timer"},
		{"/etc/systemd/system/*.socket", "systemd socket"},
		{"/etc/rc.local", "boot script (rc.local)"},
	}
	for _, g := range globs {
		dir, err := env.Root.Path(filepath.Dir(g.pattern))
		if err != nil {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(dir, filepath.Base(g.pattern)))
		for _, m := range matches {
			name := filepath.Base(m)
			if name == ".placeholder" || name == "0anacron" || strings.HasSuffix(name, "~") {
				continue
			}
			if ext := filepath.Ext(name); ext == ".dpkg-old" || ext == ".dpkg-dist" || ext == ".dpkg-new" || ext == ".rpmsave" || ext == ".rpmnew" || ext == ".rpmorig" {
				continue // cron and systemd ignore package backups
			}
			check(filepath.Join(filepath.Dir(g.pattern), name), g.what)
		}
	}
	// A preloaded library runs inside every program.
	if real, err := env.Root.Path("/etc/ld.so.preload"); err == nil {
		if b, err := os.ReadFile(real); err == nil && strings.TrimSpace(string(b)) != "" {
			out = append(out, Finding{Path: "/etc/ld.so.preload", Detail: "libraries loaded into every program: " + strings.Join(strings.Fields(string(b)), ", ")})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// HostScanner keeps the latest scan and runs one in the background when it
// is older than a day.
type HostScanner struct {
	mu      sync.Mutex
	latest  *HostScan
	running bool
	path    string
}

var scanner = &HostScanner{}

// MaxScanAge is how old a scan may be before a new one starts.
const MaxScanAge = 24 * time.Hour

func (s *HostScanner) load(env *host.Env) {
	if s.latest != nil || s.path == "" {
		return
	}
	if b, err := os.ReadFile(s.path); err == nil {
		var r HostScan
		if json.Unmarshal(b, &r) == nil {
			s.latest = &r
		}
	}
}

// Latest returns the latest scan (nil: none yet) and starts a new one in
// the background when it is missing or old, or when refresh is set.
func (s *HostScanner) Latest(env *host.Env, refresh bool, run func() HostScan) (*HostScan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" && env.Cfg.StateDir != "" {
		if p, err := env.Root.Path(filepath.Join(env.Cfg.StateDir, "hostscan.json")); err == nil {
			s.path = p
		}
	}
	s.load(env)
	stale := s.latest == nil || time.Since(time.Unix(s.latest.Time, 0)) > MaxScanAge || refresh
	if stale && !s.running {
		s.running = true
		go func() {
			r := run()
			s.mu.Lock()
			s.latest, s.running = &r, false
			path := s.path
			s.mu.Unlock()
			if path != "" && env.Cfg.AllowWriteActions {
				if b, err := json.Marshal(r); err == nil {
					tmp := path + ".tmp"
					if os.WriteFile(tmp, b, 0o600) == nil {
						os.Rename(tmp, path)
					}
				}
			}
		}()
	}
	return s.latest, s.running
}

func listPaths(fs []Finding) string {
	var l []string
	for i, f := range fs {
		if i == 8 {
			l = append(l, fmt.Sprintf("and %d more", len(fs)-8))
			break
		}
		p := f.Path
		if f.Recent {
			p += " (changed this week)"
		}
		l = append(l, p)
	}
	return strings.Join(l, ", ")
}

// stillWritable keeps the findings that are still world-writable files or
// folders: a scan is shown until the next one, and a fixed file (or one an
// earlier scan reported wrongly, such as a socket) must not stay listed.
func stillWritable(env *host.Env, list []Finding) []Finding {
	var out []Finding
	for _, f := range list {
		real, err := env.Root.Path(f.Path)
		if err != nil {
			continue
		}
		info, err := os.Lstat(real)
		if err != nil {
			continue
		}
		m := info.Mode()
		if m&fs.ModeSocket != 0 || m&fs.ModeSymlink != 0 || m.Perm()&0o002 == 0 || (info.IsDir() && m&fs.ModeSticky != 0) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// hostScanChecks turns a scan into audit checks.
func hostScanChecks(env *host.Env, r *HostScan, running bool) []Check {
	if r == nil {
		d := "The first host scan (set-user-ID programs, file capabilities, world-writable files, scheduled tasks) has not finished yet."
		if running {
			d = "The host scan (set-user-ID programs, file capabilities, world-writable files, scheduled tasks) is running; its result appears here when done."
		}
		return []Check{{ID: "host_scan", Title: "Host scan", Status: "info", Severity: "low", Detail: d}}
	}
	when := time.Unix(r.Time, 0).UTC().Format("2006-01-02 15:04") + " UTC"
	suffix := " (scan of " + when + ")"
	if r.Truncated {
		suffix = " (scan of " + when + ", incomplete: too many files)"
	}
	var out []Check
	c := Check{ID: "host_setuid", Title: "Set-user-ID programs", Severity: "high", Status: "pass", Detail: "Every set-user-ID or set-group-ID program was installed by a package" + suffix + "."}
	if !r.Owned {
		c.Detail = "No set-user-ID program in places users can write to; the package lists could not be read to check the others" + suffix + "."
	}
	if len(r.Setuid) > 0 {
		c.Status, c.Detail = "fail", "Set-user-ID/-group-ID programs no package installed, or in places users can write to: "+listPaths(r.Setuid)+suffix+"."
		c.Remedy = "Find out where they came from; if unknown, remove the bit (chmod u-s,g-s FILE) or the file. Such programs run with their owner's rights."
	}
	out = append(out, c)
	c = Check{ID: "host_capabilities", Title: "File capabilities", Severity: "medium", Status: "pass", Detail: "No program outside the packages has file capabilities" + suffix + "."}
	if len(r.Caps) > 0 {
		c.Status, c.Detail = "warn", "Programs no package installed have file capabilities: "+listPaths(r.Caps)+suffix+"."
		c.Remedy = "Check them with getcap; remove unknown ones with setcap -r FILE."
	}
	out = append(out, c)
	c = Check{ID: "host_world_writable", Title: "World-writable system files", Severity: "medium", Status: "pass", Detail: "No system file or folder is writable by every account" + suffix + "."}
	writable := r.Writable // the stored scan stays as it was
	if env != nil {
		writable = stillWritable(env, writable)
	}
	if len(writable) > 0 {
		c.Status, c.Detail = "warn", "Writable by every account: "+listPaths(writable)+suffix+"."
		c.Remedy = "Remove the write permission for others (chmod o-w PATH), or add the sticky bit to shared folders (chmod +t)."
	}
	out = append(out, c)
	c = Check{ID: "host_persistence", Title: "Scheduled tasks and services", Severity: "medium", Status: "pass", Detail: "Every system cron job, systemd unit and boot script was installed by a package or NimVor" + suffix + "."}
	persist, knownN := unknownPersist(env, r.Persist)
	if knownN > 0 {
		c.Detail = fmt.Sprintf("Every system cron job, systemd unit and boot script was installed by a package or NimVor, or marked as known (%d unchanged since)%s.", knownN, suffix)
		c.Fix = "undo"
	}
	if len(persist) > 0 {
		c.Status, c.Detail = "info", "Not installed by a package or NimVor (review that you know them): "+listPaths(persist)+suffix+"."
		for _, p := range persist {
			if p.Recent || p.Path == "/etc/ld.so.preload" {
				c.Status = "warn"
			}
		}
		c.Remedy = "Remove the ones you do not recognise (and disable the services: systemctl disable --now UNIT). If you know them all (for example your hosting provider's), mark them as known: a file changed later is reported again."
		c.Fix = "apply"
	}
	return append(out, c)
}

// kernelSetting is a sysctl value the audit expects. Compat settings can
// affect some setups (noted in the remedy); the others are safe on a
// hosting server.
type kernelSetting struct {
	key    string
	want   func(v string) bool
	value  string // the recommended value
	compat string // why it may matter, for compat settings
}

func atLeast(n int) func(string) bool {
	return func(v string) bool { x, err := strconv.Atoi(strings.TrimSpace(v)); return err == nil && x >= n }
}
func equals(n int) func(string) bool {
	return func(v string) bool { x, err := strconv.Atoi(strings.TrimSpace(v)); return err == nil && x == n }
}

var kernelSettings = []kernelSetting{
	{"kernel.kptr_restrict", atLeast(1), "1", ""},
	{"kernel.dmesg_restrict", equals(1), "1", ""},
	{"kernel.randomize_va_space", equals(2), "2", ""},
	{"fs.protected_symlinks", equals(1), "1", ""},
	{"fs.protected_hardlinks", equals(1), "1", ""},
	{"fs.protected_fifos", atLeast(1), "1", ""},
	{"fs.protected_regular", atLeast(1), "1", ""},
	{"fs.suid_dumpable", equals(0), "0", ""},
	{"net.ipv4.tcp_syncookies", equals(1), "1", ""},
	{"net.ipv4.conf.all.accept_redirects", equals(0), "0", ""},
	{"net.ipv4.conf.all.send_redirects", equals(0), "0", "a server that routes for others sends redirects"},
	{"net.ipv4.conf.all.accept_source_route", equals(0), "0", ""},
	// Reverse-path filtering drops spoofed source addresses. Loose mode
	// (2) also suits servers with several network interfaces, which strict
	// mode (1) can break.
	{"net.ipv4.conf.all.rp_filter", atLeast(1), "2", ""},
	{"net.ipv6.conf.all.accept_redirects", equals(0), "0", ""},
	{"kernel.yama.ptrace_scope", atLeast(1), "1", "debuggers can then attach only to their own child processes"},
	{"kernel.unprivileged_bpf_disabled", atLeast(1), "1", "some monitoring tools load BPF programs without root"},
}

// sysctlCheck compares kernel settings with the recommended values
// (missing ones, e.g. without IPv6 or Yama, are skipped).
func sysctlCheck(env *host.Env) Check {
	var bad, lines []string
	forwarding, _ := env.Root.ReadFile(filepath.Join(env.Cfg.ProcDir, "sys/net/ipv4/ip_forward"))
	for _, s := range kernelSettings {
		// A server that routes for others (containers, VPN) sends
		// redirects; the fix leaves it alone, so the check does too.
		if s.key == "net.ipv4.conf.all.send_redirects" && strings.TrimSpace(string(forwarding)) == "1" {
			continue
		}
		b, err := env.Root.ReadFile(filepath.Join(env.Cfg.ProcDir, "sys", strings.ReplaceAll(s.key, ".", "/")))
		if err != nil {
			continue
		}
		if !s.want(string(b)) {
			d := s.key + " = " + strings.TrimSpace(string(b))
			if s.compat != "" {
				d += " (note: " + s.compat + ")"
			}
			bad = append(bad, d)
			lines = append(lines, s.key+" = "+s.value)
		}
	}
	c := Check{ID: "kernel_settings", Title: "Kernel security settings", Severity: "medium", Status: "pass", Detail: "The kernel's security settings have the recommended values."}
	if len(bad) > 0 {
		c.Status, c.Detail = "warn", "Settings differ from the recommended values: "+strings.Join(bad, "; ")+"."
		c.Remedy = "Apply the recommended values (they are kept in " + SysctlFile + " and can be undone), or put these lines in a file in /etc/sysctl.d and run sysctl --system: " + strings.Join(lines, "; ")
		c.Fix = "apply"
	} else if KernelFixed(env) {
		c.Fix = "undo"
	}
	return c
}

// hostScanEnabled: the audit starts background scans (tests turn it off).
var hostScanEnabled = true

func hostChecks(env *host.Env) []Check {
	out := []Check{sysctlCheck(env)}
	if !hostScanEnabled {
		return out
	}
	latest, running := scanner.Latest(env, false, func() HostScan {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		owned, err := OwnedFiles(ctx, env)
		if err != nil {
			owned = nil
		}
		return Scan(ctx, env, owned, time.Now())
	})
	return append(out, hostScanChecks(env, latest, running)...)
}
