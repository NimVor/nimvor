package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/NimVor/nimvor/agent/internal/host"
)

// FileTask copies one website's files into an existing NimVor website.
type FileTask struct {
	Domain     string `json:"domain"`      // label
	SourceRoot string `json:"source_root"` // absolute path on the source
	DestRoot   string `json:"dest_root"`   // the NimVor website's folder
	DestUser   string `json:"dest_user"`   // the website's system account
	SizeKB     int64  `json:"size_kb"`
}

// DBTask copies one MySQL/MariaDB database into an existing NimVor database.
type DBTask struct {
	Source string `json:"source"`
	Dest   string `json:"dest"`
	SizeKB int64  `json:"size_kb"`
}

// Config is how the transfer is done.
type Config struct {
	Compress bool     `json:"compress"`
	Workers  int      `json:"workers"`  // files copied at the same time, 1–8
	Excludes []string `json:"excludes"` // rsync patterns, e.g. "cache/*"
	// MapOwner gives every copied file to the website's account (always on in
	// this version: files are never created as the source's users).
	TempDir string `json:"temp_dir"` // where dumps are kept while moving; default: the agent's state folder
	// AllowOverwrite lets the first copy write into a website that already has files,
	// and the first import into a database that already has tables. Off by default:
	// a migration only fills what is empty.
	AllowOverwrite bool `json:"allow_overwrite"`
}

// Plan is what a migration does.
type Plan struct {
	Files     []FileTask `json:"files"`
	Databases []DBTask   `json:"databases"`
	Mail      []MailTask `json:"mail"`
	Config    Config     `json:"config"`
}

var (
	excludeRe = regexp.MustCompile(`^[A-Za-z0-9._*?/\[\]-]{1,200}$`)
	dbNameRe  = regexp.MustCompile(`^[A-Za-z0-9_$-]{1,64}$`)
)

// Validate checks the plan against this server: every destination must be an
// existing website folder under the sites root, owned by the account it names
// (so a migration can only fill websites NimVor made), and nothing may name
// a path outside what the agent allows.
func (p *Plan) Validate(env *host.Env) error {
	if len(p.Files)+len(p.Databases)+len(p.Mail) == 0 {
		return fmt.Errorf("nothing was selected to migrate")
	}
	if len(p.Files) > 500 || len(p.Databases) > 500 || len(p.Mail) > 2000 {
		return fmt.Errorf("too many items in one migration (500 websites or databases, 2000 mailboxes at most)")
	}
	if p.Config.Workers == 0 {
		p.Config.Workers = 2
	}
	if p.Config.Workers < 1 || p.Config.Workers > 8 {
		return fmt.Errorf("config.workers must be 1–8")
	}
	for _, x := range p.Config.Excludes {
		if !excludeRe.MatchString(x) || strings.Contains(x, "..") {
			return fmt.Errorf("config.excludes: %q is not a valid pattern (letters, digits and . _ * ? / [ ] -)", x)
		}
	}
	if len(p.Config.Excludes) > 50 {
		return fmt.Errorf("config.excludes: 50 patterns at most")
	}
	seen := map[string]bool{}
	for i := range p.Files {
		f := &p.Files[i]
		if !ValidRemotePath(f.SourceRoot) || f.SourceRoot == "/" || strings.Count(strings.Trim(f.SourceRoot, "/"), "/") < 1 {
			return fmt.Errorf("files[%d].source_root: give the website's folder on the source, not %q", i, f.SourceRoot)
		}
		if err := checkDest(env, f); err != nil {
			return fmt.Errorf("files[%d]: %w", i, err)
		}
		if seen[f.DestRoot] {
			return fmt.Errorf("files[%d]: %s is the destination of two items", i, f.DestRoot)
		}
		seen[f.DestRoot] = true
	}
	mseen := map[string]bool{}
	for i, mt := range p.Mail {
		if !ValidRemotePath(mt.SourceMaildir) || strings.Count(strings.Trim(mt.SourceMaildir, "/"), "/") < 2 {
			return fmt.Errorf("mail[%d].source_maildir: give the mailbox's Maildir on the source, not %q", i, mt.SourceMaildir)
		}
		if _, _, _, err := mailDest(env, mt.Address); err != nil {
			return fmt.Errorf("mail[%d]: %w", i, err)
		}
		if mseen[mt.Address] {
			return fmt.Errorf("mail[%d]: %s is the destination of two items", i, mt.Address)
		}
		mseen[mt.Address] = true
	}
	dseen := map[string]bool{}
	for i, d := range p.Databases {
		if !dbNameRe.MatchString(d.Source) || !dbNameRe.MatchString(d.Dest) {
			return fmt.Errorf("databases[%d]: database names use letters, digits, _ $ and -", i)
		}
		if dseen[d.Dest] {
			return fmt.Errorf("databases[%d]: %s is the destination of two items", i, d.Dest)
		}
		dseen[d.Dest] = true
	}
	return nil
}

// sysGID returns the group of a file, or -1.
func sysGID(info os.FileInfo) int {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Gid)
	}
	return -1
}

// checkDest confirms the destination is a real website folder of its account.
func checkDest(env *host.Env, f *FileTask) error {
	root := strings.TrimSuffix(env.Cfg.SitesRoot, "/")
	clean := filepath.Clean(f.DestRoot)
	if !strings.HasPrefix(clean, root+"/") || clean != f.DestRoot {
		return fmt.Errorf("dest_root must be a folder inside %s", root)
	}
	if _, _, err := env.Credential(f.DestUser); err != nil {
		return fmt.Errorf("dest_user: %v", err)
	}
	real, err := env.Root.Path(clean)
	if err != nil {
		return fmt.Errorf("dest_root: %v", err)
	}
	info, err := os.Lstat(real)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("dest_root %s does not exist: create the website in NimVor first", f.DestRoot)
	}
	if resolved, err := filepath.EvalSymlinks(real); err != nil || resolved != real {
		return fmt.Errorf("dest_root must not be or contain a symbolic link")
	}
	u, err := env.LookupUser(f.DestUser)
	if err != nil {
		return fmt.Errorf("dest_user: %v", err)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != u.UID && os.Geteuid() == 0 {
		return fmt.Errorf("dest_root belongs to another account than %s", f.DestUser)
	}
	return nil
}
