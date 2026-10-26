package hosting

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Describe is the description of an account written into its backup
// (AccountBackupService::describe): the user, websites, databases, database
// users with their grants, DNS zones (with the zone file as served) and cron
// jobs. It also returns the per-website backup requests. Only hosting
// accounts (own-scope roles) have account backups.
func (s *AccountBackupService) Describe(ctx context.Context, userID int64) (map[string]any, []map[string]any, error) {
	var username, email, display, role, scope string
	var plan, parent sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT u.username, u.email, COALESCE(u.display_name,''), r.name, r.scope, p.name, par.username
		 FROM users u JOIN roles r ON r.id = u.role_id LEFT JOIN plans p ON p.id = u.plan_id LEFT JOIN users par ON par.id = u.parent_id
		 WHERE u.id = ?`, userID).Scan(&username, &email, &display, &role, &scope, &plan, &parent)
	if err == sql.ErrNoRows {
		return nil, nil, &UserError{What: "The account no longer exists."}
	}
	if err != nil {
		return nil, nil, err
	}
	if scope != "own" {
		return nil, nil, &UserError{What: "Only hosting accounts (customers and resellers) have account backups."}
	}
	opts, _ := NewAccountOptionStore(s.db).Get(ctx, &userID)

	type siteRow struct {
		id     int64
		parent sql.NullInt64
	}
	var ids []siteRow
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, parent_id FROM websites WHERE server_id = ? AND owner_id = ? ORDER BY parent_id IS NOT NULL, id`, s.serverID, userID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var r siteRow
		if err := rows.Scan(&r.id, &r.parent); err != nil {
			rows.Close()
			return nil, nil, err
		}
		ids = append(ids, r)
	}
	rows.Close()

	repo := NewWebsiteRepository(s.db)
	backups := &BackupService{db: s.db}
	byID := map[int64]*Website{}
	var order []*Website
	for _, r := range ids {
		w, err := repo.FindFull(ctx, s.serverID, r.id, nil)
		if err != nil || w == nil {
			continue
		}
		byID[w.ID] = w
		order = append(order, w)
	}
	websites := []map[string]any{}
	requests := []map[string]any{}
	for _, w := range order {
		kind, parentDomain := "main", any(nil)
		if w.ParentID.Valid {
			if p := byID[w.ParentID.Int64]; p != nil {
				parentDomain = p.Domain
				kind = "addon"
				if strings.HasSuffix(w.Domain, "."+p.Domain) {
					kind = "subdomain"
				}
			}
		}
		dbs, _ := backups.SiteDatabases(ctx, w.ID)
		aliases := w.Aliases
		if aliases == nil {
			aliases = []string{}
		}
		websites = append(websites, map[string]any{
			"domain": w.Domain, "type": kind, "parent": parentDomain, "kind": w.Kind, "php_version": w.PhpVersion,
			"system_user": w.SystemUser, "aliases": aliases, "proxy_pass": w.ProxyPass, "databases": dbs,
		})
		requests = append(requests, map[string]any{
			"domain": w.Domain, "user": w.SystemUser, "root": w.Root, "files": true, "databases": dbs,
			"label": "Account backup " + username,
			"meta":  map[string]any{"website": Spec(w, false), "panel_version": 1},
		})
	}

	// Databases the account owns or that belong to its websites.
	dbNames := map[int64]string{}
	dbList := []map[string]any{}
	rows, err = s.db.QueryContext(ctx,
		`SELECT d.id, d.name, w.domain FROM databases d LEFT JOIN websites w ON w.id = d.website_id
		 WHERE d.server_id = ? AND (d.owner_id = ? OR w.owner_id = ?) ORDER BY d.name`, s.serverID, userID, userID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id int64
		var name string
		var site sql.NullString
		if err := rows.Scan(&id, &name, &site); err != nil {
			rows.Close()
			return nil, nil, err
		}
		dbNames[id] = name
		var siteV any
		if site.Valid {
			siteV = site.String
		}
		dbList = append(dbList, map[string]any{"name": name, "website": siteV})
	}
	rows.Close()

	type du struct {
		id         int64
		name, host string
	}
	var dus []du
	rows, err = s.db.QueryContext(ctx, `SELECT id, username, host FROM database_users WHERE server_id = ? AND owner_id = ? ORDER BY username`, s.serverID, userID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var d du
		if err := rows.Scan(&d.id, &d.name, &d.host); err != nil {
			rows.Close()
			return nil, nil, err
		}
		dus = append(dus, d)
	}
	rows.Close()
	dbUsers := []map[string]any{}
	for _, d := range dus {
		grants := []map[string]any{}
		grows, err := s.db.QueryContext(ctx, `SELECT database_id, privileges FROM database_grants WHERE user_id = ?`, d.id)
		if err != nil {
			return nil, nil, err
		}
		for grows.Next() {
			var dbID int64
			var priv string
			if err := grows.Scan(&dbID, &priv); err != nil {
				grows.Close()
				return nil, nil, err
			}
			if name, ok := dbNames[dbID]; ok {
				grants = append(grants, map[string]any{"database": name, "privileges": priv})
			}
		}
		grows.Close()
		dbUsers = append(dbUsers, map[string]any{"name": d.name, "host": d.host, "grants": grants})
	}

	type zr struct {
		id   int64
		name string
		site sql.NullString
	}
	var zs []zr
	rows, err = s.db.QueryContext(ctx,
		`SELECT z.id, z.name, w.domain FROM dns_zones z LEFT JOIN websites w ON w.id = z.website_id
		 WHERE z.server_id = ? AND z.owner_id = ? ORDER BY z.name`, s.serverID, userID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var z zr
		if err := rows.Scan(&z.id, &z.name, &z.site); err != nil {
			rows.Close()
			return nil, nil, err
		}
		zs = append(zs, z)
	}
	rows.Close()
	dns := NewDnsService(s.db, NewDnsRepository(s.db), s.agent, nil, repo, nil)
	zones := []map[string]any{}
	for _, z := range zs {
		text, err := dns.Export(ctx, z.id)
		if err != nil {
			text = ""
		}
		var siteV any
		if z.site.Valid {
			siteV = z.site.String
		}
		zones = append(zones, map[string]any{"name": z.name, "website": siteV, "zonefile": text})
	}

	cron := []map[string]any{}
	for _, w := range order {
		crows, err := s.db.QueryContext(ctx, `SELECT schedule, command, description, enabled FROM cron_jobs WHERE website_id = ? ORDER BY id`, w.ID)
		if err != nil {
			return nil, nil, err
		}
		for crows.Next() {
			var sch, cmd, desc string
			var en int
			if err := crows.Scan(&sch, &cmd, &desc, &en); err != nil {
				crows.Close()
				return nil, nil, err
			}
			cron = append(cron, map[string]any{"website": w.Domain, "schedule": sch, "command": cmd, "description": desc, "enabled": en == 1})
		}
		crows.Close()
	}

	var planV, parentV any
	if plan.Valid {
		planV = plan.String
	}
	if parent.Valid {
		parentV = parent.String
	}
	meta := map[string]any{
		"format": accountBackupFormat, "made_at": time.Now().UTC().Format(time.RFC3339),
		"user": map[string]any{
			"username": username, "email": email, "display_name": display, "role": role,
			"plan": planV, "parent": parentV, "options": opts,
		},
		"websites": websites, "databases": dbList, "database_users": dbUsers, "dns_zones": zones, "cron_jobs": cron,
	}
	return meta, requests, nil
}

const accountBackupFormat = 1
