// Package validate holds the input rules shared by every agent action.
//
// Every value that ends up in a file name, a configuration file or a
// command argument passes through one of these functions first.
package validate

import (
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Error is a validation failure for one field.
type Error struct {
	Field   string
	Message string
}

func (e *Error) Error() string { return e.Field + ": " + e.Message }

func fail(field, format string, a ...any) error {
	return &Error{Field: field, Message: fmt.Sprintf(format, a...)}
}

var label = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Domain validates a fully qualified host name (lowercase, IDNA already
// converted to punycode). Wildcards are not allowed.
func Domain(field, d string) error {
	if d != strings.ToLower(d) {
		return fail(field, "must be lowercase")
	}
	if len(d) < 3 || len(d) > 253 || !strings.Contains(d, ".") {
		return fail(field, "is not a valid domain name")
	}
	for _, l := range strings.Split(d, ".") {
		if !label.MatchString(l) {
			return fail(field, "is not a valid domain name")
		}
	}
	if net.ParseIP(d) != nil {
		return fail(field, "must be a name, not an IP address")
	}
	return nil
}

// Domains validates a list and rejects duplicates.
func Domains(field string, list []string) error {
	seen := map[string]bool{}
	for _, d := range list {
		if err := Domain(field, d); err != nil {
			return err
		}
		if seen[d] {
			return fail(field, "contains %s twice", d)
		}
		seen[d] = true
	}
	return nil
}

var username = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,30}$`)

// Username validates a Linux account name.
func Username(field, u string) error {
	if !username.MatchString(u) {
		return fail(field, "must be 2–31 lowercase letters, digits, '-' or '_', starting with a letter")
	}
	switch u {
	case "root", "daemon", "bin", "sys", "www-data", "nobody", "mysql", "redis", "valkey", "nginx", "whacp", "sshd", "admin", "whacp-build":
		return fail(field, "%q is reserved", u)
	}
	return nil
}

var dbIdent = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// DBIdentifier validates a MySQL database or user name. Only a safe
// subset is allowed so identifiers never need quoting beyond backticks.
func DBIdentifier(field, s string) error {
	if !dbIdent.MatchString(s) {
		return fail(field, "may contain only letters, digits and underscores (max 64)")
	}
	switch strings.ToLower(s) {
	case "mysql", "sys", "performance_schema", "information_schema", "root", "debian-sys-maint", "mariadb.sys":
		return fail(field, "%q is reserved", s)
	}
	return nil
}

var phpVersion = regexp.MustCompile(`^[5-9]\.[0-9]{1,2}$`)

// PHPVersion validates "8.3" style versions.
func PHPVersion(field, v string) error {
	if !phpVersion.MatchString(v) {
		return fail(field, "is not a PHP version like 8.3")
	}
	return nil
}

// Password validates a secret that will be written into SQL or config.
func Password(field, p string, min int) error {
	if len(p) < min || len(p) > 128 {
		return fail(field, "must be %d–128 characters", min)
	}
	for _, r := range p {
		if r < 0x21 || r > 0x7e {
			return fail(field, "may contain only printable ASCII without spaces")
		}
	}
	return nil
}

// Within checks that p (absolute, cleaned) is base or below it.
func Within(field, base, p string) error {
	if !filepath.IsAbs(p) {
		return fail(field, "must be an absolute path")
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return fail(field, "must not contain '..'")
		}
	}
	clean := filepath.Clean(p)
	base = filepath.Clean(base)
	if clean != base && !strings.HasPrefix(clean, base+"/") {
		return fail(field, "must be inside %s", base)
	}
	if strings.ContainsAny(clean, "\x00\n\r;{}\"'$`\\") {
		return fail(field, "contains characters that are not allowed in paths")
	}
	return nil
}

// RelPath validates a path relative to some base (no leading slash,
// no "..", no control characters).
func RelPath(field, p string) error {
	if p == "" || p == "." {
		return nil
	}
	if strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\x00\n\r") {
		return fail(field, "is not a valid relative path")
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return fail(field, "must not contain '..'")
		}
	}
	return nil
}

var unit = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9@._:-]{0,199}\.service$`)

// Unit validates a systemd service name.
func Unit(field, u string) error {
	if !unit.MatchString(u) {
		return fail(field, "is not a valid service name")
	}
	return nil
}

// Debian names are lower case; RPM names may have capitals and
// underscores (ImageMagick, perl-Net_SSLeay). Never a leading "-".
var pkg = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+._-]{1,80}$`)

// Package validates a package name (Debian or RPM).
func Package(field, p string) error {
	if !pkg.MatchString(p) {
		return fail(field, "is not a valid package name")
	}
	return nil
}

// Port validates a TCP port.
func Port(field string, p int) error {
	if p < 1 || p > 65535 {
		return fail(field, "must be between 1 and 65535")
	}
	return nil
}

// IPOrCIDR validates an IPv4/IPv6 address or network.
func IPOrCIDR(field, s string) error {
	if net.ParseIP(s) != nil {
		return nil
	}
	if _, _, err := net.ParseCIDR(s); err == nil {
		return nil
	}
	return fail(field, "is not an IP address or CIDR network")
}

var cronField = regexp.MustCompile(`^[0-9*/,-]{1,40}$`)

// CronSchedule validates a 5-field schedule or an @keyword.
func CronSchedule(field, s string) error {
	switch s {
	case "@reboot", "@yearly", "@annually", "@monthly", "@weekly", "@daily", "@midnight", "@hourly":
		return nil
	}
	parts := strings.Fields(s)
	if len(parts) != 5 {
		return fail(field, "must have 5 fields (minute hour day month weekday)")
	}
	limits := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	for i, p := range parts {
		if !cronField.MatchString(p) {
			return fail(field, "field %d is invalid", i+1)
		}
		for _, n := range regexp.MustCompile(`[0-9]+`).FindAllString(strings.Split(p, "/")[0], -1) {
			v, _ := strconv.Atoi(n)
			if v < limits[i][0] || v > limits[i][1] {
				return fail(field, "field %d is out of range", i+1)
			}
		}
	}
	return nil
}

// Command validates a user-supplied shell command (cron, workers, deploy
// hooks). It never runs as root; this only rejects obviously broken input.
func Command(field, c string) error {
	if strings.TrimSpace(c) == "" || len(c) > 2000 {
		return fail(field, "must be 1–2000 characters")
	}
	if strings.ContainsAny(c, "\x00\n\r") {
		return fail(field, "must be a single line")
	}
	return nil
}

// GitURL accepts https:// and ssh (git@host:path or ssh://) repository URLs.
func GitURL(field, u string) error {
	ok := regexp.MustCompile(`^(https://[A-Za-z0-9.-]+(:[0-9]+)?/[A-Za-z0-9._~/-]+|git@[A-Za-z0-9.-]+:[A-Za-z0-9._~/-]+|ssh://git@[A-Za-z0-9.-]+(:[0-9]+)?/[A-Za-z0-9._~/-]+)$`).MatchString(u)
	if !ok || strings.Contains(u, "..") {
		return fail(field, "must be an https:// or SSH git URL")
	}
	return nil
}

var branch = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,100}$`)

// GitRef validates a branch or tag name.
func GitRef(field, b string) error {
	if !branch.MatchString(b) || strings.HasPrefix(b, "-") || strings.Contains(b, "..") {
		return fail(field, "is not a valid branch name")
	}
	return nil
}

var slug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Slug validates short identifiers used in file names (worker names …).
func Slug(field, s string) error {
	if !slug.MatchString(s) {
		return fail(field, "may contain lowercase letters, digits and '-' (max 63)")
	}
	return nil
}
