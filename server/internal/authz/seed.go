package authz

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// This file ports the code-defined permission catalogue and default roles from
// Security\Permissions, and the idempotent sync from
// RoleRepository::syncDefaults. The PHP panel runs the sync at boot so the DB
// always mirrors the code; the Go panel does the same (cmd/whacp calls
// SyncDefaults after migrating). It never creates users — the initial admin
// account is the installer's responsibility, exactly as in the PHP panel.

// Permission is one catalogue entry (name, label, group), kept in the PHP
// order so listings match.
type Permission struct {
	Name  string
	Label string
	Group string
}

// Catalogue mirrors Permissions::all() verbatim (name, label, group, order).
var Catalogue = []Permission{
	{"dashboard.view", "View dashboard", "Overview"},
	{"monitoring.view", "View monitoring, live load and running processes", "Overview"},
	{"notifications.view", "See notifications (job results and alerts, within the role's scope)", "Overview"},

	{"websites.view", "View websites", "Hosting"},
	{"websites.manage", "Create, change and delete websites", "Hosting"},
	{"php.view", "View PHP versions and pools", "Hosting"},
	{"php.manage", "Install and control PHP versions", "Hosting"},
	{"databases.view", "View databases", "Hosting"},
	{"databases.manage", "Manage databases and database users", "Hosting"},
	{"ssl.view", "View certificates", "Hosting"},
	{"ssl.manage", "Issue, renew and remove certificates", "Hosting"},
	{"files.manage", "Use the file manager and a website's terminal", "Hosting"},
	{"logs.view", "View logs (web, PHP, mail, database, security, system)", "Hosting"},
	{"cron.view", "View cron jobs", "Hosting"},
	{"cron.manage", "Manage cron jobs", "Hosting"},
	{"workers.manage", "Manage background workers", "Hosting"},
	{"deploy.manage", "Manage Git deployments", "Hosting"},
	{"redis.manage", "Manage Redis and Memcached", "Hosting"},
	{"ftp.manage", "Manage FTP accounts and SFTP access of websites", "Hosting"},
	{"dns.manage", "Manage DNS zones and records", "Hosting"},
	{"mail.manage", "Manage email domains, mailboxes and aliases", "Hosting"},
	{"backups.view", "View backups", "Hosting"},
	{"backups.manage", "Create, restore and delete backups", "Hosting"},
	{"apps.install", "Install applications", "Hosting"},
	{"discovery.run", "Run server discovery", "Hosting"},
	{"import.run", "Import existing websites", "Hosting"},

	{"server.view", "View server information, IP addresses, mail and database server status", "Server"},
	{"server.manage", "Configure the server: reboot, update, host name, IP addresses, mail and database servers, resource limits, processes, server backups", "Server"},
	{"services.view", "View services", "Server"},
	{"services.manage", "Start, stop and restart services", "Server"},
	{"system_users.view", "View system accounts", "Server"},
	{"packages.manage", "Install and update packages and server components", "Server"},
	{"servers.manage", "Add and remove servers", "Server"},
	{"mongo.view", "View the MongoDB server, its databases, users and backups, and browse data", "Server"},
	{"mongo.manage", "Manage MongoDB: databases, users, data, configuration, and backups/restores", "Server"},
	{"security.view", "View security status, firewall events and scans", "Security"},
	{"security.manage", "Change security: firewall, SSH, ModSecurity, brute-force protection, malware scans, policies, the root terminal", "Security"},

	{"users.view", "View panel users", "Administration"},
	{"users.manage", "Create, edit, suspend and terminate users and hosting accounts; bandwidth limits", "Administration"},
	{"roles.manage", "Create and edit roles (never beyond one's own permissions)", "Administration"},
	{"plans.manage", "Manage hosting plans and feature lists", "Administration"},
	{"settings.view", "View settings", "Administration"},
	{"settings.manage", "Change settings", "Administration"},
	{"audit.view", "View audit log", "Administration"},
	{"api.tokens", "Create personal API tokens", "Administration"},
	{"plugins.manage", "Install and enable plugins", "Administration"},
}

// hostingSelf is Permissions::HOSTING_SELF — what a hosting customer may hold
// for their own websites.
var hostingSelf = []string{
	"dashboard.view", "notifications.view", "websites.view", "websites.manage", "php.view",
	"databases.view", "databases.manage", "ssl.view", "ssl.manage",
	"files.manage", "logs.view", "cron.view", "cron.manage", "workers.manage",
	"deploy.manage", "backups.view", "backups.manage", "apps.install", "api.tokens", "ftp.manage", "dns.manage", "mail.manage",
}

// DefaultRole mirrors one entry of Permissions::defaultRoles().
// Permissions == ["*"] means every catalogue permission (plus plugin perms).
type DefaultRole struct {
	Name        string
	Label       string
	Description string
	Scope       string
	Permissions []string
}

// DefaultRoles mirrors Permissions::defaultRoles(), in order.
var DefaultRoles = []DefaultRole{
	{"admin", "Administrator", "Full access to every feature.", "all", []string{"*"}},
	{"operator", "Operator",
		"Runs day-to-day hosting: websites, PHP, databases, certificates, files, jobs and services. Cannot manage users, settings, security or reboot the server.",
		"all",
		concat(hostingSelf, []string{
			"monitoring.view", "server.view", "services.view", "services.manage", "system_users.view",
			"php.manage", "redis.manage", "discovery.run", "security.view", "audit.view",
			"mongo.view", "mongo.manage",
		})},
	{"viewer", "Viewer", "Read-only access to the dashboard and hosting inventory.", "all",
		[]string{
			"dashboard.view", "notifications.view", "monitoring.view", "server.view", "services.view",
			"websites.view", "php.view", "databases.view", "ssl.view", "cron.view", "backups.view", "mongo.view",
		}},
	{"reseller", "Reseller", "Manages their own customers and those customers' websites within plan limits.", "own",
		concat(hostingSelf, []string{"users.view", "users.manage", "plans.manage"})},
	{"customer", "Hosting customer", "Manages only their own websites, databases, files and jobs, within plan limits.", "own",
		hostingSelf},
}

func concat(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	out = append(out, a...)
	return append(out, b...)
}

// SyncDefaults mirrors the code-defined permission catalogue and default roles
// into the database. It is idempotent: custom grants on non-system roles are
// kept, a built-in role an administrator customised keeps its grants (except
// the Administrator, which always has everything), and plugin.* permissions
// are left untouched. Ported from RoleRepository::syncDefaults.
func SyncDefaults(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)

	// 1. Upsert the permission catalogue.
	names := make([]string, 0, len(Catalogue))
	for _, p := range Catalogue {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO permissions (name, label, grp) VALUES (?, ?, ?)
			 ON CONFLICT(name) DO UPDATE SET label = excluded.label, grp = excluded.grp`,
			p.Name, p.Label, p.Group); err != nil {
			return err
		}
		names = append(names, p.Name)
	}

	// 2. Remove permissions no longer in the catalogue, except plugin.* (owned
	//    by the plugin manager).
	place := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	delArgs := make([]any, len(names))
	for i, n := range names {
		delArgs[i] = n
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM permissions WHERE name NOT IN (`+place+`) AND name NOT LIKE 'plugin.%'`, delArgs...); err != nil {
		return err
	}

	// 3. Collect surviving plugin permissions (granted to '*' roles too).
	pluginPerms, err := scanStrings(ctx, tx, `SELECT name FROM permissions WHERE name LIKE 'plugin.%'`)
	if err != nil {
		return err
	}

	// 4. Upsert each default role and (re)apply its grants.
	for _, role := range DefaultRoles {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO roles (name, label, description, is_system, scope, created_at) VALUES (?, ?, ?, 1, ?, ?)
			 ON CONFLICT(name) DO UPDATE SET label = excluded.label, description = excluded.description, is_system = 1, scope = excluded.scope`,
			role.Name, role.Label, role.Description, role.Scope, now); err != nil {
			return err
		}
		var roleID int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM roles WHERE name = ?`, role.Name).Scan(&roleID); err != nil {
			return err
		}

		isWildcard := len(role.Permissions) == 1 && role.Permissions[0] == "*"
		if !isWildcard {
			var customized int
			if err := tx.QueryRowContext(ctx, `SELECT customized FROM roles WHERE id = ?`, roleID).Scan(&customized); err != nil {
				return err
			}
			if customized == 1 {
				// An administrator-customised built-in role keeps its grants.
				continue
			}
		}

		grants := role.Permissions
		if isWildcard {
			grants = concat(names, pluginPerms)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id = ?`, roleID); err != nil {
			return err
		}
		for _, perm := range grants {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO role_permissions (role_id, permission_id) SELECT ?, id FROM permissions WHERE name = ?`,
				roleID, perm); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func scanStrings(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
