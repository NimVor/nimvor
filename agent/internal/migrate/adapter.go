package migrate

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Adapter reads one kind of source panel. The migration engine knows
// nothing about any panel: it works from the Discovery an adapter returns,
// so a new panel is a new Adapter registered in adapters below.
//
// This is the conceptual interface of the specification adapted to what the
// panels can really answer: one Discover call returns accounts with their
// domains, databases, mailboxes and cron jobs together, because every
// supported layout stores them per account.
type Adapter interface {
	ID() string
	Name() string
	// Support is how far this adapter has been proven. It must stay
	// Experimental until it has run against real servers.
	Support() Support
	// Detect says whether this panel runs on the source, and its version.
	Detect(ctx context.Context, t Transport) (Detection, bool, error)
	// Discover lists accounts and what belongs to them. progress is told what is being read.
	Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error)
}

// adapters in detection order: a specific panel before the generic server.
func adapters() []Adapter { return []Adapter{cpanelAdapter{}, pleskAdapter{}, linuxAdapter{}} }

// Lookup returns an adapter by id.
func Lookup(id string) (Adapter, error) {
	for _, a := range adapters() {
		if a.ID() == id {
			return a, nil
		}
	}
	return nil, fmt.Errorf("the source panel %q is not supported yet", id)
}

// Supported lists the adapters, for the wizard's first step.
func Supported() []Detection {
	var out []Detection
	for _, a := range adapters() {
		out = append(out, Detection{Panel: a.ID(), Name: a.Name(), Support: a.Support()})
	}
	return out
}

// infoScript gathers the facts of every check on the connection step in one
// round trip. It only reads.
const infoScript = `echo '##os'; head -n 6 /etc/os-release 2>/dev/null
echo '##id'; id -u
echo '##host'; hostname 2>/dev/null
echo '##php'; for p in /usr/bin/php /usr/bin/php[0-9]* /opt/cpanel/ea-php*/root/usr/bin/php /opt/plesk/php/*/bin/php /usr/local/php*/bin/php; do [ -x "$p" ] && echo "$p $("$p" -r 'echo PHP_MAJOR_VERSION.".".PHP_MINOR_VERSION;' 2>/dev/null)"; done
echo '##mysql'; (mysql --version || mariadb --version) 2>/dev/null | head -n 1
echo '##df'; df -Pk /home /var/www /var 2>/dev/null
echo '##tools'; command -v rsync; command -v mysqldump
echo '##svc'; for s in nginx apache2 httpd mysql mariadb mysqld postgresql php-fpm exim dovecot named; do echo "$s $(systemctl is-active $s 2>/dev/null)"; done
echo '##end'`

// Inspect connects and reads the server's facts.
func Inspect(ctx context.Context, t Transport) (ServerInfo, error) {
	out, err := t.Exec(ctx, "sh -c "+Shq(infoScript), nil)
	if err != nil {
		return ServerInfo{}, err
	}
	return parseInfo(out), nil
}

func sections(out string) map[string][]string {
	m := map[string][]string{}
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "##") {
			cur = strings.TrimPrefix(line, "##")
			m[cur] = nil
			continue
		}
		if cur != "" && strings.TrimSpace(line) != "" {
			m[cur] = append(m[cur], line)
		}
	}
	return m
}

var (
	mariaRe   = regexp.MustCompile(`(?i)(\d+\.\d+(?:\.\d+)?)-MariaDB`)
	distribRe = regexp.MustCompile(`(?i)Distrib (\d+\.\d+(?:\.\d+)?)`)
	mysqlRe   = regexp.MustCompile(`(?i)Ver (\d+\.\d+(?:\.\d+)?)`)
)

func parseInfo(out string) ServerInfo {
	sec := sections(out)
	info := ServerInfo{Services: map[string]string{}}
	for _, l := range sec["os"] {
		if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
			info.OS = strings.Trim(v, `"`)
		}
	}
	if len(sec["id"]) > 0 {
		info.Root = strings.TrimSpace(sec["id"][0]) == "0"
	}
	if len(sec["host"]) > 0 {
		info.Hostname = strings.TrimSpace(sec["host"][0])
	}
	seen := map[string]bool{}
	for _, l := range sec["php"] {
		f := strings.Fields(l)
		if len(f) == 2 && regexp.MustCompile(`^\d+\.\d+$`).MatchString(f[1]) && !seen[f[1]] {
			seen[f[1]] = true
			info.PHP = append(info.PHP, f[1])
		}
	}
	sort.Strings(info.PHP)
	if len(sec["mysql"]) > 0 {
		line := sec["mysql"][0]
		ver := ""
		if m := distribRe.FindStringSubmatch(line); m != nil {
			ver = m[1] // "Distrib" is the server's version; "Ver" on the same line is the client's
		} else if m := mysqlRe.FindStringSubmatch(line); m != nil {
			ver = m[1]
		}
		switch {
		case mariaRe.MatchString(line):
			info.Engine, info.MySQL = "mariadb", mariaRe.FindStringSubmatch(line)[1]
		case ver != "" && strings.Contains(strings.ToLower(line), "mariadb"):
			info.Engine, info.MySQL = "mariadb", ver
		case ver != "":
			info.Engine, info.MySQL = "mysql", ver
		}
	}
	for _, l := range sec["df"] {
		f := strings.Fields(l)
		// Filesystem 1024-blocks Used Available Capacity Mounted on
		if len(f) >= 6 && f[0] != "Filesystem" {
			used, _ := strconv.ParseInt(f[2], 10, 64)
			free, err := strconv.ParseInt(f[3], 10, 64)
			if err == nil {
				info.Disk = append(info.Disk, Disk{Mount: f[5], FreeKB: free, UsedKB: used})
			}
		}
	}
	for _, l := range sec["tools"] {
		switch {
		case strings.HasSuffix(l, "/rsync"):
			info.Rsync = true
		case strings.HasSuffix(l, "/mysqldump"):
			info.Mysqldump = true
		}
	}
	for _, l := range sec["svc"] {
		if f := strings.Fields(l); len(f) == 2 && (f[1] == "active" || f[1] == "inactive" || f[1] == "failed") {
			info.Services[f[0]] = f[1]
		}
	}
	return info
}

// DetectPanel asks the adapters in turn (or the one named) what the source is.
func DetectPanel(ctx context.Context, t Transport, want string) (Adapter, Detection, error) {
	if want != "" {
		a, err := Lookup(want)
		if err != nil {
			return nil, Detection{}, err
		}
		d, ok, err := a.Detect(ctx, t)
		if err != nil {
			return nil, Detection{}, err
		}
		if !ok {
			return nil, Detection{}, fmt.Errorf("the source does not look like %s", a.Name())
		}
		return a, d, nil
	}
	for _, a := range adapters() {
		d, ok, err := a.Detect(ctx, t)
		if err != nil {
			return nil, Detection{}, err
		}
		if ok {
			return a, d, nil
		}
	}
	return nil, Detection{}, fmt.Errorf("no supported panel was recognised on the source")
}

// DiscoverAll runs the whole discovery: facts, panel, accounts.
func DiscoverAll(ctx context.Context, t Transport, want string, progress func(string)) (*Discovery, error) {
	if progress == nil {
		progress = func(string) {}
	}
	progress("Reading the server's facts…")
	info, err := Inspect(ctx, t)
	if err != nil {
		return nil, err
	}
	a, det, err := DetectPanel(ctx, t, want)
	if err != nil {
		return nil, err
	}
	progress("Found " + det.Name + " " + det.Version + ".")
	d, err := a.Discover(ctx, t, info, progress)
	if err != nil {
		return nil, err
	}
	d.Panel, d.Server = det, info
	d.NotMigrated = append(d.NotMigrated, notMigrated...)
	if !info.Root {
		d.Warnings = append(d.Warnings, "The login is not root: some things (other accounts' files, all databases) may not be readable. Use root or a user that can read them.")
	}
	return d, nil
}

// notMigrated is what this version of the agent does not move, whatever the source.
var notMigrated = []string{
	"Mailbox passwords and quotas (messages are copied into mailboxes you already made in NimVor)",
	"DNS address records (A, AAAA) and the zones themselves (other record types can be added to existing NimVor zones from a cPanel source)",
	"SSL certificates",
	"FTP accounts",
	"Cron jobs (listed)",
	"PHP settings per domain, web server rewrite rules and application configuration",
	"PostgreSQL databases",
	"Database users and permissions (a NimVor database user is created for each database by NimVor, not copied)",
}
