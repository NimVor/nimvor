package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// Disk usage by folder of a website (a background job): the size of each
// folder directly inside the website folder (or a folder below it), the
// files lying there, and at the top the domain's mailboxes.

// DUEntry is one folder of the result.
type DUEntry struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"` // dir, files (the files directly in the folder), mail
	Bytes int64  `json:"bytes"`
	Files int64  `json:"files"`
}

// DUResult is a job's result.
type DUResult struct {
	Path      string    `json:"path"`
	Entries   []DUEntry `json:"entries"`
	Bytes     int64     `json:"bytes"`
	Partial   bool      `json:"partial,omitempty"`
	Truncated int       `json:"truncated,omitempty"` // folders beyond the listed ones (counted in the total)
	ScannedAt time.Time `json:"scanned_at"`
}

const duMaxEntries = 300

// cleanRel validates a folder path inside a website folder.
func cleanRel(p string) (string, error) {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return "", nil
	}
	if len(p) > 1024 || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("invalid path")
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid path")
		}
	}
	return p, nil
}

// DiskUsage measures the folders in dir. mailDir is added as "mail" when set.
func DiskUsage(ctx context.Context, dir, rel, mailDir string, log io.Writer) (DUResult, error) {
	res := DUResult{Path: rel, Entries: []DUEntry{}}
	items, err := os.ReadDir(dir)
	if err != nil {
		return res, err
	}
	loose := DUEntry{Name: "", Kind: "files"}
	for _, it := range items {
		if ctx.Err() != nil {
			res.Partial = true
			break
		}
		switch {
		case it.IsDir():
			fmt.Fprintf(log, "measuring %s\n", filepath.Join(rel, it.Name()))
			b, f, partial, _ := Measure(ctx, filepath.Join(dir, it.Name()))
			res.Entries = append(res.Entries, DUEntry{Name: it.Name(), Kind: "dir", Bytes: b, Files: f})
			res.Partial = res.Partial || partial
		case it.Type().IsRegular():
			if info, err := it.Info(); err == nil {
				loose.Bytes += info.Size()
				loose.Files++
			}
		}
		// Symbolic links and special files are not followed or counted.
	}
	if loose.Files > 0 {
		res.Entries = append(res.Entries, loose)
	}
	if mailDir != "" {
		if info, err := os.Lstat(mailDir); err == nil && info.IsDir() {
			fmt.Fprintln(log, "measuring the mailboxes")
			b, f, partial, _ := Measure(ctx, mailDir)
			res.Entries = append(res.Entries, DUEntry{Name: "mail", Kind: "mail", Bytes: b, Files: f})
			res.Partial = res.Partial || partial
		}
	}
	sort.SliceStable(res.Entries, func(i, j int) bool { return res.Entries[i].Bytes > res.Entries[j].Bytes })
	for _, e := range res.Entries {
		res.Bytes += e.Bytes
	}
	if len(res.Entries) > duMaxEntries {
		res.Truncated = len(res.Entries) - duMaxEntries
		res.Entries = res.Entries[:duMaxEntries]
	}
	res.ScannedAt = time.Now().UTC()
	return res, nil
}

func registerDU(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "sites.disk_usage", Description: "Start a background job measuring the folders of a website folder (or a folder below it; the top level also counts the domain's mailboxes). Read-only.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
			Path   string `json:"path"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("domain", in.Domain); err != nil {
			return nil, err
		}
		rel, err := cleanRel(in.Path)
		if err != nil {
			return nil, actions.InvalidParams("path: a folder inside the website folder")
		}
		root, err := env.Root.Path(filepath.Join(env.Cfg.SitesRoot, in.Domain))
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(root, rel)
		// The website folder is not a link, and no link below it leads the
		// scan elsewhere (links above it, e.g. /home, are the system's).
		notFound := actions.NotFound("That folder does not exist in the website folder.")
		if info, err := os.Lstat(root); err != nil || !info.IsDir() {
			return nil, notFound
		}
		realRoot, err1 := filepath.EvalSymlinks(root)
		realDir, err2 := filepath.EvalSymlinks(dir)
		if err1 != nil || err2 != nil || realDir != filepath.Join(realRoot, rel) {
			return nil, notFound
		}
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
			return nil, notFound
		}
		mail := ""
		if rel == "" {
			if m, err := env.Root.Path(filepath.Join(env.M.VmailDir, in.Domain)); err == nil {
				mail = m
			}
		}
		j := env.Jobs.Start("sites.disk_usage", "site:"+in.Domain, func(ctx context.Context, log io.Writer) (any, error) {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			defer cancel()
			return DiskUsage(ctx, dir, rel, mail, log)
		})
		return map[string]any{"job_id": j.ID, "status": j.Status}, nil
	}})
}
