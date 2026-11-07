package migrate

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Panels that keep their accounts in a MySQL/MariaDB database (CyberPanel, ISPConfig, Froxlor) are read
// with read-only SELECTs through the mysql client as root. The table and column names come from each
// project's source and are tested here on synthetic rows only: if a query fails (a different version, a
// renamed column) the adapter says so in the discovery warnings instead of guessing.

var sqlIdent = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// sqlRows runs one SELECT and returns its tab-separated rows. A line the server reports as an error
// is returned as the error.
func sqlRows(ctx context.Context, t Transport, db, query string) ([][]string, error) {
	if !sqlIdent.MatchString(db) {
		return nil, fmt.Errorf("the database name %q is not plain", db)
	}
	out, err := t.Exec(ctx, "sh -c "+Shq(fmt.Sprintf("mysql -N -B %s -e %s 2>&1", db, Shq(query))), nil)
	if err != nil {
		return nil, err
	}
	var rows [][]string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if strings.HasPrefix(l, "ERROR ") || strings.Contains(l, "command not found") {
			return nil, fmt.Errorf("%s", truncate(l, 160))
		}
		rows = append(rows, strings.Split(l, "\t"))
	}
	return rows, nil
}

// finishAccounts measures document roots and mailboxes and fills database sizes.
func finishAccounts(ctx context.Context, t Transport, info ServerInfo, accts []*Account, progress func(string)) []Account {
	var dirs []string
	for _, a := range accts {
		for _, d := range a.Domains {
			dirs = append(dirs, d.DocRoot)
		}
		for _, m := range a.Email {
			dirs = append(dirs, m.Maildir)
		}
	}
	progress("Reading scheduled jobs…")
	addCrontabs(ctx, t, accts)
	progress("Measuring folders…")
	sz, _ := measure(ctx, t, uniq(dirs))
	size := map[string]int64{}
	if s, err := t.Exec(ctx, "sh -c "+Shq(dbScript), nil); err == nil {
		for _, db := range parseSchemas(s, info.Engine) {
			size[db.Name] = db.SizeKB
		}
	}
	var out []Account
	for _, a := range accts {
		for i := range a.Domains {
			if n, ok := sz[a.Domains[i].DocRoot]; ok {
				a.Domains[i].SizeKB = n
			}
		}
		for i := range a.Email {
			if n, ok := sz[a.Email[i].Maildir]; ok {
				a.Email[i].SizeKB = n
			}
		}
		for i := range a.Databases {
			if n, ok := size[a.Databases[i].Name]; ok {
				a.Databases[i].SizeKB = n
			}
		}
		out = append(out, *a)
	}
	return out
}

func newAccount(id, home string) *Account {
	return &Account{ID: id, Name: id, Home: home, Domains: []Domain{}, Databases: []Database{}, Email: []EmailAccount{}, Forwarders: []Forwarder{}, FTP: []FTPAccount{}, Cron: []CronJob{}}
}

// acctSet collects accounts by key, in a stable order.
type acctSet struct {
	m     map[string]*Account
	order []string
	home  func(string) string
}

func newAcctSet(home func(string) string) *acctSet {
	return &acctSet{m: map[string]*Account{}, home: home}
}

func (s *acctSet) get(id string) *Account {
	if a := s.m[id]; a != nil {
		return a
	}
	a := newAccount(id, s.home(id))
	s.m[id] = a
	s.order = append(s.order, id)
	return a
}

func (s *acctSet) list() []*Account {
	sort.Strings(s.order)
	var out []*Account
	for _, k := range s.order {
		out = append(out, s.m[k])
	}
	return out
}

func cell(r []string, i int) string {
	if i < len(r) {
		return strings.TrimSpace(r[i])
	}
	return ""
}

// ---- CyberPanel ----------------------------------------------------------------

// cyberPanelAdapter reads CyberPanel (OpenLiteSpeed) from its own `cyberpanel` database: websites,
// child domains and databases. A website's files are in /home/<domain>/public_html. Mailboxes and
// everything else are not read.
type cyberPanelAdapter struct{}

func (cyberPanelAdapter) ID() string       { return "cyberpanel" }
func (cyberPanelAdapter) Name() string     { return "CyberPanel" }
func (cyberPanelAdapter) Support() Support { return Experimental }

func (cyberPanelAdapter) Detect(ctx context.Context, t Transport) (Detection, bool, error) {
	out, err := t.Exec(ctx, "sh -c "+Shq("[ -d /usr/local/CyberCP ] && echo cyberpanel; true"), nil)
	if err != nil {
		return Detection{}, false, err
	}
	if !strings.Contains(out, "cyberpanel") {
		return Detection{}, false, nil
	}
	return Detection{Panel: "cyberpanel", Name: "CyberPanel", Support: Experimental}, true, nil
}

func (cyberPanelAdapter) Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error) {
	d := &Discovery{}
	progress("Reading CyberPanel's websites…")
	sites, err := sqlRows(ctx, t, "cyberpanel", "SELECT domain FROM websiteFunctions_websites")
	if err != nil {
		return nil, fmt.Errorf("CyberPanel's database could not be read (log in as root): %v", err)
	}
	var children, dbs [][]string
	if children, err = sqlRows(ctx, t, "cyberpanel", "SELECT c.domain, c.path, w.domain FROM websiteFunctions_childdomains c JOIN websiteFunctions_websites w ON w.id = c.master_id"); err != nil {
		d.Warnings = append(d.Warnings, "The child domains could not be read: "+err.Error())
	}
	if dbs, err = sqlRows(ctx, t, "cyberpanel", "SELECT d.dbName, w.domain FROM databases_databases d JOIN websiteFunctions_websites w ON w.id = d.website_id"); err != nil {
		d.Warnings = append(d.Warnings, "The databases could not be read: "+err.Error())
	}
	var mail [][]string
	if mail, err = sqlRows(ctx, t, "cyberpanel", "SELECT email, emailOwner_id FROM e_users"); err != nil {
		d.Warnings = append(d.Warnings, "The mailboxes could not be read: "+err.Error())
	}
	accts := buildCyberPanel(sites, children, dbs)
	// A mailbox is added only where its Maildir really exists on the source, under one of the places
	// CyberPanel's Dovecot setup uses; one that cannot be found is left out and counted.
	missing := 0
	for _, r := range mail {
		email, owner := strings.ToLower(cell(r, 0)), strings.ToLower(cell(r, 1))
		m := mailAddrRe.FindStringSubmatch(email)
		var acct *Account
		for _, a := range accts {
			if a.ID == owner {
				acct = a
			}
		}
		if m == nil || m[2] != owner {
			continue
		}
		if acct == nil {
			acct = newAccount("mail", "/home/vmail")
			acct.Name = "mail (domains without a website)"
			accts = append(accts, acct)
		}
		found := ""
		for _, c := range []string{"/home/vmail/" + owner + "/" + m[1] + "/Maildir", "/home/vmail/" + owner + "/" + m[1]} {
			if out, err := t.Exec(ctx, "sh -c "+Shq("[ -d "+Shq(c+"/cur")+" ] && echo yes; true"), nil); err == nil && strings.Contains(out, "yes") {
				found = c
				break
			}
		}
		if found == "" {
			missing++
			continue
		}
		acct.Email = append(acct.Email, EmailAccount{Address: email, Maildir: found, SizeKB: -1})
	}
	if missing > 0 {
		d.Warnings = append(d.Warnings, fmt.Sprintf("%d mailbox(es) were left out because their Maildir was not found under /home/vmail.", missing))
	}
	if fw, err := sqlRows(ctx, t, "cyberpanel", "SELECT source, destination FROM e_forwardings"); err != nil {
		d.Warnings = append(d.Warnings, "The forwarders could not be read: "+err.Error())
	} else {
		accts = attachForwarders(accts, forwardersFromRows(fw))
	}
	if len(accts) == 0 {
		d.Warnings = append(d.Warnings, "No websites were found in CyberPanel.")
	}
	d.Accounts = finishAccounts(ctx, t, info, accts, progress)
	d.Warnings = append(d.Warnings, "DNS and FTP are not read from CyberPanel.")
	return d, nil
}

func buildCyberPanel(sites, children, dbs [][]string) []*Account {
	set := newAcctSet(func(id string) string { return "/home/" + id })
	for _, r := range sites {
		dn := strings.ToLower(cell(r, 0))
		if !domainName.MatchString(dn) {
			continue
		}
		a := set.get(dn)
		a.Domains = append(a.Domains, Domain{Name: dn, Kind: "main", DocRoot: "/home/" + dn + "/public_html", SizeKB: -1})
	}
	for _, r := range children {
		dn, path, parent := strings.ToLower(cell(r, 0)), cell(r, 1), strings.ToLower(cell(r, 2))
		if !domainName.MatchString(dn) || !domainName.MatchString(parent) || !ValidRemotePath(path) || set.m[parent] == nil {
			continue
		}
		set.m[parent].Domains = append(set.m[parent].Domains, Domain{Name: dn, Kind: "sub", DocRoot: strings.TrimSuffix(path, "/"), SizeKB: -1})
	}
	for _, r := range dbs {
		name, owner := cell(r, 0), strings.ToLower(cell(r, 1))
		if !dbNameRe.MatchString(name) || set.m[owner] == nil {
			continue
		}
		set.m[owner].Databases = append(set.m[owner].Databases, Database{Name: name, Engine: "mysql", SizeKB: -1})
	}
	return set.list()
}

// ---- ISPConfig -----------------------------------------------------------------

// ispConfigAdapter reads ISPConfig from its `dbispconfig` database: web sites (web_domain), their
// databases (web_database) and mailboxes (mail_user). A site's files are in <document_root>/web.
type ispConfigAdapter struct{}

func (ispConfigAdapter) ID() string       { return "ispconfig" }
func (ispConfigAdapter) Name() string     { return "ISPConfig" }
func (ispConfigAdapter) Support() Support { return Experimental }

func (ispConfigAdapter) Detect(ctx context.Context, t Transport) (Detection, bool, error) {
	out, err := t.Exec(ctx, "sh -c "+Shq("f=/usr/local/ispconfig/server/lib/config.inc.php; [ -f $f ] && { echo ispconfig; grep -h ISPC_APP_VERSION $f | head -n 1; }; true"), nil)
	if err != nil {
		return Detection{}, false, err
	}
	if !strings.Contains(out, "ispconfig") {
		return Detection{}, false, nil
	}
	ver := ""
	if m := regexp.MustCompile(`ISPC_APP_VERSION'?,\s*'([0-9][0-9.]*)`).FindStringSubmatch(out); m != nil {
		ver = m[1]
	}
	return Detection{Panel: "ispconfig", Name: "ISPConfig", Version: ver, Support: Experimental}, true, nil
}

func (ispConfigAdapter) Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error) {
	d := &Discovery{}
	progress("Reading ISPConfig's websites…")
	sites, err := sqlRows(ctx, t, "dbispconfig", "SELECT domain_id, domain, document_root, system_user, type FROM web_domain WHERE active = 'y' AND type IN ('vhost','vhostsubdomain','vhostalias')")
	if err != nil {
		return nil, fmt.Errorf("ISPConfig's database could not be read (log in as root): %v", err)
	}
	var dbs, mail [][]string
	if dbs, err = sqlRows(ctx, t, "dbispconfig", "SELECT database_name, parent_domain_id FROM web_database"); err != nil {
		d.Warnings = append(d.Warnings, "The databases could not be read: "+err.Error())
	}
	if mail, err = sqlRows(ctx, t, "dbispconfig", "SELECT email, maildir FROM mail_user"); err != nil {
		d.Warnings = append(d.Warnings, "The mailboxes could not be read: "+err.Error())
	}
	accts := buildISPConfig(sites, dbs, mail)
	if fw, err := sqlRows(ctx, t, "dbispconfig", "SELECT source, destination FROM mail_forwarding WHERE active = 'y' AND type IN ('alias','forward')"); err != nil {
		d.Warnings = append(d.Warnings, "The forwarders could not be read: "+err.Error())
	} else {
		accts = attachForwarders(accts, forwardersFromRows(fw))
	}
	if len(accts) == 0 {
		d.Warnings = append(d.Warnings, "No active websites were found in ISPConfig.")
	}
	d.Accounts = finishAccounts(ctx, t, info, accts, progress)
	d.Warnings = append(d.Warnings, "Mailboxes are listed under one account \"mail\" for the whole server. DNS and FTP are not read.")
	return d, nil
}

func buildISPConfig(sites, dbs, mail [][]string) []*Account {
	set := newAcctSet(func(id string) string { return "/var/www/clients" })
	owner := map[string]string{} // domain_id → system user
	for _, r := range sites {
		id, dn, root, user := cell(r, 0), strings.ToLower(cell(r, 1)), cell(r, 2), cell(r, 3)
		if !domainName.MatchString(dn) || !ValidRemotePath(root) || !pleskLogin.MatchString(user) {
			continue
		}
		owner[id] = user
		a := set.get(user)
		a.Home = strings.TrimSuffix(root, "/")
		kind := "main"
		if cell(r, 4) != "vhost" {
			kind = "sub"
		}
		a.Domains = append(a.Domains, Domain{Name: dn, Kind: kind, DocRoot: strings.TrimSuffix(root, "/") + "/web", SizeKB: -1})
	}
	for _, r := range dbs {
		name := cell(r, 0)
		if u := owner[cell(r, 1)]; u != "" && dbNameRe.MatchString(name) {
			set.get(u).Databases = append(set.get(u).Databases, Database{Name: name, Engine: "mysql", SizeKB: -1})
		}
	}
	var mailboxes []EmailAccount
	for _, r := range mail {
		email, dir := strings.ToLower(cell(r, 0)), strings.TrimSuffix(cell(r, 1), "/")
		m := mailAddrRe.FindStringSubmatch(email)
		if m == nil || !ValidRemotePath(dir) {
			continue
		}
		mailboxes = append(mailboxes, EmailAccount{Address: email, Maildir: dir + "/Maildir", SizeKB: -1})
	}
	accts := set.list()
	if len(mailboxes) > 0 {
		a := newAccount("mail", "/var/vmail")
		a.Name = "mail (all mailboxes of the server)"
		a.Email = mailboxes
		accts = append(accts, a)
	}
	return accts
}

// ---- Froxlor -------------------------------------------------------------------

// froxlorAdapter reads Froxlor from its database (named in lib/userdata.inc.php, "froxlor" by
// default): customers (panel_customers), their domains (panel_domains), databases (panel_databases)
// and mailboxes (mail_users). A mailbox is listed only when its Maildir exists on the source.
type froxlorAdapter struct{}

func (froxlorAdapter) ID() string       { return "froxlor" }
func (froxlorAdapter) Name() string     { return "Froxlor" }
func (froxlorAdapter) Support() Support { return Experimental }

const froxlorFind = `for f in /var/www/froxlor/lib/userdata.inc.php /var/www/html/froxlor/lib/userdata.inc.php /var/www/lib/userdata.inc.php; do [ -f "$f" ] && { echo froxlor; grep -h "\['db'\]" "$f" | head -n 1; break; }; done; true`

func (froxlorAdapter) Detect(ctx context.Context, t Transport) (Detection, bool, error) {
	out, err := t.Exec(ctx, "sh -c "+Shq(froxlorFind), nil)
	if err != nil {
		return Detection{}, false, err
	}
	if !strings.Contains(out, "froxlor") {
		return Detection{}, false, nil
	}
	return Detection{Panel: "froxlor", Name: "Froxlor", Support: Experimental}, true, nil
}

var froxlorDB = regexp.MustCompile(`\['db'\]\s*=\s*['"]([A-Za-z0-9_]+)['"]`)

func (froxlorAdapter) Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error) {
	d := &Discovery{}
	db := "froxlor"
	if out, err := t.Exec(ctx, "sh -c "+Shq(froxlorFind), nil); err == nil {
		if m := froxlorDB.FindStringSubmatch(out); m != nil {
			db = m[1]
		}
	}
	progress("Reading Froxlor's customers and domains…")
	cust, err := sqlRows(ctx, t, db, "SELECT customerid, loginname, documentroot FROM panel_customers")
	if err != nil {
		return nil, fmt.Errorf("Froxlor's database could not be read (log in as root): %v", err)
	}
	var doms, dbs, mail [][]string
	if doms, err = sqlRows(ctx, t, db, "SELECT domain, documentroot, customerid, parentdomainid FROM panel_domains"); err != nil {
		d.Warnings = append(d.Warnings, "The domains could not be read: "+err.Error())
	}
	if dbs, err = sqlRows(ctx, t, db, "SELECT databasename, customerid FROM panel_databases"); err != nil {
		d.Warnings = append(d.Warnings, "The databases could not be read: "+err.Error())
	}
	if mail, err = sqlRows(ctx, t, db, "SELECT email, CONCAT(homedir, maildir), customerid FROM mail_users"); err != nil {
		d.Warnings = append(d.Warnings, "The mailboxes could not be read: "+err.Error())
	}
	accts := buildFroxlor(cust, doms, dbs, mail)
	// A mailbox whose folder is not a Maildir on this source is dropped rather than copied wrongly.
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
	if fw, err := sqlRows(ctx, t, db, "SELECT email_full, destination FROM mail_virtual"); err != nil {
		d.Warnings = append(d.Warnings, "The forwarders could not be read: "+err.Error())
	} else {
		accts = attachForwarders(accts, forwardersFromRows(fw))
	}
	if len(accts) == 0 {
		d.Warnings = append(d.Warnings, "No customers were found in Froxlor.")
	}
	d.Accounts = finishAccounts(ctx, t, info, accts, progress)
	d.Warnings = append(d.Warnings, "DNS and FTP are not read from Froxlor.")
	return d, nil
}

func buildFroxlor(cust, doms, dbs, mail [][]string) []*Account {
	set := newAcctSet(func(id string) string { return "/var/customers/webs/" + id })
	byID := map[string]string{}
	for _, r := range cust {
		id, login, root := cell(r, 0), cell(r, 1), cell(r, 2)
		if !pleskLogin.MatchString(login) {
			continue
		}
		byID[id] = login
		a := set.get(login)
		if ValidRemotePath(root) {
			a.Home = strings.TrimSuffix(root, "/")
		}
	}
	for _, r := range doms {
		dn, root, owner := strings.ToLower(cell(r, 0)), cell(r, 1), byID[cell(r, 2)]
		if owner == "" || !domainName.MatchString(dn) || !ValidRemotePath(root) {
			continue
		}
		kind := "main"
		if p := cell(r, 3); p != "" && p != "0" {
			kind = "sub"
		}
		set.get(owner).Domains = append(set.get(owner).Domains, Domain{Name: dn, Kind: kind, DocRoot: strings.TrimSuffix(root, "/"), SizeKB: -1})
	}
	for _, r := range dbs {
		name, owner := cell(r, 0), byID[cell(r, 1)]
		if owner != "" && dbNameRe.MatchString(name) {
			set.get(owner).Databases = append(set.get(owner).Databases, Database{Name: name, Engine: "mysql", SizeKB: -1})
		}
	}
	for _, r := range mail {
		email, dir, owner := strings.ToLower(cell(r, 0)), cell(r, 1), byID[cell(r, 2)]
		if owner == "" || mailAddrRe.FindStringSubmatch(email) == nil || !ValidRemotePath(dir) {
			continue
		}
		set.get(owner).Email = append(set.get(owner).Email, EmailAccount{Address: email, Maildir: strings.TrimSuffix(dir, "/"), SizeKB: -1})
	}
	return set.list()
}

// forwardersFromRows builds forwarders from (source, destinations) rows.
func forwardersFromRows(rows [][]string) []Forwarder {
	var out []Forwarder
	for _, r := range rows {
		if f, ok := makeForwarder(cell(r, 0), splitDests(cell(r, 1))); ok {
			out = append(out, f)
		}
	}
	return out
}
