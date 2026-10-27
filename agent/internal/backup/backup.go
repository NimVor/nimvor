// Package backup creates, stores, uploads and restores backups.
//
// A backup is a directory under backup_dir holding one file per part and
// a manifest.json with SHA-256 checksums:
//
//	<backup_dir>/sites/<domain>/<id>/files.tar.gz[.enc]
//	<backup_dir>/sites/<domain>/<id>/db-<name>.sql.gz[.enc]
//	<backup_dir>/server/<id>/server-config.tar.gz[.enc]
//
// Website files are archived and restored by tar running as the website
// account, so a backup can never read or write more than that account
// could itself. Encryption (AES-256-GCM) uses a key in state_dir that
// never leaves the server; copies of backups can be sent to S3-compatible
// storage with credentials the panel supplies per call.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/dbmgr"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// FileEntry is one part of a backup.
type FileEntry struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // files, database, config
	Database string `json:"database,omitempty"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	// Unpacked is the size before compression (tar stream or SQL), used to
	// check free space before a restore; 0 in backups from before 1.0.0.
	Unpacked int64 `json:"unpacked,omitempty"`
}

// Manifest describes a backup.
type Manifest struct {
	Version   int             `json:"version"`
	ID        string          `json:"id"`
	Kind      string          `json:"kind"` // site, server
	Domain    string          `json:"domain,omitempty"`
	Label     string          `json:"label,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	Encrypted bool            `json:"encrypted"`
	KeyID     string          `json:"key_id,omitempty"`
	Meta      json.RawMessage `json:"meta,omitempty"`
	Files     []FileEntry     `json:"files"`
	Size      int64           `json:"size"`
	Remote    *RemoteRef      `json:"remote,omitempty"`
	Local     bool            `json:"local"`
	Warnings  []string        `json:"warnings,omitempty"`
	// Mac authenticates the manifest with the backup key (encrypted
	// backups, since 1.0.0); see manifestMAC.
	Mac string `json:"mac,omitempty"`
}

// RemoteRef records where a copy was uploaded.
type RemoteRef struct {
	Type     string `json:"type,omitempty"` // empty for S3 (older backups)
	Endpoint string `json:"endpoint"`
	Bucket   string `json:"bucket"`
	Key      string `json:"key"`
}

var (
	idRe   = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}$`)
	nameRe = regexp.MustCompile(`^(files\.tar\.gz|server-config\.tar\.gz|db-[A-Za-z0-9_]{1,64}\.sql\.gz)(\.enc)?$`)
)

// Service implements backups on top of the host environment.
type Service struct{ Env *host.Env }

func (s Service) keyPath() string { return filepath.Join(s.Env.Cfg.StateDir, "backup.key") }

func (s Service) key() ([]byte, error) {
	p, err := s.Env.Root.Path(s.keyPath())
	if err != nil {
		return nil, err
	}
	return LoadOrCreateKey(p)
}

// dir returns the host path of a backup (domain "" = server backup).
func (s Service) dir(domain, id string) string {
	if domain == "" {
		return filepath.Join(s.Env.Cfg.BackupDir, "server", id)
	}
	return filepath.Join(s.Env.Cfg.BackupDir, "sites", domain, id)
}

func (s Service) real(hostPath string) (string, error) { return s.Env.Root.Path(hostPath) }

func (s Service) newID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return s.Env.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}

type counter struct {
	n int64
}

func (c *counter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// artifact writes one backup part through fn, encrypting when key != nil.
func (s Service) artifact(dir, name, kind string, key []byte, fn func(w io.Writer) error) (FileEntry, error) {
	if key != nil {
		name += ".enc"
	}
	final := filepath.Join(dir, name)
	tmp := final + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return FileEntry{}, err
	}
	defer os.Remove(tmp)
	h := sha256.New()
	c := &counter{}
	var w io.Writer = io.MultiWriter(newSpaceGuard(f, dir), h, c)
	var enc io.WriteCloser
	if key != nil {
		if enc, err = NewEncryptWriter(w, key); err != nil {
			f.Close()
			return FileEntry{}, err
		}
		w = enc
	}
	if err := fn(w); err != nil {
		f.Close()
		return FileEntry{}, err
	}
	if enc != nil {
		if err := enc.Close(); err != nil {
			f.Close()
			return FileEntry{}, err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return FileEntry{}, err
	}
	if err := f.Close(); err != nil {
		return FileEntry{}, err
	}
	if err := os.Rename(tmp, final); err != nil {
		return FileEntry{}, err
	}
	return FileEntry{Name: name, Kind: kind, Size: c.n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// SiteRequest describes a website backup.
type SiteRequest struct {
	Domain      string          `json:"domain"`
	User        string          `json:"user"`
	Root        string          `json:"root"`
	Files       bool            `json:"files"`
	IncludeLogs bool            `json:"include_logs"`
	Databases   []string        `json:"databases"`
	Encrypt     bool            `json:"encrypt"`
	Label       string          `json:"label"`
	Meta        json.RawMessage `json:"meta"`
	Remote      *Remote         `json:"remote"`
	KeepLocal   *bool           `json:"keep_local"`
}

func validLabel(l string) error {
	if len(l) > 100 || strings.ContainsAny(l, "\x00\n\r") {
		return &validate.Error{Field: "label", Message: "at most 100 characters on one line"}
	}
	return nil
}

func (r *SiteRequest) validate(env *host.Env) error {
	if err := validate.Domain("domain", r.Domain); err != nil {
		return err
	}
	if err := validate.Username("user", r.User); err != nil {
		return err
	}
	if r.Root == "" {
		r.Root = filepath.Join(env.Cfg.SitesRoot, r.Domain)
	}
	if err := validate.Within("root", env.Cfg.SitesRoot, r.Root); err != nil {
		return err
	}
	if !r.Files && len(r.Databases) == 0 {
		return &validate.Error{Field: "files", Message: "choose files and/or at least one database"}
	}
	if len(r.Databases) > 50 {
		return &validate.Error{Field: "databases", Message: "at most 50 databases"}
	}
	for i, d := range r.Databases {
		if err := validate.DBIdentifier(fmt.Sprintf("databases[%d]", i), d); err != nil {
			return err
		}
	}
	if err := validLabel(r.Label); err != nil {
		return err
	}
	if len(r.Meta) > 64<<10 || len(r.Meta) > 0 && !json.Valid(r.Meta) {
		return &validate.Error{Field: "meta", Message: "must be JSON up to 64 KiB"}
	}
	if r.Remote != nil {
		if err := r.Remote.Validate(); err != nil {
			return &validate.Error{Field: "remote", Message: err.Error()}
		}
	}
	return nil
}

func keepLocal(k *bool, remote *Remote) bool {
	if remote == nil {
		return true
	}
	return k == nil || *k
}

// CreateSite makes a website backup (called inside a job).
func (s Service) CreateSite(ctx context.Context, r SiteRequest, cred *execx.Credential, log io.Writer) (*Manifest, error) {
	m := &Manifest{Version: 1, ID: s.newID(), Kind: "site", Domain: r.Domain, Label: r.Label, CreatedAt: s.Env.Now().UTC(), Meta: normalizeMeta(r.Meta), Encrypted: r.Encrypt, Local: true, Files: []FileEntry{}}
	var key []byte
	if r.Encrypt {
		k, err := s.key()
		if err != nil {
			return nil, fmt.Errorf("the backup key could not be loaded: %v", err)
		}
		key, m.KeyID = k, KeyID(k)
	}
	hostDir := s.dir(r.Domain, m.ID)
	if _, err := s.Env.Writer.MkdirAll(hostDir, 0o700, agentfs.RootOwner); err != nil {
		return nil, err
	}
	dir, _ := s.real(hostDir)
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	if r.Files {
		fmt.Fprintf(log, "Archiving website files of %s…\n", r.Domain)
		root, _ := s.real(r.Root)
		args := []string{"-C", root, "--exclude=./backups", "--exclude=./tmp", "--exclude=./.whacp-upload-*", "--exclude=./.whacp-trash"}
		if !r.IncludeLogs {
			args = append(args, "--exclude=./logs")
		}
		args = append(args, "-cf", "-", ".")
		if err := needSpace(dir, 0, "the backup"); err != nil {
			return nil, err
		}
		plain := &countingWriter{}
		e, err := s.artifact(dir, "files.tar.gz", "files", key, func(w io.Writer) error {
			gz := gzip.NewWriter(w)
			plain.w = gz
			out, err := s.Env.Runner.RunWith(ctx, "tar", execx.Opts{As: cred, Dir: root, Stdout: plain, Timeout: -1}, args...)
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() == 1 {
				// GNU tar: some files changed while being read. The
				// archive is still complete and valid.
				m.Warnings = append(m.Warnings, "Some files changed while they were archived.")
				err = nil
			}
			if err != nil {
				return fmt.Errorf("tar failed: %v %s", err, host.Output(out))
			}
			return gz.Close()
		})
		if err != nil {
			return nil, err
		}
		e.Unpacked = plain.n
		m.Files = append(m.Files, e)
		fmt.Fprintf(log, "  %s: %d bytes\n", e.Name, e.Size)
	}
	db := dbmgr.Client{Env: s.Env}
	for _, name := range r.Databases {
		var sqlBytes int64
		e, err := s.artifact(dir, "db-"+name+".sql.gz", "database", key, func(w io.Writer) error {
			n, err := db.DumpTo(ctx, name, w, log)
			sqlBytes = n
			return err
		})
		if err != nil {
			return nil, err
		}
		e.Database = name
		e.Unpacked = sqlBytes
		m.Files = append(m.Files, e)
		fmt.Fprintf(log, "  %s: %d bytes\n", e.Name, e.Size)
	}
	if err := s.finish(ctx, m, dir, r.Remote, keepLocal(r.KeepLocal, r.Remote), log); err != nil {
		return nil, err
	}
	ok = true
	return m, nil
}

// finish writes the manifest and uploads the backup when requested.
func (s Service) finish(ctx context.Context, m *Manifest, dir string, remote *Remote, keep bool, log io.Writer) error {
	for _, f := range m.Files {
		m.Size += f.Size
	}
	if m.Encrypted {
		key, err := s.key()
		if err != nil {
			return err
		}
		m.Mac = manifestMAC(key, m)
	}
	if err := writeManifest(dir, m); err != nil {
		return err
	}
	if remote == nil {
		return nil
	}
	c := remote.Open(s.Env)
	prefix := remoteKey(remote, m)
	fmt.Fprintf(log, "Uploading to %s (%s)…\n", remote.Describe(), prefix)
	for _, f := range m.Files {
		if err := c.Put(ctx, prefix+"/"+f.Name, filepath.Join(dir, f.Name)); err != nil {
			return fmt.Errorf("upload of %s failed: %v", f.Name, err)
		}
	}
	m.Remote = &RemoteRef{Type: remote.Type, Endpoint: remote.Endpoint, Bucket: remote.Bucket, Key: prefix}
	if remote.Type != "s3" {
		m.Remote.Endpoint = remote.Describe()
	}
	m.Local = keep
	if err := writeManifest(dir, m); err != nil {
		return err
	}
	if err := c.Put(ctx, prefix+"/manifest.json", filepath.Join(dir, "manifest.json")); err != nil {
		return fmt.Errorf("upload of manifest.json failed: %v", err)
	}
	if !keep {
		for _, f := range m.Files {
			os.Remove(filepath.Join(dir, f.Name))
		}
		fmt.Fprintln(log, "Local copy removed (kept in remote storage only).")
	}
	fmt.Fprintln(log, "Upload complete.")
	return nil
}

func remoteKey(r *Remote, m *Manifest) string {
	if m.Kind == "server" {
		return r.Key("server", m.ID)
	}
	return r.Key("sites", m.Domain, m.ID)
}

func writeManifest(dir string, m *Manifest) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := filepath.Join(dir, ".manifest.json.tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "manifest.json"))
}

// Load reads a backup manifest.
func (s Service) Load(domain, id string) (*Manifest, string, error) {
	if !idRe.MatchString(id) {
		return nil, "", actions.InvalidParams("id: invalid backup id")
	}
	dir, err := s.real(s.dir(domain, id))
	if err != nil {
		return nil, "", err
	}
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, dir, actions.NotFound("The backup was not found on this server.")
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, dir, actions.Failed("The backup manifest is damaged.", err.Error())
	}
	return &m, dir, nil
}

// List returns the backups of a website (domain "" = server backups),
// newest first.
func (s Service) List(domain string) ([]Manifest, error) {
	base := filepath.Join(s.Env.Cfg.BackupDir, "server")
	if domain != "" {
		base = filepath.Join(s.Env.Cfg.BackupDir, "sites", domain)
	}
	entries, err := s.Env.Root.ReadDir(base)
	if err != nil {
		return []Manifest{}, nil
	}
	out := []Manifest{}
	for _, e := range entries {
		if !e.IsDir() || !idRe.MatchString(e.Name()) {
			continue
		}
		if m, _, err := s.Load(domain, e.Name()); err == nil {
			m.Meta = nil
			out = append(out, *m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// fetch makes sure part f is available locally and intact, downloading it
// from remote storage when needed. It returns the local path and whether
// it was downloaded (and should be removed afterwards).
func (s Service) fetch(ctx context.Context, m *Manifest, dir string, f FileEntry, remote *Remote, log io.Writer) (string, bool, error) {
	p := filepath.Join(dir, f.Name)
	downloaded := false
	if _, err := os.Stat(p); err != nil {
		if remote == nil || m.Remote == nil {
			return "", false, fmt.Errorf("%s is not stored on this server and no remote storage was given", f.Name)
		}
		fmt.Fprintf(log, "Downloading %s…\n", f.Name)
		out, err := os.OpenFile(p+".partial", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return "", false, err
		}
		err = remote.Open(s.Env).Get(ctx, m.Remote.Key+"/"+f.Name, newSpaceGuard(out, dir))
		out.Close()
		if err != nil {
			os.Remove(p + ".partial")
			return "", false, fmt.Errorf("download of %s failed: %v", f.Name, err)
		}
		if err := os.Rename(p+".partial", p); err != nil {
			return "", false, err
		}
		downloaded = true
	}
	if err := verify(p, f); err != nil {
		if downloaded {
			os.Remove(p)
		}
		return "", false, err
	}
	return p, downloaded, nil
}

func verify(path string, f FileEntry) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	h := sha256.New()
	n, err := io.Copy(h, file)
	if err != nil {
		return err
	}
	if n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return fmt.Errorf("%s failed its checksum: the backup is damaged", f.Name)
	}
	return nil
}

// open returns a reader of the decrypted, decompressed contents of a part.
func (s Service) open(m *Manifest, path string) (io.Reader, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	var r io.Reader = f
	if m.Encrypted {
		key, err := s.key()
		if err != nil {
			f.Close()
			return nil, nil, err
		}
		if KeyID(key) != m.KeyID {
			f.Close()
			return nil, nil, fmt.Errorf("this backup was encrypted with a different key (key id %s); restore that key to %s first", m.KeyID, s.keyPath())
		}
		if r, err = NewDecryptReader(f, key); err != nil {
			f.Close()
			return nil, nil, err
		}
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("the backup file is not a valid archive")
	}
	return gz, func() { gz.Close(); f.Close() }, nil
}

// RestoreRequest selects what to restore from a website backup.
type RestoreRequest struct {
	Domain    string            `json:"domain"`
	User      string            `json:"user"`
	Root      string            `json:"root"`
	ID        string            `json:"id"`
	Files     bool              `json:"files"`
	Databases map[string]string `json:"databases"` // source name → target database
	Remote    *Remote           `json:"remote"`
	Safety    bool              `json:"safety_backup"`
}

// Restore restores parts of a website backup (called inside a job).
func (s Service) Restore(ctx context.Context, r RestoreRequest, cred *execx.Credential, log io.Writer) (map[string]any, error) {
	m, dir, err := s.Load(r.Domain, r.ID)
	if err != nil {
		return nil, fmt.Errorf("the backup was not found")
	}
	result := map[string]any{"id": m.ID}
	var cleanup []string
	defer func() {
		for _, p := range cleanup {
			os.Remove(p)
		}
	}()
	part := func(kind, db string) (FileEntry, bool) {
		for _, f := range m.Files {
			if f.Kind == kind && f.Database == db {
				return f, true
			}
		}
		return FileEntry{}, false
	}
	// Download and verify everything first so a damaged backup changes nothing.
	paths := map[string]string{}
	need := []FileEntry{}
	if r.Files {
		f, ok := part("files", "")
		if !ok {
			return nil, fmt.Errorf("this backup does not contain website files")
		}
		need = append(need, f)
	}
	for src := range r.Databases {
		f, ok := part("database", src)
		if !ok {
			return nil, fmt.Errorf("this backup does not contain database %s", src)
		}
		need = append(need, f)
	}
	// Room for what must be downloaded (the rest is already here).
	var download int64
	for _, f := range need {
		if _, err := os.Stat(filepath.Join(dir, f.Name)); err != nil {
			download += f.Size
		}
	}
	if err := needSpace(dir, download, "downloading the backup"); err != nil {
		return nil, err
	}
	for _, f := range need {
		p, downloaded, err := s.fetch(ctx, m, dir, f, r.Remote, log)
		if err != nil {
			return nil, err
		}
		if downloaded {
			cleanup = append(cleanup, p)
		}
		paths[f.Name] = p
	}
	fmt.Fprintln(log, "Checksums verified.")
	// Room for the restored data (estimated from the sizes before
	// compression, less what it replaces); backups from before 1.0.0 do not
	// record them and are not checked here.
	if r.Files {
		if f, _ := part("files", ""); f.Unpacked > 0 {
			root, _ := s.real(r.Root)
			if err := needSpace(root, f.Unpacked-treeSize(root), "the restored website files"); err != nil {
				return nil, err
			}
		}
	}
	for src, target := range r.Databases {
		if f, _ := part("database", src); f.Unpacked > 0 {
			current, _ := dbmgr.Client{Env: s.Env}.Size(ctx, target)
			if dataDir, err := s.real(s.Env.Platform.Paths().MySQLDataDir); err == nil {
				if err := needSpace(dataDir, f.Unpacked-current, "importing "+src+" (estimate)"); err != nil {
					return nil, err
				}
			}
		}
	}
	if r.Files {
		var safety *Manifest
		if r.Safety {
			fmt.Fprintln(log, "Creating a safety backup of the current files first…")
			sm, err := s.CreateSite(ctx, SiteRequest{Domain: r.Domain, User: r.User, Root: r.Root, Files: true, Label: "Before restoring " + m.ID, Encrypt: m.Encrypted}, cred, log)
			if err != nil {
				return nil, fmt.Errorf("the safety backup failed, nothing was restored: %v", err)
			}
			result["safety_backup"] = sm.ID
			safety = sm
		}
		f, _ := part("files", "")
		root, _ := s.real(r.Root)
		fmt.Fprintf(log, "Restoring website files into %s…\n", r.Root)
		err := s.extractFiles(ctx, m, paths[f.Name], cred, root, log)
		if err == nil {
			err = s.Env.StripSetID(ctx, cred, root)
		}
		if err != nil {
			// The restore failed part-way. If a safety backup was taken,
			// put the previous files back so the website is never left in a
			// half-restored state; report whether that rollback succeeded.
			if safety == nil {
				return nil, fmt.Errorf("restoring files failed: %v", err)
			}
			fmt.Fprintln(log, "The restore failed; rolling back to the safety backup of the previous files…")
			if rbErr := s.rollbackFiles(ctx, safety, cred, r, root, log); rbErr != nil {
				return nil, fmt.Errorf("restoring files failed: %v; AND the automatic rollback also failed: %v — the previous files are in safety backup %s, restore it manually", err, rbErr, safety.ID)
			}
			fmt.Fprintln(log, "Rollback complete: the previous files were restored.")
			return nil, fmt.Errorf("restoring files failed: %v; the previous files were restored from the safety backup (%s)", err, safety.ID)
		}
		result["files"] = true
	}
	db := dbmgr.Client{Env: s.Env}
	restored := []string{}
	for src, target := range r.Databases {
		f, _ := part("database", src)
		rd, done, err := s.open(m, paths[f.Name])
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(log, "Importing %s into %s…\n", src, target)
		err = db.ImportFrom(ctx, target, rd, log)
		done()
		if err != nil {
			return nil, fmt.Errorf("importing %s failed", src)
		}
		restored = append(restored, target)
	}
	result["databases"] = restored
	fmt.Fprintln(log, "Restore complete.")
	return result, nil
}

// extractFiles unpacks a backup's files archive into root as the site
// account. The reader is decrypted/decompressed by s.open.
func (s Service) extractFiles(ctx context.Context, m *Manifest, path string, cred *execx.Credential, root string, log io.Writer) error {
	rd, done, err := s.open(m, path)
	if err != nil {
		return err
	}
	defer done()
	out, err := s.Env.Runner.RunWith(ctx, "tar", execx.Opts{As: cred, Dir: root, Stdin: rd, Output: log, Timeout: -1}, "-C", root, "--no-same-owner", "-xf", "-")
	if err != nil {
		return fmt.Errorf("%v %s", err, host.Output(out))
	}
	return nil
}

// rollbackFiles restores the safety backup's files over root after a failed
// restore, then verifies the result, so a website is never left half
// restored. The safety backup was just created locally by CreateSite, so
// its archive is on disk and needs no download.
func (s Service) rollbackFiles(ctx context.Context, safety *Manifest, cred *execx.Credential, r RestoreRequest, root string, log io.Writer) error {
	_, dir, err := s.Load(r.Domain, safety.ID)
	if err != nil {
		return fmt.Errorf("the safety backup could not be loaded: %v", err)
	}
	var files FileEntry
	ok := false
	for _, f := range safety.Files {
		if f.Kind == "files" {
			files, ok = f, true
			break
		}
	}
	if !ok {
		return fmt.Errorf("the safety backup has no files archive")
	}
	if err := s.extractFiles(ctx, safety, filepath.Join(dir, files.Name), cred, root, log); err != nil {
		return err
	}
	if err := s.Env.StripSetID(ctx, cred, root); err != nil {
		return err
	}
	// Verify the rollback produced readable content (the root exists and the
	// archive extracted without error above).
	if _, err := os.Stat(root); err != nil {
		return fmt.Errorf("the website root is missing after rollback: %v", err)
	}
	return nil
}

// serverPaths are archived by a server configuration backup.
func (s Service) serverPaths() []string {
	c := s.Env.M.ServerConfig
	return []string{
		c["nginx"], c["php"], c["mysql"], c["redis"], c["letsencrypt"],
		"/etc/whacp", c["fail2ban"], c["firewall"], "/etc/ssh/sshd_config.d",
		c["cron"], "/etc/systemd/system",
	}
}

// CreateServer archives the server configuration (called inside a job).
// Symlinks are stored as links and never followed.
func (s Service) CreateServer(ctx context.Context, encrypt bool, label string, remote *Remote, keep bool, log io.Writer) (*Manifest, error) {
	m := &Manifest{Version: 1, ID: s.newID(), Kind: "server", Label: label, CreatedAt: s.Env.Now().UTC(), Encrypted: encrypt, Local: true, Files: []FileEntry{}}
	var key []byte
	if encrypt {
		k, err := s.key()
		if err != nil {
			return nil, err
		}
		key, m.KeyID = k, KeyID(k)
	}
	hostDir := s.dir("", m.ID)
	if _, err := s.Env.Writer.MkdirAll(hostDir, 0o700, agentfs.RootOwner); err != nil {
		return nil, err
	}
	dir, _ := s.real(hostDir)
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	extra := []string{filepath.Join(s.Env.Cfg.StateDir, "certs")}
	if err := needSpace(dir, 0, "the server backup"); err != nil {
		return nil, err
	}
	e, err := s.artifact(dir, "server-config.tar.gz", "config", key, func(w io.Writer) error {
		gz := gzip.NewWriter(w)
		tw := tar.NewWriter(gz)
		for _, p := range append(s.serverPaths(), extra...) {
			if err := ctx.Err(); err != nil {
				return err
			}
			real, err := s.real(p)
			if err != nil {
				continue
			}
			if _, err := os.Lstat(real); err != nil {
				continue
			}
			fmt.Fprintf(log, "Adding %s\n", p)
			if err := addTree(tw, real, strings.TrimPrefix(p, "/")); err != nil {
				return err
			}
		}
		if err := tw.Close(); err != nil {
			return err
		}
		return gz.Close()
	})
	if err != nil {
		return nil, err
	}
	m.Files = append(m.Files, e)
	if err := s.finish(ctx, m, dir, remote, keep, log); err != nil {
		return nil, err
	}
	ok = true
	return m, nil
}

func addTree(tw *tar.Writer, real, name string) error {
	return filepath.WalkDir(real, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(real, p)
		entry := filepath.ToSlash(filepath.Join(name, rel))
		link := ""
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			link, _ = os.Readlink(p)
		case info.Mode().IsRegular(), info.IsDir():
		default:
			return nil // sockets, devices, fifos
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return nil
		}
		hdr.Name = entry
		if info.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, io.LimitReader(f, hdr.Size))
			f.Close()
			return err
		}
		return nil
	})
}

// Delete removes a backup locally and, with remote, its remote copy.
func (s Service) Delete(ctx context.Context, domain, id string, remote *Remote) error {
	m, dir, err := s.Load(domain, id)
	if err != nil && dir == "" {
		return err
	}
	if remote != nil {
		key := ""
		if m != nil && m.Remote != nil {
			key = m.Remote.Key
		} else if domain == "" {
			key = remote.Key("server", id)
		} else {
			key = remote.Key("sites", domain, id)
		}
		c := remote.Open(s.Env)
		keys, err := c.List(ctx, key+"/")
		if err != nil {
			return fmt.Errorf("remote listing failed: %v", err)
		}
		for _, k := range keys {
			if err := c.Delete(ctx, k); err != nil {
				return fmt.Errorf("remote delete failed: %v", err)
			}
		}
		c.RemoveDir(ctx, key+"/")
	}
	if dir != "" {
		return os.RemoveAll(dir)
	}
	return nil
}

// Chunk reads up to 2 MiB of a stored backup part.
func (s Service) Chunk(domain, id, name string, offset int64) ([]byte, int64, error) {
	m, dir, err := s.Load(domain, id)
	if err != nil {
		return nil, 0, err
	}
	found := name == "manifest.json"
	for _, f := range m.Files {
		found = found || f.Name == name
	}
	if !found || !(nameRe.MatchString(name) || name == "manifest.json") {
		return nil, 0, actions.NotFound("That file is not part of the backup.")
	}
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return nil, 0, actions.NotFound("That file is only stored remotely.")
	}
	defer f.Close()
	info, _ := f.Stat()
	if offset < 0 || offset > info.Size() {
		return nil, 0, actions.InvalidParams("offset: out of range")
	}
	buf := make([]byte, min(int64(2<<20), info.Size()-offset))
	n, err := f.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, err
	}
	return buf[:n], info.Size(), nil
}
