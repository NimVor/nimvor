package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
)

// Archive limits protect the server from archive bombs.
const (
	maxArchiveEntries = 50000
	maxArchiveBytes   = 8 << 30 // total uncompressed size
)

// ArchiveFormat reports the format of a file name ("" when unsupported).
func ArchiveFormat(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".zip"):
		return "zip"
	case strings.HasSuffix(n, ".tar.gz"), strings.HasSuffix(n, ".tgz"):
		return "tar.gz"
	case strings.HasSuffix(n, ".tar"):
		return "tar"
	}
	return ""
}

// Compress writes the given entries into a new archive at dest (zip or
// tar.gz by extension). Symbolic links are skipped, never followed.
func (s *Site) Compress(ctx context.Context, paths []string, dest string, log io.Writer) (int, error) {
	dest, err := clean(dest)
	if err != nil {
		return 0, err
	}
	format := ArchiveFormat(dest)
	if format == "" || format == "tar" {
		return 0, actions.InvalidParams("dest: must end in .zip or .tar.gz")
	}
	if _, err := s.root.Lstat(dest); err == nil {
		return 0, actions.Conflict("A file named " + dest + " already exists.")
	}
	var srcs []string
	for _, p := range paths {
		c, err := clean(p)
		if err != nil {
			return 0, err
		}
		if c == "." {
			return 0, actions.InvalidParams("paths: choose files or folders inside the website, not its root")
		}
		if strings.HasPrefix(dest+"/", c+"/") {
			return 0, actions.InvalidParams("the archive cannot be created inside a folder being compressed")
		}
		srcs = append(srcs, c)
	}
	if len(srcs) == 0 {
		return 0, actions.InvalidParams("paths: nothing to compress")
	}
	base := path.Dir(dest)
	tmp := path.Join(base, fmt.Sprintf(".whacp-tmp-%d", time.Now().UnixNano()))
	f, err := s.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, friendly(err)
	}
	ok := false
	defer func() {
		if !ok {
			s.root.Remove(tmp)
		}
	}()
	n := 0
	var zw *zip.Writer
	var tw *tar.Writer
	var gz *gzip.Writer
	if format == "zip" {
		zw = zip.NewWriter(f)
	} else {
		gz = gzip.NewWriter(f)
		tw = tar.NewWriter(gz)
	}
	add := func(rel string, info fs.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n++; n > maxArchiveEntries {
			return actions.InvalidParams(fmt.Sprintf("more than %d entries; compress smaller folders", maxArchiveEntries))
		}
		// Names inside the archive are relative to the folder of dest.
		name := strings.TrimPrefix(strings.TrimPrefix(rel, base+"/"), "./")
		if base == "." {
			name = rel
		}
		if info.IsDir() {
			name += "/"
		}
		if zw != nil {
			h, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			h.Name = name
			if !info.IsDir() {
				h.Method = zip.Deflate
			}
			w, err := zw.CreateHeader(h)
			if err != nil || info.IsDir() {
				return err
			}
			return s.copyInto(w, rel)
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name, h.Uname, h.Gname = name, "", ""
		if err := tw.WriteHeader(h); err != nil || info.IsDir() {
			return err
		}
		return s.copyInto(tw, rel)
	}
	var walk func(rel string, depth int) error
	walk = func(rel string, depth int) error {
		if depth > 64 {
			return errors.New("folder structure is too deep")
		}
		info, err := s.root.Lstat(rel)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0, !info.IsDir() && !info.Mode().IsRegular():
			return nil
		case strings.HasPrefix(path.Base(rel), ".whacp-"):
			return nil
		}
		if err := add(rel, info); err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		d, err := s.root.Open(rel)
		if err != nil {
			return err
		}
		names, err := d.Readdirnames(-1)
		d.Close()
		if err != nil {
			return err
		}
		for _, name := range names {
			if err := walk(path.Join(rel, name), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, src := range srcs {
		fmt.Fprintf(log, "Adding %s\n", src)
		if err := walk(src, 0); err != nil {
			f.Close()
			return n, friendly(err)
		}
	}
	if zw != nil {
		err = zw.Close()
	} else if err = tw.Close(); err == nil {
		err = gz.Close()
	}
	if err == nil {
		err = f.Chown(s.uid, s.gid)
	}
	if err == nil {
		err = f.Chmod(0o644)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, friendly(err)
	}
	if err := s.rename(tmp, dest); err != nil {
		return n, friendly(err)
	}
	ok = true
	fmt.Fprintf(log, "Created %s with %d entries.\n", dest, n)
	return n, nil
}

func (s *Site) copyInto(w io.Writer, rel string) error {
	f, err := s.root.Open(rel)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// ExtractResult summarises an extraction.
type ExtractResult struct {
	Files    int      `json:"files"`
	Folders  int      `json:"folders"`
	Skipped  []string `json:"skipped"`
	Existing []string `json:"existing"`
}

// Extract unpacks a zip, tar or tar.gz archive into dest. Entries must stay
// inside dest; links, devices and other special entries are skipped; set-id
// bits are dropped; existing files are only replaced with overwrite. The
// number of entries and the total size are limited.
func (s *Site) Extract(ctx context.Context, archive, dest string, overwrite bool, log io.Writer) (*ExtractResult, error) {
	archive, err := clean(archive)
	if err != nil {
		return nil, err
	}
	dest, err = clean(dest)
	if err != nil {
		return nil, err
	}
	format := ArchiveFormat(archive)
	if format == "" {
		return nil, actions.InvalidParams("archive: supported formats are .zip, .tar and .tar.gz")
	}
	if info, err := s.root.Stat(dest); err != nil || !info.IsDir() {
		return nil, actions.InvalidParams("dest: must be an existing folder")
	}
	res := &ExtractResult{Skipped: []string{}, Existing: []string{}}
	var total int64
	count := 0
	write := func(name string, mode fs.FileMode, dir bool, r io.Reader, size int64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if count++; count > maxArchiveEntries {
			return actions.InvalidParams(fmt.Sprintf("the archive has more than %d entries", maxArchiveEntries))
		}
		clean := path.Clean(strings.TrimPrefix(strings.ReplaceAll(name, "\\", "/"), "./"))
		if clean == "." || strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || strings.HasPrefix(path.Base(clean), ".whacp-") {
			res.Skipped = append(res.Skipped, name)
			return nil
		}
		target := path.Join(dest, clean)
		if protected[target] && !dir {
			res.Skipped = append(res.Skipped, name)
			return nil
		}
		if err := s.mkdirAll(path.Dir(target)); err != nil {
			return err
		}
		if dir {
			if err := s.mkdirAll(target); err != nil {
				return err
			}
			res.Folders++
			return nil
		}
		if info, err := s.root.Lstat(target); err == nil {
			if info.IsDir() || info.Mode()&fs.ModeSymlink != 0 || !overwrite {
				res.Existing = append(res.Existing, clean)
				return nil
			}
		}
		if size < 0 || total+size > maxArchiveBytes {
			return actions.InvalidParams("the archive expands to more than 8 GiB")
		}
		// Count what is actually written, not what the header claims.
		lr := &io.LimitedReader{R: r, N: maxArchiveBytes - total + 1}
		if err := s.atomicWrite(target, lr, mode&0o777); err != nil {
			return err
		}
		written := (maxArchiveBytes - total + 1) - lr.N
		if total += written; total > maxArchiveBytes {
			s.root.Remove(target)
			return actions.InvalidParams("the archive expands to more than 8 GiB")
		}
		res.Files++
		return nil
	}
	f, err := s.root.Open(archive)
	if err != nil {
		return nil, friendly(err)
	}
	defer f.Close()
	fmt.Fprintf(log, "Extracting %s into %s…\n", archive, dest)
	switch format {
	case "zip":
		info, err := f.Stat()
		if err != nil {
			return nil, friendly(err)
		}
		zr, err := zip.NewReader(f, info.Size())
		if err != nil {
			return nil, actions.InvalidParams("archive: not a valid zip file")
		}
		for _, zf := range zr.File {
			mode := zf.Mode()
			if mode&fs.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
				res.Skipped = append(res.Skipped, zf.Name)
				continue
			}
			if mode.IsDir() || strings.HasSuffix(zf.Name, "/") {
				if err := write(zf.Name, 0, true, nil, 0); err != nil {
					return res, friendly(err)
				}
				continue
			}
			rc, err := zf.Open()
			if err != nil {
				return res, actions.InvalidParams("archive: " + err.Error())
			}
			err = write(zf.Name, mode.Perm()|0o600, false, rc, int64(zf.UncompressedSize64))
			rc.Close()
			if err != nil {
				return res, friendly(err)
			}
		}
	default:
		var r io.Reader = f
		if format == "tar.gz" {
			gz, err := gzip.NewReader(f)
			if err != nil {
				return nil, actions.InvalidParams("archive: not a valid gzip file")
			}
			defer gz.Close()
			r = gz
		}
		tr := tar.NewReader(r)
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return res, actions.InvalidParams("archive: " + err.Error())
			}
			switch h.Typeflag {
			case tar.TypeDir:
				err = write(h.Name, 0, true, nil, 0)
			case tar.TypeReg:
				err = write(h.Name, fs.FileMode(h.Mode).Perm()|0o600, false, tr, h.Size)
			default:
				res.Skipped = append(res.Skipped, h.Name)
				continue
			}
			if err != nil {
				return res, friendly(err)
			}
		}
	}
	if len(res.Skipped) > 100 {
		res.Skipped = res.Skipped[:100]
	}
	if len(res.Existing) > 100 {
		res.Existing = res.Existing[:100]
	}
	fmt.Fprintf(log, "%d files and %d folders extracted, %d skipped, %d already existed.\n", res.Files, res.Folders, len(res.Skipped), len(res.Existing))
	return res, nil
}

// mkdirAll creates missing folders (owned by the site account) inside the
// root, refusing to pass through symbolic links.
func (s *Site) mkdirAll(p string) error {
	if p == "." || p == "" {
		return nil
	}
	cur := "."
	for _, part := range strings.Split(p, "/") {
		cur = path.Join(cur, part)
		info, err := s.root.Lstat(cur)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("%s exists and is not a folder", cur)
			}
			continue
		}
		if err := s.root.Mkdir(cur, 0o755); err != nil {
			return err
		}
		if d, err := s.root.Open(cur); err == nil {
			d.Chown(s.uid, s.gid)
			d.Close()
		}
	}
	return nil
}
