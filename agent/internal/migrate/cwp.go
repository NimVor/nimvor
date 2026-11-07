package migrate

import (
	"context"
	"fmt"
	"strings"
)

// cwpAdapter reads CentOS Web Panel (CWP, now also on AlmaLinux/Rocky). CWP keeps its users and
// domains in the root_cwp database (tables user, domains, subdomains) and its mail in the postfix
// database (mailbox, alias). Websites live under /home/<user>/public_html, mail under /var/vmail.
// CWP has no table linking databases to users: databases are named "<user>_<name>", so a database
// is attached to the user whose name is its prefix. Read-only SELECTs as root; tested on synthetic
// rows only, and a failing query is reported instead of guessed around.
type cwpAdapter struct{}

func (cwpAdapter) ID() string       { return "cwp" }
func (cwpAdapter) Name() string     { return "CentOS Web Panel (CWP)" }
func (cwpAdapter) Support() Support { return Experimental }

func (cwpAdapter) Detect(ctx context.Context, t Transport) (Detection, bool, error) {
	out, err := t.Exec(ctx, "sh -c "+Shq("[ -d /usr/local/cwpsrv ] && { echo cwp; cat /usr/local/cwp/.conf/version 2>/dev/null | head -n 1; }; true"), nil)
	if err != nil {
		return Detection{}, false, err
	}
	if !strings.Contains(out, "cwp") {
		return Detection{}, false, nil
	}
	det := Detection{Panel: "cwp", Name: "CentOS Web Panel (CWP)", Support: Experimental}
	if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) > 1 {
		det.Version = strings.TrimSpace(lines[1])
	}
	return det, true, nil
}

func (cwpAdapter) Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error) {
	d := &Discovery{}
	progress("Reading CWP's users and domains…")
	users, err := sqlRows(ctx, t, "root_cwp", "SELECT username, domain FROM user")
	if err != nil {
		return nil, fmt.Errorf("CWP's database could not be read (log in as root): %v", err)
	}
	var doms, subs, dbs, mail, fw [][]string
	if doms, err = sqlRows(ctx, t, "root_cwp", "SELECT domain, user, path FROM domains"); err != nil {
		d.Warnings = append(d.Warnings, "The domains could not be read: "+err.Error())
	}
	if subs, err = sqlRows(ctx, t, "root_cwp", "SELECT CONCAT(subdomain, '.', domain), user, path FROM subdomains"); err != nil {
		d.Warnings = append(d.Warnings, "The subdomains could not be read: "+err.Error())
	}
	if dbs, err = sqlRows(ctx, t, "information_schema", "SELECT schema_name FROM schemata WHERE schema_name LIKE '%\\_%'"); err != nil {
		d.Warnings = append(d.Warnings, "The databases could not be read: "+err.Error())
	}
	if mail, err = sqlRows(ctx, t, "postfix", "SELECT username, maildir FROM mailbox"); err != nil {
		d.Warnings = append(d.Warnings, "The mailboxes could not be read: "+err.Error())
	}
	accts := buildCWP(users, doms, subs, dbs, mail)
	dropped := 0
	for _, a := range accts {
		var keep []EmailAccount
		for _, m := range a.Email {
			if out, err := t.Exec(ctx, "sh -c "+Shq("[ -d "+Shq(m.Maildir+"/cur")+" ] && echo yes; true"), nil); err == nil && strings.Contains(out, "yes") {
				keep = append(keep, m)
			} else {
				dropped++
			}
		}
		a.Email = keep
	}
	if dropped > 0 {
		d.Warnings = append(d.Warnings, fmt.Sprintf("%d mailbox(es) were left out because their folder is not a Maildir on the source.", dropped))
	}
	if fw, err = sqlRows(ctx, t, "postfix", "SELECT address, goto FROM alias"); err != nil {
		d.Warnings = append(d.Warnings, "The forwarders could not be read: "+err.Error())
	} else {
		accts = attachForwarders(accts, cwpForwarders(fw))
	}
	if len(accts) == 0 {
		d.Warnings = append(d.Warnings, "No users were found in CWP.")
	}
	d.Accounts = finishAccounts(ctx, t, info, accts, progress)
	d.Warnings = append(d.Warnings, "DNS, FTP and cron are not read from CWP.")
	return d, nil
}

// cwpPath makes a CWP path absolute: it is stored relative to the user's home ("/public_html/x")
// or sometimes in full ("/home/user/public_html/x").
func cwpPath(home, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/home/") {
		p = home + "/" + strings.TrimPrefix(p, "/")
	}
	if !ValidRemotePath(p) {
		return ""
	}
	return strings.TrimSuffix(p, "/")
}

func buildCWP(users, doms, subs, dbs, mail [][]string) []*Account {
	set := newAcctSet(func(id string) string { return "/home/" + id })
	known := map[string]bool{}
	for _, r := range users {
		u, main := cell(r, 0), strings.ToLower(cell(r, 1))
		if !pleskLogin.MatchString(u) || u == "root" {
			continue
		}
		known[u] = true
		a := set.get(u)
		if domainName.MatchString(main) {
			a.Domains = append(a.Domains, Domain{Name: main, Kind: "main", DocRoot: a.Home + "/public_html", SizeKB: -1})
		}
	}
	addDomain := func(r []string, kind string) {
		dn, u := strings.ToLower(cell(r, 0)), cell(r, 1)
		if !known[u] || !domainName.MatchString(dn) {
			return
		}
		a := set.get(u)
		root := cwpPath(a.Home, cell(r, 2))
		if root == "" {
			return
		}
		for i := range a.Domains {
			if a.Domains[i].Name == dn { // the user's main domain: keep it, take the stored path
				a.Domains[i].DocRoot = root
				return
			}
		}
		a.Domains = append(a.Domains, Domain{Name: dn, Kind: kind, DocRoot: root, SizeKB: -1})
	}
	for _, r := range doms {
		addDomain(r, "addon")
	}
	for _, r := range subs {
		addDomain(r, "sub")
	}
	for _, r := range dbs {
		name := cell(r, 0)
		pre, _, ok := strings.Cut(name, "_")
		if ok && known[pre] && dbNameRe.MatchString(name) {
			set.get(pre).Databases = append(set.get(pre).Databases, Database{Name: name, Engine: "mysql", SizeKB: -1})
		}
	}
	owner := map[string]string{}
	for u := range known {
		for _, d := range set.get(u).Domains {
			owner[d.Name] = u
		}
	}
	for _, r := range mail {
		addr, dir := strings.ToLower(cell(r, 0)), cell(r, 1)
		m := mailAddrRe.FindStringSubmatch(addr)
		u := ""
		if m != nil {
			u = owner[m[2]]
		}
		if u == "" {
			continue
		}
		if !strings.HasPrefix(dir, "/") {
			dir = "/var/vmail/" + dir
		}
		if !ValidRemotePath(dir) {
			continue
		}
		set.get(u).Email = append(set.get(u).Email, EmailAccount{Address: addr, Maildir: strings.TrimSuffix(dir, "/"), SizeKB: -1})
	}
	return set.list()
}

// cwpForwarders keeps the rows of the alias table that send somewhere else: postfix admin tables
// also hold one self-referencing row per mailbox (address == goto), which is not a forwarder.
func cwpForwarders(rows [][]string) []Forwarder {
	var keep [][]string
	for _, r := range rows {
		if strings.EqualFold(cell(r, 0), cell(r, 1)) {
			continue
		}
		keep = append(keep, r)
	}
	return forwardersFromRows(keep)
}
