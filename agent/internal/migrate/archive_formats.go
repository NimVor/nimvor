package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/host"
)

// Backup archives of other panels. After unpacking, the layout of the folder says which panel made it:
//
//   - cPanel (cpmove-USER, backup-…): cp/, userdata/, homedir/ (see archive.go)
//   - DirectAdmin (user.RESELLER.USER.tar.gz): backup/ with the account's settings and database dumps, and
//     domains/ and imap/ with the files and the mail
//   - HestiaCP and VestaCP (USER.DATE.tar): web/, mail/, db/, cron/, with the files and mailboxes of each
//     domain in archives of their own (opened when the backup is unpacked)
//
// DirectAdmin and Hestia/Vesta are written from the layouts those projects document and are tested on
// synthetic backups only: Experimental, like their live readers.

// archiveLayout says which kind of backup an unpacked folder holds and where its top folder is.
func archiveLayout(dir string) (kind, top string, err error) {
	isDir := func(p string) bool {
		fi, err := os.Stat(p)
		return err == nil && fi.IsDir()
	}
	candidates := []string{dir}
	if ents, e := os.ReadDir(dir); e == nil {
		for _, e := range ents {
			if e.IsDir() {
				candidates = append(candidates, filepath.Join(dir, e.Name()))
			}
		}
	}
	for _, c := range candidates {
		if isDir(filepath.Join(c, "cp")) || isDir(filepath.Join(c, "userdata")) || isDir(filepath.Join(c, "homedir")) {
			return "cpanel", c, nil
		}
	}
	for _, c := range candidates {
		if isDir(filepath.Join(c, "backup")) && isDir(filepath.Join(c, "domains")) {
			return "directadmin", c, nil
		}
	}
	for _, c := range candidates {
		webMail := isDir(filepath.Join(c, "web")) || isDir(filepath.Join(c, "mail")) || isDir(filepath.Join(c, "db"))
		if webMail && (isDir(filepath.Join(c, "pam")) || fileExists(filepath.Join(c, "user.conf"))) {
			return "hestia", c, nil
		}
	}
	return "", "", fmt.Errorf("this is not a backup NimVor can read: it is not a cPanel, DirectAdmin, HestiaCP or VestaCP account backup")
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// archiveSourceName returns the file name the archive had (kept beside the unpacked folder).
func archiveSourceName(dirOfID string) string {
	b, err := os.ReadFile(dirOfID + ".name")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

var (
	daNameRe     = regexp.MustCompile(`(?i)^user\.[a-z0-9_]+\.([a-z0-9_]+)\.(tar\.gz|tgz)$`)
	hestiaNameRe = regexp.MustCompile(`(?i)^(.+?)\.\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}\.tar$`)
	confKVRe     = regexp.MustCompile(`(?m)^\s*([A-Za-z_]+)=(.*)$`)
	subLabelRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

func archiveInfo(kind, label string) *Discovery {
	return &Discovery{
		Panel:  Detection{Panel: "cpmove", Name: label + " backup archive", Support: Experimental},
		Server: ServerInfo{OS: "backup archive (unpacked on this server)", Root: true, Rsync: true, Mysqldump: true, Engine: "mysql"},
	}
}

func daZoneFile(top, domain string) string {
	for _, n := range []string{domain + ".db", domain + ".zone", "zone", "dns.zone"} {
		p := filepath.Join(top, "backup", domain, n)
		if fileExists(p) {
			return p
		}
	}
	return filepath.Join(top, "backup", domain, domain+".db")
}

// discoverDirectAdminArchive describes an unpacked DirectAdmin user backup.
func discoverDirectAdminArchive(env *host.Env, dir, top string, progress func(string)) (*Discovery, error) {
	conf := map[string]string{}
	for _, m := range confKVRe.FindAllStringSubmatch(readCapped(filepath.Join(top, "backup", "user.conf"), 1<<20), -1) {
		conf[strings.ToLower(m[1])] = strings.TrimSpace(m[2])
	}
	user := conf["username"]
	if user == "" {
		if m := daNameRe.FindStringSubmatch(archiveSourceName(dir)); m != nil {
			user = strings.ToLower(m[1])
		}
	}
	if !pleskLogin.MatchString(user) {
		return nil, fmt.Errorf("the account name in the backup could not be read")
	}
	progress("Reading the account " + user + "…")
	home := top // the unpacked folder plays the part of the account's home
	a := newAccount(user, home)
	d := archiveInfo("directadmin", "DirectAdmin")

	var doms []string
	if ents, err := os.ReadDir(filepath.Join(top, "domains")); err == nil {
		for _, e := range ents {
			n := strings.ToLower(e.Name())
			if e.IsDir() && domainName.MatchString(n) {
				doms = append(doms, n)
			}
		}
	}
	sort.Strings(doms)
	main := strings.ToLower(conf["domain"])
	known := map[string]bool{}
	for _, dn := range doms {
		known[dn] = true
	}
	progress("Measuring the website folders…")
	for _, dn := range doms {
		kind := "addon"
		if dn == main || (main == "" && len(a.Domains) == 0) {
			kind = "main"
		}
		doc := filepath.Join(top, "domains", dn, "public_html")
		a.Domains = append(a.Domains, Domain{Name: dn, Kind: kind, DocRoot: doc, SizeKB: dirSizeKB(doc)})
		for _, l := range strings.Split(readCapped(filepath.Join(top, "backup", dn, "subdomain.list"), 1<<20), "\n") {
			l = strings.ToLower(strings.TrimSpace(l))
			if i := strings.IndexAny(l, "=:"); i >= 0 {
				l = strings.TrimSpace(l[:i])
			}
			if !subLabelRe.MatchString(l) {
				continue
			}
			sub := filepath.Join(doc, l)
			a.Domains = append(a.Domains, Domain{Name: l + "." + dn, Kind: "sub", DocRoot: sub, SizeKB: dirSizeKB(sub)})
		}
		pw := readCapped(filepath.Join(top, "backup", dn, "email", "passwd"), 4<<20)
		for _, l := range strings.Split(pw, "\n") {
			u, _, _ := strings.Cut(strings.TrimSpace(l), ":")
			if !pleskLocal.MatchString(u) {
				continue
			}
			md := filepath.Join(top, "imap", dn, u, "Maildir")
			if fi, err := os.Stat(md); err != nil || !fi.IsDir() {
				md = filepath.Join(top, "imap", dn, u)
			}
			a.Email = append(a.Email, EmailAccount{Address: strings.ToLower(u) + "@" + dn, Maildir: md, SizeKB: dirSizeKB(md)})
		}
		for _, l := range strings.Split(readCapped(filepath.Join(top, "backup", dn, "email", "aliases"), 1<<20), "\n") {
			if f, ok := parseValias(dn, l); ok {
				a.Forwarders = append(a.Forwarders, f)
			}
		}
	}
	if ents, err := os.ReadDir(filepath.Join(top, "backup")); err == nil {
		for _, e := range ents {
			name, ok := strings.CutSuffix(e.Name(), ".sql")
			if !ok || e.IsDir() || !archiveDBName.MatchString(name) || name == "mysql" {
				continue
			}
			var kbsz int64 = -1
			if fi, err := e.Info(); err == nil {
				kbsz = fi.Size() / 1024
			}
			a.Databases = append(a.Databases, Database{Name: name, Engine: "mysql", SizeKB: kbsz})
		}
	}
	a.Cron = append(a.Cron, crontabJobs(readCapped(filepath.Join(top, "backup", "crontab.conf"), 1<<20))...)
	if len(a.Domains) == 0 {
		d.Warnings = append(d.Warnings, "No websites were found in the backup's domains folder.")
	}
	if len(a.Email) > 0 && dirSizeKB(filepath.Join(top, "imap")) == 0 {
		d.Warnings = append(d.Warnings, "The backup lists mailboxes but holds no messages (DirectAdmin leaves them out when mail is not included in the backup).")
	}
	d.Warnings = append(d.Warnings, "A backup archive is a snapshot: there is nothing to synchronise later, and database users, passwords and FTP accounts are not restored.")
	d.Accounts = []Account{*a}
	d.NotMigrated = append(d.NotMigrated, notMigrated...)
	return d, nil
}

var hestiaCronKV = regexp.MustCompile(`([A-Z_]+)='([^']*)'`)

// discoverHestiaArchive describes an unpacked HestiaCP or VestaCP user backup.
func discoverHestiaArchive(env *host.Env, dir, top string, progress func(string)) (*Discovery, error) {
	user := ""
	if m := hestiaNameRe.FindStringSubmatch(archiveSourceName(dir)); m != nil {
		user = m[1]
	}
	if !pleskLogin.MatchString(user) {
		if line, _, _ := strings.Cut(readCapped(filepath.Join(top, "pam", "passwd"), 1<<16), "\n"); line != "" {
			user, _, _ = strings.Cut(line, ":")
		}
	}
	if !pleskLogin.MatchString(user) {
		return nil, fmt.Errorf("the account name in the backup could not be read")
	}
	if zst, _ := filepath.Glob(filepath.Join(top, "web", "*", "*.zst")); len(zst) > 0 {
		return nil, fmt.Errorf("this backup keeps its files in zstd-compressed archives, which NimVor does not open yet; make the backup again with gzip compression chosen in the panel's backup settings")
	}
	if zst, _ := filepath.Glob(filepath.Join(top, "mail", "*", "*.zst")); len(zst) > 0 {
		return nil, fmt.Errorf("this backup keeps its mailboxes in zstd-compressed archives, which NimVor does not open yet; make the backup again with gzip compression chosen in the panel's backup settings")
	}
	progress("Reading the account " + user + "…")
	d := archiveInfo("hestia", "HestiaCP / VestaCP")
	home := top
	var b strings.Builder
	b.WriteString("##web\n")
	var webNames []string
	if ents, err := os.ReadDir(filepath.Join(top, "web")); err == nil {
		for _, e := range ents {
			if n := strings.ToLower(e.Name()); e.IsDir() && domainName.MatchString(n) {
				webNames = append(webNames, n)
				fmt.Fprintf(&b, "DOMAIN='%s'\n", n)
			}
		}
	}
	b.WriteString("##db\n")
	if ents, err := os.ReadDir(filepath.Join(top, "db")); err == nil {
		for _, e := range ents {
			if !e.IsDir() || !archiveDBName.MatchString(e.Name()) {
				continue
			}
			switch {
			case anyFile(filepath.Join(top, "db", e.Name()), e.Name()+".mysql.sql.gz", e.Name()+".sql.gz", e.Name()+".mysql.sql"):
				fmt.Fprintf(&b, "DB='%s' TYPE='mysql'\n", e.Name())
			case anyFile(filepath.Join(top, "db", e.Name()), e.Name()+".pgsql.sql.gz"):
				d.Warnings = append(d.Warnings, "The PostgreSQL database "+e.Name()+" is not moved from a backup.")
			}
		}
	}
	b.WriteString("##mail\n")
	mailDirs := map[string]string{} // address → Maildir in the unpacked backup
	if ents, err := os.ReadDir(filepath.Join(top, "mail")); err == nil {
		for _, e := range ents {
			dom := strings.ToLower(e.Name())
			if !e.IsDir() || !domainName.MatchString(dom) {
				continue
			}
			fmt.Fprintf(&b, "@@d %s\n", dom)
			seen := map[string]bool{}
			for _, cf := range []string{"vesta/mail.conf", "hestia/mail.conf", "vesta/accounts.conf", "hestia/accounts.conf"} {
				for _, l := range strings.Split(readCapped(filepath.Join(top, "mail", dom, cf), 1<<20), "\n") {
					if acct := vestaLine(l)["ACCOUNT"]; pleskLocal.MatchString(acct) && !seen[acct] {
						seen[acct] = true
						fmt.Fprintln(&b, strings.TrimSpace(l))
					}
				}
			}
			if boxes, err := os.ReadDir(filepath.Join(top, "mail", dom, "data")); err == nil {
				for _, bx := range boxes {
					if bx.IsDir() && pleskLocal.MatchString(bx.Name()) && !seen[bx.Name()] {
						seen[bx.Name()] = true
						fmt.Fprintf(&b, "ACCOUNT='%s'\n", bx.Name())
					}
				}
			}
			for acct := range seen {
				mailDirs[strings.ToLower(acct)+"@"+dom] = filepath.Join(top, "mail", dom, "data", acct)
			}
		}
	}
	a := parseVestaUser(user, b.String())
	a.Home = home
	progress("Measuring the website folders…")
	for i := range a.Domains {
		doc := filepath.Join(top, "web", a.Domains[i].Name, "data", "public_html")
		a.Domains[i].DocRoot = doc
		a.Domains[i].SizeKB = dirSizeKB(doc)
		if fi, err := os.Stat(doc); err != nil || !fi.IsDir() {
			d.Warnings = append(d.Warnings, "The backup holds no website files for "+a.Domains[i].Name+".")
		}
	}
	for i := range a.Email {
		if p, ok := mailDirs[a.Email[i].Address]; ok {
			a.Email[i].Maildir = p
			a.Email[i].SizeKB = dirSizeKB(p)
		}
	}
	for i := range a.Databases {
		var kbsz int64 = -1
		for _, f := range []string{a.Databases[i].Name + ".mysql.sql.gz", a.Databases[i].Name + ".sql.gz", a.Databases[i].Name + ".mysql.sql"} {
			if fi, err := os.Stat(filepath.Join(top, "db", a.Databases[i].Name, f)); err == nil {
				kbsz = fi.Size() / 1024
				break
			}
		}
		a.Databases[i].SizeKB = kbsz
	}
	for _, l := range strings.Split(readCapped(filepath.Join(top, "cron", "cron.conf"), 1<<20), "\n") {
		kv := map[string]string{}
		for _, m := range hestiaCronKV.FindAllStringSubmatch(l, -1) {
			kv[m[1]] = m[2]
		}
		if kv["CMD"] == "" || kv["SUSPENDED"] == "yes" {
			continue
		}
		a.Cron = append(a.Cron, crontabJobs(strings.Join([]string{kv["MIN"], kv["HOUR"], kv["DAY"], kv["MONTH"], kv["WDAY"], kv["CMD"]}, " "))...)
	}
	if len(a.Domains) == 0 {
		d.Warnings = append(d.Warnings, "No websites were found in the backup's web folder.")
	}
	d.Warnings = append(d.Warnings, "A backup archive is a snapshot: there is nothing to synchronise later, and database users, passwords, FTP accounts and DNS records are not restored.")
	d.Accounts = []Account{*a}
	d.NotMigrated = append(d.NotMigrated, notMigrated...)
	return d, nil
}

func anyFile(dir string, names ...string) bool {
	for _, n := range names {
		if fileExists(filepath.Join(dir, n)) {
			return true
		}
	}
	return false
}
