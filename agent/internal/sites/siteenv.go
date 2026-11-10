package sites

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Per-website environment variables (Spec.Env) reach the website's PHP
// processes through env[...] lines of its PHP-FPM pool. The pool file is
// owned by root and not readable by website accounts.
const (
	maxEnvVars     = 100
	maxEnvValueLen = 4096
)

var envNameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// reservedEnvPrefixes and reservedEnvNames can change how programs load code
// or what the web server tells PHP; a website may not set them.
var (
	reservedEnvPrefixes = []string{"LD_", "PHP", "NIMVOR_", "HTTP_", "REQUEST_", "SCRIPT_", "SERVER_", "REMOTE_", "REDIRECT_", "CONTENT_", "FCGI_", "PHPRC"}
	reservedEnvNames    = map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true, "TMPDIR": true, "TMP": true, "TEMP": true, "IFS": true,
		"DOCUMENT_ROOT": true, "DOCUMENT_URI": true, "GATEWAY_INTERFACE": true, "QUERY_STRING": true, "PATH_INFO": true,
		"PATH_TRANSLATED": true, "AUTH_TYPE": true, "HTTPS": true, "ORIG_PATH_INFO": true, "BASH_ENV": true, "ENV": true,
	}
)

// ValidEnvName reports why a variable name is not allowed ("" = fine).
func ValidEnvName(name string) string {
	if !envNameRe.MatchString(name) {
		return "must start with a capital letter and use capital letters, digits and _ only (at most 64 characters)"
	}
	if reservedEnvNames[name] {
		return "is reserved by the system"
	}
	for _, p := range reservedEnvPrefixes {
		if strings.HasPrefix(name, p) {
			return "starts with a prefix the system reserves (" + p + ")"
		}
	}
	return ""
}

func normalizeEnv(env map[string]string) error {
	if len(env) > maxEnvVars {
		return fail("env", "at most %d variables", maxEnvVars)
	}
	for k, v := range env {
		if why := ValidEnvName(k); why != "" {
			return fail("env", "%s %s", k, why)
		}
		if len(v) > maxEnvValueLen {
			return fail("env", "%s: the value is longer than %d characters", k, maxEnvValueLen)
		}
		for _, r := range v {
			// A single quote ends the value in the pool file; control characters would end the line.
			if r == '\'' || r < 0x20 || r == 0x7f {
				return fail("env", "%s: the value may not contain a single quote, a line break or other control characters", k)
			}
		}
	}
	return nil
}

// poolEnvLines renders the env[...] lines (sorted, single-quoted: the pool
// parser expands nothing inside single quotes).
func poolEnvLines(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "env[%s] = '%s'\n", k, env[k])
	}
	return b.String()
}
