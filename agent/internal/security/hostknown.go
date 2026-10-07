package security

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Scheduled tasks and services the administrator marked as known (cron
// files a hosting provider's image adds, for example). Each is pinned by
// its content: a known file that changes is reported again.

const hostKnownFile = "host-known.json"

func hostKnownPath(env *host.Env) (string, error) {
	if env.Cfg.StateDir == "" {
		return "", errors.New("no state folder")
	}
	return env.Root.Path(filepath.Join(env.Cfg.StateDir, hostKnownFile))
}

// loadKnown returns path → SHA-256 of the files marked as known.
func loadKnown(env *host.Env) map[string]string {
	known := map[string]string{}
	p, err := hostKnownPath(env)
	if err != nil {
		return known
	}
	if b, err := os.ReadFile(p); err == nil {
		json.Unmarshal(b, &known)
	}
	return known
}

// fileHash is the SHA-256 of a file's content ("" when it cannot be read).
func fileHash(env *host.Env, hostPath string) string {
	real, err := env.Root.Path(hostPath)
	if err != nil {
		return ""
	}
	info, err := os.Lstat(real)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	b, err := os.ReadFile(real)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// unknownPersist splits findings into those still to review and the number
// marked as known (unchanged since).
func unknownPersist(env *host.Env, list []Finding) ([]Finding, int) {
	if env == nil {
		return list, 0
	}
	known := loadKnown(env)
	var out []Finding
	n := 0
	for _, f := range list {
		if h, ok := known[f.Path]; ok && h != "" && h == fileHash(env, f.Path) {
			n++
			continue
		}
		out = append(out, f)
	}
	return out, n
}

// MarkPersistKnown records the scheduled tasks and services the latest scan
// lists as known (forget: clears the list). It returns the files recorded.
func MarkPersistKnown(env *host.Env, forget bool) ([]string, error) {
	p, err := hostKnownPath(env)
	if err != nil {
		return nil, err
	}
	if forget {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, nil
	}
	scanner.mu.Lock()
	var list []Finding
	if scanner.latest != nil {
		list = append(list, scanner.latest.Persist...)
	}
	scanner.mu.Unlock()
	pending, _ := unknownPersist(env, list)
	if len(pending) == 0 {
		return nil, fmt.Errorf("the latest host scan lists no scheduled task or service to mark")
	}
	known := loadKnown(env)
	var marked []string
	for _, f := range pending {
		if h := fileHash(env, f.Path); h != "" {
			known[f.Path] = h
			marked = append(marked, f.Path)
		}
	}
	if len(marked) == 0 {
		return nil, fmt.Errorf("none of the listed files could be read")
	}
	b, _ := json.MarshalIndent(known, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, p); err != nil {
		return nil, err
	}
	sort.Strings(marked)
	return marked, nil
}
