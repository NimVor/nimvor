package files

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
)

// The file manager's trash: deleting moves entries into the website's
// .whacp-trash folder (a rename on the same file system, so it is instant
// and keeps owners and modes), from where they can be restored until they
// are deleted permanently or the trash is emptied.
//
// Each deleted entry gets a folder .whacp-trash/<id>/ holding "item" (the
// entry itself) and "meta.json" (where it came from). Everything belongs
// to the website's account, like the rest of its folder: its files still
// count towards its disk quota, and the account's own backups and
// permission repair keep working. The file manager never lists or accepts
// names starting with .whacp-, and the website folder is not served (only
// its document root is). Nothing read back from the trash is trusted: ids
// and the recorded path are validated again before a restore.

// TrashDir is the trash folder inside a website folder.
const TrashDir = ".whacp-trash"

var trashIDRe = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`)

// TrashItem is one deleted entry.
type TrashItem struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"` // where it was, relative to the website folder
	Type      string    `json:"type"` // file, dir, link
	Size      int64     `json:"size"` // bytes (folders: their files, counted up to a limit)
	DeletedAt time.Time `json:"deleted_at"`
}

const (
	maxTrashList  = 2000
	maxSizeCount  = 50000 // entries counted for a folder's size
	maxMetaLength = 8192
)

// trashDir makes sure the trash folder is a real folder of the account.
func (s *Site) trashDir() error {
	info, err := s.root.Lstat(TrashDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := s.root.Mkdir(TrashDir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		d, err := s.root.Open(TrashDir)
		if err != nil {
			return err
		}
		defer d.Close()
		return d.Chown(s.uid, s.gid)
	case err != nil:
		return err
	case !info.IsDir():
		return actions.Conflict("The website folder has a " + TrashDir + " that is not a folder; remove it to use the trash.")
	}
	return nil
}

func newTrashID(now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}

// size adds up the bytes of the files in p (not following links), stopping
// after maxSizeCount entries.
func (s *Site) size(p string, info fs.FileInfo, count *int) int64 {
	*count++
	if !info.IsDir() || *count > maxSizeCount {
		if info.Mode().IsRegular() {
			return info.Size()
		}
		return 0
	}
	d, err := s.root.Open(p)
	if err != nil {
		return 0
	}
	items, err := d.Readdir(-1)
	d.Close()
	if err != nil {
		return 0
	}
	var n int64
	for _, it := range items {
		n += s.size(path.Join(p, it.Name()), it, count)
	}
	return n
}

// Trash moves entries into the trash.
func (s *Site) Trash(paths []string, now time.Time) (int, error) {
	if err := s.trashDir(); err != nil {
		return 0, friendly(err)
	}
	n := 0
	for _, p := range paths {
		c, err := clean(p)
		if err != nil {
			return n, err
		}
		if protected[c] {
			return n, actions.InvalidParams("the website's top-level folders cannot be deleted (" + c + ")")
		}
		info, err := s.root.Lstat(c)
		if err != nil {
			return n, friendly(err)
		}
		count := 0
		item := TrashItem{ID: newTrashID(now), Path: c, Type: entryOf(c, info).Type, Size: s.size(c, info, &count), DeletedAt: now.UTC()}
		dir := path.Join(TrashDir, item.ID)
		if err := s.root.Mkdir(dir, 0o700); err != nil {
			return n, friendly(err)
		}
		if err := s.chown(dir); err != nil {
			s.root.Remove(dir)
			return n, friendly(err)
		}
		meta, _ := json.Marshal(item)
		if err := s.atomicWrite(path.Join(dir, "meta.json"), strings.NewReader(string(meta)), 0o600); err != nil {
			s.removeAll(dir, 0)
			return n, friendly(err)
		}
		if err := s.rename(c, path.Join(dir, "item")); err != nil {
			s.removeAll(dir, 0)
			return n, friendly(err)
		}
		n++
	}
	return n, nil
}

func (s *Site) chown(p string) error {
	d, err := s.root.Open(p)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Chown(s.uid, s.gid)
}

// readItem reads a trash entry (nil when it is not a valid one).
func (s *Site) readItem(id string) *TrashItem {
	if !trashIDRe.MatchString(id) {
		return nil
	}
	dir := path.Join(TrashDir, id)
	if info, err := s.root.Lstat(dir); err != nil || !info.IsDir() {
		return nil
	}
	if _, err := s.root.Lstat(path.Join(dir, "item")); err != nil {
		return nil
	}
	f, err := s.root.OpenFile(path.Join(dir, "meta.json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxMetaLength))
	if err != nil {
		return nil
	}
	var it TrashItem
	if json.Unmarshal(b, &it) != nil {
		return nil
	}
	it.ID = id
	return &it
}

// TrashList returns the deleted entries, newest first.
func (s *Site) TrashList() ([]TrashItem, error) {
	out := []TrashItem{}
	d, err := s.root.Open(TrashDir)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, friendly(err)
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return nil, friendly(err)
	}
	for _, id := range names {
		if it := s.readItem(id); it != nil {
			out = append(out, *it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if len(out) > maxTrashList {
		out = out[:maxTrashList]
	}
	return out, nil
}

// mkdirParents creates the missing folders of p (owned by the account).
func (s *Site) mkdirParents(p string) error {
	dir := path.Dir(p)
	if dir == "." {
		return nil
	}
	cur := ""
	for _, part := range strings.Split(dir, "/") {
		cur = path.Join(cur, part)
		info, err := s.root.Lstat(cur)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := s.root.Mkdir(cur, 0o755); err != nil {
				return err
			}
			if err := s.chown(cur); err != nil {
				return err
			}
		case err != nil:
			return err
		case !info.IsDir():
			return actions.Conflict(cur + " is not a folder, so " + p + " cannot be restored there.")
		}
	}
	return nil
}

// freeName returns p, or "name (restored N).ext" when p is taken.
func (s *Site) freeName(p string) (string, error) {
	if _, err := s.root.Lstat(p); errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	dir, name := path.Dir(p), path.Base(p)
	ext := path.Ext(name)
	if ext == name || len(ext) > 10 {
		ext = ""
	}
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i <= 100; i++ {
		label := " (restored)"
		if i > 1 {
			label = fmt.Sprintf(" (restored %d)", i)
		}
		c := path.Join(dir, stem+label+ext)
		if _, err := s.root.Lstat(c); errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
	}
	return "", actions.Conflict("No free name to restore " + p + " to.")
}

// Restored is where a trash entry was put back.
type Restored struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// TrashRestore puts entries back where they were (under a new name when
// that one is taken; missing folders are created).
func (s *Site) TrashRestore(ids []string) ([]Restored, error) {
	out := []Restored{}
	for _, id := range ids {
		it := s.readItem(id)
		if it == nil {
			return out, actions.NotFound("That item is no longer in the trash.")
		}
		dest, err := clean(it.Path)
		if err != nil || strings.HasPrefix(it.Path, "/") || dest == "." || protected[dest] {
			return out, actions.Failed("The trash entry records an invalid place: "+it.Path, "")
		}
		if err := s.mkdirParents(dest); err != nil {
			return out, friendly(err)
		}
		dest, err = s.freeName(dest)
		if err != nil {
			return out, err
		}
		if err := s.rename(path.Join(TrashDir, id, "item"), dest); err != nil {
			return out, friendly(err)
		}
		s.removeAll(path.Join(TrashDir, id), 0)
		out = append(out, Restored{ID: id, Path: dest})
	}
	return out, nil
}

// TrashDelete deletes trash entries permanently.
func (s *Site) TrashDelete(ids []string) (int, error) {
	n := 0
	for _, id := range ids {
		if !trashIDRe.MatchString(id) {
			return n, actions.InvalidParams("ids: not a trash item")
		}
		if _, err := s.removeAll(path.Join(TrashDir, id), 0); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return n, friendly(err)
		}
		n++
	}
	return n, nil
}

// TrashEmpty deletes the entries deleted before cutoff (all when zero).
func (s *Site) TrashEmpty(cutoff time.Time) (int, error) {
	items, err := s.TrashList()
	if err != nil {
		return 0, err
	}
	var ids []string
	for _, it := range items {
		if cutoff.IsZero() || it.DeletedAt.Before(cutoff) {
			ids = append(ids, it.ID)
		}
	}
	return s.TrashDelete(ids)
}
