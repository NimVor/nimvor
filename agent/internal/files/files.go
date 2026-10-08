// Package files implements the website file manager.
//
// Every operation is confined to one website root with os.Root, which
// resolves paths relative to an open directory handle and refuses any path
// (including one reached through a symlink) that would leave it. New files
// are owned by the website's account, never by root.
package files

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

const (
	maxEditable = 2 << 20 // text files larger than this are not opened in the editor
	maxChunk    = 2 << 20 // upload/download chunk size
	maxEntries  = 5000
)

// protected top-level entries cannot be deleted or renamed.
var protected = map[string]bool{".": true, "public_html": true, "logs": true, "tmp": true, "backups": true}

// Site is an open website root.
type Site struct {
	root *os.Root
	uid  int
	gid  int
}

// Open opens the root of domain inside the sites root.
func Open(env *host.Env, domain string) (*Site, error) {
	if err := validate.Domain("domain", domain); err != nil {
		return nil, host.Invalid(err)
	}
	real, err := env.Root.Path(filepath.Join(env.Cfg.SitesRoot, domain))
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(real)
	if err != nil || !info.IsDir() {
		return nil, actions.NotFound("The website directory does not exist.")
	}
	r, err := os.OpenRoot(real)
	if err != nil {
		return nil, err
	}
	st := info.Sys().(*syscall.Stat_t)
	return &Site{root: r, uid: int(st.Uid), gid: int(st.Gid)}, nil
}

// Close releases the root handle.
func (s *Site) Close() error { return s.root.Close() }

// clean validates a relative path ("" and "/" mean the site root).
func clean(p string) (string, error) {
	p = strings.TrimPrefix(p, "/")
	if err := validate.RelPath("path", p); err != nil {
		return "", host.Invalid(err)
	}
	if p == "" {
		return ".", nil
	}
	c := path.Clean(p)
	// WhaCP's own folders (trash, quarantine, temporary files) and anything
	// inside them are not the file manager's.
	for _, part := range strings.Split(c, "/") {
		if strings.HasPrefix(part, ".whacp-") {
			return "", actions.InvalidParams("path: names starting with .whacp- are reserved")
		}
	}
	return c, nil
}

func friendly(err error) error {
	var ae *actions.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ae):
		return err
	case errors.Is(err, fs.ErrNotExist):
		return actions.NotFound("The file or folder does not exist.")
	case errors.Is(err, fs.ErrExist):
		return actions.Conflict("A file or folder with that name already exists.")
	case errors.Is(err, fs.ErrPermission):
		return actions.Failed("Permission denied.", "")
	case strings.Contains(err.Error(), "escapes from parent") || strings.Contains(err.Error(), "path escapes"):
		return actions.InvalidParams("path: leaves the website directory")
	}
	return actions.Failed("The file operation failed: "+err.Error(), "")
}

// Entry is one directory entry.
type Entry struct {
	Name    string    `json:"name"`
	Type    string    `json:"type"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"`
	ModTime time.Time `json:"mtime"`
	Owner   int       `json:"uid"`
}

func entryOf(name string, info fs.FileInfo) Entry {
	e := Entry{Name: name, Size: info.Size(), Mode: fmt.Sprintf("%04o", info.Mode().Perm()), ModTime: info.ModTime().UTC(), Type: "file"}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		e.Type = "link"
	case info.IsDir():
		e.Type, e.Size = "dir", 0
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		e.Owner = int(st.Uid)
	}
	return e
}

// List returns the entries of a directory, folders first.
func (s *Site) List(p string) ([]Entry, error) {
	p, err := clean(p)
	if err != nil {
		return nil, err
	}
	d, err := s.root.Open(p)
	if err != nil {
		return nil, friendly(err)
	}
	defer d.Close()
	items, err := d.ReadDir(maxEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, friendly(err)
	}
	out := make([]Entry, 0, len(items))
	for _, it := range items {
		if strings.HasPrefix(it.Name(), ".whacp-") {
			continue
		}
		info, err := it.Info()
		if err != nil {
			continue
		}
		out = append(out, entryOf(it.Name(), info))
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Type == "dir") != (out[j].Type == "dir") {
			return out[i].Type == "dir"
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if len(out) > maxEntries {
		out = out[:maxEntries]
	}
	return out, nil
}

// ReadResult is a text file opened for editing.
type ReadResult struct {
	Path    string    `json:"path"`
	Content string    `json:"content"`
	Binary  bool      `json:"binary"`
	TooBig  bool      `json:"too_big"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}

// Read opens a text file.
func (s *Site) Read(p string) (*ReadResult, error) {
	p, err := clean(p)
	if err != nil {
		return nil, err
	}
	f, err := s.root.Open(p)
	if err != nil {
		return nil, friendly(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, friendly(err)
	}
	if info.IsDir() {
		return nil, actions.InvalidParams("path: is a folder")
	}
	res := &ReadResult{Path: p, Size: info.Size(), ModTime: info.ModTime().UTC()}
	if info.Size() > maxEditable {
		res.TooBig = true
		return res, nil
	}
	b, err := io.ReadAll(io.LimitReader(f, maxEditable+1))
	if err != nil {
		return nil, friendly(err)
	}
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		res.Binary = true
		return res, nil
	}
	res.Content = string(b)
	return res, nil
}

// Write saves a text file atomically. When expected is non-zero and the
// file changed since it was read, the write is refused.
func (s *Site) Write(p, content string, expected time.Time) (*ReadResult, error) {
	p, err := clean(p)
	if err != nil {
		return nil, err
	}
	if len(content) > maxEditable*4 {
		return nil, actions.InvalidParams("content: file is too large for the editor")
	}
	mode := fs.FileMode(0o644)
	if info, err := s.root.Lstat(p); err == nil {
		if info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			return nil, actions.InvalidParams("path: not a regular file")
		}
		if !expected.IsZero() && !info.ModTime().UTC().Equal(expected.UTC()) {
			return nil, actions.Conflict("The file was changed by someone else since you opened it. Reload it before saving.")
		}
		mode = info.Mode().Perm()
	}
	if err := s.atomicWrite(p, strings.NewReader(content), mode); err != nil {
		return nil, friendly(err)
	}
	// The write succeeded; re-stat for the result. Something may have
	// changed the path concurrently (a racing request, or the account's own
	// processes), so a failed stat is not fatal — report what we wrote
	// rather than dereferencing a nil FileInfo.
	info, err := s.root.Stat(p)
	if err != nil {
		return &ReadResult{Path: p, Size: int64(len(content))}, nil
	}
	return &ReadResult{Path: p, Size: info.Size(), ModTime: info.ModTime().UTC()}, nil
}

func (s *Site) atomicWrite(p string, r io.Reader, mode fs.FileMode) error {
	tmp := path.Join(path.Dir(p), fmt.Sprintf(".whacp-tmp-%d", time.Now().UnixNano()))
	f, err := s.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			s.root.Remove(tmp)
		}
	}()
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	if err := f.Chown(s.uid, s.gid); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	if err := s.rename(tmp, p); err != nil {
		return err
	}
	ok = true
	return nil
}

// rename moves oldp to newp within the root (renameat on directory fds).
func (s *Site) rename(oldp, newp string) error {
	od, err := s.root.Open(path.Dir(oldp))
	if err != nil {
		return err
	}
	defer od.Close()
	nd, err := s.root.Open(path.Dir(newp))
	if err != nil {
		return err
	}
	defer nd.Close()
	return syscall.Renameat(int(od.Fd()), path.Base(oldp), int(nd.Fd()), path.Base(newp))
}

// Mkdir creates a directory owned by the site account.
func (s *Site) Mkdir(p string) error {
	p, err := clean(p)
	if err != nil {
		return err
	}
	if err := s.root.Mkdir(p, 0o755); err != nil {
		return friendly(err)
	}
	d, err := s.root.Open(p)
	if err != nil {
		return friendly(err)
	}
	defer d.Close()
	return friendly(d.Chown(s.uid, s.gid))
}

// Rename moves or renames an entry; the destination must not exist.
func (s *Site) Rename(from, to string) error {
	from, err := clean(from)
	if err != nil {
		return err
	}
	to, err = clean(to)
	if err != nil {
		return err
	}
	if protected[from] || protected[to] {
		return actions.InvalidParams("the website's top-level folders cannot be renamed")
	}
	if _, err := s.root.Lstat(to); err == nil {
		return actions.Conflict("A file or folder named " + to + " already exists.")
	}
	if strings.HasPrefix(to+"/", from+"/") {
		return actions.InvalidParams("a folder cannot be moved into itself")
	}
	return friendly(s.rename(from, to))
}

// Delete removes files and folders (recursively).
func (s *Site) Delete(paths []string) (int, error) {
	n := 0
	for _, p := range paths {
		c, err := clean(p)
		if err != nil {
			return n, err
		}
		if protected[c] {
			return n, actions.InvalidParams("the website's top-level folders cannot be deleted (" + c + ")")
		}
		k, err := s.removeAll(c, 0)
		n += k
		if err != nil {
			return n, friendly(err)
		}
	}
	return n, nil
}

func (s *Site) removeAll(p string, depth int) (int, error) {
	if depth > 64 {
		return 0, errors.New("folder structure is too deep")
	}
	info, err := s.root.Lstat(p)
	if err != nil {
		return 0, err
	}
	n := 0
	if info.IsDir() {
		d, err := s.root.Open(p)
		if err != nil {
			return 0, err
		}
		names, err := d.Readdirnames(-1)
		d.Close()
		if err != nil {
			return 0, err
		}
		for _, name := range names {
			k, err := s.removeAll(path.Join(p, name), depth+1)
			n += k
			if err != nil {
				return n, err
			}
		}
	}
	if err := s.root.Remove(p); err != nil {
		return n, err
	}
	return n + 1, nil
}

// Copy duplicates a file or folder (symbolic links are skipped).
func (s *Site) Copy(from, to string) (int, error) {
	from, err := clean(from)
	if err != nil {
		return 0, err
	}
	to, err = clean(to)
	if err != nil {
		return 0, err
	}
	if _, err := s.root.Lstat(to); err == nil {
		return 0, actions.Conflict("A file or folder named " + to + " already exists.")
	}
	if strings.HasPrefix(to+"/", from+"/") {
		return 0, actions.InvalidParams("a folder cannot be copied into itself")
	}
	count := 0
	n, err := s.copy(from, to, 0, &count)
	return n, friendly(err)
}

func (s *Site) copy(from, to string, depth int, count *int) (int, error) {
	if depth > 64 || *count > 50000 {
		return 0, errors.New("too many files to copy at once")
	}
	*count++
	info, err := s.root.Lstat(from)
	if err != nil {
		return 0, err
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return 0, nil
	case info.IsDir():
		if err := s.root.Mkdir(to, info.Mode().Perm()); err != nil {
			return 0, err
		}
		if d, err := s.root.Open(to); err == nil {
			d.Chown(s.uid, s.gid)
			d.Close()
		}
		d, err := s.root.Open(from)
		if err != nil {
			return 0, err
		}
		names, err := d.Readdirnames(-1)
		d.Close()
		if err != nil {
			return 0, err
		}
		n := 1
		for _, name := range names {
			k, err := s.copy(path.Join(from, name), path.Join(to, name), depth+1, count)
			n += k
			if err != nil {
				return n, err
			}
		}
		return n, nil
	default:
		src, err := s.root.Open(from)
		if err != nil {
			return 0, err
		}
		defer src.Close()
		dst, err := s.root.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return 0, err
		}
		defer dst.Close()
		if _, err := io.Copy(dst, src); err != nil {
			return 0, err
		}
		return 1, dst.Chown(s.uid, s.gid)
	}
}

// Chmod sets permission bits (no setuid/setgid/sticky).
func (s *Site) Chmod(p, mode string) error {
	p, err := clean(p)
	if err != nil {
		return err
	}
	m, err := strconv.ParseUint(mode, 8, 32)
	if err != nil || m > 0o777 {
		return actions.InvalidParams("mode must be an octal permission between 000 and 777")
	}
	if info, err := s.root.Lstat(p); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return actions.InvalidParams("permissions of symbolic links cannot be changed")
	}
	f, err := s.root.Open(p)
	if err != nil {
		return friendly(err)
	}
	defer f.Close()
	return friendly(f.Chmod(fs.FileMode(m)))
}

// Hit is a search result.
type Hit struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

// Search finds entries whose name contains query (case-insensitive).
func (s *Site) Search(p, query string) ([]Hit, bool, error) {
	p, err := clean(p)
	if err != nil {
		return nil, false, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" || len(q) > 100 {
		return nil, false, actions.InvalidParams("query: 1–100 characters")
	}
	var hits []Hit
	visited := 0
	truncated := false
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > 32 || truncated {
			return
		}
		d, err := s.root.Open(dir)
		if err != nil {
			return
		}
		items, _ := d.ReadDir(-1)
		d.Close()
		for _, it := range items {
			visited++
			if visited > 100000 || len(hits) >= 500 {
				truncated = true
				return
			}
			rel := path.Join(dir, it.Name())
			if strings.Contains(strings.ToLower(it.Name()), q) {
				info, _ := it.Info()
				e := entryOf(it.Name(), info)
				hits = append(hits, Hit{Path: strings.TrimPrefix(rel, "./"), Type: e.Type, Size: e.Size})
			}
			if it.IsDir() && it.Type()&fs.ModeSymlink == 0 {
				walk(rel, depth+1)
			}
		}
	}
	walk(p, 0)
	if hits == nil {
		hits = []Hit{}
	}
	return hits, truncated, nil
}

// Upload writes one chunk. Chunks are appended to a hidden temporary file
// that replaces the target when the last chunk arrives.
func (s *Site) Upload(p string, offset int64, data []byte, final bool) (int64, error) {
	p, err := clean(p)
	if err != nil {
		return 0, err
	}
	if len(data) > maxChunk {
		return 0, actions.InvalidParams("chunk is too large")
	}
	tmp := path.Join(path.Dir(p), ".whacp-upload-"+path.Base(p))
	flags := os.O_WRONLY | os.O_CREATE
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	f, err := s.root.OpenFile(tmp, flags, 0o600)
	if err != nil {
		return 0, friendly(err)
	}
	info, _ := f.Stat()
	if info.Size() != offset {
		f.Close()
		return info.Size(), actions.Conflict("upload offset mismatch; restart the upload")
	}
	if _, err := f.WriteAt(data, offset); err != nil {
		f.Close()
		return 0, friendly(err)
	}
	size := offset + int64(len(data))
	if !final {
		f.Close()
		return size, nil
	}
	f.Chown(s.uid, s.gid)
	f.Chmod(0o644)
	f.Sync()
	f.Close()
	if info, err := s.root.Lstat(p); err == nil && info.IsDir() {
		s.root.Remove(tmp)
		return 0, actions.Conflict("A folder with that name already exists.")
	}
	return size, friendly(s.rename(tmp, p))
}

// OpenRead opens a regular file of the website for reading, confined to
// the website root like every other operation (a symlink cannot lead
// outside it).
func (s *Site) OpenRead(p string) (*os.File, error) {
	p, err := clean(p)
	if err != nil {
		return nil, err
	}
	f, err := s.root.Open(p)
	if err != nil {
		return nil, friendly(err)
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, actions.InvalidParams("path: not a file")
	}
	return f, nil
}

// RemoveFile removes one file of the website (not a folder).
func (s *Site) RemoveFile(p string) error {
	p, err := clean(p)
	if err != nil {
		return err
	}
	if info, err := s.root.Lstat(p); err != nil || info.IsDir() {
		return actions.InvalidParams("path: not a file")
	}
	return friendly(s.root.Remove(p))
}

// Download reads one chunk.
func (s *Site) Download(p string, offset int64) ([]byte, int64, error) {
	p, err := clean(p)
	if err != nil {
		return nil, 0, err
	}
	f, err := s.root.Open(p)
	if err != nil {
		return nil, 0, friendly(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return nil, 0, actions.InvalidParams("path: not a file")
	}
	buf := make([]byte, maxChunk)
	n, err := f.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, friendly(err)
	}
	return buf[:n], info.Size(), nil
}

// Register adds the file manager actions.
func Register(r *actions.Registry, env *host.Env) {
	with := func(p json.RawMessage, v any, fn func(s *Site) (any, error)) (any, error) {
		if err := host.Decode(p, v); err != nil {
			return nil, err
		}
		var domain string
		b, _ := json.Marshal(v)
		var d struct {
			Domain string `json:"domain"`
		}
		json.Unmarshal(b, &d)
		domain = d.Domain
		s, err := Open(env, domain)
		if err != nil {
			return nil, err
		}
		defer s.Close()
		return fn(s)
	}
	type base struct {
		Domain string `json:"domain"`
		Path   string `json:"path"`
	}
	r.Register(actions.Action{Name: "files.list", Description: "List a folder of a website.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in base
		return with(p, &in, func(s *Site) (any, error) { return s.List(in.Path) })
	}})
	r.Register(actions.Action{Name: "files.read", Description: "Open a text file of a website.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in base
		return with(p, &in, func(s *Site) (any, error) { return s.Read(in.Path) })
	}})
	r.Register(actions.Action{Name: "files.download", Description: "Read a chunk of a website file (base64).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
			Path   string `json:"path"`
			Offset int64  `json:"offset"`
		}
		return with(p, &in, func(s *Site) (any, error) {
			b, size, err := s.Download(in.Path, in.Offset)
			if err != nil {
				return nil, err
			}
			return map[string]any{"data": base64.StdEncoding.EncodeToString(b), "size": size, "eof": in.Offset+int64(len(b)) >= size}, nil
		})
	}})
	r.Register(actions.Action{Name: "files.search", Description: "Find files by name in a website.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
			Path   string `json:"path"`
			Query  string `json:"query"`
		}
		return with(p, &in, func(s *Site) (any, error) {
			hits, trunc, err := s.Search(in.Path, in.Query)
			return map[string]any{"results": hits, "truncated": trunc}, err
		})
	}})
	r.Register(actions.Action{Name: "files.write", Mutating: true, Description: "Save a text file.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain   string    `json:"domain"`
			Path     string    `json:"path"`
			Content  string    `json:"content"`
			Expected time.Time `json:"expected_mtime"`
		}
		return with(p, &in, func(s *Site) (any, error) { return s.Write(in.Path, in.Content, in.Expected) })
	}})
	r.Register(actions.Action{Name: "files.upload", Mutating: true, Description: "Upload a file chunk (base64).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
			Path   string `json:"path"`
			Offset int64  `json:"offset"`
			Data   string `json:"data"`
			Final  bool   `json:"final"`
		}
		return with(p, &in, func(s *Site) (any, error) {
			b, err := base64.StdEncoding.DecodeString(in.Data)
			if err != nil {
				return nil, actions.InvalidParams("data: invalid base64")
			}
			size, err := s.Upload(in.Path, in.Offset, b, in.Final)
			return map[string]any{"size": size}, err
		})
	}})
	r.Register(actions.Action{Name: "files.mkdir", Mutating: true, Description: "Create a folder.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in base
		return with(p, &in, func(s *Site) (any, error) { return nil, s.Mkdir(in.Path) })
	}})
	r.Register(actions.Action{Name: "files.rename", Mutating: true, Description: "Rename or move a file or folder.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
			From   string `json:"from"`
			To     string `json:"to"`
		}
		return with(p, &in, func(s *Site) (any, error) { return nil, s.Rename(in.From, in.To) })
	}})
	r.Register(actions.Action{Name: "files.copy", Mutating: true, Description: "Copy a file or folder.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
			From   string `json:"from"`
			To     string `json:"to"`
		}
		return with(p, &in, func(s *Site) (any, error) {
			n, err := s.Copy(in.From, in.To)
			return map[string]any{"copied": n}, err
		})
	}})
	r.Register(actions.Action{Name: "files.delete", Mutating: true, Description: "Delete files or folders: moved to the website's trash (restorable), or removed for good with permanent.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain    string   `json:"domain"`
			Paths     []string `json:"paths"`
			Permanent bool     `json:"permanent"`
		}
		return with(p, &in, func(s *Site) (any, error) {
			if len(in.Paths) == 0 || len(in.Paths) > 500 {
				return nil, actions.InvalidParams("paths: 1–500 entries")
			}
			if in.Permanent {
				n, err := s.Delete(in.Paths)
				return map[string]any{"deleted": n, "trashed": false}, err
			}
			n, err := s.Trash(in.Paths, env.Now())
			return map[string]any{"deleted": n, "trashed": true}, err
		})
	}})
	type ids struct {
		Domain string   `json:"domain"`
		IDs    []string `json:"ids"`
	}
	checkIDs := func(v []string) error {
		if len(v) == 0 || len(v) > 500 {
			return actions.InvalidParams("ids: 1–500 entries")
		}
		return nil
	}
	r.Register(actions.Action{Name: "files.trash_list", Description: "List a website's trash: deleted files and folders, where they were and when.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
		}
		return with(p, &in, func(s *Site) (any, error) {
			items, err := s.TrashList()
			return map[string]any{"items": items}, err
		})
	}})
	r.Register(actions.Action{Name: "files.trash_restore", Mutating: true, Description: "Put trash items back where they were (under a \"(restored)\" name when that one is taken).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in ids
		return with(p, &in, func(s *Site) (any, error) {
			if err := checkIDs(in.IDs); err != nil {
				return nil, err
			}
			done, err := s.TrashRestore(in.IDs)
			return map[string]any{"restored": done}, err
		})
	}})
	r.Register(actions.Action{Name: "files.trash_delete", Mutating: true, Description: "Delete trash items permanently.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in ids
		return with(p, &in, func(s *Site) (any, error) {
			if err := checkIDs(in.IDs); err != nil {
				return nil, err
			}
			n, err := s.TrashDelete(in.IDs)
			return map[string]any{"deleted": n}, err
		})
	}})
	r.Register(actions.Action{Name: "files.trash_empty", Mutating: true, Description: "Empty a website's trash, or only what was deleted more than older_than_days days ago.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain        string `json:"domain"`
			OlderThanDays int    `json:"older_than_days"`
		}
		return with(p, &in, func(s *Site) (any, error) {
			if in.OlderThanDays < 0 || in.OlderThanDays > 3650 {
				return nil, actions.InvalidParams("older_than_days: 0 (everything) to 3650")
			}
			var cutoff time.Time
			if in.OlderThanDays > 0 {
				cutoff = env.Now().Add(-time.Duration(in.OlderThanDays) * 24 * time.Hour)
			}
			n, err := s.TrashEmpty(cutoff)
			return map[string]any{"deleted": n}, err
		})
	}})
	// Archives can be large, so they run as jobs; the site is opened inside.
	job := func(name, domain string, fn func(ctx context.Context, s *Site, log io.Writer) (any, error)) (any, error) {
		if err := validate.Domain("domain", domain); err != nil {
			return nil, host.Invalid(err)
		}
		// Fail fast when the website does not exist.
		s, err := Open(env, domain)
		if err != nil {
			return nil, err
		}
		s.Close()
		j := env.Jobs.Start(name, "site:"+domain, func(ctx context.Context, log io.Writer) (any, error) {
			s, err := Open(env, domain)
			if err != nil {
				return nil, err
			}
			defer s.Close()
			return fn(ctx, s, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}
	r.Register(actions.Action{Name: "files.compress", Mutating: true, Description: "Create a .zip or .tar.gz of files and folders of a website (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string   `json:"domain"`
			Paths  []string `json:"paths"`
			Dest   string   `json:"dest"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if len(in.Paths) == 0 || len(in.Paths) > 500 {
			return nil, actions.InvalidParams("paths: 1–500 entries")
		}
		return job("files.compress", in.Domain, func(ctx context.Context, s *Site, log io.Writer) (any, error) {
			n, err := s.Compress(ctx, in.Paths, in.Dest, log)
			return map[string]any{"entries": n, "archive": in.Dest}, err
		})
	}})
	r.Register(actions.Action{Name: "files.extract", Mutating: true, Description: "Extract a .zip, .tar or .tar.gz inside a website (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain    string `json:"domain"`
			Path      string `json:"path"`
			Dest      string `json:"dest"`
			Overwrite bool   `json:"overwrite"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return job("files.extract", in.Domain, func(ctx context.Context, s *Site, log io.Writer) (any, error) {
			return s.Extract(ctx, in.Path, in.Dest, in.Overwrite, log)
		})
	}})
	r.Register(actions.Action{Name: "files.chmod", Mutating: true, Description: "Change permissions of a file or folder.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
			Path   string `json:"path"`
			Mode   string `json:"mode"`
		}
		return with(p, &in, func(s *Site) (any, error) { return nil, s.Chmod(in.Path, in.Mode) })
	}})
}
