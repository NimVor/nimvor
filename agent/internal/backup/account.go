package backup

import (
	"archive/tar"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// Account backups put every website backup of a hosting account (files
// and databases) and the panel's description of the account (websites,
// databases, DNS zones, cron jobs…) in one file, so a whole account can be
// downloaded, kept anywhere and restored, also after the account was
// terminated or on another server:
//
//	<backup_dir>/accounts/<username>/<id>.nimvor-account   a tar holding
//	  account.json                      (AccountManifest, with a MAC)
//	  sites/<domain>/<id>/manifest.json (website backups, encrypted)
//	  sites/<domain>/<id>/<part>
//
// The website backups are always encrypted and their manifests and the
// account manifest are authenticated with the backup key: a file is only
// imported when it was made with this server's key (on a new server: after
// importing that key), so a changed or foreign file is refused.

// AccountSuffix ends every account backup file name.
const AccountSuffix = ".nimvor-account"

const (
	maxAccountMeta   = 1 << 20
	maxAccountSites  = 200
	maxAccountHeader = 2 << 20
)

// AccountSite is one website backup inside an account backup.
type AccountSite struct {
	Domain string `json:"domain"`
	ID     string `json:"id"`
}

// AccountManifest describes an account backup.
type AccountManifest struct {
	Version   int             `json:"version"`
	ID        string          `json:"id"`
	Username  string          `json:"username"`
	Label     string          `json:"label,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	KeyID     string          `json:"key_id"`
	Meta      json.RawMessage `json:"meta,omitempty"`
	Sites     []AccountSite   `json:"sites"`
	Mac       string          `json:"mac"`
}

// AccountRequest asks for an account backup.
type AccountRequest struct {
	Username string          `json:"username"`
	Label    string          `json:"label"`
	Meta     json.RawMessage `json:"meta"`
	Sites    []SiteRequest   `json:"sites"`
}

var accountFileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,150}$`)

func accountMAC(key []byte, m *AccountManifest) string {
	in := struct {
		Version   int           `json:"version"`
		ID        string        `json:"id"`
		Username  string        `json:"username"`
		Label     string        `json:"label"`
		CreatedAt time.Time     `json:"created_at"`
		KeyID     string        `json:"key_id"`
		Meta      string        `json:"meta"`
		Sites     []AccountSite `json:"sites"`
	}{m.Version, m.ID, m.Username, m.Label, m.CreatedAt, m.KeyID, macMeta(m.Meta), m.Sites}
	b, _ := json.Marshal(in)
	derive := hmac.New(sha256.New, key)
	derive.Write([]byte("nimvor-account-mac-v1"))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	mac.Write(b)
	return hex.EncodeToString(mac.Sum(nil))
}

func (r *AccountRequest) validate(s Service) error {
	if err := validate.Username("username", r.Username); err != nil {
		return err
	}
	if err := validLabel(r.Label); err != nil {
		return err
	}
	if len(r.Meta) > maxAccountMeta || len(r.Meta) > 0 && !json.Valid(r.Meta) {
		return &validate.Error{Field: "meta", Message: "must be JSON up to 1 MiB"}
	}
	if len(r.Sites) > maxAccountSites {
		return &validate.Error{Field: "sites", Message: fmt.Sprintf("at most %d websites", maxAccountSites)}
	}
	seen := map[string]bool{}
	for i := range r.Sites {
		site := &r.Sites[i]
		// Always encrypted and authenticated, kept here (the account file
		// is what leaves the server).
		site.Encrypt, site.Remote, site.KeepLocal = true, nil, nil
		if site.Label == "" {
			site.Label = "Account backup " + r.Username
		}
		if err := site.validate(s.Env); err != nil {
			return &validate.Error{Field: fmt.Sprintf("sites[%d]", i), Message: err.Error()}
		}
		if seen[site.Domain] {
			return &validate.Error{Field: "sites", Message: site.Domain + " is listed twice"}
		}
		seen[site.Domain] = true
	}
	return nil
}

func (s Service) accountDir(username string) string {
	return filepath.Join(s.Env.Cfg.BackupDir, "accounts", username)
}

func (s Service) incomingDir() string { return filepath.Join(s.Env.Cfg.BackupDir, "incoming") }

// CreateAccount backs up each website of an account and writes the account
// file (called inside a job). creds maps a system user to its credential.
func (s Service) CreateAccount(ctx context.Context, r AccountRequest, creds map[string]*execx.Credential, log io.Writer) (*AccountManifest, string, error) {
	key, err := s.key()
	if err != nil {
		return nil, "", fmt.Errorf("the backup key could not be loaded: %v", err)
	}
	m := &AccountManifest{Version: 1, ID: s.newID(), Username: r.Username, Label: r.Label, CreatedAt: s.Env.Now().UTC(), KeyID: KeyID(key), Meta: normalizeMeta(r.Meta), Sites: []AccountSite{}}
	var made []*Manifest
	for _, site := range r.Sites {
		fmt.Fprintf(log, "== %s\n", site.Domain)
		sm, err := s.CreateSite(ctx, site, creds[site.User], log)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %v", site.Domain, err)
		}
		made = append(made, sm)
		m.Sites = append(m.Sites, AccountSite{Domain: sm.Domain, ID: sm.ID})
	}
	m.Mac = accountMAC(key, m)

	hostDir := s.accountDir(r.Username)
	if _, err := s.Env.Writer.MkdirAll(hostDir, 0o700, agentfs.RootOwner); err != nil {
		return nil, "", err
	}
	dir, err := s.real(hostDir)
	if err != nil {
		return nil, "", err
	}
	var total int64
	for _, sm := range made {
		total += sm.Size
	}
	if err := needSpace(dir, total, "the account backup file"); err != nil {
		return nil, "", err
	}
	name := r.Username + "-" + m.ID + AccountSuffix
	final := filepath.Join(dir, name)
	tmp := final + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", err
	}
	defer os.Remove(tmp)
	fmt.Fprintf(log, "Writing %s…\n", name)
	tw := tar.NewWriter(newSpaceGuard(f, dir))
	head, _ := json.MarshalIndent(m, "", "  ")
	if err := addBytes(tw, "account.json", head); err != nil {
		f.Close()
		return nil, "", err
	}
	for _, sm := range made {
		_, sdir, err := s.Load(sm.Domain, sm.ID)
		if err != nil {
			f.Close()
			return nil, "", err
		}
		base := "sites/" + sm.Domain + "/" + sm.ID + "/"
		if err := addFile(tw, base+"manifest.json", filepath.Join(sdir, "manifest.json")); err != nil {
			f.Close()
			return nil, "", err
		}
		for _, part := range sm.Files {
			if err := addFile(tw, base+part.Name, filepath.Join(sdir, part.Name)); err != nil {
				f.Close()
				return nil, "", err
			}
		}
	}
	if err := tw.Close(); err != nil {
		f.Close()
		return nil, "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, "", err
	}
	f.Close()
	if err := os.Rename(tmp, final); err != nil {
		return nil, "", err
	}
	info, _ := os.Stat(final)
	fmt.Fprintf(log, "Account backup written: %s (%d bytes).\n", name, info.Size())
	return m, filepath.Join(hostDir, name), nil
}

func addBytes(tw *tar.Writer, name string, b []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(b)), ModTime: time.Now().UTC(), Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
		return err
	}
	_, err := tw.Write(b)
	return err
}

func addFile(tw *tar.Writer, name, src string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("%s is missing: %v", path.Base(src), err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: info.Size(), ModTime: info.ModTime().UTC(), Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// AccountFile is an account backup file found on this server.
type AccountFile struct {
	Path      string           `json:"path"`
	Name      string           `json:"name"`
	Size      int64            `json:"size"`
	Modified  time.Time        `json:"modified"`
	Where     string           `json:"where"` // backups, uploaded, folder
	Manifest  *AccountManifest `json:"manifest,omitempty"`
	Problem   string           `json:"problem,omitempty"`
	Removable bool             `json:"removable"`
}

// accountRoots are where account backup files are looked for and may be
// read from: the agent's own folders, and folders for mounted disks.
func (s Service) accountRoots() []string {
	roots := []string{filepath.Join(s.Env.Cfg.BackupDir, "accounts") + "/", s.incomingDir() + "/"}
	return append(roots, localRoots...)
}

// accountPath checks a host path the panel names: an account backup file
// (by its suffix) below one of accountRoots, a regular file reached
// without symbolic links. It returns the real path.
func (s Service) accountPath(p string) (string, error) {
	clean := path.Clean("/" + strings.TrimSpace(p))
	if clean != strings.TrimSpace(p) || !strings.HasSuffix(clean, AccountSuffix) || strings.ContainsAny(clean, "\x00\n\r") {
		return "", actions.InvalidParams("path: choose a " + AccountSuffix + " file")
	}
	inside := false
	for _, root := range s.accountRoots() {
		if strings.HasPrefix(clean, root) {
			inside = true
		}
	}
	if !inside {
		return "", actions.InvalidParams("path: account backups are read from " + strings.Join(s.accountRoots(), ", "))
	}
	real, err := s.Env.Root.Path(clean)
	if err != nil {
		return "", actions.InvalidParams("path: " + err.Error())
	}
	info, err := os.Lstat(real)
	if err != nil {
		return "", actions.NotFound("The file was not found.")
	}
	if !info.Mode().IsRegular() {
		return "", actions.InvalidParams("path: not a regular file")
	}
	if resolved, err := filepath.EvalSymlinks(real); err != nil || resolved != real {
		return "", actions.InvalidParams("path: must not be or contain a symbolic link")
	}
	return real, nil
}

// readAccountManifest reads and checks the account.json at the start of
// an account backup file.
func (s Service) readAccountManifest(real string) (*AccountManifest, error) {
	f, err := os.Open(real)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	h, err := tr.Next()
	if err != nil || h.Name != "account.json" || h.Typeflag != tar.TypeReg || h.Size > maxAccountHeader {
		return nil, errors.New("not a NimVor account backup")
	}
	var m AccountManifest
	if err := json.NewDecoder(io.LimitReader(tr, maxAccountHeader)).Decode(&m); err != nil {
		return nil, errors.New("not a NimVor account backup")
	}
	if m.Version != 1 || !idRe.MatchString(m.ID) || validate.Username("username", m.Username) != nil || len(m.Sites) > maxAccountSites {
		return nil, errors.New("not a valid NimVor account backup")
	}
	for _, site := range m.Sites {
		if validate.Domain("domain", site.Domain) != nil || !idRe.MatchString(site.ID) {
			return nil, errors.New("not a valid NimVor account backup")
		}
	}
	return &m, nil
}

// checkAccountManifest refuses an account backup this server cannot trust.
func (s Service) checkAccountManifest(m *AccountManifest) error {
	key, err := existingKey(s.keyReal())
	if err != nil {
		return err
	}
	if key == nil {
		return fmt.Errorf("this server has no backup key yet: import the key of the server that made this backup (key id %s) first", m.KeyID)
	}
	if m.KeyID != KeyID(key) {
		return fmt.Errorf("made with another backup key (key id %s; this server's is %s): import that key first", m.KeyID, KeyID(key))
	}
	if !hmac.Equal([]byte(m.Mac), []byte(accountMAC(key, m))) {
		return errors.New("its description is not authenticated by this backup key (the file was changed)")
	}
	return nil
}

func (s Service) keyReal() string {
	p, err := s.Env.Root.Path(s.keyPath())
	if err != nil {
		return s.keyPath()
	}
	return p
}

// InspectAccount reads an account backup file and checks it can be
// restored here (the key and the authenticated description; the parts'
// checksums are checked when it is imported).
func (s Service) InspectAccount(p string) (*AccountManifest, error) {
	real, err := s.accountPath(p)
	if err != nil {
		return nil, err
	}
	m, err := s.readAccountManifest(real)
	if err != nil {
		return nil, actions.Failed("The file is not a NimVor account backup.", err.Error())
	}
	if err := s.checkAccountManifest(m); err != nil {
		return m, actions.Failed("This account backup cannot be restored on this server.", err.Error())
	}
	return m, nil
}

// ImportAccount puts the website backups of an account backup file in the
// local backup folders (checked against the account's description, their
// authenticated manifests and their checksums), so they can be restored
// like any other website backup. Backups already here are kept as they are.
func (s Service) ImportAccount(ctx context.Context, p string, log io.Writer) (*AccountManifest, []map[string]any, error) {
	real, err := s.accountPath(p)
	if err != nil {
		return nil, nil, err
	}
	m, err := s.readAccountManifest(real)
	if err != nil {
		return nil, nil, err
	}
	if err := s.checkAccountManifest(m); err != nil {
		return nil, nil, err
	}
	key, _ := s.key()
	want := map[string]string{}
	for _, site := range m.Sites {
		want[site.Domain+"/"+site.ID] = site.Domain
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	if _, err := tr.Next(); err != nil { // account.json
		return nil, nil, err
	}
	type staged struct {
		domain, id, dir string
		manifest        *Manifest
		parts           map[string]bool
	}
	cur := (*staged)(nil)
	results := []map[string]any{}
	done := map[string]bool{}
	finish := func(st *staged) error {
		if st == nil {
			return nil
		}
		defer os.RemoveAll(st.dir)
		key2 := st.domain + "/" + st.id
		if st.manifest == nil {
			return fmt.Errorf("%s: the website backup has no manifest", key2)
		}
		mm := st.manifest
		switch {
		case mm.Kind != "site" || mm.Domain != st.domain || mm.ID != st.id:
			return fmt.Errorf("%s: the manifest belongs to another backup", key2)
		case !mm.Encrypted || mm.KeyID != KeyID(key):
			return fmt.Errorf("%s: not encrypted with this server's backup key", key2)
		case mm.Mac == "" || !hmac.Equal([]byte(mm.Mac), []byte(manifestMAC(key, mm))):
			return fmt.Errorf("%s: the website backup's manifest is not authenticated", key2)
		}
		for _, part := range mm.Files {
			if !st.parts[part.Name] {
				return fmt.Errorf("%s: %s is missing from the file", key2, part.Name)
			}
			if err := verify(filepath.Join(st.dir, part.Name), part); err != nil {
				return fmt.Errorf("%s: %v", key2, err)
			}
		}
		done[key2] = true
		final := s.dir(st.domain, st.id)
		if s.Env.Root.Exists(filepath.Join(final, "manifest.json")) {
			fmt.Fprintf(log, "%s: already on this server.\n", key2)
			results = append(results, map[string]any{"domain": st.domain, "id": st.id, "imported": false})
			return nil
		}
		mm.Local, mm.Remote = true, nil
		if err := writeManifest(st.dir, mm); err != nil {
			return err
		}
		finalReal, err := s.real(final)
		if err != nil {
			return err
		}
		if err := os.Rename(st.dir, finalReal); err != nil {
			return err
		}
		fmt.Fprintf(log, "%s: imported (%d bytes).\n", key2, mm.Size)
		results = append(results, map[string]any{"domain": st.domain, "id": st.id, "imported": true})
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("the file is damaged: %v", err)
		}
		parts := strings.Split(h.Name, "/")
		if h.Typeflag != tar.TypeReg || len(parts) != 4 || parts[0] != "sites" {
			return nil, nil, fmt.Errorf("unexpected entry %q in the file", h.Name)
		}
		domain, id, name := parts[1], parts[2], parts[3]
		if _, ok := want[domain+"/"+id]; !ok || done[domain+"/"+id] {
			return nil, nil, fmt.Errorf("entry %q is not part of the account's description", h.Name)
		}
		if name != "manifest.json" && !nameRe.MatchString(name) {
			return nil, nil, fmt.Errorf("unexpected entry %q in the file", h.Name)
		}
		if cur == nil || cur.domain != domain || cur.id != id {
			if err := finish(cur); err != nil {
				return nil, nil, err
			}
			siteBase := filepath.Join(s.Env.Cfg.BackupDir, "sites", domain)
			if _, err := s.Env.Writer.MkdirAll(siteBase, 0o700, agentfs.RootOwner); err != nil {
				return nil, nil, err
			}
			baseReal, err := s.real(siteBase)
			if err != nil {
				return nil, nil, err
			}
			tmp, err := os.MkdirTemp(baseReal, ".import-")
			if err != nil {
				return nil, nil, err
			}
			cur = &staged{domain: domain, id: id, dir: tmp, parts: map[string]bool{}}
			fmt.Fprintf(log, "Checking %s/%s…\n", domain, id)
		}
		if name == "manifest.json" {
			if h.Size > maxManifest {
				return nil, nil, fmt.Errorf("%s: manifest too large", domain)
			}
			var mm Manifest
			if err := json.NewDecoder(io.LimitReader(tr, maxManifest)).Decode(&mm); err != nil {
				return nil, nil, fmt.Errorf("%s: the manifest is damaged", domain)
			}
			cur.manifest = &mm
			continue
		}
		if cur.parts[name] {
			return nil, nil, fmt.Errorf("%s: %s appears twice", domain, name)
		}
		out, err := os.OpenFile(filepath.Join(cur.dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, nil, err
		}
		_, err = io.Copy(newSpaceGuard(out, cur.dir), io.LimitReader(tr, h.Size))
		out.Close()
		if err != nil {
			os.RemoveAll(cur.dir)
			return nil, nil, err
		}
		cur.parts[name] = true
	}
	if err := finish(cur); err != nil {
		return nil, nil, err
	}
	for k := range want {
		if !done[k] {
			return nil, results, fmt.Errorf("the website backup %s is missing from the file", k)
		}
	}
	return m, results, nil
}

// AccountFiles lists the account backup files on this server: those made
// here, those uploaded, and those in folders for mounted disks (two levels
// deep), with their descriptions when readable.
func (s Service) AccountFiles() []AccountFile {
	out := []AccountFile{}
	add := func(hostPath, where string, removable bool) {
		if len(out) >= 500 {
			return
		}
		real, err := s.accountPath(hostPath)
		if err != nil {
			return
		}
		info, err := os.Stat(real)
		if err != nil {
			return
		}
		af := AccountFile{Path: hostPath, Name: filepath.Base(hostPath), Size: info.Size(), Modified: info.ModTime().UTC(), Where: where, Removable: removable}
		if m, err := s.readAccountManifest(real); err != nil {
			af.Problem = err.Error()
		} else {
			if err := s.checkAccountManifest(m); err != nil {
				af.Problem = err.Error()
			}
			m.Meta = nil
			af.Manifest = m
		}
		out = append(out, af)
	}
	walk := func(base, where string, depth int, removable bool) {
		var rec func(dir string, d int)
		rec = func(dir string, d int) {
			entries, err := s.Env.Root.ReadDir(dir)
			if err != nil {
				return
			}
			for _, e := range entries {
				p := filepath.Join(dir, e.Name())
				switch {
				case e.Type()&fs.ModeSymlink != 0:
				case e.IsDir() && d < depth && !strings.HasPrefix(e.Name(), "."):
					rec(p, d+1)
				case e.Type().IsRegular() && strings.HasSuffix(e.Name(), AccountSuffix):
					add(p, where, removable)
				}
			}
		}
		rec(strings.TrimSuffix(base, "/"), 0)
	}
	walk(filepath.Join(s.Env.Cfg.BackupDir, "accounts"), "backups", 1, true)
	walk(s.incomingDir(), "uploaded", 0, true)
	for _, root := range localRoots {
		walk(root, "folder", 2, false)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out
}

// RemoveAccountFile deletes an account backup made or uploaded here (files
// in other folders are left to their owner).
func (s Service) RemoveAccountFile(p string) error {
	real, err := s.accountPath(p)
	if err != nil {
		return err
	}
	clean := path.Clean(p)
	own := strings.HasPrefix(clean, filepath.Join(s.Env.Cfg.BackupDir, "accounts")+"/") || strings.HasPrefix(clean, s.incomingDir()+"/")
	if !own {
		return actions.InvalidParams("path: only account backups made or uploaded here can be deleted")
	}
	return os.Remove(real)
}

// AccountChunk reads up to 2 MiB of an account backup file.
func (s Service) AccountChunk(p string, offset int64) ([]byte, int64, error) {
	real, err := s.accountPath(p)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, 0, actions.NotFound("The file was not found.")
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

// UploadAccountChunk appends a chunk to an uploaded account backup (in
// the incoming folder); final makes it available under its name.
func (s Service) UploadAccountChunk(name string, offset int64, data []byte, final bool) (int64, string, error) {
	if !accountFileRe.MatchString(name) || !strings.HasSuffix(name, AccountSuffix) {
		return 0, "", actions.InvalidParams("name: an account backup file name ending in " + AccountSuffix)
	}
	if _, err := s.Env.Writer.MkdirAll(s.incomingDir(), 0o700, agentfs.RootOwner); err != nil {
		return 0, "", err
	}
	dir, err := s.real(s.incomingDir())
	if err != nil {
		return 0, "", err
	}
	finalPath := filepath.Join(dir, name)
	partial := finalPath + ".partial"
	flags := os.O_WRONLY
	if offset == 0 {
		flags |= os.O_CREATE | os.O_TRUNC
		if _, err := os.Lstat(finalPath); err == nil {
			return 0, "", actions.InvalidParams("name: a file with this name was already uploaded; delete it first")
		}
	}
	f, err := os.OpenFile(partial, flags, 0o600)
	if err != nil {
		return 0, "", actions.InvalidParams("offset: the upload was not started")
	}
	defer f.Close()
	info, _ := f.Stat()
	if info.Size() != offset {
		return 0, "", actions.InvalidParams(fmt.Sprintf("offset: expected %d", info.Size()))
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, "", err
	}
	if _, err := newSpaceGuard(f, dir).Write(data); err != nil {
		f.Close()
		os.Remove(partial)
		return 0, "", err
	}
	size := offset + int64(len(data))
	if !final {
		return size, "", nil
	}
	if err := f.Sync(); err != nil {
		return 0, "", err
	}
	if err := os.Rename(partial, finalPath); err != nil {
		return 0, "", err
	}
	return size, filepath.Join(s.incomingDir(), name), nil
}
