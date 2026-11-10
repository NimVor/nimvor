package migrate

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Importing a cPanel backup archive (cpmove-USER.tar.gz or a full backup-*.tar.gz). The archive is a file on
// THIS server, extracted into the agent's own state folder with every path confined to it; what is found
// there is described like any other source (accounts, websites, databases, mailboxes, forwarders, cron,
// FTP, DNS) and then copied by the same engine, with a local puller instead of SSH. The extracted copy is
// only read; it is deleted on request. Nothing is changed on the archive file.

// archiveRoots are the folders an archive may be taken from.
var archiveRoots = []string{"/root", "/home", "/var/backups", "/tmp", "/var/tmp"}

var (
	archiveName   = regexp.MustCompile(`(?i)^(cpmove|backup)-[A-Za-z0-9._-]+\.(tar\.gz|tgz|tar)$`)
	archiveIDRe   = regexp.MustCompile(`^[0-9a-f]{16}$`)
	archiveDBName = regexp.MustCompile(`^[A-Za-z0-9_$-]{1,64}$`)
)

const maxArchiveFiles = 3_000_000

// ArchiveInfo is one candidate archive found on this server.
type ArchiveInfo struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	SizeKB   int64  `json:"size_kb"`
	Modified string `json:"modified"`
}

func archivesRoot(env *host.Env) (string, error) {
	return env.Root.Path(filepath.Join(env.Cfg.StateDir, "archives"))
}

// ListArchives finds files that look like cPanel backups in the usual folders (one level deep, and one
// more under /home). Only names are matched; nothing is opened.
func ListArchives(env *host.Env) []ArchiveInfo {
	var out []ArchiveInfo
	var scan func(logical string, depth int)
	scan = func(logical string, depth int) {
		real, err := env.Root.Path(logical)
		if err != nil {
			return
		}
		ents, err := os.ReadDir(real)
		if err != nil {
			return
		}
		for _, e := range ents {
			lp := filepath.Join(logical, e.Name())
			if e.IsDir() {
				if depth > 0 && !strings.HasPrefix(e.Name(), ".") {
					scan(lp, depth-1)
				}
				continue
			}
			if !archiveName.MatchString(e.Name()) {
				continue
			}
			fi, err := e.Info()
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
			out = append(out, ArchiveInfo{Path: lp, Name: e.Name(), SizeKB: fi.Size() / 1024, Modified: fi.ModTime().UTC().Format(time.RFC3339)})
		}
	}
	for _, r := range archiveRoots {
		d := 0
		if r == "/home" {
			d = 1
		}
		scan(r, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified > out[j].Modified })
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

// checkArchivePath validates an archive path given by a person and returns the real file.
func checkArchivePath(env *host.Env, p string) (string, error) {
	if !strings.HasPrefix(p, "/") || !ValidRemotePath(p) {
		return "", fmt.Errorf("give the full path of the archive, for example /root/cpmove-user.tar.gz")
	}
	if !archiveName.MatchString(filepath.Base(p)) {
		return "", fmt.Errorf("the file name must look like cpmove-USER.tar.gz or backup-…tar.gz")
	}
	ok := false
	for _, r := range archiveRoots {
		if strings.HasPrefix(p, r+"/") {
			ok = true
		}
	}
	if !ok {
		return "", fmt.Errorf("put the archive under /root, /home, /var/backups or /tmp")
	}
	real, err := env.Root.Path(p)
	if err != nil {
		return "", err
	}
	fi, err := os.Lstat(real)
	if err != nil {
		return "", fmt.Errorf("the archive %s was not found", p)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", p)
	}
	return real, nil
}

func inside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ExtractArchive unpacks an archive into a new private folder and returns its id. Every entry is confined to
// that folder: absolute names and ".." are refused, links may only point inside it, devices and hard links are
// skipped, set-id bits are dropped, and the extraction stops when the disk would be filled.
func ExtractArchive(ctx context.Context, env *host.Env, p string, log io.Writer) (string, error) {
	real, err := checkArchivePath(env, p)
	if err != nil {
		return "", err
	}
	root, err := archivesRoot(env)
	if err != nil {
		return "", err
	}
	f, err := os.Open(real)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, _ := f.Stat()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	free := freeKB(root)
	if need := fi.Size()/1024*2 + reserveKB; free < need {
		return "", fmt.Errorf("the disk has %s free but about %s are needed to unpack this archive (twice its size, plus NimVor's reserve)", kb(free), kb(need))
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])
	dest := filepath.Join(root, id)
	if err := os.Mkdir(dest, 0o700); err != nil {
		return "", err
	}
	fail := func(err error) (string, error) {
		os.RemoveAll(dest) // the folder this call just created
		return "", err
	}
	var r io.Reader = bufio.NewReaderSize(f, 1<<20)
	head := make([]byte, 2)
	if _, err := io.ReadFull(f, head); err != nil {
		return fail(fmt.Errorf("the archive is empty or unreadable"))
	}
	f.Seek(0, io.SeekStart)
	r = bufio.NewReaderSize(f, 1<<20)
	if head[0] == 0x1f && head[1] == 0x8b {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return fail(fmt.Errorf("the archive is damaged: %v", err))
		}
		defer zr.Close()
		r = zr
	}
	quota := (free - reserveKB) * 1024
	var written int64
	files, skipped := 0, 0
	tr := tar.NewReader(r)
	for {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail(fmt.Errorf("the archive is damaged: %v", err))
		}
		name := strings.TrimPrefix(filepath.ToSlash(h.Name), "./")
		if name == "" || name == "." {
			continue
		}
		if strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") || strings.HasSuffix(name, "/..") || len(name) > 4000 {
			return fail(fmt.Errorf("the archive holds a path that leaves its folder (%q); it was not unpacked", truncate(name, 80)))
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if !inside(dest, target) {
			return fail(fmt.Errorf("the archive holds a path that leaves its folder; it was not unpacked"))
		}
		files++
		if files > maxArchiveFiles {
			return fail(fmt.Errorf("the archive holds more than %d entries", maxArchiveFiles))
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fail(err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if h.Size > quota-written {
				return fail(fmt.Errorf("the disk would be full: stopped after %s (NimVor keeps %s free)", kb(written/1024), kb(reserveKB)))
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fail(err)
			}
			if real, err := filepath.EvalSymlinks(filepath.Dir(target)); err != nil || !inside(dest, real) {
				return fail(fmt.Errorf("the archive writes through a link that leaves its folder; it was not unpacked"))
			}
			mode := os.FileMode(h.Mode&0o755) | 0o600
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, mode)
			if err != nil {
				return fail(err)
			}
			n, err := io.CopyN(out, tr, h.Size)
			written += n
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return fail(fmt.Errorf("a file could not be unpacked: %v", err))
			}
		case tar.TypeSymlink:
			link := filepath.ToSlash(h.Linkname)
			resolved := filepath.Join(filepath.Dir(target), filepath.FromSlash(link))
			if strings.HasPrefix(link, "/") || !inside(dest, resolved) {
				skipped++ // a link to somewhere else is never recreated
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fail(err)
			}
			os.Remove(target)
			if err := os.Symlink(h.Linkname, target); err != nil {
				return fail(err)
			}
		default:
			skipped++ // hard links, devices, fifos
		}
		if files%5000 == 0 && log != nil {
			fmt.Fprintf(log, "Unpacked %d entries, %s…\n", files, kb(written/1024))
		}
	}
	if log != nil {
		fmt.Fprintf(log, "Unpacked %d entries, %s", files, kb(written/1024))
		if skipped > 0 {
			fmt.Fprintf(log, " (%d links and special files were left out)", skipped)
		}
		fmt.Fprintln(log, ".")
	}
	return id, nil
}

// ForgetArchive deletes an extracted copy.
func ForgetArchive(env *host.Env, id string) error {
	if !archiveIDRe.MatchString(id) {
		return fmt.Errorf("not an archive id")
	}
	root, err := archivesRoot(env)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, id)
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return nil
	}
	return os.RemoveAll(dir)
}

// archiveDir returns the extracted folder of an id.
func archiveDir(env *host.Env, id string) (string, error) {
	if !archiveIDRe.MatchString(id) {
		return "", fmt.Errorf("not an archive id")
	}
	root, err := archivesRoot(env)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, id)
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("the unpacked archive is gone (it may have been deleted); unpack it again")
	}
	return dir, nil
}

// archiveTop finds the folder that holds cp/, userdata/, homedir/ and mysql/ (cpmove-USER, or backup-…).
func archiveTop(dir string) (string, error) {
	has := func(p string) bool {
		for _, n := range []string{"cp", "userdata", "homedir"} {
			if fi, err := os.Stat(filepath.Join(p, n)); err == nil && fi.IsDir() {
				return true
			}
		}
		return false
	}
	if has(dir) {
		return dir, nil
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.IsDir() && has(filepath.Join(dir, e.Name())) {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("this is not a cPanel account backup: no cp/, userdata/ or homedir/ folder was found in it")
}

func readCapped(p string, max int64) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, max))
	return string(b)
}

func dirSizeKB(p string) int64 {
	var n int64
	filepath.WalkDir(p, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += (fi.Size() + 1023) / 1024
			}
		}
		return nil
	})
	return n
}

var cpOrigHome = regexp.MustCompile(`(?m)^homedir:\s*(\S+)\s*$`)

// DiscoverArchive describes an extracted cPanel backup as a discovery, reusing the cPanel parser: the same
// sections a live cPanel read produces are rebuilt from the files of the backup, with the account's old home
// folder replaced by the extracted one, so website and mail paths point at files that exist here.
func DiscoverArchive(env *host.Env, id string, progress func(string)) (*Discovery, error) {
	if progress == nil {
		progress = func(string) {}
	}
	dir, err := archiveDir(env, id)
	if err != nil {
		return nil, err
	}
	top, err := archiveTop(dir)
	if err != nil {
		return nil, err
	}
	user := ""
	if ents, err := os.ReadDir(filepath.Join(top, "cp")); err == nil {
		for _, e := range ents {
			if !e.IsDir() {
				user = e.Name()
				break
			}
		}
	}
	if user == "" {
		b := filepath.Base(top)
		b = strings.TrimPrefix(b, "cpmove-")
		if i := strings.LastIndex(b, "_"); strings.HasPrefix(b, "backup-") && i >= 0 {
			b = b[i+1:]
		}
		user = b
	}
	if !pleskLogin.MatchString(user) {
		return nil, fmt.Errorf("the account name in the backup could not be read")
	}
	home := filepath.Join(top, "homedir")
	if fi, err := os.Stat(home); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("the backup has no homedir folder: it holds no website files")
	}
	progress("Reading the account " + user + "…")
	var b strings.Builder
	origHome := "/home/" + user
	files, _ := os.ReadDir(filepath.Join(top, "userdata"))
	for _, e := range files {
		n := e.Name()
		if e.IsDir() || strings.HasSuffix(n, ".cache") || strings.HasSuffix(n, ".stor") || strings.HasSuffix(n, "_SSL") || strings.HasSuffix(n, ".json") || strings.HasSuffix(n, ".yaml") || strings.Contains(n, "..") || strings.ContainsAny(n, " /") {
			continue
		}
		body := readCapped(filepath.Join(top, "userdata", n), 1<<20)
		if m := cpOrigHome.FindStringSubmatch(body); m != nil && strings.HasPrefix(m[1], "/") {
			origHome = strings.TrimSuffix(m[1], "/")
		}
		fmt.Fprintf(&b, "##file /var/cpanel/userdata/%s/%s\n%s\n", user, n, body)
	}
	rewrite := func(s string) string {
		if origHome == "" || origHome == "/" {
			return s
		}
		return strings.ReplaceAll(s, origHome+"/", home+"/")
	}
	text := rewrite(b.String())
	var rest strings.Builder
	rest.WriteString("##home\n" + home + "\n##mail\n")
	var maildirs []string
	if doms, err := os.ReadDir(filepath.Join(home, "etc")); err == nil {
		for _, d := range doms {
			if !d.IsDir() || !domainName.MatchString(d.Name()) {
				continue
			}
			pw := readCapped(filepath.Join(home, "etc", d.Name(), "passwd"), 4<<20)
			if pw == "" {
				continue
			}
			fmt.Fprintf(&rest, "##dom %s\n", d.Name())
			for _, l := range strings.Split(pw, "\n") {
				if u, _, ok := strings.Cut(l, ":"); ok && u != "" {
					fmt.Fprintf(&rest, "%s\n", u)
					maildirs = append(maildirs, filepath.Join(home, "mail", d.Name(), u))
				}
			}
		}
	}
	rest.WriteString("##msize\n")
	for _, m := range maildirs {
		if fi, err := os.Stat(m); err == nil && fi.IsDir() {
			fmt.Fprintf(&rest, "%d\t%s/\n", dirSizeKB(m), m)
		}
	}
	rest.WriteString("##fwd\n")
	if vas, err := os.ReadDir(filepath.Join(top, "va")); err == nil {
		for _, v := range vas {
			if v.IsDir() || !domainName.MatchString(v.Name()) {
				continue
			}
			fmt.Fprintf(&rest, "##vdom %s\n%s\n", v.Name(), readCapped(filepath.Join(top, "va", v.Name()), 1<<20))
		}
	}
	rest.WriteString("##ftp\n")
	rest.WriteString(rewrite(readCapped(filepath.Join(top, "proftpdpasswd"), 1<<20)) + "\n")
	rest.WriteString("##cron\n")
	rest.WriteString(readCapped(filepath.Join(top, "cron", user), 1<<20) + "\n")

	a := newAccount(user, home)
	parseCpanelAccount(a, text+rest.String())
	d := &Discovery{
		Panel:  Detection{Panel: "cpmove", Name: "cPanel backup archive", Support: Experimental},
		Server: ServerInfo{OS: "backup archive (unpacked on this server)", Root: true, Rsync: true, Mysqldump: true, Engine: "mysql"},
	}
	if a.PHP != "" {
		d.Server.PHP = []string{a.PHP}
	}
	progress("Measuring the website folders…")
	for i := range a.Domains {
		a.Domains[i].SizeKB = dirSizeKB(a.Domains[i].DocRoot)
	}
	if dbs, err := os.ReadDir(filepath.Join(top, "mysql")); err == nil {
		for _, e := range dbs {
			name, ok := strings.CutSuffix(e.Name(), ".sql")
			if !ok || e.IsDir() || !archiveDBName.MatchString(name) || name == "mysql" {
				continue
			}
			var kbsz int64 = -1
			if fi, err := e.Info(); err == nil {
				kbsz = fi.Size() / 1024
			}
			a.Databases = append(a.Databases, Database{Name: name, Engine: "mysql", SizeKB: kbsz})
		}
	}
	if len(a.Domains) == 0 {
		d.Warnings = append(d.Warnings, "No websites were found in the backup's userdata folder.")
	}
	d.Warnings = append(d.Warnings, "A backup archive is a snapshot: there is nothing to synchronise later, and database users and passwords are not restored.")
	d.Accounts = []Account{*a}
	d.NotMigrated = append(d.NotMigrated, notMigrated...)
	return d, nil
}

// ArchiveZone returns a domain's zone file from an extracted backup.
func ArchiveZone(env *host.Env, id, domain string) (string, error) {
	if !domainName.MatchString(domain) {
		return "", fmt.Errorf("%q is not a domain name", domain)
	}
	dir, err := archiveDir(env, id)
	if err != nil {
		return "", err
	}
	top, err := archiveTop(dir)
	if err != nil {
		return "", err
	}
	text := readCapped(filepath.Join(top, "dnszones", domain+".db"), maxZoneBytes+1)
	if text == "" {
		return "", fmt.Errorf("the zone file of %s is not in the backup", domain)
	}
	if len(text) > maxZoneBytes {
		return "", fmt.Errorf("the zone file of %s is larger than 1 MB", domain)
	}
	return strings.ReplaceAll(text, "\r", ""), nil
}

// archivePuller lets the migration engine read an extracted backup as if it were a source: files are
// copied with a local rsync, and the two questions the engine asks a source about a database are answered
// from the backup's SQL file.
type archivePuller struct {
	env  *host.Env
	top  string
	desc string
}

func newArchivePuller(env *host.Env, id string) (*archivePuller, error) {
	dir, err := archiveDir(env, id)
	if err != nil {
		return nil, err
	}
	top, err := archiveTop(dir)
	if err != nil {
		return nil, err
	}
	return &archivePuller{env: env, top: top, desc: "backup archive " + id}, nil
}

func (a *archivePuller) Describe() string { return a.desc }

var (
	apDump  = regexp.MustCompile(`mysqldump .*-- ([A-Za-z0-9_$-]{1,64}) \| gzip -c`)
	apCount = regexp.MustCompile(`table_schema = ([A-Za-z0-9_$-]{1,64})\b`)
)

func (a *archivePuller) sqlFile(name string) (string, error) {
	if !archiveDBName.MatchString(name) {
		return "", fmt.Errorf("the database name %q cannot be used", name)
	}
	p := filepath.Join(a.top, "mysql", name+".sql")
	if real, err := filepath.EvalSymlinks(p); err != nil || !inside(a.top, real) {
		return "", fmt.Errorf("the backup holds no dump of %s", name)
	}
	return p, nil
}

func (a *archivePuller) Exec(ctx context.Context, cmd string, stdout io.Writer) (string, error) {
	// The engine quotes a quoted command; only the two names it asks about matter, so quotes are dropped.
	cmd = strings.NewReplacer("'", "", `\`, "").Replace(cmd)
	if m := apDump.FindStringSubmatch(cmd); m != nil {
		if stdout == nil {
			return "", fmt.Errorf("no output to write the dump to")
		}
		p, err := a.sqlFile(m[1])
		if err != nil {
			return "", err
		}
		f, err := os.Open(p)
		if err != nil {
			return "", err
		}
		defer f.Close()
		zw := gzip.NewWriter(stdout)
		if _, err := io.Copy(zw, f); err != nil {
			return "", err
		}
		return "", zw.Close()
	}
	if m := apCount.FindStringSubmatch(cmd); m != nil {
		p, err := a.sqlFile(m[1])
		if err != nil {
			return "", err
		}
		f, err := os.Open(p)
		if err != nil {
			return "", err
		}
		defer f.Close()
		n := 0
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "CREATE TABLE ") {
				n++
			}
		}
		return strconv.Itoa(n) + "\n", sc.Err()
	}
	return "", fmt.Errorf("this question cannot be asked of a backup archive")
}

func (a *archivePuller) Rsync(ctx context.Context, extra []string, remote, local string, stdout io.Writer) (string, error) {
	real, err := filepath.EvalSymlinks(strings.TrimSuffix(remote, "/"))
	if err != nil || !inside(a.top, real) {
		return "", fmt.Errorf("%s is not inside the unpacked backup", remote)
	}
	args := append(append([]string{}, extra...), "--", strings.TrimSuffix(real, "/")+"/", local)
	out, err := a.env.Runner.RunWith(ctx, "rsync", execx.Opts{Stdout: stdout, Timeout: -1}, args...)
	text := host.Output(out)
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("%s", strings.TrimSpace(text))
	}
	return text, nil
}

// cpmoveAdapter only lists the archive source in the wizard: a backup archive is not read over SSH, so
// it is never detected and has no live discovery (see ExtractArchive and DiscoverArchive).
type cpmoveAdapter struct{}

func (cpmoveAdapter) ID() string       { return "cpmove" }
func (cpmoveAdapter) Name() string     { return "cPanel backup archive (cpmove)" }
func (cpmoveAdapter) Support() Support { return Experimental }
func (cpmoveAdapter) Detect(ctx context.Context, t Transport) (Detection, bool, error) {
	return Detection{}, false, nil
}
func (cpmoveAdapter) Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error) {
	return nil, fmt.Errorf("a backup archive is unpacked on this server, not read over SSH")
}
