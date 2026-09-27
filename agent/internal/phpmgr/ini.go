package phpmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/changeset"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// php.ini settings are changed in WhaCP's own file, loaded after the
// distribution's php.ini, never in php.ini itself. Only the directives
// below are accepted, each with its own check: others (extension=,
// auto_prepend_file, open_basedir, session.save_path, sendmail_path, …)
// could load code into the root-run FPM master or weaken the websites'
// isolation.

// iniKind is how a directive's value is checked.
type iniKind int

const (
	iniBool iniKind = iota
	iniInt
	iniSize
	iniChoice
	iniTimezone
	iniErrorLevel
)

// IniDirective is a php.ini setting WhaCP lets administrators change.
type IniDirective struct {
	Key         string   `json:"key"`
	Kind        string   `json:"kind"`
	Description string   `json:"description"`
	Choices     []string `json:"choices,omitempty"`
	Min         int64    `json:"min,omitempty"`
	Max         int64    `json:"max,omitempty"`
	// PerWebsite: WhaCP websites set it in their own pool, so this value
	// applies to other pools and to the command line.
	PerWebsite bool `json:"per_website,omitempty"`
	kind       iniKind
}

func d(key string, k iniKind, desc string, min, max int64, choices ...string) IniDirective {
	name := map[iniKind]string{iniBool: "bool", iniInt: "int", iniSize: "size", iniChoice: "choice", iniTimezone: "timezone", iniErrorLevel: "error_level"}[k]
	return IniDirective{Key: key, Kind: name, Description: desc, Min: min, Max: max, Choices: choices, kind: k}
}

func perSite(x IniDirective) IniDirective { x.PerWebsite = true; return x }

// IniDirectives are the settings offered, in display order.
var IniDirectives = []IniDirective{
	perSite(d("memory_limit", iniSize, "Memory a script may use (-1: no limit)", 0, 0)),
	perSite(d("upload_max_filesize", iniSize, "Largest uploaded file", 0, 0)),
	perSite(d("post_max_size", iniSize, "Largest request body (at least the upload size)", 0, 0)),
	perSite(d("max_execution_time", iniInt, "Seconds a script may run (0: no limit)", 0, 86400)),
	d("max_input_time", iniInt, "Seconds to parse request data (-1: max_execution_time)", -1, 86400),
	d("max_input_vars", iniInt, "Input variables per request", 100, 1000000),
	d("max_input_nesting_level", iniInt, "Nesting depth of input arrays", 16, 1024),
	d("max_file_uploads", iniInt, "Files per request", 1, 1000),
	d("file_uploads", iniBool, "Allow file uploads", 0, 0),
	d("default_socket_timeout", iniInt, "Seconds to wait for network streams", 1, 3600),
	d("allow_url_fopen", iniBool, "Open URLs like files", 0, 0),
	d("date.timezone", iniTimezone, "Default time zone", 0, 0),
	d("default_charset", iniChoice, "Default character set", 0, 0, "UTF-8", "ISO-8859-1", "windows-1252"),
	perSite(d("display_errors", iniBool, "Show errors in pages (development only)", 0, 0)),
	d("display_startup_errors", iniBool, "Show start-up errors (development only)", 0, 0),
	perSite(d("log_errors", iniBool, "Write errors to the error log", 0, 0)),
	d("error_reporting", iniErrorLevel, "Which errors are reported", 0, 0),
	d("html_errors", iniBool, "Format error messages as HTML", 0, 0),
	d("short_open_tag", iniBool, "Accept <? as the PHP opening tag", 0, 0),
	d("output_buffering", iniChoice, "Output buffering", 0, 0, "Off", "On", "4096", "8192", "16384"),
	d("zlib.output_compression", iniBool, "Compress output with zlib", 0, 0),
	d("expose_php", iniBool, "Send the X-Powered-By header", 0, 0),
	d("realpath_cache_size", iniSize, "Path cache size", 0, 0),
	d("realpath_cache_ttl", iniInt, "Seconds paths stay cached", 0, 86400),
	d("session.gc_maxlifetime", iniInt, "Seconds before idle session data is removed", 60, 31536000),
	d("session.cookie_httponly", iniBool, "Session cookie not readable by scripts", 0, 0),
	d("session.cookie_secure", iniBool, "Session cookie over HTTPS only", 0, 0),
	d("session.cookie_samesite", iniChoice, "SameSite attribute of the session cookie", 0, 0, "Lax", "Strict", "None"),
	d("session.use_strict_mode", iniBool, "Refuse session IDs the server did not create", 0, 0),
	d("pcre.jit", iniBool, "PCRE just-in-time compilation", 0, 0),
	d("opcache.enable", iniBool, "OPcache for PHP-FPM", 0, 0),
	d("opcache.enable_cli", iniBool, "OPcache on the command line", 0, 0),
	d("opcache.memory_consumption", iniInt, "OPcache memory (MB)", 8, 4096),
	d("opcache.interned_strings_buffer", iniInt, "OPcache string memory (MB)", 1, 512),
	d("opcache.max_accelerated_files", iniInt, "Scripts OPcache keeps", 200, 1000000),
	d("opcache.validate_timestamps", iniBool, "Check scripts for changes", 0, 0),
	d("opcache.revalidate_freq", iniInt, "Seconds between checks for changed scripts", 0, 86400),
	d("opcache.jit", iniChoice, "OPcache JIT", 0, 0, "disable", "off", "tracing", "function"),
	d("opcache.jit_buffer_size", iniSize, "JIT buffer size (0: JIT off)", 0, 0),
}

func findDirective(key string) (IniDirective, bool) {
	for _, x := range IniDirectives {
		if x.Key == key {
			return x, true
		}
	}
	return IniDirective{}, false
}

var (
	sizeRe  = regexp.MustCompile(`^[0-9]{1,7}[KMG]?$`)
	tzRe    = regexp.MustCompile(`^(UTC|[A-Z][A-Za-z_]+(/[A-Za-z0-9_+-]+){1,2})$`)
	errLvRe = regexp.MustCompile(`^E_[A-Z_]+( *[&|] *~? *E_[A-Z_]+)*$`)
	lineRe  = regexp.MustCompile(`^([a-z0-9_.]+)\s*=\s*(.*?)\s*$`)
)

// check validates a value; tz reports whether a time zone exists.
func (x IniDirective) check(v string, tz func(string) bool) (string, error) {
	v = strings.Trim(v, `"`)
	switch x.kind {
	case iniBool:
		switch strings.ToLower(v) {
		case "on", "1", "true", "yes":
			return "On", nil
		case "off", "0", "false", "no", "":
			return "Off", nil
		}
		return "", fmt.Errorf("%s must be On or Off", x.Key)
	case iniInt:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < x.Min || n > x.Max {
			return "", fmt.Errorf("%s must be a whole number from %d to %d", x.Key, x.Min, x.Max)
		}
		return strconv.FormatInt(n, 10), nil
	case iniSize:
		v = strings.ToUpper(v)
		if (x.Key == "memory_limit" && v == "-1") || sizeRe.MatchString(v) {
			return v, nil
		}
		return "", fmt.Errorf("%s must be a size such as 256M or 2G", x.Key)
	case iniChoice:
		for _, c := range x.Choices {
			if strings.EqualFold(c, v) {
				return c, nil
			}
		}
		return "", fmt.Errorf("%s must be one of: %s", x.Key, strings.Join(x.Choices, ", "))
	case iniTimezone:
		if tzRe.MatchString(v) && tz(v) {
			return `"` + v + `"`, nil
		}
		return "", fmt.Errorf("%s: %q is not a time zone", x.Key, v)
	case iniErrorLevel:
		if errLvRe.MatchString(v) {
			return v, nil
		}
		return "", fmt.Errorf("%s must combine E_ constants, e.g. E_ALL & ~E_DEPRECATED", x.Key)
	}
	return "", fmt.Errorf("%s cannot be set", x.Key)
}

// ParseIni checks the text of WhaCP's php.ini file: comments (;) and
// "directive = value" lines of allowed directives, each once.
func ParseIni(content string, tz func(string) bool) (map[string]string, error) {
	out := map[string]string{}
	if len(content) > 64*1024 {
		return nil, fmt.Errorf("the settings are too long")
	}
	for i, l := range strings.Split(content, "\n") {
		l = strings.TrimSpace(strings.TrimSuffix(l, "\r"))
		if l == "" || strings.HasPrefix(l, ";") || strings.HasPrefix(l, "#") {
			continue
		}
		m := lineRe.FindStringSubmatch(l)
		if m == nil {
			return nil, fmt.Errorf("line %d: expected \"directive = value\"", i+1)
		}
		x, ok := findDirective(m[1])
		if !ok {
			return nil, fmt.Errorf("line %d: %s cannot be set here (only the listed settings are allowed)", i+1, m[1])
		}
		if _, dup := out[x.Key]; dup {
			return nil, fmt.Errorf("line %d: %s is set twice", i+1, x.Key)
		}
		v, err := x.check(m[2], tz)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", i+1, err)
		}
		out[x.Key] = v
	}
	return out, nil
}

// RenderIni writes the checked settings in the catalog's order.
func RenderIni(v string, values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "; Managed by WhaCP (PHP → %s → php.ini). Do not edit: changes are overwritten.\n", v)
	b.WriteString("; Loaded after the distribution's php.ini; websites' own settings (their pools) still win.\n")
	for _, x := range IniDirectives {
		if val, ok := values[x.Key]; ok {
			fmt.Fprintf(&b, "%s = %s\n", x.Key, val)
		}
	}
	return b.String()
}

// IniFiles are WhaCP's php.ini files for version v: PHP-FPM and the command
// line on Debian (cron jobs use the same values), php.d on RHEL.
func IniFiles(env *host.Env, v string) []string {
	if rhel(env) {
		return []string{IniDir(env, v) + "/99-whacp.ini"}
	}
	return []string{"/etc/php/" + v + "/fpm/conf.d/99-whacp.ini", "/etc/php/" + v + "/cli/conf.d/99-whacp.ini"}
}

// fpmWarnings turns warnings in php-fpm -t output into an error: PHP-FPM
// accepts invalid values (e.g. an unknown time zone) and only warns.
func fpmWarnings(ctx context.Context, env *host.Env, v string) error {
	if err := env.FPMTest(ctx, v); err != nil {
		return err
	}
	out, _ := env.Runner.Run(ctx, fmt.Sprintf(env.M.PHPFPMBinary, v), "-t")
	var warn []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, "PHP Warning") || strings.Contains(l, "PHP Fatal") || strings.Contains(l, "PHP Startup") {
			warn = append(warn, strings.TrimSpace(l))
		}
	}
	if len(warn) > 0 {
		return &changeset.ValidationError{Step: "php-fpm" + v + " -t", Output: strings.Join(warn, "\n"), Err: fmt.Errorf("PHP reported problems with the settings")}
	}
	return nil
}

// fpmInfo returns the values PHP-FPM uses for the given directives
// (php-fpm -i: "key => local => master").
func fpmInfo(ctx context.Context, env *host.Env, v string) map[string]string {
	out, _ := env.Runner.Run(ctx, fmt.Sprintf(env.M.PHPFPMBinary, v), "-i")
	res := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		parts := strings.Split(l, " => ")
		if len(parts) == 3 {
			if _, ok := findDirective(parts[0]); ok {
				res[parts[0]] = strings.TrimSpace(parts[2])
			}
		}
	}
	return res
}

func registerIni(r *actions.Registry, env *host.Env) {
	tz := func(z string) bool { return z == "UTC" || env.Root.Exists("/usr/share/zoneinfo/"+z) }
	type req struct {
		Version string `json:"version"`
		Content string `json:"content"`
	}
	r.Register(actions.Action{Name: "php.ini_get", Description: "WhaCP's php.ini settings for a PHP version, the settings that may be changed and the values PHP-FPM uses now.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in req
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.PHPVersion("version", in.Version); err != nil {
			return nil, host.Invalid(err)
		}
		files := IniFiles(env, in.Version)
		b, _ := env.Root.ReadFile(files[0])
		values, _ := ParseIni(string(b), tz)
		return map[string]any{"file": files[0], "content": string(b), "values": values, "directives": IniDirectives, "effective": fpmInfo(ctx, env, in.Version)}, nil
	}})
	r.Register(actions.Action{Name: "php.ini_set", Mutating: true, Description: "Write WhaCP's php.ini settings for a PHP version (allowed directives only; empty removes them), checked with php-fpm -t (warnings count as errors) and applied with a reload; rolled back on failure.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in req
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.PHPVersion("version", in.Version); err != nil {
			return nil, host.Invalid(err)
		}
		if !env.Runner.Available(fmt.Sprintf(env.M.PHPFPMBinary, in.Version)) {
			return nil, actions.Conflict("PHP " + in.Version + " (FPM) is not installed.")
		}
		values, err := ParseIni(in.Content, tz)
		if err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		body := RenderIni(in.Version, values)
		defer env.LockWebConfig()()
		set := env.Changes("php-ini-" + in.Version)
		for i, f := range IniFiles(env, in.Version) {
			if !env.Root.Exists(filepath.Dir(f)) {
				if i == 0 {
					return nil, actions.Conflict("PHP " + in.Version + " has no " + filepath.Dir(f) + " folder.")
				}
				continue // PHP-FPM without the command line
			}
			if body == "" {
				set.Remove(f)
			} else {
				set.Write(f, []byte(body), 0o644, agentfs.RootOwner)
			}
		}
		a, err := set.Commit(func() error { return fpmWarnings(ctx, env, in.Version) })
		if err != nil {
			return nil, host.FailWithValidation("The php.ini settings were not applied", err)
		}
		if len(a.Changed) > 0 {
			if err := env.ReloadFPM(ctx, in.Version); err != nil {
				_ = a.Rollback()
				_ = env.ReloadFPM(ctx, in.Version)
				return nil, host.FailWithValidation("PHP-FPM "+in.Version+" did not reload; the previous settings were restored", err)
			}
		}
		return map[string]any{"values": values, "changed": a.Changed}, nil
	}})
}
