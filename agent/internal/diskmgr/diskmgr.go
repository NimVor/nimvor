// Package diskmgr shows where a server's disk space goes (filesystems,
// the places that fill them, each website's share, the biggest files) and
// removes big files the administrator picks, from a short list of places.
package diskmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// FS is one mounted filesystem.
type FS struct {
	Mount        string  `json:"mount"`
	Device       string  `json:"device"`
	Type         string  `json:"type"`
	Total        uint64  `json:"total_bytes"`
	Used         uint64  `json:"used_bytes"`
	Avail        uint64  `json:"avail_bytes"`
	Percent      float64 `json:"used_percent"`
	Inodes       uint64  `json:"inodes"`
	InodesUsed   uint64  `json:"inodes_used"`
	InodePercent float64 `json:"inodes_percent"`
}

// realTypes are the filesystems that hold data (not proc, tmpfs, overlays…).
var realTypes = map[string]bool{"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true, "f2fs": true, "vfat": true, "ntfs": true, "exfat": true}

type mount struct{ device, point, fstype string }

func unescape(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}

func parseMounts(text string) []mount {
	var out []mount
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || !realTypes[f[2]] || seen[f[0]] {
			continue // other filesystem kinds, and bind mounts of a device already listed
		}
		seen[f[0]] = true
		out = append(out, mount{device: f[0], point: unescape(f[1]), fstype: f[2]})
	}
	return out
}

// Filesystems lists the mounted data filesystems with their space and inode use.
func Filesystems(env *host.Env) []FS {
	b, err := os.ReadFile(filepath.Join(env.Cfg.ProcDir, "mounts"))
	if err != nil {
		return []FS{}
	}
	out := []FS{}
	for _, m := range parseMounts(string(b)) {
		path, err := env.Root.Path(m.point)
		if err != nil {
			continue
		}
		var st syscall.Statfs_t
		if err := syscall.Statfs(path, &st); err != nil {
			continue
		}
		bs := uint64(st.Bsize)
		total := st.Blocks * bs
		avail := st.Bavail * bs
		used := total - st.Bfree*bs
		f := FS{Mount: m.point, Device: m.device, Type: m.fstype, Total: total, Used: used, Avail: avail, Inodes: st.Files, InodesUsed: st.Files - st.Ffree}
		if total > 0 {
			f.Percent = round1(float64(used) * 100 / float64(used+avail))
		}
		if st.Files > 0 {
			f.InodePercent = round1(float64(f.InodesUsed) * 100 / float64(st.Files))
		}
		out = append(out, f)
	}
	return out
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

// Area is a place on the disk that is measured as a whole.
type Area struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Path      string `json:"path"`
	Bytes     int64  `json:"bytes"`
	Files     int64  `json:"files"`
	Partial   bool   `json:"partial,omitempty"`
	Missing   bool   `json:"missing,omitempty"`
	Deletable bool   `json:"deletable"`
}

// SiteUse is one website's share of the websites folder.
type SiteUse struct {
	Domain string `json:"domain"`
	Bytes  int64  `json:"bytes"`
	Files  int64  `json:"files"`
}

// BigFile is one large file.
type BigFile struct {
	Path      string `json:"path"`
	Bytes     int64  `json:"bytes"`
	Modified  string `json:"modified"`
	Area      string `json:"area"`
	Site      string `json:"site,omitempty"`
	Deletable bool   `json:"deletable"`
}

// ScanResult is what a scan found.
type ScanResult struct {
	ScannedAt string    `json:"scanned_at"`
	Areas     []Area    `json:"areas"`
	Sites     []SiteUse `json:"sites"`
	Large     []BigFile `json:"large"`
	MinBytes  int64     `json:"min_bytes"`
	Partial   bool      `json:"partial,omitempty"`
}

const (
	maxLarge      = 200
	defaultMinMB  = 50
	scanTimeLimit = 25 * time.Minute
)

type areaDef struct {
	key, label, path string
	deletable        bool
	large            bool // its biggest files are listed
}

func areas(env *host.Env) []areaDef {
	return []areaDef{
		{"sites", "Websites", env.Cfg.SitesRoot, true, true},
		{"mail", "Mailboxes", env.M.VmailDir, false, true},
		{"backups", "NimVor backups", env.Cfg.BackupDir, true, true},
		{"logs", "System and service logs", "/var/log", true, true},
		{"mysql", "Databases (MariaDB)", "/var/lib/mysql", false, false},
		{"cache", "Package and system caches", "/var/cache", false, false},
		{"tmp", "Temporary files (/tmp)", "/tmp", true, true},
		{"vartmp", "Temporary files (/var/tmp)", "/var/tmp", true, true},
		{"state", "NimVor's own data", env.Cfg.StateDir, false, false},
	}
}

// Scan measures every area, each website and the biggest files. minBytes is
// the size from which a file is listed.
func Scan(ctx context.Context, env *host.Env, minBytes int64) ScanResult {
	res := ScanResult{ScannedAt: time.Now().UTC().Format(time.RFC3339), MinBytes: minBytes, Areas: []Area{}, Sites: []SiteUse{}, Large: []BigFile{}}
	sites := map[string]*SiteUse{}
	sitesRoot := filepath.Clean(env.Cfg.SitesRoot)
	var large []BigFile
	for _, def := range areas(env) {
		a := Area{Key: def.key, Label: def.label, Path: def.path, Deletable: def.deletable}
		real, err := env.Root.Path(def.path)
		if err != nil {
			a.Missing = true
			res.Areas = append(res.Areas, a)
			continue
		}
		if info, err := os.Lstat(real); err != nil || !info.IsDir() {
			a.Missing = true
			res.Areas = append(res.Areas, a)
			continue
		}
		filepath.WalkDir(real, func(p string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				a.Partial = true
				return ctx.Err()
			}
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			size := info.Size()
			a.Bytes += size
			a.Files++
			hostPath := env.Root.HostPath(p)
			site := ""
			if def.key == "sites" {
				if rel, err := filepath.Rel(sitesRoot, hostPath); err == nil {
					if first, _, ok := strings.Cut(rel, string(filepath.Separator)); ok {
						site = first
						u := sites[first]
						if u == nil {
							u = &SiteUse{Domain: first}
							sites[first] = u
						}
						u.Bytes += size
						u.Files++
					}
				}
			}
			if def.large && size >= minBytes {
				large = append(large, BigFile{Path: hostPath, Bytes: size, Modified: info.ModTime().UTC().Format(time.RFC3339), Area: def.key, Site: site, Deletable: def.deletable})
				if len(large) > maxLarge*4 {
					large = topLarge(large)
				}
			}
			return nil
		})
		if a.Partial {
			res.Partial = true
		}
		res.Areas = append(res.Areas, a)
	}
	for _, u := range sites {
		res.Sites = append(res.Sites, *u)
	}
	sort.Slice(res.Sites, func(i, j int) bool { return res.Sites[i].Bytes > res.Sites[j].Bytes })
	res.Large = topLarge(large)
	return res
}

func topLarge(l []BigFile) []BigFile {
	sort.Slice(l, func(i, j int) bool { return l[i].Bytes > l[j].Bytes })
	if len(l) > maxLarge {
		l = l[:maxLarge]
	}
	return l
}

// rotated matches log files that were rotated: removing them is safe.
var rotated = regexp.MustCompile(`(\.\d+|-\d{8}|\.old)?(\.gz|\.xz|\.bz2|\.zst)?$`)

func isRotated(name string) bool {
	m := rotated.FindString(name)
	return m != "" && m != name
}

// Removed is the outcome for one file.
type Removed struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Action string `json:"action,omitempty"` // deleted or emptied
	Error  string `json:"error,omitempty"`
}

// maxDelete is how many files one request may remove.
const maxDelete = 200

// deletableAreas are the places files may be removed from (a website's
// folder, NimVor's backups, logs and temporary files). Mailboxes, databases
// and system files are not on the list.
func deletableAreas(env *host.Env) map[string]string {
	return map[string]string{"sites": filepath.Clean(env.Cfg.SitesRoot), "backups": filepath.Clean(env.Cfg.BackupDir), "logs": "/var/log", "tmp": "/tmp", "vartmp": "/var/tmp"}
}

// Remove deletes the given files, each checked: an absolute path inside a
// deletable area (inside a website's own folder for websites), a regular
// file that is not a link, with no link in the folders leading to it. Active
// logs are emptied instead of removed, so their writer keeps working.
func Remove(env *host.Env, paths []string) []Removed {
	out := make([]Removed, 0, len(paths))
	areaRoots := deletableAreas(env)
	for _, p := range paths {
		r := Removed{Path: p}
		clean := filepath.Clean(p)
		if !filepath.IsAbs(p) || strings.ContainsRune(p, 0) || clean != p {
			r.Error = "not an absolute, clean path"
			out = append(out, r)
			continue
		}
		var root, key string
		for k, a := range areaRoots {
			if strings.HasPrefix(clean, a+"/") && len(a) > len(root) {
				root, key = a, k
			}
		}
		if root == "" {
			r.Error = "not in a place files may be removed from"
			out = append(out, r)
			continue
		}
		rel := strings.TrimPrefix(clean, root+"/")
		if key == "sites" && strings.Count(rel, "/") < 1 {
			r.Error = "not inside a website's folder"
			out = append(out, r)
			continue
		}
		real, err := env.Root.Path(clean)
		realRoot, err2 := env.Root.Path(root)
		if err != nil || err2 != nil {
			r.Error = "path not allowed"
			out = append(out, r)
			continue
		}
		info, err := os.Lstat(real)
		if err != nil || !info.Mode().IsRegular() {
			r.Error = "not a regular file (or it is gone)"
			out = append(out, r)
			continue
		}
		dir, err := filepath.EvalSymlinks(filepath.Dir(real))
		rootResolved, err2 := filepath.EvalSymlinks(realRoot)
		if err != nil || err2 != nil || (dir != rootResolved && !strings.HasPrefix(dir, rootResolved+string(filepath.Separator))) {
			r.Error = "a link leads out of the place the file is in"
			out = append(out, r)
			continue
		}
		r.Bytes = info.Size()
		if key == "logs" && !isRotated(filepath.Base(clean)) {
			if err := os.Truncate(real, 0); err != nil {
				r.Bytes, r.Error = 0, err.Error()
			} else {
				r.Action = "emptied"
			}
		} else if err := os.Remove(real); err != nil {
			r.Bytes, r.Error = 0, err.Error()
		} else {
			r.Action = "deleted"
		}
		out = append(out, r)
	}
	return out
}

// Register adds the disk actions.
func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "disk.filesystems", Description: "Mounted data filesystems with their space and inode use (read-only).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return Filesystems(env), nil
	}})
	r.Register(actions.Action{Name: "disk.scan", Description: "Start a background job measuring where the disk space goes: the big places, each website and the largest files (read-only).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			MinMB int `json:"min_mb"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.MinMB == 0 {
			in.MinMB = defaultMinMB
		}
		if in.MinMB < 1 || in.MinMB > 100000 {
			return nil, actions.InvalidParams("min_mb: between 1 and 100000")
		}
		j := env.Jobs.Start("disk.scan", "disk", func(ctx context.Context, log io.Writer) (any, error) {
			ctx, cancel := context.WithTimeout(ctx, scanTimeLimit)
			defer cancel()
			fmt.Fprintln(log, "Measuring the disk…")
			return Scan(ctx, env, int64(in.MinMB)*1024*1024), nil
		})
		return map[string]any{"job_id": j.ID, "status": j.Status}, nil
	}})
	r.Register(actions.Action{Name: "disk.remove", Mutating: true, Description: "Delete the given large files, from a website's folder, NimVor's backups, logs or temporary files only; active logs are emptied instead. Each path is checked; a file that is not allowed is reported and left alone.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Paths []string `json:"paths"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if len(in.Paths) == 0 || len(in.Paths) > maxDelete {
			return nil, actions.InvalidParams(fmt.Sprintf("paths: between 1 and %d files", maxDelete))
		}
		res := Remove(env, in.Paths)
		var freed int64
		for _, r := range res {
			freed += r.Bytes
		}
		return map[string]any{"results": res, "freed_bytes": freed}, nil
	}})
}
