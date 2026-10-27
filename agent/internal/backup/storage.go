package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/NimVor/nimvor/agent/internal/host"
)

// Storage is a remote backup destination. Keys are slash-separated paths
// (prefix/sites/<domain>/<id>/<file>).
type Storage interface {
	Put(ctx context.Context, key, localPath string) error
	Get(ctx context.Context, key string, w io.Writer) error
	Delete(ctx context.Context, key string) error
	// List returns the keys of the files directly inside prefix ("a/b/").
	List(ctx context.Context, prefix string) ([]string, error)
	// RemoveDir removes an (empty) folder where the storage has folders.
	RemoveDir(ctx context.Context, prefix string) error
}

// Open returns the storage client for a validated remote.
func (r Remote) Open(env *host.Env) Storage {
	switch r.Type {
	case "sftp", "ftp":
		return &fileRemote{R: r, Env: env}
	case "gdrive":
		return NewDrive(r)
	case "local":
		return &localDir{R: r, Env: env}
	}
	return NewS3(r)
}

// Describe is a short, secret-free description of the destination.
func (r Remote) Describe() string {
	switch r.Type {
	case "sftp", "ftp":
		return fmt.Sprintf("%s://%s@%s:%d", r.Type, r.Username, r.Host, r.port())
	case "gdrive":
		return "Google Drive folder " + r.FolderID
	case "local":
		return r.Path
	}
	return r.Endpoint + "/" + r.Bucket
}

func (r Remote) port() int {
	if r.Port != 0 {
		return r.Port
	}
	switch {
	case r.Type == "sftp":
		return 22
	case r.TLS == "implicit":
		return 990
	}
	return 21
}

var (
	hostRe    = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	userRe    = regexp.MustCompile(`^[A-Za-z0-9._@+-]{1,100}$`)
	driveIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)
	tlsPinRe  = regexp.MustCompile(`^sha256//[A-Za-z0-9+/]{43}=$`)
)

func (r *Remote) validateFile() error {
	h := strings.Trim(r.Host, "[]")
	if !hostRe.MatchString(h) && net.ParseIP(h) == nil {
		return fmt.Errorf("host must be a host name or IP address")
	}
	r.Host = h
	if r.Port < 0 || r.Port > 65535 {
		return fmt.Errorf("port must be 1–65535")
	}
	if !userRe.MatchString(r.Username) {
		return fmt.Errorf("user name may contain letters, digits and . _ @ + -")
	}
	if strings.ContainsAny(r.Password, "\r\n\x00") || len(r.Password) > 500 {
		return fmt.Errorf("password is invalid")
	}
	if r.Type == "sftp" {
		if r.Password == "" && r.PrivateKey == "" {
			return fmt.Errorf("a password or a private key is required")
		}
		if r.PrivateKey != "" && (!strings.Contains(r.PrivateKey, "PRIVATE KEY-----") || len(r.PrivateKey) > 16384) {
			return fmt.Errorf("private key must be an unencrypted key in PEM/OpenSSH format")
		}
		if r.HostKey != "" && !knownKeyRe.MatchString(r.HostKey) {
			return fmt.Errorf("host key must be an SSH public key (type and base64)")
		}
		return nil
	}
	if r.PrivateKey != "" || r.HostKey != "" {
		return fmt.Errorf("private key and host key apply to SFTP only")
	}
	if r.TLSPin != "" && !tlsPinRe.MatchString(r.TLSPin) {
		return fmt.Errorf("tls_pin must look like sha256//<base64>")
	}
	switch r.TLS {
	case "":
		r.TLS = "explicit"
	case "explicit", "implicit", "none":
	default:
		return fmt.Errorf("tls must be explicit, implicit or none")
	}
	if r.Password == "" {
		return fmt.Errorf("password is required")
	}
	return nil
}

func (r *Remote) validateDrive() error {
	if r.ClientID == "" || r.ClientSecret == "" || r.RefreshToken == "" {
		return fmt.Errorf("connect Google Drive first (client ID, client secret and authorisation)")
	}
	for _, v := range []string{r.ClientID, r.ClientSecret, r.RefreshToken} {
		if len(v) > 2048 || strings.ContainsAny(v, "\r\n \x00") {
			return fmt.Errorf("Google credentials are invalid")
		}
	}
	if r.FolderID != "" && !driveIDRe.MatchString(r.FolderID) {
		return fmt.Errorf("folder ID is invalid")
	}
	return nil
}

// Local destinations must live under these roots (mounted disks, NAS).
var localRoots = []string{"/mnt/", "/media/", "/backup/", "/backups/", "/srv/backup/", "/srv/backups/"}

// creatableRoots are local backup folders on this server's own disk:
// WhaCP may use them as they are and creates them when missing. Under
// /mnt and /media a missing folder means the disk is not mounted, so
// those are never created (backups would fill the system disk instead).
var creatableRoots = []string{"/backup", "/backups", "/srv/backup", "/srv/backups"}

func creatable(p string) bool {
	for _, root := range creatableRoots {
		if p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	return false
}

func (r *Remote) validateLocal() error {
	p := path.Clean("/" + strings.TrimSpace(r.Path))
	ok := creatable(p)
	for _, root := range localRoots {
		if strings.HasPrefix(p+"/", root) && p+"/" != root {
			ok = true
		}
	}
	if !ok || strings.ContainsAny(p, "\x00\n") {
		return fmt.Errorf("path must be a folder under %s", strings.Join(localRoots, ", "))
	}
	r.Path = p
	return nil
}

// ---- local directory ---------------------------------------------------

type localDir struct {
	R   Remote
	Env *host.Env
}

// base opens the destination folder: it must exist, belong to root and not
// be writable by others, and no part of it may be a symlink.
func (l *localDir) base() (string, error) {
	real, err := l.Env.Root.Path(l.R.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(real)
	if err != nil || !info.IsDir() {
		if creatable(l.R.Path) {
			return "", fmt.Errorf("%s does not exist or is not a folder", l.R.Path)
		}
		return "", fmt.Errorf("%s does not exist or is not a folder: is the disk mounted? (WhaCP does not create folders under /mnt or /media, so that backups never fill the system disk while a disk is missing)", l.R.Path)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && (st.Uid != 0 && os.Geteuid() == 0 || info.Mode().Perm()&0o002 != 0) {
		return "", fmt.Errorf("%s must belong to root and not be writable by others", l.R.Path)
	}
	if resolved, err := filepath.EvalSymlinks(real); err != nil || resolved != real {
		return "", fmt.Errorf("%s must not be or contain a symbolic link", l.R.Path)
	}
	return real, nil
}

// Ensure creates a missing destination under one of the creatable roots,
// each missing folder owned by root with mode 0700. It reports whether it
// created something. Existing folders are left as they are (base checks
// them); a symbolic link on the way is refused.
func (l *localDir) Ensure() (bool, error) {
	if !creatable(l.R.Path) {
		return false, nil
	}
	created := false
	cur := ""
	for _, part := range strings.Split(strings.TrimPrefix(l.R.Path, "/"), "/") {
		cur += "/" + part
		real, err := l.Env.Root.Path(cur)
		if err != nil {
			return created, err
		}
		info, err := os.Lstat(real)
		switch {
		case err == nil && info.Mode()&os.ModeSymlink != 0:
			return created, fmt.Errorf("%s is a symbolic link", cur)
		case err == nil && !info.IsDir():
			return created, fmt.Errorf("%s exists and is not a folder", cur)
		case err == nil:
			continue
		case !os.IsNotExist(err):
			return created, err
		}
		if err := os.Mkdir(real, 0o700); err != nil {
			return created, fmt.Errorf("could not create %s: %w", cur, err)
		}
		if os.Geteuid() == 0 {
			if err := os.Lchown(real, 0, 0); err != nil {
				return true, err
			}
		}
		created = true
	}
	return created, nil
}

func (l *localDir) open() (*os.Root, error) {
	b, err := l.base()
	if err != nil {
		return nil, err
	}
	return os.OpenRoot(b)
}

func (l *localDir) Put(ctx context.Context, key, localPath string) error {
	root, err := l.open()
	if err != nil {
		return err
	}
	defer root.Close()
	dir := path.Dir(key)
	cur := "."
	for _, part := range strings.Split(dir, "/") {
		if part == "." || part == "" {
			continue
		}
		cur = path.Join(cur, part)
		if err := root.Mkdir(cur, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := key + ".partial"
	dst, err := root.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		root.Remove(tmp)
		return err
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		return err
	}
	dst.Close()
	b, _ := l.base()
	return os.Rename(filepath.Join(b, tmp), filepath.Join(b, key))
}

func (l *localDir) Get(ctx context.Context, key string, w io.Writer) error {
	root, err := l.open()
	if err != nil {
		return err
	}
	defer root.Close()
	f, err := root.Open(key)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func (l *localDir) Delete(ctx context.Context, key string) error {
	root, err := l.open()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (l *localDir) List(ctx context.Context, prefix string) ([]string, error) {
	root, err := l.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir := strings.TrimSuffix(prefix, "/")
	if dir == "" {
		dir = "."
	}
	d, err := root.Open(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer d.Close()
	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			out = append(out, strings.TrimPrefix(path.Join(dir, e.Name()), "./"))
		}
	}
	sort.Strings(out)
	return out, nil
}

func (l *localDir) RemoveDir(ctx context.Context, prefix string) error {
	root, err := l.open()
	if err != nil {
		return err
	}
	defer root.Close()
	root.Remove(strings.TrimSuffix(prefix, "/"))
	return nil
}
