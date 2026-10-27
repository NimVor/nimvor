// Package adopt imports an existing (non-WhaCP) website into WhaCP.
//
// WhaCP never edits configuration it did not create, so an import is a
// migration into a new managed website:
//
//  1. preview (read-only): which Nginx files serve the site and how each
//     would be disabled, what would block the import, the files to copy,
//     the database the application uses and the exact configuration WhaCP
//     would generate;
//  2. run (job): create the WhaCP website (disabled), copy the files in as
//     the new account, then switch: disable the old vhost (remove its
//     sites-enabled link, or move a file out of an include directory into
//     the state directory), enable the new site and validate with nginx -t.
//     Any failure puts the old configuration back;
//  3. undo: re-enable the old configuration and disable the new site.
//
// The old configuration files themselves are never modified.
package adopt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/nginx"
	"github.com/NimVor/nimvor/agent/internal/sites"
)

// Step describes how one old vhost file is disabled.
type Step struct {
	File   string   `json:"file"`             // the vhost file (resolved)
	Action string   `json:"action"`           // unlink, move
	Path   string   `json:"path"`             // the link or file acted on
	Names  []string `json:"names"`            // server names in the file
	Stored string   `json:"stored,omitempty"` // where a moved file is kept
}

// Preview is the import plan.
type Preview struct {
	Domain    string   `json:"domain"`
	Source    string   `json:"source"`
	Target    string   `json:"target"`
	User      string   `json:"user"`
	Steps     []Step   `json:"steps"`
	Files     int64    `json:"files"`
	Bytes     int64    `json:"bytes"`
	Skipped   []string `json:"skipped_links"`
	Database  string   `json:"database,omitempty"`
	DBSource  string   `json:"database_source,omitempty"`
	NewConfig string   `json:"new_config"`
	OldConfig string   `json:"old_config"`
	Blockers  []string `json:"blockers"`
	Warnings  []string `json:"warnings"`
}

// Request identifies what to import.
type Request struct {
	Spec   sites.Spec `json:"spec"`
	Source string     `json:"source"` // current document root
}

// Record is kept in the state directory so an import can be undone.
type Record struct {
	Domain string    `json:"domain"`
	Steps  []Step    `json:"steps"`
	At     time.Time `json:"at"`
}

var forbiddenSources = []string{"/", "/etc", "/usr", "/bin", "/sbin", "/lib", "/boot", "/dev", "/proc", "/sys", "/root", "/run", "/var/lib", "/var/log"}

// Service implements imports.
type Service struct {
	Env   *host.Env
	Sites *sites.Manager
}

func (s Service) recordPath(domain string) string {
	return filepath.Join(s.Env.Cfg.StateDir, "imports", domain+".json")
}

// Plan builds the preview. It changes nothing.
func (s Service) Plan(ctx context.Context, r Request) (*Preview, error) {
	e := s.Env
	spec := r.Spec
	if err := spec.Normalize(e.Cfg.SitesRoot, e.Cfg.StateDir); err != nil {
		return nil, host.Invalid(err)
	}
	p := &Preview{Domain: spec.Domain, Source: filepath.Clean(r.Source), Target: spec.DocumentRoot, User: spec.User, Steps: []Step{}, Skipped: []string{}, Blockers: []string{}, Warnings: []string{}}
	want := map[string]bool{}
	for _, n := range spec.Names() {
		want[n] = true
	}

	// Which vhost files serve these names, and does each serve only them?
	ng := e.Discovery.Nginx(ctx)
	files := map[string][]nginx.VHost{}
	for _, v := range ng.VHosts {
		if strings.HasPrefix(filepath.Base(v.File), "whacp-") {
			continue
		}
		for _, n := range v.ServerNames {
			if want[strings.ToLower(n)] {
				files[v.File] = nil
			}
		}
	}
	rootOK := false
	for f := range files {
		for _, v := range ng.VHosts {
			if v.File == f {
				files[f] = append(files[f], v)
			}
		}
		var names []string
		for _, v := range files[f] {
			if v.Root != "" && filepath.Clean(v.Root) == p.Source {
				rootOK = true
			}
			if len(v.ServerNames) == 0 || v.DefaultServer {
				p.Blockers = append(p.Blockers, fmt.Sprintf("%s also contains a default/catch-all server block; disabling it would affect other traffic.", f))
			}
			for _, n := range v.ServerNames {
				n = strings.ToLower(n)
				names = append(names, n)
				if !want[n] {
					p.Blockers = append(p.Blockers, fmt.Sprintf("%s also serves %s, which is not part of this import. Include it as an alias or split the file first.", f, n))
				}
			}
		}
		step, err := s.disableStep(f, files[f][0].Enabled)
		if err != nil {
			p.Blockers = append(p.Blockers, err.Error())
			continue
		}
		step.Names = uniq(names)
		p.Steps = append(p.Steps, step)
		if b, err := e.Root.ReadFile(f); err == nil && len(p.OldConfig) < 64<<10 {
			p.OldConfig += "# " + f + "\n" + string(b) + "\n"
		}
	}
	if len(files) == 0 {
		p.Blockers = append(p.Blockers, "No existing Nginx configuration serves "+spec.Domain+".")
	} else if !rootOK {
		p.Blockers = append(p.Blockers, fmt.Sprintf("%s is not the document root of the configuration being imported.", p.Source))
	}

	// Source and target directories.
	for _, bad := range forbiddenSources {
		if p.Source == bad {
			p.Blockers = append(p.Blockers, p.Source+" cannot be imported.")
		}
	}
	if strings.HasPrefix(p.Source+"/", filepath.Clean(e.Cfg.SitesRoot)+"/") {
		p.Blockers = append(p.Blockers, "The website already lives under "+e.Cfg.SitesRoot+".")
	}
	if info, err := e.Root.Lstat(p.Source); err != nil || !info.IsDir() {
		p.Blockers = append(p.Blockers, p.Source+" does not exist or is not a directory.")
	} else if real, err := e.Root.Path(p.Source); err == nil {
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		p.Files, p.Bytes, p.Skipped, _ = scan(cctx, real)
		cancel()
	}
	if e.Root.Exists(spec.Root) {
		p.Blockers = append(p.Blockers, spec.Root+" already exists; choose another domain folder or remove it.")
	}
	if _, err := e.LookupUser(spec.User); err == nil {
		p.Blockers = append(p.Blockers, "The account "+spec.User+" already exists; choose another name.")
	}
	if spec.HTTPS && (!e.Root.Exists(spec.CertFile) || !e.Root.Exists(spec.KeyFile)) {
		p.Blockers = append(p.Blockers, "The certificate files do not exist.")
	}
	p.Database, p.DBSource = detectDatabase(e, p.Source)

	l := sites.Layout{NginxFile: filepath.Join(e.M.NginxAvailable, "whacp-"+spec.Domain+".conf"), Socket: filepath.Join(e.M.PHPSocketDir, "whacp-"+spec.Domain+".sock"), LogDir: filepath.Join(spec.Root, "logs"), TmpDir: filepath.Join(spec.Root, "tmp"), ACMERoot: e.M.ACMERoot, WebUser: e.M.WebUser, IPv6: e.IPv6()}
	p.NewConfig = sites.RenderNginx(spec, l)
	if len(p.Skipped) > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%d symbolic link(s) point outside the website and will not be copied.", len(p.Skipped)))
	}
	if !spec.HTTPS {
		for _, vs := range files {
			for _, v := range vs {
				if v.SSL {
					p.Warnings = append(p.Warnings, "The site uses HTTPS today but its certificate is not reusable; it will be served over HTTP until you issue a certificate in WhaCP.")
				}
			}
		}
	}
	p.Warnings = uniq(p.Warnings)
	p.Blockers = uniq(p.Blockers)
	return p, nil
}

// disableStep decides how to take one vhost file out of service.
func (s Service) disableStep(file string, enabled bool) (Step, error) {
	e := s.Env
	if !enabled {
		return Step{File: file, Action: "none", Path: file}, nil
	}
	// A sites-available file linked from sites-enabled: remove the link.
	if entries, err := e.Root.ReadDir(e.M.NginxEnabled); err == nil {
		for _, en := range entries {
			link := filepath.Join(e.M.NginxEnabled, en.Name())
			info, err := e.Root.Lstat(link)
			if err != nil || info.Mode()&fs.ModeSymlink == 0 {
				continue
			}
			if resolved, err := e.Root.Resolve(link); err == nil && resolved == file {
				return Step{File: file, Action: "unlink", Path: link}, nil
			}
		}
	}
	dir := filepath.Dir(file)
	if dir == e.M.NginxEnabled || dir == "/etc/nginx/conf.d" {
		stored := filepath.Join(e.Cfg.StateDir, "imports", "disabled", strings.ReplaceAll(strings.TrimPrefix(file, "/"), "/", "__"))
		return Step{File: file, Action: "move", Path: file, Stored: stored}, nil
	}
	return Step{}, fmt.Errorf("%s is not a separate site file WhaCP can disable (e.g. it is part of nginx.conf); move that server block to its own file first", file)
}

// writerFor returns a writer allowed to touch only these steps' paths.
func (s Service) writerFor(steps []Step) agentfs.Writer {
	patterns := []string{s.Env.Cfg.StateDir + "/**"}
	for _, st := range steps {
		patterns = append(patterns, st.Path)
	}
	return agentfs.Writer{Root: s.Env.Root, Policy: agentfs.Policy{Patterns: patterns}, Owners: s.Env.Writer.Owners}
}

// disable takes the old vhost files out of service; undo reverses it.
func (s Service) disable(steps []Step) ([]Step, error) {
	w := s.writerFor(steps)
	var done []Step
	for _, st := range steps {
		switch st.Action {
		case "unlink":
			if _, err := w.Remove(st.Path); err != nil {
				s.enable(done)
				return nil, err
			}
		case "move":
			if _, err := w.MkdirAll(filepath.Dir(st.Stored), 0o700, agentfs.RootOwner); err != nil {
				s.enable(done)
				return nil, err
			}
			if err := w.Rename(st.Path, st.Stored); err != nil {
				s.enable(done)
				return nil, err
			}
		}
		done = append(done, st)
	}
	return done, nil
}

func (s Service) enable(steps []Step) error {
	w := s.writerFor(steps)
	var first error
	for _, st := range steps {
		var err error
		switch st.Action {
		case "unlink":
			_, err = w.Symlink(st.File, st.Path)
		case "move":
			err = w.Rename(st.Stored, st.Path)
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Run performs the import (called inside a job).
func (s Service) Run(ctx context.Context, r Request, log io.Writer) (map[string]any, error) {
	e := s.Env
	p, err := s.Plan(ctx, r)
	if err != nil {
		return nil, err
	}
	if len(p.Blockers) > 0 {
		return nil, fmt.Errorf("the import is blocked: %s", strings.Join(p.Blockers, " "))
	}
	ignore := make([]string, 0, len(p.Steps))
	for _, st := range p.Steps {
		ignore = append(ignore, st.File)
	}
	spec := r.Spec
	spec.CreateUser = true
	wantEnabled := spec.Enabled

	fmt.Fprintf(log, "Creating the WhaCP website %s (disabled while files are copied)…\n", spec.Domain)
	spec.Enabled = false
	// The web configuration lock covers the two configuration steps, not the
	// copy between them (which can take long).
	unlock := e.LockWebConfig()
	_, err = s.Sites.ApplyIgnoring(ctx, spec, ignore)
	unlock()
	if err != nil {
		return nil, fmt.Errorf("creating the website failed: %v", err)
	}
	u, err := e.LookupUser(p.User)
	if err != nil {
		return nil, fmt.Errorf("the new account was not created")
	}
	web, err := e.WebUser()
	if err != nil {
		return nil, err
	}
	src, _ := e.Root.Path(p.Source)
	dst, _ := e.Root.Path(p.Target)
	fmt.Fprintf(log, "Copying %d files (%d bytes) from %s…\n", p.Files, p.Bytes, p.Source)
	_ = os.Remove(filepath.Join(dst, "index.html")) // WhaCP's placeholder
	n, skipped, err := copyTree(ctx, src, dst, u.UID, web.GID)
	if err != nil {
		return nil, fmt.Errorf("copying files failed after %d files: %v (the old website is still live and unchanged)", n, err)
	}
	fmt.Fprintf(log, "Copied %d files; skipped %d link(s) pointing outside the website.\n", n, len(skipped))

	fmt.Fprintln(log, "Switching: disabling the old configuration and enabling the new website…")
	defer e.LockWebConfig()()
	done, err := s.disable(p.Steps)
	if err != nil {
		return nil, fmt.Errorf("the old configuration could not be disabled: %v", err)
	}
	spec.Enabled = wantEnabled
	if _, err := s.Sites.ApplyIgnoring(ctx, spec, ignore); err != nil {
		_ = s.enable(done)
		_ = e.ReloadNginx(ctx)
		return nil, fmt.Errorf("the new website did not pass validation, so the old configuration was restored: %v", err)
	}
	rec := Record{Domain: spec.Domain, Steps: done, At: e.Now()}
	b, _ := json.MarshalIndent(rec, "", "  ")
	if _, err := e.Writer.MkdirAll(filepath.Dir(s.recordPath(spec.Domain)), 0o700, agentfs.RootOwner); err == nil {
		_, _ = e.Writer.WriteFile(s.recordPath(spec.Domain), b, 0o600, agentfs.RootOwner)
	}
	fmt.Fprintln(log, "Import complete. The old files were left in place.")
	return map[string]any{"domain": spec.Domain, "files": n, "skipped": skipped, "database": p.Database}, nil
}

// Undo re-enables the old configuration and disables the WhaCP site.
func (s Service) Undo(ctx context.Context, spec sites.Spec) error {
	e := s.Env
	b, err := e.Root.ReadFile(s.recordPath(spec.Domain))
	if err != nil {
		return actions.NotFound("There is no import to undo for this website.")
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return actions.Failed("The import record is damaged.", err.Error())
	}
	ignore := []string{}
	for _, st := range rec.Steps {
		ignore = append(ignore, st.File)
	}
	spec.Enabled = false
	if _, err := s.Sites.ApplyIgnoring(ctx, spec, ignore); err != nil {
		return actions.Failed("The WhaCP website could not be disabled.", err.Error())
	}
	if err := s.enable(rec.Steps); err != nil {
		return actions.Failed("The old configuration could not be restored.", err.Error())
	}
	if err := e.NginxTest(ctx); err != nil {
		return actions.Failed("The old configuration was restored but nginx -t fails; check it before reloading.", err.Error())
	}
	if err := e.ReloadNginx(ctx); err != nil {
		return actions.Failed("Nginx could not be reloaded.", err.Error())
	}
	_, _ = e.Writer.Remove(s.recordPath(spec.Domain))
	return nil
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

var (
	wpDB  = regexp.MustCompile(`define\(\s*['"]DB_NAME['"]\s*,\s*['"]([A-Za-z0-9_$-]{1,64})['"]`)
	envDB = regexp.MustCompile(`(?m)^DB_DATABASE=["']?([A-Za-z0-9_$-]{1,64})`)
)

// detectDatabase finds the database an application uses (WordPress,
// Laravel) without executing anything.
func detectDatabase(e *host.Env, docroot string) (string, string) {
	for _, c := range []struct {
		file string
		re   *regexp.Regexp
	}{
		{filepath.Join(docroot, "wp-config.php"), wpDB},
		{filepath.Join(filepath.Dir(docroot), "wp-config.php"), wpDB},
		{filepath.Join(docroot, ".env"), envDB},
		{filepath.Join(filepath.Dir(docroot), ".env"), envDB},
	} {
		if b, err := e.Root.ReadFile(c.file); err == nil {
			if m := c.re.FindSubmatch(b); m != nil {
				return string(m[1]), c.file
			}
		}
	}
	return "", ""
}

// inside reports whether a relative symlink target stays within the tree.
func inside(rel, target string) bool {
	if filepath.IsAbs(target) {
		return false
	}
	j := filepath.Clean(filepath.Join(filepath.Dir(rel), target))
	return j != ".." && !strings.HasPrefix(j, "../")
}

func scan(ctx context.Context, root string) (files, bytes int64, skipped []string, err error) {
	skipped = []string{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			if t, err := os.Readlink(p); err != nil || !inside(rel, t) {
				skipped = append(skipped, rel)
			}
		case d.Type().IsRegular():
			if info, err := d.Info(); err == nil {
				files++
				bytes += info.Size()
			}
		}
		return nil
	})
	return
}

// copyTree copies src into dst (an existing directory) without following
// symlinks. Everything written is owned by uid:gid; setuid/setgid bits on
// files and world-write bits are dropped. Writes go through an os.Root on
// dst, so nothing can be written outside it.
func copyTree(ctx context.Context, src, dst string, uid, gid int) (int64, []string, error) {
	root, err := os.OpenRoot(dst)
	if err != nil {
		return 0, nil, err
	}
	defer root.Close()
	var n int64
	skipped := []string{}
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, werr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if werr != nil {
			return werr
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			if err := root.Mkdir(rel, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
			f, err := root.Open(rel)
			if err != nil {
				return err
			}
			defer f.Close()
			if err := f.Chown(uid, gid); err != nil {
				return err
			}
			return f.Chmod(fs.ModeSetgid | (info.Mode().Perm()&0o775 | 0o700))
		case d.Type()&fs.ModeSymlink != 0:
			t, err := os.Readlink(p)
			if err != nil || !inside(rel, t) {
				skipped = append(skipped, rel)
				return nil
			}
			// The new account has no processes yet (the site is disabled),
			// so its directory cannot change under us here.
			link := filepath.Join(dst, rel)
			if err := os.Symlink(t, link); err != nil {
				return err
			}
			return os.Lchown(link, uid, gid)
		case d.Type().IsRegular():
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := root.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			if err := out.Chown(uid, gid); err != nil {
				out.Close()
				return err
			}
			if err := out.Chmod(info.Mode().Perm()&0o775 | 0o600); err != nil {
				out.Close()
				return err
			}
			n++
			return out.Close()
		}
		return nil // sockets, devices, fifos
	})
	return n, skipped, err
}

// Register adds the import actions.
func Register(r *actions.Registry, env *host.Env) {
	s := Service{Env: env, Sites: &sites.Manager{Env: env}}
	decode := func(p json.RawMessage) (Request, error) {
		var in Request
		err := host.Decode(p, &in)
		return in, err
	}
	r.Register(actions.Action{Name: "adopt.preview", Timeout: 2 * time.Minute, Description: "Plan the import of an existing website into WhaCP (read-only).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		in, err := decode(p)
		if err != nil {
			return nil, err
		}
		return s.Plan(ctx, in)
	}})
	r.Register(actions.Action{Name: "adopt.run", Mutating: true, Description: "Import an existing website into WhaCP (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		in, err := decode(p)
		if err != nil {
			return nil, err
		}
		plan, err := s.Plan(ctx, in)
		if err != nil {
			return nil, err
		}
		if len(plan.Blockers) > 0 {
			return nil, actions.Conflict("The import is blocked: " + strings.Join(plan.Blockers, " "))
		}
		j := env.Jobs.Start("adopt.run", "site:"+plan.Domain, func(ctx context.Context, log io.Writer) (any, error) {
			return s.Run(ctx, in, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "adopt.undo", Mutating: true, Description: "Undo an import: re-enable the old configuration and disable the WhaCP website.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		in, err := decode(p)
		if err != nil {
			return nil, err
		}
		if err := in.Spec.Normalize(env.Cfg.SitesRoot, env.Cfg.StateDir); err != nil {
			return nil, host.Invalid(err)
		}
		defer env.LockWebConfig()()
		return map[string]any{"undone": in.Spec.Domain}, s.Undo(ctx, in.Spec)
	}})
}
