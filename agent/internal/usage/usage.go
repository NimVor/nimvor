// Package usage measures disk use of websites (read-only).
package usage

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// Result is the size of one website directory.
type Result struct {
	Domain   string `json:"domain"`
	Bytes    int64  `json:"bytes"`
	Files    int64  `json:"files"`
	Partial  bool   `json:"partial,omitempty"` // stopped by the time limit
	NotFound bool   `json:"not_found,omitempty"`
}

var errStop = errors.New("stop")

// Measure walks dir without following symlinks, counting allocated bytes
// of regular files, until ctx expires.
func Measure(ctx context.Context, dir string) (bytes, files int64, partial bool, err error) {
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			partial = true
			return errStop
		}
		if err != nil {
			return nil // unreadable entries are skipped
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				bytes += info.Size()
				files++
			}
		}
		return nil
	})
	if errors.Is(err, errStop) {
		err = nil
	}
	return
}

// Register adds sites.usage and sites.bandwidth.
func Register(r *actions.Registry, env *host.Env) {
	registerBandwidth(r, env)
	registerDU(r, env)
	r.Register(actions.Action{Name: "sites.usage", Timeout: 5 * time.Minute, Description: "Disk use of website directories (read-only).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domains []string `json:"domains"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if len(in.Domains) > 1000 {
			return nil, actions.InvalidParams("domains: at most 1000")
		}
		out := []Result{}
		deadline, cancel := context.WithTimeout(ctx, 4*time.Minute)
		defer cancel()
		for _, d := range in.Domains {
			if validate.Domain("domain", d) != nil {
				return nil, actions.InvalidParams("domains: invalid domain " + d)
			}
			real, err := env.Root.Path(filepath.Join(env.Cfg.SitesRoot, d))
			if err != nil {
				return nil, err
			}
			if info, err := os.Lstat(real); err != nil || !info.IsDir() {
				out = append(out, Result{Domain: d, NotFound: true})
				continue
			}
			per, cancelOne := context.WithTimeout(deadline, time.Minute)
			b, f, partial, _ := Measure(per, real)
			cancelOne()
			out = append(out, Result{Domain: d, Bytes: b, Files: f, Partial: partial})
		}
		return out, nil
	}})
}
