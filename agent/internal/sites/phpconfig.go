package sites

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// Per-website PHP configuration: detection, the pool the customer's
// php.ini produces, and the state shown in the panel.
//
// The pool file is written by sites.apply from the website's spec. The
// agent keeps that spec (root-only, in its state directory) so that when a
// customer creates, changes, renames or deletes a php.ini the pool can be
// rendered again without the panel being involved: WatchPHPConfig compares
// three cheap file signatures per website every few seconds and, only when
// one changed, reads the files, renders the pool, and, only when the pool
// differs, validates it (php-fpm -t), writes it and reloads the FPM master
// of that PHP version (or of that account). Nothing else is touched.
//
// .user.ini needs none of this: PHP reads it itself, every
// user_ini.cache_ttl seconds.

const (
	specDir   = "sitespec"
	phpCfgDir = "phpconfig"
	// phpWatchEvery is how often the signatures are compared.
	phpWatchEvery = 10 * time.Second
	// defaultUserINITTL is PHP's user_ini.cache_ttl.
	defaultUserINITTL = 300
)

// PHPConfig is what sites.phpconfig returns.
type PHPConfig struct {
	Domain     string             `json:"domain"`
	PHPVersion string             `json:"php_version"`
	Kind       string             `json:"kind"`
	Managed    bool               `json:"managed"` // the agent holds the website's spec (php.ini can be applied)
	LockINI    bool               `json:"lock_ini"`
	Status     string             `json:"status"` // active, pending or error
	Message    string             `json:"message,omitempty"`
	LastChange time.Time          `json:"last_change,omitempty"`
	CacheTTL   int                `json:"user_ini_cache_ttl"`
	PHPINI     []ConfigFile       `json:"php_ini"`
	UserINI    ConfigFile         `json:"user_ini"`
	Effective  []EffectiveSetting `json:"effective"`
	Warnings   int                `json:"warnings"`
}

// phpStatus is the agent's memory of a website between runs.
type phpStatus struct {
	Signature  string            `json:"signature"`
	LastChange time.Time         `json:"last_change,omitempty"`
	Layer      map[string]string `json:"layer,omitempty"` // the php.ini values the pool file carries
	Error      string            `json:"error,omitempty"`
}

func (m *Manager) specFile(domain string) string {
	return filepath.Join(m.Env.Cfg.StateDir, specDir, domain+".json")
}
func (m *Manager) statusFile(domain string) string {
	return filepath.Join(m.Env.Cfg.StateDir, phpCfgDir, domain+".json")
}

func (m *Manager) writeState(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := m.Env.Writer.MkdirAll(filepath.Dir(path), 0o700, agentfs.RootOwner); err != nil {
		return err
	}
	_, err = m.Env.Writer.WriteFile(path, b, 0o600, agentfs.RootOwner)
	return err
}

// saveSpec remembers a normalised, applied spec.
func (m *Manager) saveSpec(s Spec) {
	if err := m.writeState(m.specFile(s.Domain), s); err != nil {
		m.Env.Log.Warn("website record not saved", "domain", s.Domain, "err", err)
	}
}

func (m *Manager) loadSpec(domain string) (Spec, bool) {
	var s Spec
	b, err := m.Env.Root.ReadFile(m.specFile(domain))
	if err != nil || json.Unmarshal(b, &s) != nil || s.Domain != domain {
		return s, false
	}
	return s, true
}

func (m *Manager) loadStatus(domain string) phpStatus {
	var st phpStatus
	if b, err := m.Env.Root.ReadFile(m.statusFile(domain)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func (m *Manager) forgetSite(domain string) {
	m.Env.Writer.Remove(m.specFile(domain))
	m.Env.Writer.Remove(m.statusFile(domain))
}

// rp maps a host path to the path the agent opens (see pathMapper).
func (m *Manager) rp(p string) string {
	if r, err := m.Env.Root.Path(p); err == nil {
		return r
	}
	return p
}

func (m *Manager) siteUID(s Spec) int {
	if u, err := m.Env.LookupUser(s.User); err == nil {
		return u.UID
	}
	return -1
}

// customerFor reads the website's php.ini files for a layout whose own
// pool lines are base (the locked keys are taken from it).
func (m *Manager) customerFor(s Spec, l Layout) CustomerLayer {
	l.Customer = nil
	return BuildCustomerLayer(s, lockedKeys(PoolLines(s, l)), m.siteUID(s), m.rp)
}

// signature is a cheap fingerprint of the files NimVor watches.
func signature(s Spec, rp pathMapper) string {
	files, user := siteINIPaths(s)
	var parts []string
	for _, f := range append(files, user) {
		if st, err := os.Lstat(rp(f.Path)); err != nil {
			parts = append(parts, "-")
		} else {
			ino := uint64(0)
			if sys := inodeOf(st); sys != 0 {
				ino = sys
			}
			parts = append(parts, fmt.Sprintf("%d:%d:%d:%d", st.ModTime().UnixNano(), st.Size(), ino, st.Mode()))
		}
	}
	return strings.Join(parts, "|")
}

// phpLayout is the layout of a site's pool with the current customer files.
func (m *Manager) phpLayout(s Spec) (Layout, CustomerLayer) {
	l := m.layout(s)
	layer := m.customerFor(s, l)
	l.Customer = layer.Values
	return l, layer
}

// reconcilePHP renders the website's pool from its stored spec and the
// customer's files and, when the file on disk differs because the customer's
// files changed, replaces it. force re-evaluates even when nothing changed.
func (m *Manager) reconcilePHP(ctx context.Context, domain string, force bool) error {
	defer m.Env.LockWebConfig()()
	e := m.Env
	s, ok := m.loadSpec(domain)
	if !ok || s.Kind != "php" || s.PHPVersion == "" {
		return nil
	}
	sig := signature(s, m.rp)
	st := m.loadStatus(domain)
	if sig == st.Signature && !force {
		return nil
	}
	if sig != st.Signature && st.Signature != "" {
		st.LastChange = time.Now().UTC().Truncate(time.Second)
	} else if st.LastChange.IsZero() {
		st.LastChange = newestMod(s, m.rp)
	}
	st.Signature = sig

	l, layer := m.phpLayout(s)
	if !s.Enabled {
		// A disabled website keeps no pool.
		st.Layer, st.Error = layer.Values, ""
		return m.writeState(m.statusFile(domain), st)
	}
	cur, err := e.Root.ReadFile(l.PoolFile)
	if err != nil {
		st.Error = "The website's PHP pool file is missing; save the website to recreate it."
		return m.writeState(m.statusFile(domain), st)
	}
	want := RenderPool(s, l)
	if string(cur) == want {
		st.Layer, st.Error = layer.Values, ""
		return m.writeState(m.statusFile(domain), st)
	}
	// Only a pool NimVor wrote from this record is replaced: a hand-edited
	// or older pool is left alone until the website is saved.
	prev := l
	prev.Customer = st.Layer
	// A pool written before the opt-in status page was switched on (or off) is still NimVor's own.
	other := prev
	other.FPMStatus = !prev.FPMStatus
	if string(cur) != RenderPool(s, prev) && string(cur) != RenderPool(s, other) {
		st.Error = "The website's PHP pool was not written by this version of NimVor or was changed by hand. Save the website once to bring it up to date."
		return m.writeState(m.statusFile(domain), st)
	}

	cfg := e.Changes("phpini-" + domain)
	cfg.Write(l.PoolFile, []byte(want), ConfigMode, agentfs.RootOwner)
	fpm := newFPMTargets()
	fpm.add(l.PoolFile)
	applied, err := cfg.Commit(func() error { return m.testFPM(ctx, fpm) })
	if err != nil {
		st.Error = "The PHP configuration was not accepted: " + errText(err)
		e.Log.Warn("php.ini of a website not applied", "domain", domain, "err", err)
		return m.writeState(m.statusFile(domain), st)
	}
	if len(applied.Changed) > 0 {
		if err := m.reloadFPM(ctx, fpm); err != nil {
			_ = applied.Rollback()
			_ = m.reloadFPM(ctx, fpm)
			st.Error = "PHP-FPM did not accept the new configuration: " + errText(err)
			e.Log.Warn("php.ini of a website not applied", "domain", domain, "err", err)
			return m.writeState(m.statusFile(domain), st)
		}
	}
	st.Layer, st.Error = layer.Values, ""
	e.Log.Info("website php.ini applied", "domain", domain, "directives", len(layer.Values))
	return m.writeState(m.statusFile(domain), st)
}

func newestMod(s Spec, rp pathMapper) time.Time {
	files, user := siteINIPaths(s)
	var t time.Time
	for _, f := range append(files, user) {
		if st, err := os.Lstat(rp(f.Path)); err == nil && st.ModTime().After(t) {
			t = st.ModTime()
		}
	}
	return t.UTC().Truncate(time.Second)
}

// WatchPHPConfig watches the websites' php.ini files until ctx ends.
func (m *Manager) WatchPHPConfig(ctx context.Context) {
	t := time.NewTicker(phpWatchEvery)
	defer t.Stop()
	cache := &specCache{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.scanPHPConfig(ctx, cache)
		}
	}
}

type specCache struct {
	mu   sync.Mutex
	sigs map[string]string
}

// scanPHPConfig compares the cheap signatures and reconciles the websites
// whose files changed.
func (m *Manager) scanPHPConfig(ctx context.Context, c *specCache) {
	e := m.Env
	matches, _ := e.Root.Glob(filepath.Join(e.Cfg.StateDir, specDir, "*.json"))
	c.mu.Lock()
	if c.sigs == nil {
		c.sigs = map[string]string{}
	}
	c.mu.Unlock()
	for _, f := range matches {
		domain := strings.TrimSuffix(filepath.Base(f), ".json")
		if validate.Domain("domain", domain) != nil {
			continue
		}
		s, ok := m.loadSpec(domain)
		if !ok || s.Kind != "php" {
			continue
		}
		now := signature(s, m.rp)
		c.mu.Lock()
		last, seen := c.sigs[domain]
		c.sigs[domain] = now
		c.mu.Unlock()
		if seen && last == now {
			continue
		}
		if err := m.reconcilePHP(ctx, domain, false); err != nil {
			e.Log.Warn("php configuration check failed", "domain", domain, "err", err)
		}
		if ctx.Err() != nil {
			return
		}
	}
}

var iniTTLRe = regexp.MustCompile(`(?m)^\s*user_ini\.cache_ttl\s*=\s*([0-9]+)`)

// userINITTL is the server's user_ini.cache_ttl for a PHP version (PHP's
// default of 300 when it is not set in the files NimVor can read).
func (m *Manager) userINITTL(version string) int {
	e := m.Env
	files := []string{fmt.Sprintf("/etc/php/%s/fpm/php.ini", version)}
	if c, _ := e.Root.Glob(fmt.Sprintf("/etc/php/%s/fpm/conf.d/*.ini", version)); len(c) > 0 {
		files = append(files, c...)
	}
	ttl := defaultUserINITTL
	for _, f := range files {
		if b, err := e.Root.ReadFile(f); err == nil {
			if mm := iniTTLRe.FindAllStringSubmatch(string(b), -1); len(mm) > 0 {
				if n, err := strconv.Atoi(mm[len(mm)-1][1]); err == nil {
					ttl = n
				}
			}
		}
	}
	return ttl
}

var rootDirectiveRe = regexp.MustCompile(`(?m)^\s*root\s+([^;\s]+);`)

// specFromDisk builds the little of a Spec needed to show a website that
// predates the stored record: its folders from the Nginx file.
func (m *Manager) specFromDisk(domain string) (Spec, bool) {
	e := m.Env
	b, err := e.Root.ReadFile(filepath.Join(e.M.NginxAvailable, "nimvor-"+domain+".conf"))
	if err != nil {
		return Spec{}, false
	}
	s := Spec{Domain: domain, Root: filepath.Join(e.Cfg.SitesRoot, domain), Kind: "php"}
	s.DocumentRoot = filepath.Join(s.Root, "public_html")
	if mm := rootDirectiveRe.FindStringSubmatch(string(b)); mm != nil {
		if d := filepath.Clean(mm[1]); strings.HasPrefix(d, s.Root+"/") {
			s.DocumentRoot = d
		}
	}
	if pf := m.poolFiles(domain); len(pf) > 0 {
		s.PHPVersion = versionOfPool(pf[0])
		if a, ok := accountOfPool(pf[0]); ok {
			s.PHPVersion = a.Version
		}
	}
	if fi, err := os.Stat(m.rp(s.Root)); err == nil {
		if sys := ownerOf(fi); sys >= 0 {
			if all, err := e.Accounts(); err == nil {
				for _, u := range all {
					if u.UID == sys {
						s.User = u.Name
					}
				}
			}
		}
	}
	return s, true
}

// Describe reports a website's PHP configuration.
func (m *Manager) Describe(domain string) (*PHPConfig, error) {
	e := m.Env
	s, managed := m.loadSpec(domain)
	if !managed {
		var ok bool
		if s, ok = m.specFromDisk(domain); !ok {
			return nil, actions.Failed("This website does not exist.", "")
		}
	}
	out := &PHPConfig{Domain: domain, PHPVersion: s.PHPVersion, Kind: s.Kind, Managed: managed, LockINI: s.PHP.LockINI, CacheTTL: defaultUserINITTL, PHPINI: []ConfigFile{}, Effective: []EffectiveSetting{}}
	if s.PHPVersion != "" {
		out.CacheTTL = m.userINITTL(s.PHPVersion)
	}
	uid := m.siteUID(s)
	var baseLines []iniLine
	var layer CustomerLayer
	if managed {
		l := m.layout(s)
		l.Customer = nil
		baseLines = PoolLines(s, l)
		layer = BuildCustomerLayer(s, lockedKeys(baseLines), uid, m.rp)
	} else {
		// Without the record the pool's locked keys are the fixed set.
		locked := map[string]bool{}
		for _, k := range []string{"open_basedir", "upload_tmp_dir", "session.save_path", "sys_temp_dir", "error_log", "sendmail_path", "disable_functions"} {
			locked[k] = true
		}
		layer = BuildCustomerLayer(s, locked, uid, m.rp)
		baseLines = nil
		for k := range locked {
			baseLines = append(baseLines, iniLine{key: k, admin: true})
		}
		sort.Slice(baseLines, func(i, j int) bool { return baseLines[i].key < baseLines[j].key })
	}
	out.PHPINI = layer.Files
	out.UserINI = UserINIFile(s, lockedKeys(baseLines), uid, m.rp)
	if managed {
		out.Effective = Effective(baseLines, layer, out.UserINI)
	}
	for _, f := range append(append([]ConfigFile{}, out.PHPINI...), out.UserINI) {
		if f.Problem != "" {
			out.Warnings++
		}
		for _, d := range f.Directives {
			if d.State == StateUnsupported || d.State == StateInvalid || d.State == StateLocked || d.State == StateSystem {
				out.Warnings++
			}
		}
	}

	st := m.loadStatus(domain)
	out.LastChange = st.LastChange
	if out.LastChange.IsZero() {
		out.LastChange = newestMod(s, m.rp)
	}
	out.Status = "active"
	any := false
	for _, f := range out.PHPINI {
		any = any || f.Exists
	}
	switch {
	case s.Kind != "php":
		out.Message = "This website does not run PHP."
	case !managed && any:
		out.Status = "pending"
		out.Message = "NimVor has no record of this website's settings yet, so its php.ini is not applied. Save the website's settings once; a .user.ini already works."
	case st.Error != "":
		out.Status, out.Message = "error", st.Error
	case managed && s.Enabled:
		l, _ := m.phpLayout(s)
		if cur, err := e.Root.ReadFile(l.PoolFile); err == nil && string(cur) != RenderPool(s, l) {
			out.Status = "pending"
			out.Message = "A change was detected and is being applied."
		}
	}
	return out, nil
}

func (m *Manager) handlePHPConfig(ctx context.Context, p json.RawMessage) (any, error) {
	var in struct {
		Domain string `json:"domain"`
	}
	if err := host.Decode(p, &in); err != nil {
		return nil, err
	}
	if err := validate.Domain("domain", in.Domain); err != nil {
		return nil, host.Invalid(err)
	}
	return m.Describe(in.Domain)
}

func (m *Manager) handlePHPConfigScan(ctx context.Context, p json.RawMessage) (any, error) {
	var in struct {
		Domain string `json:"domain"`
	}
	if err := host.Decode(p, &in); err != nil {
		return nil, err
	}
	if err := validate.Domain("domain", in.Domain); err != nil {
		return nil, host.Invalid(err)
	}
	if err := m.reconcilePHP(ctx, in.Domain, true); err != nil {
		return nil, actions.Failed("The PHP configuration could not be checked.", err.Error())
	}
	return m.Describe(in.Domain)
}

// recordSite remembers an applied website so its php.ini can be applied
// later without the panel, and what the pool just written carries.
func (m *Manager) recordSite(s Spec, l Layout) {
	m.saveSpec(s)
	if s.Kind != "php" {
		return
	}
	st := m.loadStatus(s.Domain)
	if sig := signature(s, m.rp); st.Signature != sig && st.Signature != "" {
		st.LastChange = time.Now().UTC().Truncate(time.Second)
		st.Signature = sig
	} else {
		st.Signature = sig
		if st.LastChange.IsZero() {
			st.LastChange = newestMod(s, m.rp)
		}
	}
	st.Layer, st.Error = l.Customer, ""
	if err := m.writeState(m.statusFile(s.Domain), st); err != nil {
		m.Env.Log.Warn("php configuration state not saved", "domain", s.Domain, "err", err)
	}
}
