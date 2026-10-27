package phpmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// FPMGlobal are server-wide PHP-FPM settings of a version, written to
// WhaCP's own whacp-global.conf in the pool folder (php-fpm.conf includes
// it; an included [global] section applies like php-fpm.conf's own).
// Zero values are left out, so PHP-FPM's defaults apply.
type FPMGlobal struct {
	// Restart the master when this many children fail within the interval.
	EmergencyRestartThreshold int `json:"emergency_restart_threshold"`
	EmergencyRestartInterval  int `json:"emergency_restart_interval_seconds"`
	// Seconds children get to react to signals from the master.
	ProcessControlTimeout int `json:"process_control_timeout_seconds"`
	// Most processes across all pools of this version (0: unlimited).
	ProcessMax int    `json:"process_max"`
	LogLevel   string `json:"log_level"`
}

var logLevels = []string{"alert", "error", "warning", "notice", "debug"}

func (g FPMGlobal) check() error {
	for _, l := range []struct {
		field   string
		v, hi   int
		hasUnit bool
	}{
		{"emergency_restart_threshold", g.EmergencyRestartThreshold, 1000, false},
		{"emergency_restart_interval_seconds", g.EmergencyRestartInterval, 86400, true},
		{"process_control_timeout_seconds", g.ProcessControlTimeout, 3600, true},
		{"process_max", g.ProcessMax, 100000, false},
	} {
		if l.v < 0 || l.v > l.hi {
			return fmt.Errorf("%s must be between 0 and %d", l.field, l.hi)
		}
	}
	if (g.EmergencyRestartThreshold == 0) != (g.EmergencyRestartInterval == 0) {
		return fmt.Errorf("emergency restart needs both a threshold and an interval (or neither)")
	}
	if g.LogLevel != "" {
		ok := false
		for _, l := range logLevels {
			ok = ok || l == g.LogLevel
		}
		if !ok {
			return fmt.Errorf("log_level must be one of: %s", strings.Join(logLevels, ", "))
		}
	}
	return nil
}

// Render produces whacp-global.conf ("" when nothing is set).
func (g FPMGlobal) Render(v string) string {
	var lines []string
	if g.EmergencyRestartThreshold > 0 {
		lines = append(lines, "emergency_restart_threshold = "+strconv.Itoa(g.EmergencyRestartThreshold),
			fmt.Sprintf("emergency_restart_interval = %ds", g.EmergencyRestartInterval))
	}
	if g.ProcessControlTimeout > 0 {
		lines = append(lines, fmt.Sprintf("process_control_timeout = %ds", g.ProcessControlTimeout))
	}
	if g.ProcessMax > 0 {
		lines = append(lines, "process.max = "+strconv.Itoa(g.ProcessMax))
	}
	if g.LogLevel != "" {
		lines = append(lines, "log_level = "+g.LogLevel)
	}
	if len(lines) == 0 {
		return ""
	}
	return "; Managed by WhaCP (PHP → " + v + " → PHP-FPM). Do not edit: changes are overwritten.\n[global]\n" + strings.Join(lines, "\n") + "\n"
}

// ParseFPMGlobal reads back what Render wrote.
func ParseFPMGlobal(content string) FPMGlobal {
	var g FPMGlobal
	num := func(s string) int { n, _ := strconv.Atoi(strings.TrimSuffix(s, "s")); return n }
	for _, l := range strings.Split(content, "\n") {
		k, val, ok := strings.Cut(l, " = ")
		if !ok {
			continue
		}
		switch k {
		case "emergency_restart_threshold":
			g.EmergencyRestartThreshold = num(val)
		case "emergency_restart_interval":
			g.EmergencyRestartInterval = num(val)
		case "process_control_timeout":
			g.ProcessControlTimeout = num(val)
		case "process.max":
			g.ProcessMax = num(val)
		case "log_level":
			g.LogLevel = val
		}
	}
	return g
}

// FPMGlobalFile is WhaCP's global PHP-FPM file for version v.
func FPMGlobalFile(env *host.Env, v string) string {
	return filepath.Join(env.M.PoolDir(v), "whacp-global.conf")
}

func registerFPMGlobal(r *actions.Registry, env *host.Env) {
	type req struct {
		Version string `json:"version"`
		FPMGlobal
	}
	r.Register(actions.Action{Name: "php.fpm_get", Description: "Server-wide PHP-FPM settings WhaCP set for a PHP version (zero: PHP-FPM's default).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in req
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.PHPVersion("version", in.Version); err != nil {
			return nil, host.Invalid(err)
		}
		b, _ := env.Root.ReadFile(FPMGlobalFile(env, in.Version))
		return map[string]any{"file": FPMGlobalFile(env, in.Version), "settings": ParseFPMGlobal(string(b)), "log_levels": logLevels}, nil
	}})
	r.Register(actions.Action{Name: "php.fpm_set", Mutating: true, Description: "Write server-wide PHP-FPM settings for a PHP version (emergency restart, process control timeout, process.max, log level), checked with php-fpm -t and applied with a reload; rolled back on failure.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in req
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.PHPVersion("version", in.Version); err != nil {
			return nil, host.Invalid(err)
		}
		if err := in.FPMGlobal.check(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		if !env.Runner.Available(fmt.Sprintf(env.M.PHPFPMBinary, in.Version)) || !env.Root.Exists(env.M.PoolDir(in.Version)) {
			return nil, actions.Conflict("PHP " + in.Version + " (FPM) is not installed.")
		}
		defer env.LockWebConfig()()
		set := env.Changes("php-fpm-global-" + in.Version)
		if body := in.FPMGlobal.Render(in.Version); body == "" {
			set.Remove(FPMGlobalFile(env, in.Version))
		} else {
			set.Write(FPMGlobalFile(env, in.Version), []byte(body), 0o644, agentfs.RootOwner)
		}
		a, err := set.Commit(func() error { return env.FPMTest(ctx, in.Version) })
		if err != nil {
			return nil, host.FailWithValidation("The PHP-FPM settings were not applied", err)
		}
		if len(a.Changed) > 0 {
			if err := env.ReloadFPM(ctx, in.Version); err != nil {
				_ = a.Rollback()
				_ = env.ReloadFPM(ctx, in.Version)
				return nil, host.FailWithValidation("PHP-FPM "+in.Version+" did not reload; the previous settings were restored", err)
			}
		}
		return map[string]any{"settings": in.FPMGlobal, "changed": a.Changed}, nil
	}})
}
