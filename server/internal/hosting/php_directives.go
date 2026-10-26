package hosting

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PhpDirective is one php.ini directive a website owner may set for the website
// (PhpDirectives::ALL): its group in the editor, the kind of value and a hint.
type PhpDirective struct{ Key, Group, Kind, Hint string }

// PhpDirectives lists them in the PHP panel's order. The agent checks the
// values again when they are applied.
var PhpDirectives = []PhpDirective{
	{"display_errors", "Errors and logging", "bool", "Show errors in pages (Off on live sites)"},
	{"display_startup_errors", "Errors and logging", "bool", ""},
	{"error_reporting", "Errors and logging", "re:/^[A-Z0-9_ &|~^()!-]{1,120}$/", "e.g. E_ALL & ~E_DEPRECATED"},
	{"log_errors", "Errors and logging", "bool", ""},
	{"error_log", "Errors and logging", "path", "A file in the website folder"},
	{"ignore_repeated_errors", "Errors and logging", "bool", ""},
	{"ignore_repeated_source", "Errors and logging", "bool", ""},
	{"report_memleaks", "Errors and logging", "bool", ""},
	{"html_errors", "Errors and logging", "bool", ""},
	{"post_max_size", "Requests and uploads", "size", "e.g. 128M"},
	{"max_input_time", "Requests and uploads", "int:-1:86400", "Seconds"},
	{"max_input_vars", "Requests and uploads", "int:100:1000000", ""},
	{"max_file_uploads", "Requests and uploads", "int:1:1000", ""},
	{"file_uploads", "Requests and uploads", "bool", ""},
	{"upload_tmp_dir", "Requests and uploads", "path", "A folder in the website folder"},
	{"default_socket_timeout", "Requests and uploads", "int:-1:3600", "Seconds"},
	{"allow_url_fopen", "Requests and uploads", "bool", ""},
	{"variables_order", "Requests and uploads", "re:/^[EGPCS]{1,5}$/", "e.g. GPCS"},
	{"register_argc_argv", "Requests and uploads", "bool", ""},
	{"auto_globals_jit", "Requests and uploads", "bool", ""},
	{"output_buffering", "Output", "boolint", "On, Off or bytes (e.g. 4096)"},
	{"implicit_flush", "Output", "bool", ""},
	{"zlib.output_compression", "Output", "boolint", "On, Off or a buffer size"},
	{"expose_php", "Output", "bool", "Send the X-Powered-By header"},
	{"default_mimetype", "Output", "re:/^[a-z]+\\/[a-z0-9.+-]{1,60}$/", "e.g. text/html"},
	{"precision", "Output", "int:-1:50", ""},
	{"serialize_precision", "Output", "int:-1:50", ""},
	{"date.timezone", "Scripts", "tz", "e.g. Asia/Karachi"},
	{"short_open_tag", "Scripts", "bool", ""},
	{"auto_prepend_file", "Scripts", "path", "A file in the website folder"},
	{"auto_append_file", "Scripts", "path", "A file in the website folder"},
	{"unserialize_callback_func", "Scripts", "re:/^[A-Za-z_][A-Za-z0-9_\\\\]{0,100}$/", ""},
	{"disable_functions", "Scripts", "list", "Added to the server's list, e.g. mail,exec"},
	{"disable_classes", "Scripts", "list", "Comma-separated class names"},
	{"session.save_path", "Sessions", "path", "A folder in the website folder"},
	{"session.save_handler", "Sessions", "enum:files|redis|memcached", ""},
	{"session.serialize_handler", "Sessions", "enum:php|php_binary|php_serialize", ""},
	{"session.name", "Sessions", "re:/^[A-Za-z][A-Za-z0-9_]{0,63}$/", "e.g. PHPSESSID"},
	{"session.auto_start", "Sessions", "bool", ""},
	{"session.cache_limiter", "Sessions", "enum:nocache|private|private_no_expire|public", ""},
	{"session.cache_expire", "Sessions", "int:0:525600", "Minutes"},
	{"session.cookie_lifetime", "Sessions", "int:0:31536000", "Seconds (0: until the browser closes)"},
	{"session.cookie_path", "Sessions", "re:#^/[A-Za-z0-9._~/-]{0,200}$#", "e.g. /"},
	{"session.cookie_domain", "Sessions", "re:/^\\.?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/", ""},
	{"session.cookie_httponly", "Sessions", "bool", ""},
	{"session.cookie_secure", "Sessions", "bool", ""},
	{"session.cookie_samesite", "Sessions", "enum:Lax|Strict|None", ""},
	{"session.use_cookies", "Sessions", "bool", ""},
	{"session.use_only_cookies", "Sessions", "bool", ""},
	{"session.use_strict_mode", "Sessions", "bool", ""},
	{"session.use_trans_sid", "Sessions", "bool", ""},
	{"session.referer_check", "Sessions", "re:#^[A-Za-z0-9.:/-]{1,200}$#", ""},
	{"session.gc_probability", "Sessions", "int:0:1000000", ""},
	{"session.gc_divisor", "Sessions", "int:1:1000000", ""},
	{"session.gc_maxlifetime", "Sessions", "int:1:31536000", "Seconds"},
	{"url_rewriter.tags", "Sessions", "re:/^[a-z]+=[a-z]*(,[a-z]+=[a-z]*){0,20}$/", "e.g. a=href,form="},
	{"mysqli.default_host", "MySQLi defaults", "re:/^[A-Za-z0-9.:-]{1,253}$/", ""},
	{"mysqli.default_port", "MySQLi defaults", "int:1:65535", ""},
	{"mysqli.default_socket", "MySQLi defaults", "re:#^/[A-Za-z0-9._/-]{1,400}$#", ""},
	{"mysqli.default_user", "MySQLi defaults", "re:/^[A-Za-z0-9_.-]{1,80}$/", ""},
	{"mysqli.max_links", "MySQLi defaults", "int:-1:100000", ""},
}

var (
	iniSizeRe = regexp.MustCompile(`^[0-9]{1,6}[KMG]?$`)
	iniPathRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,400}$`)
	iniListRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_\\]{0,100}$`)
)

var phpDirectiveIndex = func() map[string]PhpDirective {
	m := make(map[string]PhpDirective, len(PhpDirectives))
	for _, d := range PhpDirectives {
		m[d.Key] = d
	}
	return m
}()

// ValidatePhpINI checks the submitted directives like PhpDirectives::validate:
// blanks mean "server default" and are dropped, unknown names are refused, and
// each value is normalised for its kind. root is the website's folder (paths
// must lie inside it). Errors are keyed "ini" or "ini.<directive>".
func ValidatePhpINI(input map[string]string, root string) (map[string]string, map[string]string) {
	out := map[string]string{}
	errs := map[string]string{}
	for k, v := range input {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		d, ok := phpDirectiveIndex[k]
		if !ok {
			errs["ini"] = k + " is not a PHP setting a website may change."
			continue
		}
		typ, arg, _ := strings.Cut(d.Kind, ":")
		bad := ""
		switch typ {
		case "bool", "boolint":
			switch strings.ToLower(v) {
			case "1", "on", "true", "yes":
				v = "On"
			case "0", "off", "false", "no":
				v = "Off"
			default:
				n, err := strconv.Atoi(v)
				if typ == "bool" || err != nil || n < 0 || n > 1<<24 {
					bad = "Use On or Off."
					if typ == "boolint" {
						bad = "Use On, Off or a number."
					}
				}
			}
		case "int":
			lo, hi := 0, 0
			if a, b, ok := strings.Cut(arg, ":"); ok {
				lo, _ = strconv.Atoi(a)
				hi, _ = strconv.Atoi(b)
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < lo || n > hi {
				bad = fmt.Sprintf("Enter a whole number between %d and %d.", lo, hi)
			} else {
				v = strconv.Itoa(n)
			}
		case "size":
			v = strings.ToUpper(v)
			if !iniSizeRe.MatchString(v) {
				bad = "Enter a size such as 64M."
			}
		case "path":
			base := strings.TrimRight(root, "/") + "/"
			if !iniPathRe.MatchString(v) || strings.Contains(v, "..") || strings.Contains(v, "//") || strings.HasSuffix(v, "/") || !strings.HasPrefix(v, base) {
				bad = "Enter a path inside the website folder (" + base + "…)."
			}
		case "enum":
			found := false
			for _, o := range strings.Split(arg, "|") {
				found = found || o == v
			}
			if !found {
				bad = "Choose one of " + strings.ReplaceAll(arg, "|", ", ") + "."
			}
		case "tz":
			if _, err := time.LoadLocation(v); err != nil || v == "Local" {
				bad = "Choose a time zone such as Europe/London."
			}
		case "list":
			v = strings.ReplaceAll(v, " ", "")
			for _, item := range strings.Split(v, ",") {
				if !iniListRe.MatchString(item) {
					bad = "Enter names separated by commas."
					break
				}
			}
		case "re":
			pat := strings.Trim(arg, "/")
			if re, err := regexp.Compile(pat); err != nil || !re.MatchString(v) {
				bad = "This value is not accepted."
			}
		}
		if bad != "" {
			errs["ini."+k] = bad
			continue
		}
		out[k] = v
	}
	return out, errs
}
