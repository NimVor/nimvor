package migrate

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Severity of a finding.
const (
	Critical = "critical" // the migration cannot safely proceed
	Warning  = "warning"  // manual attention is recommended
	Info     = "info"     // nothing to do
)

// Finding is one result of the compatibility assessment.
type Finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Fix      string `json:"fix,omitempty"`
}

// DestInfo is what the destination (this server) offers.
type DestInfo struct {
	PHP     []string `json:"php"`
	MySQL   string   `json:"mysql"`
	Engine  string   `json:"engine"`
	FreeKB  int64    `json:"free_kb"` // free space where the files go, after the reserve
	TempKB  int64    `json:"temp_kb"` // free space where dumps are kept
	Reserve int64    `json:"reserve_kb"`
	MailKB  int64    `json:"mail_free_kb"` // free space where mailboxes are stored, after the reserve
}

func majorMinor(v string) (int, int) {
	p := strings.SplitN(v, ".", 3)
	a, _ := strconv.Atoi(p[0])
	b := 0
	if len(p) > 1 {
		b, _ = strconv.Atoi(p[1])
	}
	return a, b
}

func less(a, b string) bool {
	am, an := majorMinor(a)
	bm, bn := majorMinor(b)
	return am < bm || am == bm && an < bn
}

// Assess compares what is going to be moved with what the destination can
// take. It never contacts a server: the inputs are the discovery and the
// destination's facts, so the same answer can be shown again and tested.
func Assess(d *Discovery, p *Plan, dest DestInfo) []Finding {
	var out []Finding
	add := func(sev, code, msg, fix string) {
		out = append(out, Finding{Severity: sev, Code: code, Message: msg, Fix: fix})
	}

	if d.Server.Rsync == false {
		add(Critical, "no_rsync_source", "rsync is not installed on the source server.", "Install rsync on the source (for example apt install rsync, or dnf install rsync) and discover again.")
	}
	var filesKB, dbKB int64
	unknown := 0
	for _, f := range p.Files {
		if f.SizeKB < 0 {
			unknown++
			continue
		}
		filesKB += f.SizeKB
	}
	for _, db := range p.Databases {
		dbKB += db.SizeKB
	}
	if unknown > 0 {
		add(Warning, "size_unknown", fmt.Sprintf("The size of %d website folder(s) is not known, so the disk check cannot count them.", unknown), "Make sure the login can read them, then discover again.")
	}
	// Files need their size; a changed file is copied beside its old copy before it replaces it, so allow 10% extra.
	needFiles := filesKB + filesKB/10
	if needFiles > dest.FreeKB {
		add(Critical, "disk_space", fmt.Sprintf("The websites need about %s but only %s is free here (after the %s NimVor keeps free).", kb(needFiles), kb(dest.FreeKB), kb(dest.Reserve)), "Free disk space or migrate fewer websites at a time.")
	}
	if len(p.Mail) > 0 {
		var mailKB int64
		for _, mt := range p.Mail {
			if mt.SizeKB > 0 {
				mailKB += mt.SizeKB
			}
		}
		if mailKB+mailKB/10 > dest.MailKB {
			add(Critical, "mail_space", fmt.Sprintf("The mailboxes need about %s but only %s is free where mail is stored.", kb(mailKB+mailKB/10), kb(dest.MailKB)), "Free disk space or migrate fewer mailboxes at a time.")
		}
		add(Info, "mail_scope", "Messages and folders are copied. Passwords, quotas, aliases and forwarders are not: set them up on the NimVor mailboxes. Dovecot rebuilds its indexes, so a mailbox may take a moment to open the first time.", "")
	}
	// A dump is kept compressed while it moves: assume a third of the data, with a floor.
	needTemp := dbKB/3 + 64*1024
	if len(p.Databases) > 0 && needTemp > dest.TempKB {
		add(Critical, "temp_space", fmt.Sprintf("Moving the databases needs about %s of temporary space but only %s is free.", kb(needTemp), kb(dest.TempKB)), "Choose a temporary folder on a bigger disk in the configuration step.")
	}
	if len(p.Databases) > 0 {
		if !d.Server.Mysqldump {
			add(Critical, "no_mysqldump_source", "mysqldump is not installed on the source server.", "Install the MariaDB/MySQL client tools on the source and discover again.")
		}
		if dest.Engine == "" {
			add(Critical, "no_database_here", "No MySQL or MariaDB server was found on this server.", "Install one under Databases before migrating databases.")
		} else if d.Server.Engine != "" {
			sm, sn := majorMinor(d.Server.MySQL)
			switch {
			case d.Server.Engine == "mysql" && dest.Engine == "mariadb" && sm >= 8:
				add(Warning, "mysql8_to_mariadb", fmt.Sprintf("The source runs MySQL %s and this server runs MariaDB %s. MySQL 8 collations (utf8mb4_0900_ai_ci) and some features do not exist in MariaDB; the import of such a database fails.", d.Server.MySQL, dest.MySQL), "Check the databases for utf8mb4_0900_* collations and JSON/ROLE features before migrating.")
			case d.Server.Engine == dest.Engine && less(dest.MySQL, d.Server.MySQL):
				add(Warning, "database_downgrade", fmt.Sprintf("The source runs %s %s and this server an older %s %s. A dump from a newer server may use features the older one lacks.", d.Server.Engine, d.Server.MySQL, dest.Engine, dest.MySQL), "Upgrade the database server here, or test the import first.")
			default:
				add(Info, "database_compatible", fmt.Sprintf("Source %s %s → destination %s %s.", d.Server.Engine, d.Server.MySQL, dest.Engine, dest.MySQL), "")
			}
			_ = sn
		}
	}
	// PHP: each selected account's version must exist here.
	var missing []string
	have := map[string]bool{}
	for _, v := range dest.PHP {
		have[v] = true
	}
	seen := map[string]bool{}
	for _, a := range d.Accounts {
		if a.PHP != "" && !have[a.PHP] && !seen[a.PHP] {
			seen[a.PHP] = true
			missing = append(missing, a.PHP)
		}
	}
	sort.Strings(missing)
	for _, v := range missing {
		add(Warning, "php_version", fmt.Sprintf("Websites use PHP %s on the source, which is not installed here (installed: %s).", v, strings.Join(dest.PHP, ", ")), "Install that PHP version under PHP, or move the sites to an installed version and test them.")
	}
	if !d.Server.Root {
		add(Warning, "not_root", "The source login is not root: files and databases of other accounts may be unreadable.", "Use root, or an account that can read everything you select.")
	}
	if len(p.Files) > 0 {
		add(Info, "htaccess", "Apache .htaccess rules and rewrite rules are not translated to Nginx. NimVor's own site configuration is used.", "After the move, check redirects, clean URLs and access rules of each site.")
		add(Info, "ownership", "Files are given to the NimVor website's account; the source's owners and groups are not kept.", "")
	}
	for _, n := range d.NotMigrated {
		add(Info, "not_migrated", "Not moved by this version: "+n+".", "Move it by hand or wait for a later version.")
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i].Severity) < rank(out[j].Severity) })
	return out
}

func rank(s string) int {
	switch s {
	case Critical:
		return 0
	case Warning:
		return 1
	}
	return 2
}

// Blocking reports whether any finding forbids starting.
func Blocking(f []Finding) bool {
	for _, x := range f {
		if x.Severity == Critical {
			return true
		}
	}
	return false
}

func kb(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d MiB", n>>10)
	}
	return fmt.Sprintf("%d KiB", n)
}
