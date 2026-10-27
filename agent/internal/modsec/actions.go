package modsec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/capability"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
	"github.com/NimVor/nimvor/agent/internal/txn"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// Packages are the ones the Packages page installs (Debian names).
var Packages = []string{"libnginx-mod-http-modsecurity", "modsecurity-crs"}

// vhostMarker is a line every WhaCP server block with content carries;
// the website's include goes right after it.
const vhostMarker = "    add_header X-Content-Type-Options \"nosniff\" always;\n"

var mu sync.Mutex

func vhostPath(e *host.Env, domain string) string {
	return filepath.Join(e.M.NginxAvailable, "whacp-"+domain+".conf")
}

// Register adds the ModSecurity actions.
func Register(r *actions.Registry, e *host.Env) {
	e.Writer.Policy.Patterns = append(e.Writer.Policy.Patterns, Dir(e)+"/**", AuditLog(e))
	allowInstallerPaths(e)

	r.Register(actions.Action{Name: "modsec.install", Mutating: true, Description: "Install (or upgrade) the ModSecurity connector as a background job and one transaction: the distribution's package when installing it does not touch Nginx itself, otherwise built from source for the exact installed Nginx (pinned, checksum-verified ModSecurity releases; Nginx's own verified source). Everything is backed up first and rolled back on any failure (build, dependencies, libraries, module load, nginx -t, reload). A working connector is left alone unless upgrade is set. The Nginx binary is never changed.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var opt InstallOptions
		if err := host.Decode(raw, &opt); err != nil {
			return nil, err
		}
		if opt.Method != "" && opt.Method != "auto" && opt.Method != "source" {
			return nil, actions.InvalidParams("method: auto or source")
		}
		if opt.RetentionDays < 0 || opt.RetentionDays > 3650 {
			return nil, actions.InvalidParams("retention_days: 1 to 3650")
		}
		if opt.NginxSourceSHA256 != "" && !sha256Re.MatchString(strings.ToLower(opt.NginxSourceSHA256)) {
			return nil, actions.InvalidParams("nginx_source_sha256: 64 hexadecimal characters")
		}
		j := e.Jobs.Start("modsec.install", "server", func(ctx context.Context, log io.Writer) (any, error) {
			return Install(ctx, e, DefaultSources, opt, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "modsec.crs_install", Mutating: true, Description: "Install the OWASP Core Rule Set (background job, one transaction): the distribution's package when there is one, otherwise the pinned, checksum-verified CRS release unpacked by the agent (rule and data files only). It is turned on in detection only: websites set to block only log until blocking is allowed. Rolled back on failure.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			RetentionDays int `json:"retention_days"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		j := e.Jobs.Start("modsec.crs_install", "server", func(ctx context.Context, log io.Writer) (any, error) {
			return InstallCRS(ctx, e, DefaultCRSSource, in.RetentionDays, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "modsec.installs", Description: "ModSecurity connector installations and rollbacks (transactions, newest first): status, method, versions and sources, the module, the backup folder, and which one can be rolled back.", Handler: func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"installs": Installs(e)}, nil
	}})
	r.Register(actions.Action{Name: "modsec.rollback", Mutating: true, Description: "Roll back the ModSecurity connector installation in effect (id) as a background job: its files are put back, packages it added removed, then nginx -t and a reload; if that fails, nothing changes.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if _, err := txn.Load(e, TxKind, in.ID); err != nil {
			return nil, actions.NotFound("There is no ModSecurity installation " + in.ID + ".")
		}
		j := e.Jobs.Start("modsec.rollback", "server", func(ctx context.Context, log io.Writer) (any, error) {
			return RollbackInstall(ctx, e, in.ID, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})

	r.Register(actions.Action{Name: "modsec.status", Description: "ModSecurity: the Nginx connector as a capability (missing, installed but not loaded or usable, functional; from a package or not), the OWASP Core Rule Set found, the server-wide configuration, rule vendors, websites' modes and the audit log size.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		st := Load(e)
		crs := FindCRS(e)
		custom, _ := e.Root.ReadFile(filepath.Join(Dir(e), "custom.conf"))
		ids, _ := CheckCustom(stripHeader(string(custom)))
		var size int64
		if real, err := e.Root.Path(AuditLog(e)); err == nil {
			if fi, err := os.Stat(real); err == nil {
				size = fi.Size()
			}
		}
		_, missing := pkgs.Resolve(e, Packages)
		conn := Connector(ctx, e, true)
		return map[string]any{
			"module_loaded": conn.State == capability.Functional, "connector": conn, "crs": crs, "config": st.Config, "sites": st.Sites,
			"active": st.Config.Enabled && e.Root.Exists(HTTPPath(e)), "vendors": Vendors,
			"custom_rules": len(ids), "audit_log_bytes": size, "packages": Packages, "packages_available": len(missing) == 0,
		}, nil
	}})

	r.Register(actions.Action{Name: "modsec.configure", Mutating: true, Description: "Turn ModSecurity on or off server-wide and set the rule vendors (OWASP CRS paranoia level and anomaly thresholds, custom rules). Checked with nginx -t, rolled back on failure.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var c Config
		if err := host.Decode(raw, &c); err != nil {
			return nil, err
		}
		if err := c.Validate(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		mu.Lock()
		defer mu.Unlock()
		if c.Enabled {
			if conn := Connector(ctx, e, false); conn.State != capability.Functional {
				what := "is not installed"
				if conn.State == capability.Installed {
					what = "is installed but not usable"
				}
				return nil, actions.Conflict("The ModSecurity module for Nginx " + what + ": " + conn.Reason + " Install it with Install ModSecurity on this page first.")
			}
		}
		if c.Enabled && c.CRS.Enabled && !FindCRS(e).Available {
			return nil, actions.Conflict("The OWASP Core Rule Set is not installed (package modsecurity-crs). Install it, or turn the Core Rule Set off.")
		}
		st := Load(e)
		st.Config = c
		if err := apply(ctx, e, st, nil); err != nil {
			return nil, err
		}
		return map[string]any{"config": c, "active": c.Enabled}, nil
	}})

	r.Register(actions.Action{Name: "modsec.custom_get", Description: "The administrator's custom ModSecurity rules.", Handler: func(context.Context, json.RawMessage) (any, error) {
		b, _ := e.Root.ReadFile(filepath.Join(Dir(e), "custom.conf"))
		return map[string]any{"rules": stripHeader(string(b))}, nil
	}})

	r.Register(actions.Action{Name: "modsec.custom_set", Mutating: true, Description: "Save the custom ModSecurity rules (rule directives only, IDs 10000–99999, no programs, files or includes). Checked with nginx -t, rolled back on failure.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Rules string `json:"rules"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		in.Rules = strings.ReplaceAll(in.Rules, "\r\n", "\n")
		ids, err := CheckCustom(in.Rules)
		if err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		mu.Lock()
		defer mu.Unlock()
		if err := apply(ctx, e, Load(e), &in.Rules); err != nil {
			return nil, err
		}
		return map[string]any{"rules": len(ids)}, nil
	}})

	r.Register(actions.Action{Name: "modsec.site", Mutating: true, Description: "Set a website's ModSecurity: off, detect (log only) or on (block), which rule vendors apply and single rules turned off. Checked with nginx -t, rolled back on failure.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
			Site
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("domain", in.Domain); err != nil {
			return nil, err
		}
		if err := in.Site.Normalize(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		mu.Lock()
		defer mu.Unlock()
		if !e.Root.Exists(vhostPath(e, in.Domain)) {
			return nil, actions.NotFound("That website has no Nginx configuration on this server.")
		}
		st := Load(e)
		if in.Site.Mode != ModeOff && !st.Config.Enabled {
			return nil, actions.Conflict("ModSecurity is off on this server. An administrator turns it on under Security → ModSecurity first.")
		}
		if in.Site.Mode == ModeOff && len(in.Site.Exclude) == 0 && len(in.Site.Disabled) == 0 {
			delete(st.Sites, in.Domain)
		} else {
			st.Sites[in.Domain] = in.Site
		}
		if err := apply(ctx, e, st, nil); err != nil {
			return nil, err
		}
		return map[string]any{"domain": in.Domain, "site": in.Site}, nil
	}})

	r.Register(actions.Action{Name: "modsec.events", Description: "Recent requests ModSecurity blocked or detected (newest first), optionally only those for the given host names: time, host, client address, method, URI, status and the rules that matched.", Handler: func(_ context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Hosts []string `json:"hosts"`
			Limit int      `json:"limit"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if in.Limit <= 0 || in.Limit > 500 {
			in.Limit = 100
		}
		hosts := map[string]bool{}
		for _, h := range in.Hosts {
			if validate.Domain("hosts", h) == nil {
				hosts[strings.ToLower(h)] = true
			}
		}
		real, err := e.Root.Path(AuditLog(e))
		if err != nil {
			return nil, err
		}
		ev, err := ReadEvents(real, hosts, in.Limit)
		if err != nil && !os.IsNotExist(err) {
			return nil, actions.Failed("The ModSecurity audit log could not be read.", err.Error())
		}
		return map[string]any{"events": ev}, nil
	}})

	r.Register(actions.Action{Name: "modsec.update", Mutating: true, Description: "Update the rule packages (OWASP CRS and the connector) with the package manager as a background job, then check with nginx -t and reload. If the new rules break the configuration, the Core Rule Set is turned off and the rest restored.", Handler: func(context.Context, json.RawMessage) (any, error) {
		if !pkgs.Available(e) {
			return nil, actions.Conflict("The package manager is not available.")
		}
		natives, missing := pkgs.Resolve(e, Packages)
		if len(missing) > 0 {
			return nil, actions.Conflict("The rule packages are not available on this system: " + strings.Join(missing, ", "))
		}
		j := e.Jobs.Start("modsec.update", "server", func(ctx context.Context, log io.Writer) (any, error) {
			before := FindCRS(e).Version
			if err := pkgs.InstallNative(ctx, e, log, natives); err != nil {
				return nil, err
			}
			mu.Lock()
			defer mu.Unlock()
			st := Load(e)
			after := FindCRS(e).Version
			fmt.Fprintf(log, "OWASP CRS: %s → %s\n", orNone(before), orNone(after))
			if !st.Config.Enabled {
				return map[string]any{"before": before, "after": after}, nil
			}
			if err := apply(ctx, e, st, nil); err != nil {
				fmt.Fprintf(log, "The updated rules do not load: %v\nTurning the Core Rule Set off.\n", err)
				st.Config.CRS.Enabled = false
				if err2 := apply(ctx, e, st, nil); err2 != nil {
					return nil, fmt.Errorf("the updated rules do not load and turning the Core Rule Set off failed too: %v", err2)
				}
				return nil, fmt.Errorf("the updated Core Rule Set %s does not load (%v); it was turned off so the websites keep working", after, err)
			}
			return map[string]any{"before": before, "after": after}, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})

	r.Register(actions.Action{Name: "modsec.trim", Mutating: true, Description: "Keep the ModSecurity audit log small: above 50 MiB its last 5 MiB are kept.", Handler: func(context.Context, json.RawMessage) (any, error) {
		real, err := e.Root.Path(AuditLog(e))
		if err != nil {
			return nil, err
		}
		n, err := Trim(real, 50<<20, 5<<20)
		if err != nil && !os.IsNotExist(err) {
			return nil, actions.Failed("The audit log could not be trimmed.", err.Error())
		}
		return map[string]any{"removed_bytes": n}, nil
	}})
}

func orNone(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

func stripHeader(s string) string { return strings.TrimPrefix(s, header) }

var vhostRe = regexp.MustCompile(`^whacp-(.+)\.conf$`)

// apply writes every file for st (and custom rules when given), checks
// with nginx -t and reloads, restoring everything on failure.
func apply(ctx context.Context, e *host.Env, st State, custom *string) error {
	unlock := e.LockWebConfig()
	defer unlock()
	if err := e.PrecheckNginx(ctx); err != nil {
		return err
	}
	set := e.Changes("modsec")
	dir := Dir(e)
	active := st.Config.Enabled && ModuleLoaded(e)
	set.Mkdir(dir, 0o755, agentfs.RootOwner)
	set.Mkdir(filepath.Join(dir, "sites"), 0o755, agentfs.RootOwner)
	customPath := filepath.Join(dir, "custom.conf")
	if custom != nil {
		set.Write(customPath, []byte(header+*custom), 0o644, agentfs.RootOwner)
	}
	hasCustom := custom != nil || e.Root.Exists(customPath)
	crs := FindCRS(e)
	cfg := st.Config
	cfg.Custom.Enabled = cfg.Custom.Enabled && hasCustom
	if cfg.CRS.Enabled && crs.Available {
		set.Write(filepath.Join(dir, "crs.conf"), []byte(RenderCRS(cfg, crs)), 0o644, agentfs.RootOwner)
	}
	main := RenderMain(e, cfg, crs)
	if custom != nil && cfg.Custom.Enabled && !strings.Contains(main, customPath) {
		main += "Include " + customPath + "\n"
	}
	set.Write(filepath.Join(dir, "main.conf"), []byte(main), 0o644, agentfs.RootOwner)

	// Website files: written for websites with ModSecurity on, removed
	// for the others (and all of them while it is off server-wide).
	existing, _ := e.Root.Glob(filepath.Join(dir, "sites", "*.conf"))
	keep := map[string]bool{}
	if active {
		set.Write(HTTPPath(e), []byte(RenderHTTP(e)), 0o644, agentfs.RootOwner)
		for d, s := range st.Sites {
			if st.Config.DetectionOnly && s.Mode == ModeOn {
				s.Mode = ModeDetect // blocking not allowed yet
			}
			body := RenderSite(s)
			if body == "" || !e.Root.Exists(vhostPath(e, d)) {
				continue
			}
			keep[SitePath(e, d)] = true
			set.Write(SitePath(e, d), []byte(body), 0o644, agentfs.RootOwner)
		}
		// Websites created before this release: add the include line.
		vhosts, _ := e.Root.Glob(filepath.Join(e.M.NginxAvailable, "whacp-*.conf"))
		for _, f := range vhosts {
			m := vhostRe.FindStringSubmatch(filepath.Base(f))
			if m == nil || validate.Domain("domain", m[1]) != nil {
				continue
			}
			b, err := e.Root.ReadFile(f)
			inc := SiteInclude(e, m[1])
			if err != nil || strings.Contains(string(b), inc) || !strings.Contains(string(b), vhostMarker) {
				continue
			}
			set.Write(f, []byte(strings.ReplaceAll(string(b), vhostMarker, vhostMarker+inc)), 0o644, agentfs.RootOwner)
		}
	} else if e.Root.Exists(HTTPPath(e)) {
		set.Remove(HTTPPath(e))
	}
	for _, f := range existing {
		if !keep[f] {
			set.Remove(f)
		}
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	set.Write(statePath(e), b, 0o600, agentfs.RootOwner)

	applied, err := set.Commit(func() error { return e.NginxTest(ctx) })
	if err != nil {
		return host.FailWithValidation("ModSecurity was not changed", err)
	}
	if len(applied.Changed) > 0 {
		if err := e.ReloadNginx(ctx); err != nil {
			applied.Rollback()
			e.ReloadNginx(ctx)
			return host.FailWithValidation("Nginx did not reload; the previous ModSecurity configuration was restored", err)
		}
	}
	return nil
}

// Events.

// Rule is a rule that matched.
type Rule struct {
	ID       string `json:"id"`
	Message  string `json:"message"`
	Severity string `json:"severity,omitempty"`
	Data     string `json:"data,omitempty"`
}

// Event is one audited request.
type Event struct {
	Time     string `json:"time"`
	Host     string `json:"host"`
	ClientIP string `json:"client_ip"`
	Method   string `json:"method"`
	URI      string `json:"uri"`
	Status   int    `json:"status"`
	Engine   string `json:"engine"`
	Rules    []Rule `json:"rules"`
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// ReadEvents reads the newest events from the audit log (its last 8 MiB).
func ReadEvents(path string, hosts map[string]bool, limit int) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return []Event{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return []Event{}, err
	}
	const window = 8 << 20
	off := max(int64(0), fi.Size()-window)
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return []Event{}, err
	}
	lines := strings.Split(string(buf), "\n")
	if off > 0 && len(lines) > 0 {
		lines = lines[1:] // a partial line
	}
	out := []Event{}
	for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
		var rec struct {
			Transaction struct {
				ClientIP  string `json:"client_ip"`
				TimeStamp string `json:"time_stamp"`
				Request   struct {
					Method  string            `json:"method"`
					URI     string            `json:"uri"`
					Headers map[string]string `json:"headers"`
				} `json:"request"`
				Response struct {
					Code int `json:"http_code"`
				} `json:"response"`
				Producer struct {
					Engine string `json:"secrules_engine"`
				} `json:"producer"`
				Messages []struct {
					Message string `json:"message"`
					Details struct {
						RuleID   string `json:"ruleId"`
						Severity string `json:"severity"`
						Data     string `json:"data"`
					} `json:"details"`
				} `json:"messages"`
			} `json:"transaction"`
		}
		if strings.TrimSpace(lines[i]) == "" || json.Unmarshal([]byte(lines[i]), &rec) != nil {
			continue
		}
		t := rec.Transaction
		h := ""
		for k, v := range t.Request.Headers {
			if strings.EqualFold(k, "host") {
				h = strings.ToLower(v)
			}
		}
		if hp, _, ok := strings.Cut(h, ":"); ok && !strings.HasPrefix(h, "[") {
			h = hp
		}
		if len(hosts) > 0 && !hosts[h] {
			continue
		}
		ev := Event{Time: t.TimeStamp, Host: clip(h, 255), ClientIP: t.ClientIP, Method: clip(t.Request.Method, 16), URI: clip(t.Request.URI, 400), Status: t.Response.Code, Engine: t.Producer.Engine, Rules: []Rule{}}
		if ts, err := time.Parse("Mon Jan 2 15:04:05 2006", t.TimeStamp); err == nil {
			ev.Time = ts.Format(time.RFC3339)
		}
		for _, m := range t.Messages {
			ev.Rules = append(ev.Rules, Rule{ID: m.Details.RuleID, Message: clip(m.Message, 300), Severity: m.Details.Severity, Data: clip(m.Details.Data, 200)})
		}
		sort.SliceStable(ev.Rules, func(a, b int) bool { return ev.Rules[a].ID < ev.Rules[b].ID })
		out = append(out, ev)
	}
	return out, nil
}

// Trim keeps the log below limit: above it only its last keep bytes stay
// (the file is rewritten in place, so Nginx keeps appending to it).
func Trim(path string, limit, keep int64) (int64, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() <= limit {
		return 0, err
	}
	tail := make([]byte, keep)
	n, err := f.ReadAt(tail, fi.Size()-keep)
	if err != nil && err != io.EOF {
		return 0, err
	}
	tail = tail[:n]
	if i := strings.IndexByte(string(tail), '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	if err := f.Truncate(0); err != nil {
		return 0, err
	}
	if _, err := f.WriteAt(tail, 0); err != nil {
		return 0, err
	}
	return fi.Size() - int64(len(tail)), nil
}
