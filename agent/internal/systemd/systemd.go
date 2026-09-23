// Package systemd reads service state through `systemctl show`.
//
// Only read-only queries are implemented in this release.
package systemd

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
)

// Service is the observed state of one unit.
type Service struct {
	Unit          string     `json:"unit"`
	Description   string     `json:"description"`
	LoadState     string     `json:"load_state"`
	ActiveState   string     `json:"active_state"`
	SubState      string     `json:"sub_state"`
	UnitFileState string     `json:"unit_file_state"`
	MainPID       int        `json:"main_pid"`
	MemoryBytes   *uint64    `json:"memory_bytes"`
	ActiveSince   *time.Time `json:"active_since"`
	Running       bool       `json:"running"`
}

// unitName restricts unit names to a safe character set.
var unitName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9@._:-]{0,199}\.(service|timer|socket)$`)

// ValidUnit reports whether name is a syntactically safe unit name.
func ValidUnit(name string) bool { return unitName.MatchString(name) }

var showProps = "Id,Description,LoadState,ActiveState,SubState,UnitFileState,MainPID,MemoryCurrent,ActiveEnterTimestamp"

// Client queries systemd.
type Client struct {
	Runner execx.Runner
}

// Status returns the state of the given units. Units that are not loaded
// ("not-found") are returned with LoadState set so callers can filter them.
func (c Client) Status(ctx context.Context, units []string) ([]Service, error) {
	valid := make([]string, 0, len(units))
	for _, u := range units {
		if ValidUnit(u) {
			valid = append(valid, u)
		}
	}
	if len(valid) == 0 {
		return []Service{}, nil
	}
	args := append([]string{"show", "--no-pager", "--property=" + showProps, "--"}, valid...)
	out, err := c.Runner.Run(ctx, "systemctl", args...)
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("systemctl show: %w", err)
	}
	return ParseShow(string(out)), nil
}

// ParseShow parses `systemctl show` output (blank-line separated records).
func ParseShow(out string) []Service {
	var services []Service
	for _, block := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		kv := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			k, v, ok := strings.Cut(line, "=")
			if ok {
				kv[k] = v
			}
		}
		s := Service{
			Unit:          kv["Id"],
			Description:   kv["Description"],
			LoadState:     kv["LoadState"],
			ActiveState:   kv["ActiveState"],
			SubState:      kv["SubState"],
			UnitFileState: kv["UnitFileState"],
		}
		if s.Unit == "" {
			continue
		}
		s.MainPID, _ = strconv.Atoi(kv["MainPID"])
		if m, err := strconv.ParseUint(kv["MemoryCurrent"], 10, 64); err == nil {
			s.MemoryBytes = &m
		}
		if ts := strings.TrimSpace(kv["ActiveEnterTimestamp"]); ts != "" && ts != "n/a" {
			if t, err := parseTimestamp(ts); err == nil {
				s.ActiveSince = &t
			}
		}
		s.Running = s.ActiveState == "active"
		services = append(services, s)
	}
	return services
}

func parseTimestamp(ts string) (time.Time, error) {
	// e.g. "Tue 2026-09-22 10:11:12 UTC"
	layouts := []string{"Mon 2006-01-02 15:04:05 MST", "Mon 2006-01-02 15:04:05 -0700"}
	for _, l := range layouts {
		if t, err := time.Parse(l, ts); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised timestamp %q", ts)
}

// DiscoverUnits returns unit names matching patterns in the unit directories.
func DiscoverUnits(root filesystem.Root, dirs, patterns []string) []string {
	seen := map[string]bool{}
	for _, dir := range dirs {
		for _, pat := range patterns {
			matches, err := root.Glob(filepath.Join(dir, pat))
			if err != nil {
				continue
			}
			for _, m := range matches {
				name := filepath.Base(m)
				if ValidUnit(name) && !strings.Contains(name, "@") {
					seen[name] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
