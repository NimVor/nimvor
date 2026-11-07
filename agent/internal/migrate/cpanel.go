package migrate

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// cpanelAdapter reads a cPanel & WHM server from its files, over SSH as root:
//
//   - accounts: /etc/trueuserdomains ("domain: user")
//   - domains and document roots: /var/cpanel/userdata/<user>/ (main, and one
//     file per domain with its documentroot and PHP version)
//   - mailboxes: /home/<user>/etc/<domain>/passwd
//   - cron jobs: /var/spool/cron/<user>
//   - databases: cPanel names a database "<user>_<name>", so the databases of
//     an account are the schemas that start with its user name and an underscore.
//
// Written from cPanel's documented layout and tested against synthetic data
// only: it stays Experimental until it has run on real servers (and the
// layout differs between cPanel versions).
type cpanelAdapter struct{}

func (cpanelAdapter) ID() string       { return "cpanel" }
func (cpanelAdapter) Name() string     { return "cPanel / WHM" }
func (cpanelAdapter) Support() Support { return Experimental }

func (cpanelAdapter) Detect(ctx context.Context, t Transport) (Detection, bool, error) {
	out, err := t.Exec(ctx, "sh -c "+Shq("cat /usr/local/cpanel/version 2>/dev/null; true"), nil)
	if err != nil {
		return Detection{}, false, err
	}
	v := strings.TrimSpace(out)
	if !regexp.MustCompile(`^\d+(\.\d+)+`).MatchString(v) {
		return Detection{}, false, nil
	}
	return Detection{Panel: "cpanel", Name: "cPanel / WHM", Version: v, Support: Experimental}, true, nil
}

var cpUserRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

func (cpanelAdapter) Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error) {
	d := &Discovery{}
	out, err := t.Exec(ctx, "sh -c "+Shq("cat /etc/trueuserdomains 2>/dev/null"), nil)
	if err != nil {
		return nil, err
	}
	owners := parseTrueUserDomains(out)
	if len(owners) == 0 {
		d.Warnings = append(d.Warnings, "No accounts were found in /etc/trueuserdomains. The login must be root (or able to read it).")
	}
	users := make([]string, 0, len(owners))
	for u := range owners {
		users = append(users, u)
	}
	sort.Strings(users)
	schemas := ""
	if len(users) > 0 {
		progress("Listing the databases…")
		schemas, err = t.Exec(ctx, "sh -c "+Shq(dbScript), nil)
		if err != nil || strings.TrimSpace(schemas) == "" {
			d.Warnings = append(d.Warnings, "The databases could not be listed (the login needs access to MariaDB/MySQL).")
		}
	}
	allDBs := parseSchemas(schemas, info.Engine)
	for _, u := range users {
		progress("Reading the account " + u + "…")
		a := Account{ID: u, Name: u, Home: "/home/" + u, Domains: []Domain{}, Databases: []Database{}, Email: []EmailAccount{}, Forwarders: []Forwarder{}, FTP: []FTPAccount{}, Cron: []CronJob{}}
		script := fmt.Sprintf(`for f in /var/cpanel/userdata/%[1]s/*; do case "$f" in *.cache|*.stor|*_SSL|*.json|*.yaml) continue;; esac; [ -f "$f" ] && { echo "##file $f"; cat "$f"; }; done
echo '##home'; getent passwd %[1]s | cut -d: -f6
echo '##mail'; for d in /home/%[1]s/etc/*/; do dom=$(basename "$d"); [ -f "$d/passwd" ] && { echo "##dom $dom"; cut -d: -f1 "$d/passwd"; }; done
echo '##msize'; du -sk /home/%[1]s/mail/*/*/ 2>/dev/null
echo '##fwd'; for f in $(find /etc/valiases -maxdepth 1 -type f -user %[1]s 2>/dev/null); do echo "##vdom $(basename "$f")"; cat "$f"; done
echo '##ftp'; cat /etc/proftpd/%[1]s 2>/dev/null
echo '##cron'; cat /var/spool/cron/%[1]s 2>/dev/null; true`, u)
		res, err := t.Exec(ctx, "sh -c "+Shq(script), nil)
		if err != nil {
			d.Warnings = append(d.Warnings, "The account "+u+" could not be read: "+err.Error())
			continue
		}
		parseCpanelAccount(&a, res)
		for _, db := range allDBs {
			if strings.HasPrefix(db.Name, u+"_") {
				a.Databases = append(a.Databases, db)
			}
		}
		var dirs []string
		for _, dom := range a.Domains {
			dirs = append(dirs, dom.DocRoot)
		}
		if sizes, err := measure(ctx, t, uniq(dirs)); err == nil {
			for i := range a.Domains {
				if n, ok := sizes[a.Domains[i].DocRoot]; ok {
					a.Domains[i].SizeKB = n
				}
			}
		}
		d.Accounts = append(d.Accounts, a)
	}
	d.Warnings = append(d.Warnings, "Databases are matched to accounts by cPanel's naming (the account name and an underscore): a database made another way is not listed under an account.")
	return d, nil
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// parseTrueUserDomains reads "domain: user" lines into user → main domain.
func parseTrueUserDomains(out string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		dom, user, ok := strings.Cut(strings.TrimSpace(l), ":")
		user = strings.TrimSpace(user)
		dom = strings.TrimSpace(dom)
		if ok && cpUserRe.MatchString(user) && domainName.MatchString(dom) && user != "nobody" {
			m[user] = dom
		}
	}
	return m
}

var (
	cpDocRoot = regexp.MustCompile(`(?m)^documentroot:\s*(\S.*)$`)
	cpPHP     = regexp.MustCompile(`(?m)^phpversion:\s*ea-php(\d)(\d+)\s*$`)
)

// parseCpanelAccount fills an account from the output of the account script.
func parseCpanelAccount(a *Account, out string) {
	idx := strings.Index(out, "\n##home")
	files := out
	rest := ""
	if idx >= 0 {
		files, rest = out[:idx], out[idx:]
	}
	var main string
	var kinds = map[string]string{}
	for _, f := range cpFiles(files) {
		base := f.path[strings.LastIndex(f.path, "/")+1:]
		if base == "main" {
			main, kinds = parseCpanelMain(f.body)
			continue
		}
		root := cpDocRoot.FindStringSubmatch(f.body)
		if root == nil || !domainName.MatchString(base) || !ValidRemotePath(strings.TrimSpace(root[1])) {
			continue
		}
		dom := Domain{Name: strings.ToLower(base), DocRoot: strings.TrimSuffix(strings.TrimSpace(root[1]), "/"), SizeKB: -1}
		if m := cpPHP.FindStringSubmatch(f.body); m != nil && a.PHP == "" {
			a.PHP = m[1] + "." + m[2]
		}
		a.Domains = append(a.Domains, dom)
	}
	for i := range a.Domains {
		switch {
		case a.Domains[i].Name == main:
			a.Domains[i].Kind = "main"
		case kinds[a.Domains[i].Name] != "":
			a.Domains[i].Kind = kinds[a.Domains[i].Name]
		default:
			a.Domains[i].Kind = "sub"
		}
	}
	sort.SliceStable(a.Domains, func(i, j int) bool {
		if (a.Domains[i].Kind == "main") != (a.Domains[j].Kind == "main") {
			return a.Domains[i].Kind == "main"
		}
		return a.Domains[i].Name < a.Domains[j].Name
	})
	section := ""
	dom := ""
	sizes := map[string]int64{}
	for _, l := range strings.Split(rest, "\n") {
		l = strings.TrimRight(l, "\r")
		switch {
		case strings.HasPrefix(l, "##home"):
			section = "home"
		case strings.HasPrefix(l, "##mail"):
			section = "mail"
		case strings.HasPrefix(l, "##msize"):
			section = "msize"
		case strings.HasPrefix(l, "##fwd"):
			section = "fwd"
		case strings.HasPrefix(l, "##ftp"):
			section = "ftp"
		case strings.HasPrefix(l, "##cron"):
			section = "cron"
		case section == "ftp":
			if f, ok := parseProFTPD(a.ID, l); ok {
				a.FTP = append(a.FTP, f)
			}
		case section == "fwd" && strings.HasPrefix(l, "##vdom "):
			dom = strings.TrimSpace(strings.TrimPrefix(l, "##vdom "))
		case section == "fwd":
			if f, ok := parseValias(dom, l); ok {
				a.Forwarders = append(a.Forwarders, f)
			}
		case section == "msize":
			if f := strings.SplitN(l, "\t", 2); len(f) == 2 {
				if n, err := strconv.ParseInt(strings.TrimSpace(f[0]), 10, 64); err == nil {
					sizes[strings.TrimRight(strings.TrimSpace(f[1]), "/")] = n
				}
			}
		case section == "home" && strings.HasPrefix(l, "/"):
			if ValidRemotePath(l) {
				a.Home = l
			}
		case section == "mail" && strings.HasPrefix(l, "##dom "):
			dom = strings.TrimSpace(strings.TrimPrefix(l, "##dom "))
		case section == "mail" && dom != "" && domainName.MatchString(dom) && regexp.MustCompile(`^[A-Za-z0-9._+-]+$`).MatchString(l):
			a.Email = append(a.Email, EmailAccount{Address: l + "@" + dom, Maildir: a.Home + "/mail/" + dom + "/" + l, SizeKB: -1})
		case section == "cron":
			if j, ok := parseCron(l); ok {
				a.Cron = append(a.Cron, j)
			}
		}
	}
	for i := range a.Email {
		if n, ok := sizes[a.Email[i].Maildir]; ok {
			a.Email[i].SizeKB = n
		}
	}
}

type cpFile struct{ path, body string }

func cpFiles(s string) []cpFile {
	idx := fileMarker.FindAllStringSubmatchIndex(s, -1)
	var files []cpFile
	for i, m := range idx {
		end := len(s)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		files = append(files, cpFile{path: s[m[2]:m[3]], body: s[m[1]:end]})
	}
	return files
}

// parseCpanelMain reads the account's "main" userdata file: the main domain
// and the kind of each other domain.
func parseCpanelMain(body string) (main string, kinds map[string]string) {
	kinds = map[string]string{}
	list := ""
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimRight(l, "\r")
		switch {
		case strings.HasPrefix(l, "main_domain:"):
			main = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(l, "main_domain:")))
			list = ""
		case l == "sub_domains:":
			list = "sub"
		case l == "addon_domains:":
			list = "addon"
		case l == "parked_domains:":
			list = "parked"
		case l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "-"):
			list = ""
		case list != "" && strings.TrimSpace(l) != "":
			v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "-"))
			name, _, _ := strings.Cut(v, ":")
			name = strings.ToLower(strings.TrimSpace(name))
			if domainName.MatchString(name) {
				kinds[name] = list
			}
		}
	}
	return main, kinds
}

var cronRe = regexp.MustCompile(`^\s*((?:@\w+)|(?:\S+\s+\S+\s+\S+\s+\S+\s+\S+))\s+(\S.*)$`)

// parseCron reads one crontab line; comments, blanks and variable settings are skipped.
func parseCron(l string) (CronJob, bool) {
	l = strings.TrimSpace(l)
	if l == "" || strings.HasPrefix(l, "#") || regexp.MustCompile(`^[A-Za-z_]+=`).MatchString(l) {
		return CronJob{}, false
	}
	m := cronRe.FindStringSubmatch(l)
	if m == nil {
		return CronJob{}, false
	}
	return CronJob{Schedule: m[1], Command: m[2]}, true
}

var (
	valiasLocal = regexp.MustCompile(`^(\*|[A-Za-z0-9][A-Za-z0-9._+-]{0,63})$`)
	valiasAddr  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}@[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`)
)

// parseValias reads one line of /etc/valiases/<domain>: "local: dest1, dest2". A line that does
// anything but forward to plain addresses (a pipe, :fail:, :blackhole:, a local mailbox name) is skipped.
func parseValias(dom, line string) (Forwarder, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || !domainName.MatchString(dom) {
		return Forwarder{}, false
	}
	local, rest, ok := strings.Cut(line, ":")
	local = strings.TrimSpace(local)
	if !ok || !valiasLocal.MatchString(local) {
		return Forwarder{}, false
	}
	var dests []string
	for _, d := range strings.Split(rest, ",") {
		d = strings.TrimSpace(d)
		if !valiasAddr.MatchString(d) {
			return Forwarder{}, false
		}
		dests = append(dests, strings.ToLower(d))
	}
	if len(dests) == 0 || len(dests) > 50 {
		return Forwarder{}, false
	}
	return Forwarder{Source: strings.ToLower(local) + "@" + strings.ToLower(dom), Destinations: dests}, true
}

var ftpLoginRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@+-]{0,63}$`)

// parseProFTPD reads one line of cPanel's /etc/proftpd/<user> (a passwd-style file: login, hash, uid, gid,
// comment, home, shell). The account's own login and the anonymous one are not extra FTP accounts, and the
// password hash is never kept.
func parseProFTPD(user, line string) (FTPAccount, bool) {
	f := strings.Split(strings.TrimSpace(line), ":")
	if len(f) < 6 {
		return FTPAccount{}, false
	}
	login, home := f[0], strings.TrimSuffix(f[5], "/")
	if !ftpLoginRe.MatchString(login) || login == user || login == "ftp" || login == "anonymous" || !ValidRemotePath(home) {
		return FTPAccount{}, false
	}
	return FTPAccount{Login: login, Home: home}, true
}
