package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Files fetched from a remote SFTP or FTPS server land in one folder,
// <backup dir>/fetched, and nowhere else: the remote side only chooses which
// file to read, never where on this server it is written.

var fetchedNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,150}$`)

// maxFetchedBatch bounds one bulk delete.
const maxFetchedBatch = 500

func (s Service) fetchedDir() string { return filepath.Join(s.Env.Cfg.BackupDir, "fetched") }

// FetchRequest asks for one file of a remote server. The credentials are
// used for this transfer only; the agent keeps none of them.
type FetchRequest struct {
	Remote Remote `json:"remote"`
	Path   string `json:"path"`
	Name   string `json:"name"`
}

// validFetchPath checks the file's path on the remote server. It may be
// absolute or relative to the login folder, and must name a file.
func validFetchPath(p string) error {
	if p == "" || len(p) > 1024 {
		return fmt.Errorf("enter the path of the file on the remote server (up to 1024 characters)")
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("the path contains a control character")
		}
	}
	if strings.HasSuffix(p, "/") {
		return fmt.Errorf("the path ends with / : give the file, not a folder")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("the path must not contain ..")
		}
	}
	return nil
}

// fetchURL builds the URL of a remote file path, with every segment escaped.
func (f *fileRemote) fetchURL(p string) string {
	abs := strings.HasPrefix(p, "/")
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	kept := segs[:0]
	for _, seg := range segs {
		if seg == "" || seg == "." {
			continue
		}
		kept = append(kept, url.PathEscape(seg))
	}
	rel := strings.Join(kept, "/")
	base := f.url("")
	// url("") ends in "/~/" (SFTP, relative to the login folder) or "/".
	switch {
	case f.R.Type == "sftp" && abs:
		return strings.TrimSuffix(base, "~/") + rel
	case f.R.Type == "sftp":
		return base + rel
	case abs:
		return base + "/" + rel
	}
	return base + rel
}

// localFetchName makes a file name for this server out of the remote path:
// the last segment, with anything but letters, digits and . _ - replaced.
func localFetchName(remotePath string) string {
	base := path.Base(strings.TrimRight(remotePath, "/"))
	var b strings.Builder
	dash := false
	for _, r := range base {
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	name := strings.TrimLeft(b.String(), ".-")
	if len(name) > 150 {
		name = name[len(name)-150:]
		name = strings.TrimLeft(name, ".-")
	}
	if name == "" {
		name = "fetched-file"
	}
	return name
}

func (in *FetchRequest) validate() error {
	if in.Remote.Type != "sftp" && in.Remote.Type != "ftp" {
		return fmt.Errorf("remote.type must be sftp or ftp")
	}
	if err := in.Remote.Validate(); err != nil {
		return err
	}
	if in.Remote.Type == "sftp" && in.Remote.HostKey == "" {
		return fmt.Errorf("remote.host_key: the server's SSH host key must be confirmed first")
	}
	if in.Remote.Type == "ftp" && in.Remote.TLS == "none" {
		return fmt.Errorf("remote.tls: plain FTP is not supported for fetching files; use FTPS (explicit or implicit TLS)")
	}
	if err := validFetchPath(in.Path); err != nil {
		return err
	}
	if in.Name != "" && (!fetchedNameRe.MatchString(in.Name) || strings.HasSuffix(in.Name, ".partial")) {
		return fmt.Errorf("name: letters, digits and . _ - only")
	}
	return nil
}

// uniqueFetchedName returns name, or name with -1, -2, … before its
// extension when a file with that name exists.
func uniqueFetchedName(dir, name string) (string, error) {
	taken := func(n string) bool {
		_, err := os.Lstat(filepath.Join(dir, n))
		return err == nil
	}
	if !taken(name) {
		return name, nil
	}
	ext := ""
	stem := name
	if i := strings.Index(name, "."); i > 0 {
		stem, ext = name[:i], name[i:]
	}
	for i := 1; i < 1000; i++ {
		n := stem + "-" + strconv.Itoa(i) + ext
		if len(n) <= 150 && !taken(n) {
			return n, nil
		}
	}
	return "", fmt.Errorf("too many files named %s", name)
}

// FetchRemote downloads the file into the fetched folder (called inside a
// job) and returns its name, size and SHA-256.
func (s Service) FetchRemote(ctx context.Context, in FetchRequest, log io.Writer) (map[string]any, error) {
	dirHost := s.fetchedDir()
	if _, err := s.Env.Writer.MkdirAll(dirHost, 0o700, agentfs.RootOwner); err != nil {
		return nil, fmt.Errorf("the folder for fetched files could not be made: %v", err)
	}
	dir, err := s.real(dirHost)
	if err != nil {
		return nil, err
	}
	if err := needSpace(dir, 0, "fetching a file"); err != nil {
		return nil, err
	}
	name := in.Name
	if name == "" {
		name = localFetchName(in.Path)
	}
	if name, err = uniqueFetchedName(dir, name); err != nil {
		return nil, err
	}
	final := filepath.Join(dir, name)
	partial := final + ".partial"
	out, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("the file could not be created: %v", err)
	}
	fail := func(err error) (map[string]any, error) {
		out.Close()
		os.Remove(partial)
		return nil, err
	}
	fmt.Fprintf(log, "Fetching %s from %s…\n", in.Path, in.Remote.Describe())
	remote := &fileRemote{R: in.Remote, Env: s.Env}
	h := sha256.New()
	w := io.MultiWriter(newSpaceGuard(out, dir), h)
	if _, err := remote.runURL(ctx, remote.fetchURL(in.Path), w); err != nil {
		return fail(fmt.Errorf("the file could not be fetched: %v", err))
	}
	info, err := out.Stat()
	if err != nil {
		return fail(err)
	}
	if info.Size() == 0 {
		return fail(fmt.Errorf("the remote file is empty or was not found"))
	}
	if err := out.Sync(); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		os.Remove(partial)
		return nil, err
	}
	if err := os.Rename(partial, final); err != nil {
		os.Remove(partial)
		return nil, err
	}
	fmt.Fprintf(log, "Saved %s (%d bytes).\n", name, info.Size())
	return map[string]any{
		"name":   name,
		"size":   info.Size(),
		"sha256": hex.EncodeToString(h.Sum(nil)),
		"path":   filepath.Join(dirHost, name),
	}, nil
}

// DeleteFetched removes fetched files by name. A file that is already gone
// counts as deleted, so a repeated request is harmless.
func (s Service) DeleteFetched(names []string) ([]map[string]any, error) {
	dir, err := s.real(s.fetchedDir())
	if err != nil {
		return nil, err
	}
	results := make([]map[string]any, 0, len(names))
	root, err := os.OpenRoot(dir)
	if err != nil {
		if os.IsNotExist(err) {
			for _, n := range names {
				results = append(results, map[string]any{"name": n, "deleted": true, "missing": true})
			}
			return results, nil
		}
		return nil, err
	}
	defer root.Close()
	for _, n := range names {
		res := map[string]any{"name": n}
		switch err := root.Remove(n); {
		case err == nil:
			res["deleted"] = true
		case os.IsNotExist(err):
			res["deleted"], res["missing"] = true, true
		default:
			res["deleted"], res["error"] = false, err.Error()
		}
		results = append(results, res)
	}
	return results, nil
}

// preferredHostKey picks the key to show and pin: the order in which a server
// answers is not stable, and the same server should show the same key every time.
func preferredHostKey(keys []string) string {
	for _, kind := range []string{"ssh-ed25519", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "ssh-rsa"} {
		for _, k := range keys {
			if strings.HasPrefix(k, kind+" ") {
				return k
			}
		}
	}
	return keys[0]
}

// registerFetched adds the actions of the remote fetched files.
func registerFetched(r *actions.Registry, env *host.Env, s Service) {
	r.Register(actions.Action{Name: "backup.fetch_probe", Timeout: 40 * time.Second, Description: "Ask an SFTP server for its SSH host key, or an FTPS server for its certificate key, so the user can confirm it before a file is fetched. Sends no credentials.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Type string `json:"type"`
			Host string `json:"host"`
			Port int    `json:"port"`
			TLS  string `json:"tls"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.Type != "sftp" && in.Type != "ftp" {
			return nil, actions.InvalidParams("type: sftp or ftp")
		}
		// Placeholder login: only the server's address is checked here.
		rm := Remote{Type: in.Type, Host: in.Host, Port: in.Port, TLS: in.TLS, Username: "probe", Password: "probe"}
		if err := rm.Validate(); err != nil {
			return nil, actions.InvalidParams("remote: " + err.Error())
		}
		if rm.Type == "ftp" && rm.TLS == "none" {
			return nil, actions.InvalidParams("tls: plain FTP is not supported for fetching files; use FTPS")
		}
		if rm.Type == "sftp" {
			keys, err := ScanHostKeys(ctx, env, rm)
			if err != nil {
				return nil, actions.Failed("The server could not be reached.", err.Error())
			}
			key := preferredHostKey(keys)
			return map[string]any{"type": "sftp", "host_key": key, "key_type": strings.Fields(key)[0], "fingerprint": Fingerprint(key)}, nil
		}
		pin, subject, err := ScanTLSPin(ctx, rm)
		if err != nil {
			return nil, actions.Failed("The server could not be reached over FTPS.", err.Error())
		}
		return map[string]any{"type": "ftp", "tls_pin": pin, "subject": subject}, nil
	}})
	r.Register(actions.Action{Name: "backup.fetch_remote", Mutating: true, Description: "Download one file from a remote SFTP or FTPS server into the folder of fetched files (job). The credentials are used for this transfer only and are not stored; the server's key must be pinned.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in FetchRequest
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		j := env.Jobs.Start("backup.fetch_remote", "server:fetch", func(ctx context.Context, log io.Writer) (any, error) {
			return s.FetchRemote(ctx, in, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "backup.fetched_delete", Mutating: true, Description: "Delete fetched files by name (several at once). A file already gone counts as deleted.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Names []string `json:"names"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if len(in.Names) == 0 || len(in.Names) > maxFetchedBatch {
			return nil, actions.InvalidParams(fmt.Sprintf("names: 1 to %d file names", maxFetchedBatch))
		}
		for _, n := range in.Names {
			if !fetchedNameRe.MatchString(n) {
				return nil, actions.InvalidParams("names: invalid file name")
			}
		}
		res, err := s.DeleteFetched(in.Names)
		if err != nil {
			return nil, actions.Failed("The files could not be deleted.", err.Error())
		}
		return map[string]any{"results": res}, nil
	}})
}
