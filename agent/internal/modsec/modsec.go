// Package modsec manages ModSecurity (libmodsecurity 3 with its Nginx
// connector) for WhaCP's websites.
//
// Layout (all WhaCP's own files):
//
//	<nginx>/whacp-modsec/main.conf    base engine settings + enabled vendors
//	<nginx>/whacp-modsec/crs.conf     OWASP CRS (the distribution's package)
//	<nginx>/whacp-modsec/custom.conf  the administrator's own rules
//	<nginx>/whacp-modsec/sites/D.conf a website's mode and exclusions
//	<enabled>/whacp-00-modsecurity.conf  http{}: rules loaded once, off
//
// The rules are loaded once at http level with "modsecurity off"; a
// website's vhost includes its own file by a glob that matches nothing
// while the website has ModSecurity off, and the file turns it on
// (blocking or detection only), removes whole vendors by their rule ID
// ranges and disables single rules. Every change is checked with nginx -t
// and rolled back on failure. Administrators only: the panel never lets
// hosting accounts change these rules.
package modsec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/capability"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Modes of a website.
const (
	ModeOff    = "off"
	ModeDetect = "detect"
	ModeOn     = "on"
)

// Vendor is a source of rules, identified by its rule ID range.
type Vendor struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	First       int    `json:"first_id"`
	Last        int    `json:"last_id"`
}

// Vendors are the supported rule sources.
var Vendors = []Vendor{
	{ID: "crs", Name: "OWASP Core Rule Set", Description: "Generic protection against SQL injection, XSS, file inclusion, remote code execution and protocol abuse, from the distribution's package (updated with it).", First: 900000, Last: 999999},
	{ID: "custom", Name: "Custom rules", Description: "Rules the server administrators write here (IDs 10000–99999).", First: 10000, Last: 99999},
}

func vendorByID(id string) (Vendor, bool) {
	for _, v := range Vendors {
		if v.ID == id {
			return v, true
		}
	}
	return Vendor{}, false
}

// Config is the server-wide configuration.
type Config struct {
	Enabled bool `json:"enabled"`
	// DetectionOnly: websites set to block only log until an
	// administrator allows blocking (new installations start so; older
	// configurations without the setting keep blocking).
	DetectionOnly bool `json:"detection_only"`
	CRS           struct {
		Enabled  bool `json:"enabled"`
		Paranoia int  `json:"paranoia"`
		Inbound  int  `json:"inbound_threshold"`
		Outbound int  `json:"outbound_threshold"`
	} `json:"crs"`
	Custom struct {
		Enabled bool `json:"enabled"`
	} `json:"custom"`
}

// Validate checks the configuration.
func (c *Config) Validate() error {
	if c.CRS.Paranoia == 0 {
		c.CRS.Paranoia = 1
	}
	if c.CRS.Inbound == 0 {
		c.CRS.Inbound = 5
	}
	if c.CRS.Outbound == 0 {
		c.CRS.Outbound = 4
	}
	if c.CRS.Paranoia < 1 || c.CRS.Paranoia > 4 {
		return fmt.Errorf("paranoia level: 1 to 4")
	}
	if c.CRS.Inbound < 2 || c.CRS.Inbound > 1000 || c.CRS.Outbound < 2 || c.CRS.Outbound > 1000 {
		return fmt.Errorf("anomaly thresholds: 2 to 1000")
	}
	return nil
}

// Site is a website's setting.
type Site struct {
	Mode string `json:"mode"`
	// Exclude lists vendors whose rules do not apply to the website.
	Exclude []string `json:"exclude,omitempty"`
	// Disabled lists single rule IDs turned off for the website.
	Disabled []int `json:"disabled_rules,omitempty"`
}

// Normalize validates and sorts a website's setting.
func (s *Site) Normalize() error {
	if s.Mode != ModeOff && s.Mode != ModeDetect && s.Mode != ModeOn {
		return fmt.Errorf("mode: off, detect or on")
	}
	seen := map[string]bool{}
	var ex []string
	for _, v := range s.Exclude {
		if _, ok := vendorByID(v); !ok {
			return fmt.Errorf("unknown rule vendor %q", v)
		}
		if !seen[v] {
			seen[v] = true
			ex = append(ex, v)
		}
	}
	sort.Strings(ex)
	s.Exclude = ex
	if len(s.Disabled) > 200 {
		return fmt.Errorf("at most 200 disabled rules")
	}
	ids := map[int]bool{}
	var d []int
	for _, id := range s.Disabled {
		if id < 1 || id > 9999999 {
			return fmt.Errorf("rule ID %d is out of range", id)
		}
		if !ids[id] {
			ids[id] = true
			d = append(d, id)
		}
	}
	sort.Ints(d)
	s.Disabled = d
	return nil
}

// State is what the agent keeps.
type State struct {
	Config Config          `json:"config"`
	Sites  map[string]Site `json:"sites"`
}

// Paths.

// Dir holds WhaCP's ModSecurity files.
func Dir(e *host.Env) string { return filepath.Join(filepath.Dir(e.M.NginxAvailable), "whacp-modsec") }

// SitePath is a website's file.
func SitePath(e *host.Env, domain string) string {
	return filepath.Join(Dir(e), "sites", domain+".conf")
}

// SiteInclude is the vhost line: a glob, so it matches nothing while the
// website has no file (ModSecurity off or not installed).
func SiteInclude(e *host.Env, domain string) string {
	p := SitePath(e, domain)
	return "    include " + strings.TrimSuffix(p, "f") + "[f];\n"
}

// HTTPPath is the http{} level file.
func HTTPPath(e *host.Env) string {
	return filepath.Join(e.M.NginxEnabled, "whacp-00-modsecurity.conf")
}

// AuditLog is where blocked and detected requests are written (next to
// Nginx's logs, so SELinux lets Nginx write it; not *.log, so logrotate's
// Nginx rule leaves it alone: the agent trims it).
func AuditLog(*host.Env) string { return "/var/log/nginx/whacp-modsec-audit.json" }

func statePath(e *host.Env) string { return filepath.Join(e.Cfg.StateDir, "modsec.json") }

// Load returns the saved state.
func Load(e *host.Env) State {
	st := State{Sites: map[string]Site{}}
	st.Config.CRS.Enabled = true
	st.Config.CRS.Paranoia, st.Config.CRS.Inbound, st.Config.CRS.Outbound = 1, 5, 4
	if b, err := e.Root.ReadFile(statePath(e)); err == nil {
		json.Unmarshal(b, &st)
	} else {
		st.Config.DetectionOnly = true // a new installation starts detecting only
	}
	if st.Sites == nil {
		st.Sites = map[string]Site{}
	}
	return st
}

// Detection.

// Connector reports the ModSecurity connector as a capability: missing,
// installed (not loaded or not usable, with the reason) or functional,
// whether it came from a package, was built from source or copied by hand.
func Connector(ctx context.Context, e *host.Env, testLoad bool) capability.Status {
	n, ok := capability.Inspect(ctx, e)
	if !ok {
		return capability.Status{ID: capability.ModSecurity.ID, Name: capability.ModSecurity.Name, State: capability.Missing, Reason: "Nginx is not installed."}
	}
	return capability.DetectModule(ctx, e, n, capability.NewLinker(e), capability.ModSecurity, testLoad)
}

// ModuleLoaded reports (cheaply, for every configuration change) whether
// Nginx loads the ModSecurity connector: a load_module line in the usual
// module configuration, or the connector built into nginx.
func ModuleLoaded(e *host.Env) bool {
	if e.Runner.Available("nginx") {
		if out, err := e.Runner.Run(context.Background(), "nginx", "-V"); err == nil {
			for _, a := range capability.ParseV(string(out)).AddModules {
				if capability.ModSecurity.Static.MatchString(a) {
					return true
				}
			}
		}
	}
	for _, g := range []string{"/etc/nginx/modules-enabled/*.conf", "/usr/share/nginx/modules/*.conf", "/etc/nginx/conf.modules.d/*.conf", "/etc/nginx/nginx.conf"} {
		files, _ := e.Root.Glob(g)
		for _, f := range files {
			if b, err := e.Root.ReadFile(f); err == nil && bytes.Contains(b, []byte("ngx_http_modsecurity_module")) && !commentedOnly(b) {
				return true
			}
		}
	}
	return false
}

func commentedOnly(b []byte) bool {
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, "ngx_http_modsecurity_module") && !strings.HasPrefix(strings.TrimSpace(l), "#") {
			return false
		}
	}
	return true
}

// CRS is where the Core Rule Set is installed.
type CRS struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Rules     string `json:"rules_dir,omitempty"`
	Setup     string `json:"setup_file,omitempty"`
	before    string
	after     string
}

var crsLocations = []struct{ rules, setupDir string }{
	{"/usr/share/modsecurity-crs/rules", "/etc/modsecurity/crs"},
	{"/usr/share/mod_modsecurity_crs/rules", "/etc/httpd/modsecurity.d"},
	{"/usr/share/modsecurity-crs/rules", "/usr/share/modsecurity-crs"},
}

var crsVersionRe = regexp.MustCompile(`(?m)^[^#]*setvar:'?tx\.crs_setup_version=(\d)(\d)(\d+)`)

// FindCRS locates the Core Rule Set.
func FindCRS(e *host.Env) CRS {
	for _, l := range crsLocations {
		rules, _ := e.Root.Glob(filepath.Join(l.rules, "*.conf"))
		setup := filepath.Join(l.setupDir, "crs-setup.conf")
		if len(rules) == 0 || !e.Root.Exists(setup) {
			continue
		}
		c := CRS{Available: true, Rules: l.rules, Setup: setup}
		if b, err := e.Root.ReadFile(setup); err == nil {
			if m := crsVersionRe.FindSubmatch(b); m != nil {
				c.Version = string(m[1]) + "." + string(m[2]) + "." + string(m[3])
			}
		}
		for _, n := range []string{"REQUEST-900-EXCLUSION-RULES-BEFORE-CRS.conf"} {
			if p := filepath.Join(l.setupDir, n); e.Root.Exists(p) {
				c.before = p
			}
		}
		if p := filepath.Join(l.setupDir, "RESPONSE-999-EXCLUSION-RULES-AFTER-CRS.conf"); e.Root.Exists(p) {
			c.after = p
		}
		return c
	}
	// A release WhaCP installed (when there is no distribution package).
	return whacpCRS(e)
}

// Rendering.

const header = "# Managed by WhaCP (Security → ModSecurity). Changes here are overwritten.\n"

// RenderMain is the base configuration with the enabled vendors.
func RenderMain(e *host.Env, c Config, crs CRS) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString(`SecRuleEngine On
SecRequestBodyAccess On
SecRule REQUEST_HEADERS:Content-Type "^(?:application(?:/soap\+|/)|text/)xml" "id:200000,phase:1,t:none,t:lowercase,pass,nolog,ctl:requestBodyProcessor=XML"
SecRule REQUEST_HEADERS:Content-Type "^application/json" "id:200001,phase:1,t:none,t:lowercase,pass,nolog,ctl:requestBodyProcessor=JSON"
SecRequestBodyLimit 13107200
SecRequestBodyNoFilesLimit 1048576
SecRequestBodyLimitAction ProcessPartial
SecRule REQBODY_ERROR "!@eq 0" "id:200002,phase:2,t:none,log,deny,status:400,msg:'Failed to parse request body.'"
SecRule MULTIPART_STRICT_ERROR "!@eq 0" "id:200003,phase:2,t:none,log,deny,status:400,msg:'Multipart request body failed strict validation.'"
SecResponseBodyAccess Off
SecTmpDir /tmp/
SecArgumentSeparator &
SecCookieFormat 0
SecAuditEngine RelevantOnly
SecAuditLogRelevantStatus "^(?:5|4(?!04))"
SecAuditLogParts ABHZ
SecAuditLogType Serial
SecAuditLogFormat JSON
`)
	fmt.Fprintf(&b, "SecAuditLog %s\n", AuditLog(e))
	if c.CRS.Enabled && crs.Available {
		fmt.Fprintf(&b, "Include %s\n", filepath.Join(Dir(e), "crs.conf"))
	}
	if c.Custom.Enabled && e.Root.Exists(filepath.Join(Dir(e), "custom.conf")) {
		fmt.Fprintf(&b, "Include %s\n", filepath.Join(Dir(e), "custom.conf"))
	}
	return b.String()
}

// RenderCRS includes the Core Rule Set with WhaCP's settings.
func RenderCRS(c Config, crs CRS) string {
	var b strings.Builder
	b.WriteString(header)
	fmt.Fprintf(&b, "Include %s\n", crs.Setup)
	fmt.Fprintf(&b, "SecAction \"id:900000,phase:1,nolog,pass,t:none,setvar:tx.paranoia_level=%d,setvar:tx.blocking_paranoia_level=%d\"\n", c.CRS.Paranoia, c.CRS.Paranoia)
	fmt.Fprintf(&b, "SecAction \"id:900110,phase:1,nolog,pass,t:none,setvar:tx.inbound_anomaly_score_threshold=%d,setvar:tx.outbound_anomaly_score_threshold=%d\"\n", c.CRS.Inbound, c.CRS.Outbound)
	if crs.before != "" {
		fmt.Fprintf(&b, "Include %s\n", crs.before)
	}
	fmt.Fprintf(&b, "Include %s/*.conf\n", crs.Rules)
	if crs.after != "" {
		fmt.Fprintf(&b, "Include %s\n", crs.after)
	}
	return b.String()
}

// RenderHTTP is the http{} level file.
func RenderHTTP(e *host.Env) string {
	return header + "modsecurity off;\nmodsecurity_rules_file " + filepath.Join(Dir(e), "main.conf") + ";\n"
}

// RenderSite is a website's file ("" when ModSecurity is off for it).
func RenderSite(s Site) string {
	if s.Mode != ModeDetect && s.Mode != ModeOn {
		return ""
	}
	var rules []string
	if s.Mode == ModeDetect {
		rules = append(rules, "SecRuleEngine DetectionOnly")
	}
	for _, id := range s.Exclude {
		v, _ := vendorByID(id)
		rules = append(rules, fmt.Sprintf("SecRuleRemoveById %d-%d", v.First, v.Last))
	}
	for i := 0; i < len(s.Disabled); i += 20 {
		j := min(i+20, len(s.Disabled))
		ids := make([]string, 0, j-i)
		for _, id := range s.Disabled[i:j] {
			ids = append(ids, strconv.Itoa(id))
		}
		rules = append(rules, "SecRuleRemoveById "+strings.Join(ids, " "))
	}
	var b strings.Builder
	b.WriteString(header + "modsecurity on;\n")
	if len(rules) > 0 {
		b.WriteString("modsecurity_rules '\n")
		for _, r := range rules {
			b.WriteString("    " + r + "\n")
		}
		b.WriteString("';\n")
	}
	return b.String()
}

// Custom rules.

// MaxCustom bounds the custom rules file.
const MaxCustom = 64 << 10

var (
	customDirectives = map[string]bool{
		"secrule": true, "secaction": true, "secmarker": true,
		"secruleremovebyid": true, "secruleremovebytag": true, "secruleremovebymsg": true,
		"secruleupdatetargetbyid": true, "secruleupdatetargetbytag": true, "secruleupdatetargetbymsg": true,
		"secruleupdateactionbyid": true,
	}
	idRe        = regexp.MustCompile(`(?i)\bid\s*:\s*'?(\d+)`)
	forbiddenRe = regexp.MustCompile(`(?i)\bexec\s*:|@inspectFile|FromFile|@gsbLookup|\bSecRemoteRules|\bInclude`)
)

// CheckCustom validates the administrator's rules: only rule directives,
// every SecRule/SecAction with an ID in the custom range, nothing that
// runs programs or reads files. Nginx then checks the syntax.
func CheckCustom(text string) ([]int, error) {
	if len(text) > MaxCustom {
		return nil, fmt.Errorf("the rules are larger than %d KiB", MaxCustom>>10)
	}
	if strings.ContainsRune(text, 0) {
		return nil, fmt.Errorf("the rules contain a NUL byte")
	}
	if m := forbiddenRe.FindString(text); m != "" {
		return nil, fmt.Errorf("%q is not allowed in custom rules (no programs, files or includes)", m)
	}
	v, _ := vendorByID("custom")
	var ids []int
	var directives []string
	var cur strings.Builder
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64<<10), MaxCustom+1)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		if cur.Len() == 0 && (strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#")) {
			continue
		}
		if strings.HasSuffix(line, "\\") {
			cur.WriteString(strings.TrimSuffix(line, "\\") + " ")
			continue
		}
		cur.WriteString(line)
		directives = append(directives, strings.TrimSpace(cur.String()))
		cur.Reset()
	}
	if cur.Len() > 0 {
		return nil, fmt.Errorf("the last rule ends with a line continuation")
	}
	for i, d := range directives {
		name := strings.ToLower(strings.Fields(d)[0])
		if !customDirectives[name] {
			return nil, fmt.Errorf("rule %d: %s is not allowed here (rule directives only: SecRule, SecAction, SecMarker, SecRuleRemove…, SecRuleUpdate…)", i+1, strings.Fields(d)[0])
		}
		if name != "secrule" && name != "secaction" {
			continue
		}
		m := idRe.FindAllStringSubmatch(d, -1)
		if len(m) != 1 {
			return nil, fmt.Errorf("rule %d: every SecRule and SecAction needs one id", i+1)
		}
		id, _ := strconv.Atoi(m[0][1])
		if id < v.First || id > v.Last {
			return nil, fmt.Errorf("rule %d: id %d is outside the custom range %d–%d", i+1, id, v.First, v.Last)
		}
		for _, x := range ids {
			if x == id {
				return nil, fmt.Errorf("rule %d: id %d is used twice", i+1, id)
			}
		}
		ids = append(ids, id)
	}
	return ids, nil
}
