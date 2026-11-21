package phpmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/platform"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// fpmLogTail is how much of the end of a PHP-FPM log is read.
const fpmLogTail = 512 << 10

// FPMHealth is what a PHP-FPM version's service and log say about its pools.
// Counts are nil when the log could not be read (never a made-up zero).
type FPMHealth struct {
	Version   string `json:"version"`
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
	LogRead   bool   `json:"log_read"`
	// Events in the last 24 hours, from the end of the FPM log.
	MaxChildren *int `json:"max_children_reached,omitempty"`
	Busy        *int `json:"busy_warnings,omitempty"`
	Crashes     *int `json:"child_crashes,omitempty"`
	// LastMaxChildren is the pool that last reached its limit.
	LastPool string `json:"last_max_children_pool,omitempty"`
}

var (
	// "[21-Nov-2026 21:00:00] WARNING: [pool site1] server reached pm.max_children setting (5), consider raising it"
	fpmLineRe  = regexp.MustCompile(`^\[(\d{2}-[A-Za-z]{3}-\d{4} \d{2}:\d{2}:\d{2})(?:\.\d+)?\] (\w+): (.*)$`)
	fpmPoolRe  = regexp.MustCompile(`\[pool ([^\]]+)\]`)
	fpmCrashRe = regexp.MustCompile(`exited on signal \d+|exited with code [1-9]`)
)

// ScanFPMLog counts the interesting events of the last 24 hours in the tail
// of a PHP-FPM log. Lines older than the window, or without a time stamp, are
// not counted.
func ScanFPMLog(log string, now time.Time) (maxChildren, busy, crashes int, lastPool string) {
	cutoff := now.Add(-24 * time.Hour)
	for _, line := range strings.Split(log, "\n") {
		m := fpmLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		t, err := time.ParseInLocation("02-Jan-2006 15:04:05", m[1], now.Location())
		if err != nil || t.Before(cutoff) {
			continue
		}
		msg := m[3]
		switch {
		case strings.Contains(msg, "reached pm.max_children") || strings.Contains(msg, "reached max_children"):
			maxChildren++
			if p := fpmPoolRe.FindStringSubmatch(msg); p != nil {
				lastPool = p[1]
			}
		case strings.Contains(msg, "seems busy"):
			busy++
		case fpmCrashRe.MatchString(msg):
			crashes++
		}
	}
	return
}

func tailFile(path string, n int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", os.ErrInvalid
	}
	if info.Size() > n {
		if _, err := f.Seek(info.Size()-n, io.SeekStart); err != nil {
			return "", err
		}
	}
	b, err := io.ReadAll(io.LimitReader(f, n))
	return string(b), err
}

// Health reads the service state and the log of one PHP version.
func Health(ctx context.Context, env *host.Env, v string) FPMHealth {
	h := FPMHealth{Version: v}
	h.Installed = env.Runner.Available(fmt.Sprintf(env.M.PHPFPMBinary, v))
	if !h.Installed {
		return h
	}
	h.Active = env.UnitActive(ctx, env.M.FPMUnit(v))
	logPath := fmt.Sprintf(env.Platform.Paths().PHPFPMLog, platform.PHPTag(env.M.PHPCompact, v))
	if logPath == "" {
		return h
	}
	real, err := env.Root.Path(logPath)
	if err != nil {
		return h
	}
	text, err := tailFile(real, fpmLogTail)
	if err != nil {
		return h
	}
	h.LogRead = true
	mc, busy, crashes, pool := ScanFPMLog(text, env.Now())
	h.MaxChildren, h.Busy, h.Crashes, h.LastPool = &mc, &busy, &crashes, pool
	return h
}

func registerHealth(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "php.fpm_health", Description: "PHP-FPM health per PHP version: installed, running, and in the last 24 hours how often a pool reached pm.max_children, was busy or had a worker crash (counted from the end of the FPM log; absent when the log cannot be read). Read-only.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Versions []string `json:"versions"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if len(in.Versions) > 12 {
			return nil, actions.InvalidParams("versions: at most 12")
		}
		out := make([]FPMHealth, 0, len(in.Versions))
		for _, v := range in.Versions {
			if err := validate.PHPVersion("versions", v); err != nil {
				return nil, host.Invalid(err)
			}
			out = append(out, Health(ctx, env, v))
		}
		return map[string]any{"versions": out}, nil
	}})
}
