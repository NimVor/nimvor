package security

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/dbmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/hardening"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/pkgs"
)

// sshVersionCheck: the SSH server has no pending update.
func sshVersionCheck(ups []pkgs.Upgrade) Check {
	for _, u := range ups {
		if u.Name == "openssh-server" || u.Name == "openssh" {
			st, sev := "warn", "medium"
			if u.Security {
				st, sev = "fail", "high"
			}
			return Check{ID: "ssh_version", Title: "SSH version", Status: st, Severity: sev, Detail: fmt.Sprintf("The SSH server %s can be updated to %s.", u.Current, u.New), Remedy: "Install the update under Server → Packages."}
		}
	}
	return Check{ID: "ssh_version", Title: "SSH version", Status: "pass", Severity: "medium", Detail: "The current SSH version is up to date (according to the last package index refresh)."}
}

// End of upstream support per release series (the last date with fixes).
var dbSupport = map[string]map[string]string{
	"mariadb": {"10.3": "2023-05-25", "10.4": "2024-06-18", "10.5": "2025-06-24", "10.6": "2026-07-06", "10.11": "2028-02-16", "11.4": "2029-05-29", "11.8": "2030-06-04"},
	"mysql":   {"5.7": "2023-10-31", "8.0": "2026-04-30", "8.4": "2032-04-30"},
}

var dbVersionRe = regexp.MustCompile(`^(\d+)\.(\d+)\.\d+`)

// dbSupported says whether a server version still gets upstream fixes.
// Series not in the table are short-term releases (MariaDB rolling or MySQL
// innovation) that are supported until the next one.
func dbSupported(version string, now time.Time) (string, string) {
	m := dbVersionRe.FindStringSubmatch(version)
	if m == nil {
		return "info", "The database server version (" + version + ") was not recognized."
	}
	flavor, series := "mysql", m[1]+"."+m[2]
	if strings.Contains(strings.ToLower(version), "mariadb") {
		flavor = "mariadb"
	}
	name := map[string]string{"mariadb": "MariaDB", "mysql": "MySQL"}[flavor]
	eol, known := dbSupport[flavor][series]
	if !known {
		major, _ := strconv.Atoi(m[1])
		if (flavor == "mariadb" && major >= 11) || (flavor == "mysql" && major >= 9) {
			return "warn", fmt.Sprintf("%s %s is a short-term release, supported only until the next one.", name, series)
		}
		return "fail", fmt.Sprintf("%s %s is no longer supported.", name, series)
	}
	end, _ := time.Parse("2006-01-02", eol)
	if now.Before(end) {
		return "pass", fmt.Sprintf("The system is running a supported database: %s %s (supported until %s).", name, series, eol)
	}
	if regexp.MustCompile(`(?i)ubuntu|deb|el\d`).MatchString(version) {
		return "warn", fmt.Sprintf("%s %s is past its upstream end of support (%s); it only gets fixes from the operating system's packages.", name, series, eol)
	}
	return "fail", fmt.Sprintf("%s %s is no longer supported (ended %s).", name, series, eol)
}

// databaseChecks: a supported server version and no anonymous accounts.
func databaseChecks(ctx context.Context, env *host.Env) []Check {
	if !env.Runner.Available("mysql") {
		return nil
	}
	c := dbmgr.Client{Env: env}
	rows, err := c.Query(ctx, "SELECT VERSION(); SELECT COUNT(*) FROM mysql.user WHERE User = '';")
	if err != nil || len(rows) < 2 || len(rows[0]) < 1 || len(rows[1]) < 1 {
		return []Check{{ID: "mysql_anonymous", Title: "MySQL anonymous users", Status: "info", Severity: "low", Detail: "The database server could not be checked (is it running?)."}}
	}
	out := []Check{}
	st, detail := dbSupported(rows[0][0], time.Now())
	c1 := Check{ID: "database_version", Title: "Supported database", Status: st, Severity: "high", Detail: detail}
	if st != "pass" && st != "info" {
		c1.Remedy = "Upgrade the database server to a long-term supported release."
	}
	out = append(out, c1)
	if n, _ := strconv.Atoi(rows[1][0]); n > 0 {
		out = append(out, Check{ID: "mysql_anonymous", Title: "MySQL anonymous users", Status: "fail", Severity: "high", Detail: fmt.Sprintf("%d anonymous account(s) can connect without a user name.", n), Remedy: "Remove the anonymous users (button above)."})
	} else {
		out = append(out, Check{ID: "mysql_anonymous", Title: "MySQL anonymous users", Status: "pass", Severity: "high", Detail: "There are no anonymous MySQL users."})
	}
	return out
}

// nobodyMailCheck: the pseudo-user "nobody" can neither submit mail locally
// (Postfix authorized_submit_users) nor reach remote SMTP servers (SMTP
// restrictions).
func nobodyMailCheck(ctx context.Context, env *host.Env) Check {
	problems := []string{}
	if env.Runner.Available("postconf") {
		out, err := env.Runner.Run(ctx, "postconf", "-h", "authorized_submit_users")
		if err == nil && !regexp.MustCompile(`!\s*nobody\b`).Match(out) {
			problems = append(problems, "it can send mail with sendmail (Postfix authorized_submit_users)")
		}
	}
	if st := hardening.SMTPStatus(ctx, env); st["active"] != true {
		problems = append(problems, "it can connect to remote SMTP servers (SMTP Restrictions are off)")
	}
	if len(problems) == 0 {
		return Check{ID: "nobody_mail", Title: "“nobody” may not send email", Status: "pass", Severity: "medium", Detail: "The pseudo-user “nobody” is not permitted to send email."}
	}
	return Check{ID: "nobody_mail", Title: "“nobody” may not send email", Status: "warn", Severity: "medium", Detail: "The pseudo-user “nobody” " + strings.Join(problems, " and ") + ".", Remedy: "Turn on Security → SMTP Restrictions; set “Local accounts that may send mail with sendmail” to !nobody, static:anyone under Email → Mail server."}
}

// indexCheck: update checks are only as current as the package index.
func indexCheck(refreshed, now time.Time) Check {
	c := Check{ID: "package_index", Title: "Package index", Severity: "medium"}
	switch {
	case refreshed.IsZero():
		c.Status, c.Detail = "warn", "The package index was never refreshed, so pending security updates are unknown."
	case now.Sub(refreshed) > pkgs.IndexStaleAfter:
		c.Status, c.Detail = "warn", fmt.Sprintf("The package index is %d days old (refreshed %s), so the list of security updates cannot be trusted.", int(now.Sub(refreshed).Hours()/24), refreshed.UTC().Format("2006-01-02"))
	default:
		c.Status, c.Detail = "pass", "The package index was refreshed on "+refreshed.UTC().Format("2006-01-02")+"."
	}
	if c.Status != "pass" {
		c.Remedy = "Refresh it under Server → Packages (Check for updates), and make sure automatic updates refresh it daily."
	}
	return c
}

// dnsRecursionCheck: BIND answers recursive queries only for this server
// and its local networks (an open resolver is abused for amplification).
// It reads the effective configuration (named-checkconf -p; keys in it are
// never returned).
func dnsRecursionCheck(ctx context.Context, env *host.Env) (Check, bool) {
	if !env.Runner.Available("named-checkconf") {
		return Check{}, false
	}
	out, err := env.Runner.Run(ctx, "named-checkconf", "-p")
	c := Check{ID: "dns_recursion", Title: "DNS recursion", Severity: "high"}
	if err != nil {
		return Check{ID: "dns_recursion", Title: "DNS recursion", Status: "info", Severity: "low", Detail: "The BIND configuration could not be read."}, true
	}
	open, why := openResolver(string(out))
	if open {
		c.Status, c.Detail = "fail", "BIND answers recursive queries from anywhere ("+why+"): the server can be used for DNS amplification attacks."
		c.Remedy = "In BIND's options add: recursion no; (or allow-recursion { localhost; localnets; };) and reload named."
	} else {
		c.Status, c.Detail = "pass", "Recursion is "+why+"."
	}
	return c, true
}

var (
	optionsRe = regexp.MustCompile(`(?s)(^|\n)options\s*\{`)
	stmtRe    = func(name string) *regexp.Regexp {
		return regexp.MustCompile(`(?s)(?:^|[\s;{])` + regexp.QuoteMeta(name) + `\s*(\{[^}]*\}|[^;]*);`)
	}
)

// openResolver decides from named-checkconf -p output whether recursion is
// open to everyone. BIND's rule: allow-recursion, else allow-query-cache,
// else allow-query (at the options level), else localnets and localhost.
func openResolver(conf string) (bool, string) {
	loc := optionsRe.FindStringIndex(conf)
	if loc == nil {
		return false, "limited to BIND's defaults (localhost and local networks)"
	}
	// The options block, braces balanced.
	depth, end := 0, len(conf)
	for i := loc[1] - 1; i < len(conf); i++ {
		if conf[i] == '{' {
			depth++
		} else if conf[i] == '}' {
			depth--
			if depth == 0 {
				end = i
				break
			}
		}
	}
	opts := conf[loc[1]:end]
	if m := stmtRe("recursion").FindStringSubmatch(opts); m != nil && strings.TrimSpace(m[1]) == "no" {
		return false, "off"
	}
	for _, name := range []string{"allow-recursion", "allow-query-cache", "allow-query"} {
		if m := stmtRe(name).FindStringSubmatch(opts); m != nil {
			acl := strings.Trim(strings.TrimSpace(m[1]), "{} ")
			for _, e := range strings.Split(acl, ";") {
				e = strings.Trim(strings.TrimSpace(e), `"`)
				if e == "any" || e == "0.0.0.0/0" || e == "::/0" {
					return true, name + " { " + acl + " }"
				}
			}
			return false, "limited by " + name + " { " + acl + " }"
		}
	}
	return false, "limited to BIND's defaults (localhost and local networks)"
}

// procCheck: with /proc mounted hidepid=2 (or "invisible"), website accounts
// see only their own processes, not other websites' command lines. It is
// not applied automatically: some services (polkit, systemd-logind on
// older systems) need a gid= exception, chosen by the administrator.
func procCheck(mounts string) Check {
	c := Check{ID: "proc_hidepid", Title: "Process visibility", Severity: "low"}
	for _, line := range strings.Split(mounts, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[1] != "/proc" || f[2] != "proc" {
			continue
		}
		for _, o := range strings.Split(f[3], ",") {
			if o == "hidepid=2" || o == "hidepid=invisible" || o == "hidepid=1" || o == "hidepid=noaccess" {
				c.Status, c.Detail = "pass", "/proc is mounted with "+o+": accounts see only their own processes."
				return c
			}
		}
		c.Status, c.Detail = "warn", "Every account can list all processes and their command lines, including other websites'."
		c.Remedy = "Mount /proc with hidepid=invisible (or hidepid=2) and gid= a group for monitoring tools, e.g. in /etc/fstab: proc /proc proc defaults,hidepid=invisible,gid=proc 0 0 (test that polkit and logins still work)."
		return c
	}
	c.Status, c.Detail = "info", "The /proc mount could not be read."
	return c
}
