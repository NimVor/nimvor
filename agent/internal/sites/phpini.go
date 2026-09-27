package sites

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// PHP directives a website's owner may set for the website's PHP-FPM pool.
// Only directives that exist in PHP 8 are accepted. Each has a type that
// the value must match exactly; the character sets keep a value on one
// line and free of quotes, so it cannot change the pool file around it.
//
// Not offered, on purpose: open_basedir, sys_temp_dir (the website's
// confinement), allow_url_include (includes remote code; deprecated),
// enable_dl (no dl() under PHP-FPM), mysqli.default_pw (the pool file is
// readable by every local account). Paths must be inside the website's
// folder. disable_functions and disable_classes only add to the server's
// list.

type iniKind int

const (
	iniBool iniKind = iota
	iniBoolOrInt
	iniInt
	iniSize
	iniPath
	iniEnum
	iniPattern
	iniList
)

type iniDirective struct {
	kind   iniKind
	lo, hi int64          // iniInt
	enum   []string       // iniEnum
	re     *regexp.Regexp // iniPattern
	// admin: a system-level directive, set with php_admin_value (PHP
	// cannot change it at run time); others with php_value/php_flag.
	admin bool
}

var (
	errLevelRe = regexp.MustCompile(`^[A-Z0-9_ &|~^()!-]{1,120}$`)
	tzRe       = regexp.MustCompile(`^[A-Za-z]+(/[A-Za-z0-9_+-]+){0,2}$|^UTC$`)
	mimeRe     = regexp.MustCompile(`^[a-z]+/[a-z0-9.+-]{1,60}$`)
	cookieDom  = regexp.MustCompile(`^\.?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	cookiePath = regexp.MustCompile(`^/[A-Za-z0-9._~/-]{0,200}$`)
	identRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_\\]{0,100}$`)
	pathRe     = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,400}$`)
	sizeRe     = regexp.MustCompile(`^[0-9]{1,6}[KMG]?$`)
)

// PHPDirectives are the directives a website may set.
var PHPDirectives = map[string]iniDirective{
	// Errors and output
	"display_errors":          {kind: iniBool},
	"display_startup_errors":  {kind: iniBool},
	"error_reporting":         {kind: iniPattern, re: errLevelRe},
	"log_errors":              {kind: iniBool},
	"error_log":               {kind: iniPath},
	"ignore_repeated_errors":  {kind: iniBool},
	"ignore_repeated_source":  {kind: iniBool},
	"report_memleaks":         {kind: iniBool},
	"html_errors":             {kind: iniBool},
	"output_buffering":        {kind: iniBoolOrInt, lo: 0, hi: 1 << 24},
	"implicit_flush":          {kind: iniBool},
	"zlib.output_compression": {kind: iniBoolOrInt, lo: 0, hi: 1 << 20},
	"expose_php":              {kind: iniBool, admin: true},
	"default_mimetype":        {kind: iniPattern, re: mimeRe},
	"precision":               {kind: iniInt, lo: -1, hi: 50},
	"serialize_precision":     {kind: iniInt, lo: -1, hi: 50},
	// Requests and uploads
	"post_max_size":             {kind: iniSize},
	"max_input_time":            {kind: iniInt, lo: -1, hi: 86400},
	"max_input_vars":            {kind: iniInt, lo: 100, hi: 1000000},
	"max_file_uploads":          {kind: iniInt, lo: 1, hi: 1000},
	"file_uploads":              {kind: iniBool, admin: true},
	"upload_tmp_dir":            {kind: iniPath, admin: true},
	"default_socket_timeout":    {kind: iniInt, lo: -1, hi: 3600},
	"allow_url_fopen":           {kind: iniBool, admin: true},
	"variables_order":           {kind: iniPattern, re: regexp.MustCompile(`^[EGPCS]{1,5}$`)},
	"register_argc_argv":        {kind: iniBool},
	"auto_globals_jit":          {kind: iniBool},
	"auto_prepend_file":         {kind: iniPath},
	"auto_append_file":          {kind: iniPath},
	"short_open_tag":            {kind: iniBool},
	"date.timezone":             {kind: iniPattern, re: tzRe},
	"unserialize_callback_func": {kind: iniPattern, re: identRe},
	"disable_functions":         {kind: iniList, admin: true},
	"disable_classes":           {kind: iniList, admin: true},
	// Sessions
	"session.save_path":         {kind: iniPath},
	"session.save_handler":      {kind: iniEnum, enum: []string{"files", "redis", "memcached"}},
	"session.serialize_handler": {kind: iniEnum, enum: []string{"php", "php_binary", "php_serialize"}},
	"session.name":              {kind: iniPattern, re: regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)},
	"session.auto_start":        {kind: iniBool},
	"session.cache_limiter":     {kind: iniEnum, enum: []string{"nocache", "private", "private_no_expire", "public"}},
	"session.cache_expire":      {kind: iniInt, lo: 0, hi: 525600},
	"session.cookie_lifetime":   {kind: iniInt, lo: 0, hi: 31536000},
	"session.cookie_path":       {kind: iniPattern, re: cookiePath},
	"session.cookie_domain":     {kind: iniPattern, re: cookieDom},
	"session.cookie_httponly":   {kind: iniBool},
	"session.cookie_secure":     {kind: iniBool},
	"session.cookie_samesite":   {kind: iniEnum, enum: []string{"Lax", "Strict", "None"}},
	"session.use_cookies":       {kind: iniBool},
	"session.use_only_cookies":  {kind: iniBool},
	"session.use_strict_mode":   {kind: iniBool},
	"session.use_trans_sid":     {kind: iniBool},
	"session.referer_check":     {kind: iniPattern, re: regexp.MustCompile(`^[A-Za-z0-9.:/-]{1,200}$`)},
	"session.gc_probability":    {kind: iniInt, lo: 0, hi: 1000000},
	"session.gc_divisor":        {kind: iniInt, lo: 1, hi: 1000000},
	"session.gc_maxlifetime":    {kind: iniInt, lo: 1, hi: 31536000},
	"url_rewriter.tags":         {kind: iniPattern, re: regexp.MustCompile(`^[a-z]+=[a-z]*(,[a-z]+=[a-z]*){0,20}$`)},
	// MySQLi client defaults
	"mysqli.default_host":   {kind: iniPattern, re: regexp.MustCompile(`^[A-Za-z0-9.:-]{1,253}$`)},
	"mysqli.default_port":   {kind: iniInt, lo: 1, hi: 65535},
	"mysqli.default_socket": {kind: iniPattern, re: pathRe},
	"mysqli.default_user":   {kind: iniPattern, re: regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`)},
	"mysqli.max_links":      {kind: iniInt, lo: -1, hi: 100000, admin: true},
}

// NormalizeINI checks a website's directives and returns them with
// canonical values (booleans as On/Off). root is the website's folder.
func NormalizeINI(ini map[string]string, root string) (map[string]string, error) {
	if len(ini) > len(PHPDirectives) {
		return nil, fail("php.ini", "too many directives")
	}
	out := make(map[string]string, len(ini))
	for k, v := range ini {
		d, ok := PHPDirectives[k]
		field := "php.ini." + k
		if !ok {
			return nil, fail(field, "is not a PHP setting a website may change")
		}
		v = strings.TrimSpace(v)
		bad := func(what string) error { return fail(field, "must be %s", what) }
		switch d.kind {
		case iniBool, iniBoolOrInt:
			switch strings.ToLower(v) {
			case "1", "on", "true", "yes":
				v = "On"
			case "0", "off", "false", "no":
				v = "Off"
			default:
				n, err := strconv.ParseInt(v, 10, 64)
				if d.kind == iniBool || err != nil || n < d.lo || n > d.hi {
					if d.kind == iniBool {
						return nil, bad("On or Off")
					}
					return nil, bad(fmt.Sprintf("On, Off or a number up to %d", d.hi))
				}
			}
		case iniInt:
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < d.lo || n > d.hi {
				return nil, bad(fmt.Sprintf("a whole number between %d and %d", d.lo, d.hi))
			}
			v = strconv.FormatInt(n, 10)
		case iniSize:
			v = strings.ToUpper(v)
			if !sizeRe.MatchString(v) {
				return nil, bad("a size such as 64M")
			}
		case iniPath:
			if !pathRe.MatchString(v) || strings.Contains(v, "..") || filepath.Clean(v) != v {
				return nil, bad("an absolute path")
			}
			if !strings.HasPrefix(v, filepath.Clean(root)+"/") {
				return nil, bad("inside the website's folder " + root)
			}
		case iniEnum:
			found := false
			for _, e := range d.enum {
				found = found || e == v
			}
			if !found {
				return nil, bad("one of " + strings.Join(d.enum, ", "))
			}
		case iniPattern:
			if !d.re.MatchString(v) {
				return nil, bad("a valid value")
			}
		case iniList:
			v = strings.ReplaceAll(v, " ", "")
			if v != "" {
				items := strings.Split(v, ",")
				if len(items) > 200 {
					return nil, bad("at most 200 names")
				}
				for _, it := range items {
					if !identRe.MatchString(it) {
						return nil, bad("names separated by commas")
					}
				}
			}
		}
		out[k] = v
	}
	return out, nil
}

// iniLine is one directive of a pool.
type iniLine struct {
	key, value string
	admin      bool
}

// poolINI merges WhaCP's settings of a website's pool with the website's
// own directives (which replace a default of the same name; disable_*
// lists are joined) and renders them in a stable order.
func poolINI(defaults []iniLine, ini map[string]string) string {
	byKey := map[string]int{}
	lines := append([]iniLine{}, defaults...)
	for i, l := range lines {
		byKey[l.key] = i
	}
	keys := make([]string, 0, len(ini))
	for k := range ini {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v, d := ini[k], PHPDirectives[k]
		if d.kind == iniList {
			if i, ok := byKey[k]; ok {
				v = mergeList(lines[i].value, v)
			}
			if v == "" {
				continue
			}
		}
		l := iniLine{key: k, value: v, admin: d.admin}
		if i, ok := byKey[k]; ok {
			// Defaults WhaCP sets as admin values stay admin values.
			l.admin = l.admin || lines[i].admin
			lines[i] = l
			continue
		}
		byKey[k] = len(lines)
		lines = append(lines, l)
	}
	var b strings.Builder
	for _, l := range lines {
		prefix := "php_"
		if l.admin {
			prefix = "php_admin_"
		}
		if l.value == "On" || l.value == "Off" {
			fmt.Fprintf(&b, "%sflag[%s] = %s\n", prefix, l.key, strings.ToLower(l.value))
		} else {
			fmt.Fprintf(&b, "%svalue[%s] = %s\n", prefix, l.key, l.value)
		}
	}
	return b.String()
}

func mergeList(a, b string) string {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(a+","+b, ",") {
		if part = strings.TrimSpace(part); part != "" && !seen[part] {
			seen[part] = true
			out = append(out, part)
		}
	}
	return strings.Join(out, ",")
}
