package wordpress

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/webprotect"
)

// Security hardening of an installation, measure by measure, each one
// applied and rolled back on its own: web server rules (webprotect) and
// WordPress settings. What a WordPress measure replaced is recorded, so a
// rollback puts back exactly the earlier value (or removes the setting
// when there was none). Changing the security keys cannot be undone (the
// old keys are not kept); the toolkit says so.

// Measure describes one hardening measure.
type Measure struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Help       string `json:"help"`
	Kind       string `json:"kind"` // web or wordpress
	Revertible bool   `json:"revertible"`
}

// Measures are all measures, in the order the panel shows them.
var Measures = []Measure{
	{webprotect.BlockSensitive, "Block access to sensitive files", "wp-config.php, readme.html, license.txt, the installer and backup, log and database dump files are not served.", "web", true},
	{webprotect.BlockXMLRPC, "Block access to xmlrpc.php", "Closes an old remote interface used for password guessing and DDoS amplification. Jetpack and some mobile apps need it.", "web", true},
	{webprotect.BlockAuthorScan, "Block author scans", "Stops ?author=N from revealing administrator user names.", "web", true},
	{webprotect.NoPHPUploads, "Forbid PHP in wp-content/uploads", "Uploaded files can never run as PHP.", "web", true},
	{webprotect.NoPHPIncludes, "Forbid PHP in wp-includes", "WordPress's own library files cannot be requested directly.", "web", true},
	{webprotect.BlockBots, "Block known attack tools", "Requests from vulnerability scanners (sqlmap, WPScan, Nikto, …) are refused.", "web", true},
	{"disable_file_edit", "Turn off the file editor in the dashboard", "Sets DISALLOW_FILE_EDIT: a stolen administrator account cannot change PHP code from the dashboard.", "wordpress", true},
	{"disable_pingbacks", "Turn off pingbacks", "New posts do not send or accept pingbacks (used for DDoS and spam).", "wordpress", true},
	{"disable_script_concat", "Turn off script concatenation for the dashboard", "Sets CONCATENATE_SCRIPTS to false (closes a known denial-of-service vector through load-scripts.php).", "wordpress", true},
	{"debug_off", "Turn off debugging", "Sets WP_DEBUG to false so errors do not reveal paths and code.", "wordpress", true},
	{"config_permissions", "Restrict access to wp-config.php", "Only the website account can read wp-config.php (mode 0600).", "wordpress", true},
	{"security_keys", "Change the security keys", "New random keys and salts: everyone is signed out. Cannot be rolled back.", "wordpress", false},
}

func measureByID(id string) (Measure, bool) {
	for _, m := range Measures {
		if m.ID == id {
			return m, true
		}
	}
	return Measure{}, false
}

// Previous is what a WordPress measure replaced.
type Previous struct {
	// Constant: Defined false means it was not set in wp-config.php.
	Defined bool              `json:"defined,omitempty"`
	Raw     string            `json:"raw,omitempty"`
	Options map[string]string `json:"options,omitempty"`
	Mode    string            `json:"mode,omitempty"`
	At      string            `json:"at"`
}

type secState struct {
	// Installs maps "domain/path" to measure ID to what it replaced.
	Installs map[string]map[string]Previous `json:"installs"`
}

var secMu sync.Mutex

func secPath(env *host.Env) string { return filepath.Join(env.Cfg.StateDir, "wordpress-security.json") }

func loadSec(env *host.Env) secState {
	st := secState{Installs: map[string]map[string]Previous{}}
	if b, err := env.Root.ReadFile(secPath(env)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Installs == nil {
		st.Installs = map[string]map[string]Previous{}
	}
	return st
}

func saveSec(env *host.Env, st secState) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	set := env.Changes("wordpress-security")
	set.Write(secPath(env), b, 0o600, agentfs.RootOwner)
	_, err := set.Commit(nil)
	return err
}

// securityScript reads the current values the WordPress measures change.
const securityScript = `<?php
$cfg = is_file(ABSPATH . 'wp-config.php') ? ABSPATH . 'wp-config.php' : dirname(ABSPATH) . '/wp-config.php';
$c = is_file($cfg) ? (string) file_get_contents($cfg) : '';
$has = fn ($n) => (bool) preg_match('/define\s*\(\s*[\'"]' . $n . '[\'"]\s*,/', $c);
$raw = fn ($n) => defined($n) ? var_export(constant($n), true) : '';
$const = [];
foreach (['DISALLOW_FILE_EDIT', 'CONCATENATE_SCRIPTS', 'WP_DEBUG'] as $n) {
    $const[$n] = ['defined' => $has($n), 'raw' => $raw($n)];
}
$keys = true;
foreach (['AUTH_KEY', 'SECURE_AUTH_KEY', 'LOGGED_IN_KEY', 'NONCE_KEY'] as $k) {
    if (!defined($k) || constant($k) === '' || constant($k) === 'put your unique phrase here' || strlen(constant($k)) < 32) {
        $keys = false;
    }
}
echo "\nNIMVOR-JSON " . json_encode([
    'constants' => $const,
    'options' => ['default_ping_status' => (string) get_option('default_ping_status'), 'default_pingback_flag' => (string) get_option('default_pingback_flag')],
    'config_mode' => is_file($cfg) ? sprintf('%04o', fileperms($cfg) & 0777) : '',
    'config_path' => $cfg,
    'keys_set' => $keys,
]) . "\n";
`

type current struct {
	Constants map[string]struct {
		Defined bool   `json:"defined"`
		Raw     string `json:"raw"`
	} `json:"constants"`
	Options    map[string]string `json:"options"`
	ConfigMode string            `json:"config_mode"`
	ConfigPath string            `json:"config_path"`
	KeysSet    bool              `json:"keys_set"`
}

var rawRe = regexp.MustCompile(`^(true|false|[0-9]{1,10}|'[A-Za-z0-9 ._-]{0,40}')$`)

// applied says whether a WordPress measure is in effect now.
func (c current) applied(id string) bool {
	switch id {
	case "disable_file_edit":
		return c.Constants["DISALLOW_FILE_EDIT"].Raw == "true"
	case "disable_script_concat":
		return c.Constants["CONCATENATE_SCRIPTS"].Raw == "false"
	case "debug_off":
		return c.Constants["WP_DEBUG"].Raw != "true"
	case "disable_pingbacks":
		return c.Options["default_ping_status"] == "closed" && (c.Options["default_pingback_flag"] == "" || c.Options["default_pingback_flag"] == "0")
	case "config_permissions":
		return c.ConfigMode == "0600" || c.ConfigMode == "0400" || c.ConfigMode == "0440" || c.ConfigMode == "0640"
	case "security_keys":
		return c.KeysSet
	}
	return false
}

var constantOf = map[string][2]string{
	"disable_file_edit":     {"DISALLOW_FILE_EDIT", "true"},
	"disable_script_concat": {"CONCATENATE_SCRIPTS", "false"},
	"debug_off":             {"WP_DEBUG", "false"},
}

// SecurityRequest is a wordpress.security call.
type SecurityRequest struct {
	Target
	// URLPath is the installation's address on the website ("/" or
	// "/blog/"), for the web server rules.
	URLPath  string   `json:"url_path"`
	Op       string   `json:"op"` // status, apply, rollback
	Measures []string `json:"measures"`
}

func security(ctx context.Context, env *host.Env, in SecurityRequest) (map[string]any, error) {
	urlPath, err := webprotect.CleanPath(in.URLPath)
	if err != nil {
		return nil, actions.InvalidParams("url_path: " + err.Error())
	}
	for _, m := range in.Measures {
		if _, ok := measureByID(m); !ok {
			return nil, actions.InvalidParams("measures: " + m + " is not a known measure")
		}
	}
	if in.Op != "status" && len(in.Measures) == 0 {
		return nil, actions.InvalidParams("measures: choose at least one")
	}
	r, dir, err := prepare(ctx, env, in.Target)
	if err != nil {
		return nil, err
	}
	secMu.Lock()
	defer secMu.Unlock()
	key := in.Domain + "/" + in.Path
	st := loadSec(env)
	var cur current
	if err := r.evalResult(ctx, dir, securityScript, &cur); err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, m := range in.Measures {
		want[m] = true
	}
	now := env.Now().UTC().Format(time.RFC3339)
	var problems []string
	switch in.Op {
	case "apply", "rollback":
		apply := in.Op == "apply"
		// Web server rules: one change for all of them.
		var web []string
		for _, m := range in.Measures {
			if mm, _ := measureByID(m); mm.Kind == "web" {
				web = append(web, m)
			}
		}
		if len(web) > 0 {
			if _, err := webprotect.Update(ctx, env, in.Domain, func(s *webprotect.Site) error {
				if s.WordPress == nil {
					s.WordPress = map[string][]string{}
				}
				have := map[string]bool{}
				for _, x := range s.WordPress[urlPath] {
					have[x] = true
				}
				for _, x := range web {
					have[x] = apply
				}
				var list []string
				for _, x := range webprotect.Rules {
					if have[x] {
						list = append(list, x)
					}
				}
				if len(list) == 0 {
					delete(s.WordPress, urlPath)
				} else {
					s.WordPress[urlPath] = list
				}
				return nil
			}); err != nil {
				return nil, err
			}
		}
		rec := st.Installs[key]
		if rec == nil {
			rec = map[string]Previous{}
			st.Installs[key] = rec
		}
		for _, m := range in.Measures {
			mm, _ := measureByID(m)
			if mm.Kind != "wordpress" {
				continue
			}
			var err error
			if apply {
				err = r.applyMeasure(ctx, env, dir, m, cur, rec, now)
			} else {
				err = r.rollbackMeasure(ctx, dir, m, cur, rec)
			}
			if err != nil {
				problems = append(problems, mm.Title+": "+err.Error())
			}
		}
		if len(rec) == 0 {
			delete(st.Installs, key)
		}
		if err := saveSec(env, st); err != nil {
			return nil, err
		}
		if err := r.evalResult(ctx, dir, securityScript, &cur); err != nil {
			return nil, err
		}
	case "status":
	default:
		return nil, actions.InvalidParams("op: status, apply or rollback")
	}
	web := webprotect.Load(env).Sites[in.Domain]
	webOn := map[string]bool{}
	if web != nil {
		for _, x := range web.WordPress[urlPath] {
			webOn[x] = true
		}
	}
	list := []map[string]any{}
	for _, m := range Measures {
		on := webOn[m.ID]
		_, recorded := st.Installs[key][m.ID]
		if m.Kind == "wordpress" {
			on = cur.applied(m.ID)
		}
		list = append(list, map[string]any{"id": m.ID, "title": m.Title, "help": m.Help, "kind": m.Kind, "applied": on,
			"can_rollback": m.Revertible && (m.Kind == "web" && on || recorded)})
	}
	res := map[string]any{"measures": list, "problems": problems}
	if len(problems) > 0 && in.Op != "status" {
		return res, actions.Failed("Some measures could not be changed: "+strings.Join(problems, "; "), "")
	}
	return res, nil
}

func (r *runner) applyMeasure(ctx context.Context, env *host.Env, dir, id string, cur current, rec map[string]Previous, now string) error {
	if cur.applied(id) && id != "security_keys" {
		return nil
	}
	if c, ok := constantOf[id]; ok {
		old := cur.Constants[c[0]]
		prev := Previous{Defined: old.Defined, At: now}
		if old.Defined && rawRe.MatchString(old.Raw) {
			prev.Raw = old.Raw
		}
		if err := r.setConstant(ctx, dir, c[0], c[1]); err != nil {
			return err
		}
		if _, had := rec[id]; !had {
			rec[id] = prev
		}
		return nil
	}
	switch id {
	case "disable_pingbacks":
		prev := Previous{Options: map[string]string{}, At: now}
		for k, v := range map[string]string{"default_ping_status": "closed", "default_pingback_flag": "0"} {
			prev.Options[k] = clip(cur.Options[k], 20)
			if out, err := r.wp(ctx, dir, execx.Opts{}, "option", "update", k, v); err != nil {
				return fmt.Errorf("%s", cliError(out))
			}
		}
		if _, had := rec[id]; !had {
			rec[id] = prev
		}
	case "config_permissions":
		if err := r.chmodConfig(ctx, cur, "0600"); err != nil {
			return err
		}
		if _, had := rec[id]; !had && modeRe.MatchString(cur.ConfigMode) {
			rec[id] = Previous{Mode: cur.ConfigMode, At: now}
		}
	case "security_keys":
		if out, err := r.wp(ctx, dir, execx.Opts{}, "config", "shuffle-salts"); err != nil {
			return fmt.Errorf("%s", cliError(out))
		}
	}
	return nil
}

func (r *runner) rollbackMeasure(ctx context.Context, dir, id string, cur current, rec map[string]Previous) error {
	prev, ok := rec[id]
	if !ok {
		if id == "security_keys" {
			return fmt.Errorf("the earlier keys are not kept, so this cannot be rolled back")
		}
		return nil // not applied by the toolkit: nothing to put back
	}
	if c, ok := constantOf[id]; ok {
		var err error
		if !prev.Defined {
			err = r.deleteConstant(ctx, dir, c[0])
		} else if prev.Raw != "" {
			err = r.setConstant(ctx, dir, c[0], prev.Raw)
		}
		if err != nil {
			return err
		}
		delete(rec, id)
		return nil
	}
	switch id {
	case "disable_pingbacks":
		keys := make([]string, 0, len(prev.Options))
		for k := range prev.Options {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k != "default_ping_status" && k != "default_pingback_flag" {
				continue
			}
			if out, err := r.wp(ctx, dir, execx.Opts{}, "option", "update", k, prev.Options[k]); err != nil {
				return fmt.Errorf("%s", cliError(out))
			}
		}
	case "config_permissions":
		if modeRe.MatchString(prev.Mode) {
			if err := r.chmodConfig(ctx, cur, prev.Mode); err != nil {
				return err
			}
		}
	}
	delete(rec, id)
	return nil
}

func (r *runner) chmodConfig(ctx context.Context, cur current, mode string) error {
	p := filepath.Clean(cur.ConfigPath)
	if !filepath.IsAbs(p) || filepath.Base(p) != "wp-config.php" || !strings.HasPrefix(p, r.root+"/") {
		return fmt.Errorf("wp-config.php was not found inside the website")
	}
	if _, _, err := r.script(ctx, `<?php exit(!is_link(getenv('NIMVOR_FILE')) && chmod(getenv('NIMVOR_FILE'), octdec(getenv('NIMVOR_MODE'))) ? 0 : 1);`, []string{"NIMVOR_FILE=" + p, "NIMVOR_MODE=" + mode}, time.Minute); err != nil {
		return fmt.Errorf("the permissions of wp-config.php could not be changed")
	}
	return nil
}
