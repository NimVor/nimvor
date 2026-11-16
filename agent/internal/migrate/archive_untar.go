package migrate

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// untarState counts what an extraction did, across the archive and the archives nested in it.
type untarState struct {
	files, skipped int
	written        int64
}

// untarInto writes the entries of a tar stream into dest. Every entry is confined to dest: absolute names
// and ".." are refused, links may only point inside it, devices and hard links are skipped, set-id bits are
// dropped, and it stops when more than quota bytes would be written.
func untarInto(ctx context.Context, tr *tar.Reader, dest string, quota int64, st *untarState, log io.Writer) error {
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("the archive is damaged: %v", err)
		}
		name := strings.TrimPrefix(filepath.ToSlash(h.Name), "./")
		if name == "" || name == "." {
			continue
		}
		if strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") || strings.HasSuffix(name, "/..") || len(name) > 4000 {
			return fmt.Errorf("the archive holds a path that leaves its folder (%q); it was not unpacked", truncate(name, 80))
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if !inside(dest, target) {
			return fmt.Errorf("the archive holds a path that leaves its folder; it was not unpacked")
		}
		st.files++
		if st.files > maxArchiveFiles {
			return fmt.Errorf("the archive holds more than %d entries", maxArchiveFiles)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if h.Size > quota-written {
				return fmt.Errorf("the disk would be full: stopped after %s (NimVor keeps %s free)", kb((st.written+written)/1024), kb(reserveKB))
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if real, err := filepath.EvalSymlinks(filepath.Dir(target)); err != nil || !inside(dest, real) {
				return fmt.Errorf("the archive writes through a link that leaves its folder; it was not unpacked")
			}
			mode := os.FileMode(h.Mode&0o755) | 0o600
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, mode)
			if err != nil {
				return err
			}
			n, err := io.CopyN(out, tr, h.Size)
			written += n
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return fmt.Errorf("a file could not be unpacked: %v", err)
			}
		case tar.TypeSymlink:
			link := filepath.ToSlash(h.Linkname)
			resolved := filepath.Join(filepath.Dir(target), filepath.FromSlash(link))
			if strings.HasPrefix(link, "/") || !inside(dest, resolved) {
				st.skipped++ // a link to somewhere else is never recreated
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			os.Remove(target)
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
		default:
			st.skipped++ // hard links, devices, fifos
		}
		if st.files%5000 == 0 && log != nil {
			fmt.Fprintf(log, "Unpacked %d entries, %s…\n", st.files, kb((st.written+written)/1024))
		}
	}
	st.written += written
	return nil
}

// nestedArchives are the inner archives of a HestiaCP or VestaCP backup: the website files and the
// mailboxes of each domain. Each is opened into a "data" folder next to it.
func nestedArchives(dest string) []string {
	var out []string
	for _, kind := range []struct{ dir, file string }{{"web", "domain_data"}, {"mail", "mail_data"}} {
		ents, err := os.ReadDir(filepath.Join(dest, kind.dir))
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			for _, ext := range []string{".tar.gz", ".tgz", ".tar"} {
				p := filepath.Join(dest, kind.dir, e.Name(), kind.file+ext)
				if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
					out = append(out, p)
					break
				}
			}
		}
	}
	return out
}

// expandNested opens the inner archives found by nestedArchives. quota is what may still be written.
func expandNested(ctx context.Context, dest string, quota int64, st *untarState, log io.Writer) error {
	inner := nestedArchives(dest)
	if len(inner) == 0 {
		return nil
	}
	if log != nil {
		fmt.Fprintf(log, "Opening %d archives inside the backup…\n", len(inner))
	}
	for _, p := range inner {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		var r io.Reader = bufio.NewReaderSize(f, 1<<20)
		var zr *gzip.Reader
		if !strings.HasSuffix(p, ".tar") {
			zr, err = gzip.NewReader(r)
			if err != nil {
				f.Close()
				return fmt.Errorf("%s is damaged: %v", strings.TrimPrefix(p, dest+"/"), err)
			}
			r = zr
		}
		data := filepath.Join(filepath.Dir(p), "data")
		if err := os.MkdirAll(data, 0o755); err != nil {
			f.Close()
			return err
		}
		before := st.written
		err = untarInto(ctx, tar.NewReader(r), data, quota, st, log)
		if zr != nil {
			zr.Close()
		}
		f.Close()
		if err != nil {
			return err
		}
		quota -= st.written - before
		os.Remove(p) // the copy inside the unpacked backup is not needed any more
	}
	return nil
}
