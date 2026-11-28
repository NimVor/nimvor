package sites

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// FPMStatusPath is the pool's status page. It is answered by PHP-FPM itself
// when the agent asks the pool's FastCGI socket: Nginx never routes this URL
// (a request that does reach PHP carries the script's name, not this path),
// no PHP file runs, and it shows nothing but counters of that pool.
const FPMStatusPath = "/nimvor-status"

func fpmStatusFile(e *host.Env) string { return filepath.Join(e.Cfg.StateDir, "fpm-status.json") }

// FPMStatusEnabled reports whether an administrator switched the pools' status page on.
func FPMStatusEnabled(e *host.Env) bool {
	b, err := e.Root.ReadFile(fpmStatusFile(e))
	if err != nil {
		return false
	}
	var st struct {
		Enabled bool `json:"enabled"`
	}
	return json.Unmarshal(b, &st) == nil && st.Enabled
}

// FPMStatusReport is what sites.fpm_status answers.
type FPMStatusReport struct {
	Enabled    bool     `json:"enabled"`
	PHPSites   int      `json:"php_sites"`   // enabled PHP websites NimVor has a record of
	WithStatus int      `json:"with_status"` // ... whose pool file carries the status page now
	Skipped    []string `json:"skipped,omitempty"`
}

// phpSpecs lists the stored specs of enabled PHP websites.
func (m *Manager) phpSpecs() []Spec {
	e := m.Env
	matches, _ := e.Root.Glob(filepath.Join(e.Cfg.StateDir, specDir, "*.json"))
	sort.Strings(matches)
	var out []Spec
	for _, f := range matches {
		domain := strings.TrimSuffix(filepath.Base(f), ".json")
		if validate.Domain("domain", domain) != nil {
			continue
		}
		if s, ok := m.loadSpec(domain); ok && s.Kind == "php" && s.PHPVersion != "" && s.Enabled {
			out = append(out, s)
		}
	}
	return out
}

func (m *Manager) fpmStatusReport() FPMStatusReport {
	r := FPMStatusReport{Enabled: FPMStatusEnabled(m.Env)}
	for _, s := range m.phpSpecs() {
		r.PHPSites++
		l, _ := m.phpLayout(s)
		if cur, err := m.Env.Root.ReadFile(l.PoolFile); err == nil && strings.Contains(string(cur), "pm.status_path") {
			r.WithStatus++
		}
	}
	return r
}

// setFPMStatus switches the status page on or off in every pool NimVor wrote
// and has not been changed by hand. All the pools are written in one
// change set, checked with php-fpm -t (the configuration of every affected
// PHP-FPM master) before anything is reloaded; a refusal or a failed reload
// puts every file back. Pools that were edited by hand are left alone and listed.
func (m *Manager) setFPMStatus(ctx context.Context, enable bool) (FPMStatusReport, error) {
	defer m.Env.LockWebConfig()()
	e := m.Env
	old := FPMStatusEnabled(e)
	cfg := e.Changes("fpm-status")
	fpm := newFPMTargets()
	var skipped []string
	for _, s := range m.phpSpecs() {
		l, layer := m.phpLayout(s)
		cur, err := e.Root.ReadFile(l.PoolFile)
		if err != nil {
			skipped = append(skipped, s.Domain+" (no pool file)")
			continue
		}
		st := m.loadStatus(s.Domain)
		was, want := l, l
		was.FPMStatus, want.FPMStatus = old, enable
		was.Customer = st.Layer
		want.Customer = layer.Values
		switch string(cur) {
		case RenderPool(s, want):
			continue // already as wanted
		case RenderPool(s, was):
			cfg.Write(l.PoolFile, []byte(RenderPool(s, want)), ConfigMode, agentfs.RootOwner)
			fpm.add(l.PoolFile)
		default:
			skipped = append(skipped, s.Domain+" (pool file was changed by hand)")
		}
	}
	applied, err := cfg.Commit(func() error { return m.testFPM(ctx, fpm) })
	if err != nil {
		return FPMStatusReport{}, actions.Failed("PHP-FPM did not accept the change; no website was changed.", errText(err))
	}
	if len(applied.Changed) > 0 {
		if err := m.reloadFPM(ctx, fpm); err != nil {
			_ = applied.Rollback()
			_ = m.reloadFPM(ctx, fpm)
			return FPMStatusReport{}, actions.Failed("PHP-FPM did not accept the new configuration; every pool was put back.", errText(err))
		}
	}
	b, _ := json.Marshal(map[string]bool{"enabled": enable})
	if err := m.writeState(fpmStatusFile(e), json.RawMessage(b)); err != nil {
		_ = applied.Rollback()
		_ = m.reloadFPM(ctx, fpm)
		return FPMStatusReport{}, actions.Failed("The setting could not be saved; every pool was put back.", err.Error())
	}
	e.Log.Info("PHP-FPM status page switched", "enabled", enable, "pools", len(applied.Changed))
	r := m.fpmStatusReport()
	r.Skipped = skipped
	return r, nil
}

func (m *Manager) registerFPMStatus(r *actions.Registry) {
	r.Register(actions.Action{Name: "sites.fpm_status", Description: "Whether PHP-FPM's status page (workers in use, queue, saturation) is switched on for the websites' pools, and for how many. Read-only.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		return m.fpmStatusReport(), nil
	}})
	r.Register(actions.Action{Name: "sites.fpm_status_set", Mutating: true, Description: "Switch PHP-FPM's status page on or off for every website pool NimVor wrote (one added line, pm.status_path). Checked with php-fpm -t before a reload; pools changed by hand are left alone; everything is put back if a step fails. No limit is changed.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return m.setFPMStatus(ctx, in.Enabled)
	}})
}
