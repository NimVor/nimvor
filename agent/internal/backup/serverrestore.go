package backup

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/selinux"
)

// ServerComponents are the parts of a server backup that can be restored in
// place. Each replaces one directory as a whole, is validated before any
// service reloads, and is rolled back if validation or the reload fails.
// Firewall, SSH, systemd units and /etc/whacp are never restored in place
// (a mistake there can lock the operator out or disconnect the panel); use
// ExtractOnly and copy what you need by hand.
var ServerComponents = []string{"nginx", "php", "mysql", "redis", "letsencrypt", "fail2ban", "cron"}

// component returns the folder of a restorable component on this system.
func (s Service) component(c string) (string, bool) {
	for _, k := range ServerComponents {
		if k == c {
			return s.Env.M.ServerConfig[c], true
		}
	}
	return "", false
}

// ServerRestoreRequest selects what to restore from a server backup.
type ServerRestoreRequest struct {
	ID          string   `json:"id"`
	Components  []string `json:"components"`
	ExtractOnly bool     `json:"extract_only"`
	Remote      *Remote  `json:"remote"`
	Confirm     bool     `json:"confirm"`
}

func (r *ServerRestoreRequest) validate() error {
	if !idRe.MatchString(r.ID) {
		return errors.New("id: invalid backup id")
	}
	if !r.Confirm {
		return errors.New("confirm: a server restore must be confirmed")
	}
	seen := map[string]bool{}
	for _, c := range r.Components {
		if !slices.Contains(ServerComponents, c) {
			return fmt.Errorf("components: %q cannot be restored in place", c)
		}
		if seen[c] {
			return fmt.Errorf("components: %q is listed twice", c)
		}
		seen[c] = true
	}
	if r.ExtractOnly == (len(r.Components) > 0) {
		return errors.New("choose components to restore, or extract_only")
	}
	if r.Remote != nil {
		if err := r.Remote.Validate(); err != nil {
			return fmt.Errorf("remote: %v", err)
		}
	}
	sort.Strings(r.Components)
	return nil
}

// RestoreServer restores components of a server configuration backup, or
// extracts the whole archive for manual review (called inside a job).
func (s Service) RestoreServer(ctx context.Context, r ServerRestoreRequest, log io.Writer) (map[string]any, error) {
	m, dir, err := s.Load("", r.ID)
	if err != nil || m.Kind != "server" || len(m.Files) != 1 {
		return nil, fmt.Errorf("the server backup was not found")
	}
	path, downloaded, err := s.fetch(ctx, m, dir, m.Files[0], r.Remote, log)
	if err != nil {
		return nil, err
	}
	if downloaded {
		defer os.Remove(path)
	}
	fmt.Fprintln(log, "Checksum verified.")

	stageHost := filepath.Join(s.Env.Cfg.StateDir, "restore", m.ID+"-"+s.Env.Now().UTC().Format("20060102T150405Z"))
	if _, err := s.Env.Writer.MkdirAll(stageHost, 0o700, agentfs.RootOwner); err != nil {
		return nil, err
	}
	stage, _ := s.real(stageHost)
	keepStage := r.ExtractOnly
	defer func() {
		if !keepStage {
			os.RemoveAll(stage)
		}
	}()
	rd, done, err := s.open(m, path)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(log, "Extracting the archive into %s…\n", stageHost)
	n, err := s.extract(rd, stage)
	done()
	if err != nil {
		return nil, fmt.Errorf("the archive could not be extracted: %v", err)
	}
	fmt.Fprintf(log, "%d entries extracted.\n", n)
	if r.ExtractOnly {
		top, _ := os.ReadDir(filepath.Join(stage, "etc"))
		var parts []string
		for _, d := range top {
			parts = append(parts, "/etc/"+d.Name())
		}
		fmt.Fprintln(log, "Nothing on the server was changed. Review the files and copy what you need.")
		return map[string]any{"id": m.ID, "extracted_to": stageHost, "contents": parts}, nil
	}

	for _, c := range r.Components {
		dir, _ := s.component(c)
		if _, err := os.Lstat(filepath.Join(stage, strings.TrimPrefix(dir, "/"))); err != nil {
			return nil, fmt.Errorf("this backup does not contain %s; nothing was changed", dir)
		}
	}
	fmt.Fprintln(log, "Creating a safety backup of the current configuration first…")
	_, keyErr := os.Stat(func() string { p, _ := s.real(s.keyPath()); return p }())
	safety, err := s.CreateServer(ctx, keyErr == nil, "Before restoring "+m.ID, nil, true, log)
	if err != nil {
		return nil, fmt.Errorf("the safety backup failed, nothing was restored: %v", err)
	}

	type swapped struct{ cur, old string }
	var done2 []swapped
	suffix := ".whacp-old-" + m.ID[len(m.ID)-6:]
	rollback := func() {
		for i := len(done2) - 1; i >= 0; i-- {
			sw := done2[i]
			os.RemoveAll(sw.cur)
			if sw.old != "" {
				if err := os.Rename(sw.old, sw.cur); err != nil {
					fmt.Fprintf(log, "Rollback of %s failed: %v (the previous copy is %s)\n", sw.cur, err, sw.old)
				}
			}
		}
	}
	for _, c := range r.Components {
		host, _ := s.component(c)
		cur, err := s.real(host)
		if err != nil {
			rollback()
			return nil, err
		}
		sw := swapped{cur: cur}
		if _, err := os.Lstat(cur); err == nil {
			sw.old = cur + suffix
			os.RemoveAll(sw.old)
			if err := os.Rename(cur, sw.old); err != nil {
				rollback()
				return nil, fmt.Errorf("could not set aside %s: %v", host, err)
			}
		}
		done2 = append(done2, sw)
		fmt.Fprintf(log, "Restoring %s…\n", host)
		if err := moveTree(filepath.Join(stage, strings.TrimPrefix(host, "/")), cur); err != nil {
			rollback()
			return nil, fmt.Errorf("restoring %s failed: %v", host, err)
		}
		// SELinux: files extracted in the staging folder carry its label;
		// services may only read the policy's labels for this folder.
		if err := selinux.Restore(ctx, s.Env, host); err != nil {
			fmt.Fprintf(log, "Warning: %v\n", err)
		}
	}
	has := map[string]bool{}
	for _, c := range r.Components {
		has[c] = true
	}
	var warnings []string
	versions := s.phpVersions()
	fmt.Fprintln(log, "Validating…")
	vErr := func() error {
		if has["nginx"] || has["letsencrypt"] {
			if err := s.Env.NginxTest(ctx); err != nil {
				return err
			}
		}
		if has["php"] {
			for _, v := range versions {
				if !s.Env.Runner.Available(fmt.Sprintf(s.Env.M.PHPFPMBinary, v)) {
					warnings = append(warnings, "PHP "+v+" is configured in the backup but not installed here; its configuration was restored but not loaded.")
					continue
				}
				if err := s.Env.FPMTest(ctx, v); err != nil {
					return err
				}
			}
		}
		return nil
	}()
	if vErr != nil {
		rollback()
		return nil, fmt.Errorf("the restored configuration is invalid, so the previous one was put back: %v", vErr)
	}
	fmt.Fprintln(log, "Reloading services…")
	aErr := s.applyRestored(ctx, has, versions)
	if aErr != nil {
		rollback()
		s.applyRestored(ctx, has, s.phpVersions())
		return nil, fmt.Errorf("a service did not come back with the restored configuration, so the previous one was put back: %v", aErr)
	}
	for _, sw := range done2 {
		if sw.old != "" {
			os.RemoveAll(sw.old)
		}
	}
	for _, w := range warnings {
		fmt.Fprintln(log, "Warning: "+w)
	}
	fmt.Fprintln(log, "Restore complete.")
	return map[string]any{"id": m.ID, "restored": r.Components, "safety_backup": safety.ID, "warnings": warnings}, nil
}

// phpVersions lists versions with an FPM configuration directory
// (/etc/php/8.3/fpm, or Remi's /etc/opt/remi/php83/php-fpm.d).
func (s Service) phpVersions() []string {
	real, err := s.real(s.Env.M.ServerConfig["php"])
	if err != nil {
		return nil
	}
	entries, _ := os.ReadDir(real)
	var out []string
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(real, e.Name(), "fpm")); err == nil && strings.Count(e.Name(), ".") == 1 {
			out = append(out, e.Name())
		}
		if m := remiDirRe.FindStringSubmatch(e.Name()); m != nil {
			if _, err := os.Stat(filepath.Join(real, e.Name(), "php-fpm.d")); err == nil {
				out = append(out, m[1]+"."+m[2])
			}
		}
	}
	return out
}

var remiDirRe = regexp.MustCompile(`^php([0-9])([0-9]+)$`)

// applyRestored reloads or restarts what the restored components configure,
// and checks the services are running afterwards.
func (s Service) applyRestored(ctx context.Context, has map[string]bool, versions []string) error {
	e := s.Env
	if has["nginx"] || has["letsencrypt"] {
		if err := e.ReloadNginx(ctx); err != nil {
			return err
		}
	}
	if has["php"] {
		for _, v := range versions {
			unit := e.M.FPMUnit(v)
			if e.Runner.Available(fmt.Sprintf(e.M.PHPFPMBinary, v)) && e.UnitActive(ctx, unit) {
				if err := e.Systemctl(ctx, "reload", unit); err != nil {
					return err
				}
			}
		}
	}
	restart := func(unit string) error {
		if !e.UnitActive(ctx, unit) {
			return nil
		}
		if err := e.Systemctl(ctx, "restart", unit); err != nil {
			return err
		}
		if !e.UnitActive(ctx, unit) {
			return fmt.Errorf("%s is not running after the restart", unit)
		}
		return nil
	}
	if has["mysql"] {
		for _, u := range e.M.MySQLUnit {
			if err := restart(u); err != nil {
				return err
			}
		}
	}
	if has["redis"] {
		if err := restart(e.M.RedisUnit); err != nil {
			return err
		}
	}
	if has["fail2ban"] {
		if err := restart("fail2ban.service"); err != nil {
			return err
		}
	}
	return nil
}

// extract unpacks a server archive into dest without ever writing outside
// it: entries must be local paths, no parent may be a symlink, and only
// directories, regular files and symlinks are created (set-id bits are
// dropped). Owners are mapped by name through this server's account files.
func (s Service) extract(r io.Reader, dest string) (int, error) {
	root, err := os.OpenRoot(dest)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	uids := s.idMap(s.Env.Platform.Paths().PasswdFile)
	gids := s.idMap("/etc/group")
	tr := tar.NewReader(r)
	n := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		name := filepath.Clean(strings.TrimSuffix(hdr.Name, "/"))
		if !filepath.IsLocal(name) || !strings.HasPrefix(name, "etc/") && name != "etc" && !strings.HasPrefix(name, strings.TrimPrefix(s.Env.Cfg.StateDir, "/")+"/") {
			return n, fmt.Errorf("unexpected entry %q", hdr.Name)
		}
		if err := noLinkParents(dest, name); err != nil {
			return n, err
		}
		full := filepath.Join(dest, name)
		if dir := filepath.Dir(name); dir != "." {
			if err := os.MkdirAll(filepath.Join(dest, dir), 0o755); err != nil {
				return n, err
			}
		}
		// A repeated entry must not write through an earlier symlink.
		if info, err := os.Lstat(full); err == nil && info.Mode()&fs.ModeSymlink != 0 {
			os.Remove(full)
		}
		mode := fs.FileMode(hdr.Mode) & 0o1777
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.Mkdir(full, 0o700); err != nil && !os.IsExist(err) {
				return n, err
			}
			os.Chmod(full, mode)
		case tar.TypeReg:
			f, err := root.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
			if err != nil {
				return n, err
			}
			_, err = io.Copy(f, io.LimitReader(tr, hdr.Size))
			if err == nil {
				err = f.Chmod(mode)
			}
			f.Close()
			if err != nil {
				return n, err
			}
		case tar.TypeSymlink:
			// Links are recreated as links and never followed here.
			if err := os.Symlink(hdr.Linkname, full); err != nil {
				return n, err
			}
		default:
			continue
		}
		uid, gid := hdr.Uid, hdr.Gid
		if id, ok := uids[hdr.Uname]; ok {
			uid = id
		}
		if id, ok := gids[hdr.Gname]; ok {
			gid = id
		}
		os.Lchown(full, uid, gid)
		n++
	}
}

// noLinkParents refuses a path whose existing parents inside dest are
// symlinks (a crafted archive could otherwise redirect writes).
func noLinkParents(dest, name string) error {
	p := dest
	parts := strings.Split(name, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		if err != nil {
			return nil
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("entry %q is below a symbolic link", name)
		}
	}
	return nil
}

// idMap reads name → id from a passwd- or group-format file on the host.
func (s Service) idMap(hostPath string) map[string]int {
	out := map[string]int{}
	b, err := s.Env.Root.ReadFile(hostPath)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) >= 3 {
			if id, err := strconv.Atoi(f[2]); err == nil {
				out[f[0]] = id
			}
		}
	}
	return out
}

// moveTree moves src to dst, copying (and preserving modes, owners and
// links) when they are on different filesystems.
func moveTree(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if err := os.Symlink(link, target); err != nil {
				return err
			}
		case info.IsDir():
			if err := os.Mkdir(target, info.Mode().Perm()); err != nil && !os.IsExist(err) {
				return err
			}
		default:
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
			if err != nil {
				in.Close()
				return err
			}
			_, err = io.Copy(out, in)
			in.Close()
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			os.Lchown(target, int(st.Uid), int(st.Gid))
		}
		if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			return nil
		}
		return os.Chmod(target, info.Mode()&0o1777)
	})
}
