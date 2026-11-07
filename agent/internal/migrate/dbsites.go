package migrate

import (
	"context"
	"fmt"
	"strings"
)

// Some panels (FASTPANEL, aaPanel, CloudPanel) do not name a database after its site, so the databases
// were listed for the whole server. When the panel can say which site a database belongs to, the link
// is read here; when it cannot (the tool is missing, a column is named differently) nothing is guessed
// and the databases stay in the server-wide list, as before.

// sqliteRows runs one SELECT against a panel's SQLite file through the sqlite3 client (read-only).
func sqliteRows(ctx context.Context, t Transport, file, query string) ([][]string, error) {
	if !ValidRemotePath(file) {
		return nil, fmt.Errorf("the file name %q is not plain", file)
	}
	out, err := t.Exec(ctx, "sh -c "+Shq(fmt.Sprintf("sqlite3 -readonly -separator '|' %s %s 2>&1", Shq(file), Shq(query))), nil)
	if err != nil {
		return nil, err
	}
	var rows [][]string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if strings.HasPrefix(l, "Error:") || strings.HasPrefix(l, "Parse error") || strings.Contains(l, "command not found") || strings.Contains(l, "not found") && !strings.Contains(l, "|") {
			return nil, fmt.Errorf("%s", truncate(l, 160))
		}
		rows = append(rows, strings.Split(l, "|"))
	}
	return rows, nil
}

// dbSiteMap maps a database name to the domain of its site, from (database, site) rows.
func dbSiteMap(rows [][]string) map[string]string {
	m := map[string]string{}
	for _, r := range rows {
		name, site := cell(r, 0), strings.ToLower(cell(r, 1))
		if dbNameRe.MatchString(name) && domainName.MatchString(site) {
			m[name] = site
		}
	}
	return m
}

// placeDatabases puts each database into the account that owns its site and returns the ones it could not
// place, for the server-wide list.
func placeDatabases(accts []*Account, dbs []Database, siteOf map[string]string) []Database {
	owner := map[string]*Account{}
	for _, a := range accts {
		for _, d := range a.Domains {
			owner[strings.ToLower(d.Name)] = a
		}
	}
	var rest []Database
	for _, db := range dbs {
		if a := owner[siteOf[db.Name]]; a != nil && siteOf[db.Name] != "" {
			a.Databases = append(a.Databases, db)
		} else {
			rest = append(rest, db)
		}
	}
	return rest
}

// parseFastPanelDatabases reads `mogwai databases list`: a NAME column and a column naming the site
// (SITE, SITE_NAME or SERVER_NAME). The result maps a database to the site's domain.
func parseFastPanelDatabases(out string) map[string]string {
	rows := parseCLITable(out, "NAME")
	m := map[string]string{}
	for _, r := range rows {
		name := r["NAME"]
		site := ""
		for _, k := range []string{"SITE", "SITE_NAME", "SERVER_NAME", "DOMAIN"} {
			if v := strings.ToLower(strings.TrimSpace(r[k])); domainName.MatchString(v) {
				site = v
				break
			}
		}
		if dbNameRe.MatchString(name) && site != "" {
			m[name] = site
		}
	}
	return m
}
