package hosting

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// seedAddonFamily seeds an addon (site 50) under site 10 with a subdomain
// child (51, folder inside the addon's), the addon's database (plus a shared
// database user), its DNS zone, mail domain, cron job and worker — and an
// addon (60) under site 12 for the visibility guard.
func seedAddonFamily(t *testing.T, h *accountHarness) {
	t.Helper()
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := h.db.Exec(q, a...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO websites (id, server_id, owner_id, parent_id, domain, system_user, root, document_root, kind, php_version, status, created_at, updated_at)
		VALUES (50, 1, 3, 10, 'addon.example', 'u_cust', '/home/whacp/addon.example', '/home/whacp/addon.example/public_html', 'php', '8.3.1', 'active', '', '')`)
	exec(`INSERT INTO domains (server_id, website_id, name, is_primary) VALUES (1, 50, 'addon.example', 1)`)
	// Subdomain of the addon, living inside the addon's folder.
	exec(`INSERT INTO websites (id, server_id, owner_id, parent_id, domain, system_user, root, document_root, kind, php_version, status, created_at, updated_at)
		VALUES (51, 1, 3, 50, 'app.addon.example', 'u_cust', '/home/whacp/addon.example/app', '/home/whacp/addon.example/app/public_html', 'php', '8.3.1', 'active', '', '')`)
	exec(`INSERT INTO domains (server_id, website_id, name, is_primary) VALUES (1, 51, 'app.addon.example', 1)`)
	// A subdomain of the PARENT (not an addon) for the guard test.
	exec(`INSERT INTO websites (id, server_id, owner_id, parent_id, domain, system_user, root, document_root, kind, php_version, status, created_at, updated_at)
		VALUES (52, 1, 3, 10, 'app.cust.example', 'u_cust', '/home/whacp/cust.example/app', '/home/whacp/cust.example/app/public_html', 'php', '8.3.1', 'active', '', '')`)
	exec(`INSERT INTO domains (server_id, website_id, name, is_primary) VALUES (1, 52, 'app.cust.example', 1)`)
	// The addon's database, a database user only it uses, and a shared user
	// also granted on the parent's database (must not move).
	exec(`INSERT INTO databases (id, server_id, website_id, owner_id, name, created_at) VALUES (50, 1, 50, 3, 'addon_db', '')`)
	exec(`INSERT INTO databases (id, server_id, website_id, owner_id, name, created_at) VALUES (51, 1, 10, 3, 'cust_db', '')`)
	exec(`INSERT INTO database_users (id, server_id, owner_id, username, created_at) VALUES (50, 1, 3, 'addon_user', ''), (51, 1, 3, 'shared_user', '')`)
	exec(`INSERT INTO database_grants (database_id, user_id, privileges) VALUES (50, 50, 'SELECT'), (50, 51, 'SELECT'), (51, 51, 'SELECT')`)
	// DNS zone and mail domain for the addon.
	exec(`INSERT INTO dns_zones (id, server_id, name, website_id, owner_id, primary_ns, hostmaster, created_at, updated_at)
		VALUES (50, 1, 'addon.example', 50, 3, 'ns1.example', 'admin@example.com', '', '')`)
	exec(`INSERT INTO mail_domains (id, server_id, domain, website_id, owner_id, created_at)
		VALUES (50, 1, 'addon.example', 50, 3, '')`)
	// A cron job and a worker on the addon.
	exec(`INSERT INTO cron_jobs (id, server_id, website_id, schedule, command, description, email, enabled, created_at, updated_at)
		VALUES (50, 1, 50, '0 * * * *', 'php backup.php', 'Nightly', 'ok@example.com', 1, '', '')`)
	exec(`INSERT INTO workers (id, server_id, website_id, slug, name, command, restart, enabled, created_at, updated_at)
		VALUES (50, 1, 50, 'addon-worker', 'worker', 'node app.js', 'always', 1, '', '')`)
	// An addon under solo's site (owner 4, no reseller parent) for the
	// visibility guard: reseller 2 must not see it.
	exec(`INSERT INTO websites (id, server_id, owner_id, parent_id, domain, system_user, root, document_root, kind, php_version, status, created_at, updated_at)
		VALUES (60, 1, 4, 12, 'other.example', 'u_solo', '/home/whacp/other.example', '/home/whacp/other.example/public_html', 'php', '8.3.1', 'active', '', '')`)
	exec(`INSERT INTO domains (server_id, website_id, name, is_primary) VALUES (1, 60, 'other.example', 1)`)
}

func newConversion(t *testing.T) (*accountHarness, *AddonConversionService) {
	t.Helper()
	h := newAccountHarness(t)
	seedAddonFamily(t, h)
	audit := func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
		h.audit = append(h.audit, auditRec{action: action, target: target, result: result, details: details})
	}
	conv := NewAddonConversionService(
		h.db, h.agent, h.accounts, identity.NewRepository(h.db),
		NewWebsiteRepository(h.db), h.sites, h.cron,
		NewWorkerService(h.db, h.agent, NewPlanLimits(h.db), audit, 1),
		NewPlanLimits(h.db), audit, 1,
	)
	return h, conv
}

func convertForm() map[string]string {
	return map[string]string{
		"username": "addonuser", "email": "addon@example.test", "display_name": "Addon",
		"password": testPassword, "password_confirmation": testPassword,
	}
}

func findFullSite(t *testing.T, h *accountHarness, id int64) *Website {
	t.Helper()
	site, err := NewWebsiteRepository(h.db).FindFull(context.Background(), 1, id, nil)
	if err != nil || site == nil {
		t.Fatalf("find site %d: %v", id, err)
	}
	return site
}

func ownerOf(t *testing.T, h *accountHarness, q string) int64 {
	t.Helper()
	var owner int64
	if err := h.db.QueryRowContext(context.Background(), q).Scan(&owner); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return owner
}

func TestConvertHappyPath(t *testing.T) {
	h, conv := newConversion(t)
	ctx := context.Background()
	admin := loadUser(t, h.db, 1)
	site := findFullSite(t, h, 50)

	res, err := conv.Convert(ctx, site, convertForm(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if res.Username != "addonuser" || res.ID == 0 || res.Password.Valid {
		t.Fatalf("result = %+v", res)
	}
	// The new login: a customer.
	u, err := identity.NewRepository(h.db).Find(ctx, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if u.RoleName != "customer" || !u.IsActive {
		t.Fatalf("account = %+v", u)
	}

	// Both websites re-owned; the addon detached from the parent, the
	// subdomain still under the addon.
	for _, id := range []int64{50, 51} {
		w := findFullSite(t, h, id)
		if w.SystemUser != "addonuser" || !w.OwnerID.Valid || w.OwnerID.Int64 != res.ID {
			t.Fatalf("site %d = user %q owner %+v", id, w.SystemUser, w.OwnerID)
		}
	}
	if findFullSite(t, h, 50).ParentID.Valid {
		t.Fatal("the addon was not detached from its parent")
	}
	if p := findFullSite(t, h, 51).ParentID; !p.Valid || p.Int64 != 50 {
		t.Fatalf("subdomain parent = %+v, want 50", p)
	}

	// Agent steps: both sites re-applied (the addon creates the system
	// account), permissions + FTP only for the addon (the subdomain lives
	// inside its folder), cron for both, the worker re-applied.
	if n := h.agent.callCount("sites.apply"); n != 2 {
		t.Fatalf("sites.apply = %d, want 2 (%v)", n, h.agent.calls)
	}
	if spec := h.agent.lastParams("sites.apply"); spec["user"] != "addonuser" {
		t.Fatalf("last sites.apply = %v", spec)
	}
	if n := h.agent.callCount("sites.permissions"); n != 1 {
		t.Fatalf("sites.permissions = %d, want 1 (the subdomain's folder went with the addon)", n)
	}
	if n := h.agent.callCount("ftp.move_site"); n != 1 {
		t.Fatalf("ftp.move_site = %d, want 1", n)
	}
	if p := h.agent.lastParams("ftp.move_site"); p["domain"] != "addon.example" || p["user"] != "addonuser" {
		t.Fatalf("ftp.move_site = %v", p)
	}
	if n := h.agent.callCount("cron.apply"); n != 2 {
		t.Fatalf("cron.apply = %d, want 2", n)
	}
	if n := h.agent.callCount("workers.apply"); n != 1 {
		t.Fatalf("workers.apply = %d, want 1", n)
	}

	// Panel ownership: the addon's database and its exclusive user moved;
	// the parent's database and the shared user did not.
	if got := ownerOf(t, h, "SELECT owner_id FROM databases WHERE id = 50"); got != res.ID {
		t.Fatalf("addon_db owner = %d, want %d", got, res.ID)
	}
	if got := ownerOf(t, h, "SELECT owner_id FROM databases WHERE id = 51"); got != 3 {
		t.Fatalf("cust_db owner = %d, want 3 (unchanged)", got)
	}
	if got := ownerOf(t, h, "SELECT owner_id FROM database_users WHERE id = 50"); got != res.ID {
		t.Fatalf("addon_user owner = %d, want %d", got, res.ID)
	}
	if got := ownerOf(t, h, "SELECT owner_id FROM database_users WHERE id = 51"); got != 3 {
		t.Fatalf("shared_user owner = %d, want 3 (still used elsewhere)", got)
	}
	if got := ownerOf(t, h, "SELECT owner_id FROM dns_zones WHERE id = 50"); got != res.ID {
		t.Fatalf("dns owner = %d", got)
	}
	if got := ownerOf(t, h, "SELECT owner_id FROM mail_domains WHERE id = 50"); got != res.ID {
		t.Fatalf("mail owner = %d", got)
	}

	a := lastAuditOf(t, h.audit, "account.convert_addon")
	if a.result != "success" || a.details["from"] != "u_cust" || a.details["username"] != "addonuser" {
		t.Fatalf("audit = %+v", a)
	}
	if ws, ok := a.details["websites"].([]string); !ok || len(ws) != 2 || ws[0] != "addon.example" {
		t.Fatalf("websites detail = %v", a.details["websites"])
	}
	// The account starts from the default hosting options.
	var raw string
	if err := h.db.QueryRowContext(ctx, "SELECT options FROM account_options WHERE user_id = ?", res.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"dns_dkim":true`) || !strings.Contains(raw, `"mail_routing":"auto"`) {
		t.Fatalf("options = %s", raw)
	}
}

func TestConvertGeneratesPassword(t *testing.T) {
	h, conv := newConversion(t)
	ctx := context.Background()
	admin := loadUser(t, h.db, 1)
	in := convertForm()
	in["generate"] = "1"
	delete(in, "password")
	delete(in, "password_confirmation")
	site := findFullSite(t, h, 50)

	res, err := conv.Convert(ctx, site, in, admin)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Password.Valid || len(res.Password.String) != 20 {
		t.Fatalf("generated password = %+v", res.Password)
	}
}

func TestConvertGuards(t *testing.T) {
	h, conv := newConversion(t)
	ctx := context.Background()
	admin := loadUser(t, h.db, 1)

	// A subdomain of the parent is not an addon.
	sub := findFullSite(t, h, 52)
	if _, err := conv.Convert(ctx, sub, convertForm(), admin); err == nil {
		t.Fatal("a subdomain converted")
	} else if msg := userErrorMsg(t, err); msg != "Only an addon domain can become an account of its own." {
		t.Fatalf("error = %q", msg)
	}

	// Suspended account.
	if _, err := h.db.Exec(`UPDATE users SET suspended_at = '2026-01-01T00:00:00Z' WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	site := findFullSite(t, h, 50)
	if _, err := conv.Convert(ctx, site, convertForm(), admin); err == nil {
		t.Fatal("a suspended account converted")
	} else if msg := userErrorMsg(t, err); msg != "The hosting account is suspended; unsuspend it first." {
		t.Fatalf("error = %q", msg)
	}
	if _, err := h.db.Exec(`UPDATE users SET suspended_at = NULL WHERE id = 3`); err != nil {
		t.Fatal(err)
	}

	// A non-standard folder.
	if _, err := h.db.Exec(`UPDATE websites SET root = '/home/whacp/weird' WHERE id = 50`); err != nil {
		t.Fatal(err)
	}
	site = findFullSite(t, h, 50)
	if _, err := conv.Convert(ctx, site, convertForm(), admin); err == nil {
		t.Fatal("a non-standard folder converted")
	} else if msg := userErrorMsg(t, err); msg != "The website folder is not the standard one for addon.example; it cannot be converted." {
		t.Fatalf("error = %q", msg)
	}
	if _, err := h.db.Exec(`UPDATE websites SET root = '/home/whacp/addon.example' WHERE id = 50`); err != nil {
		t.Fatal(err)
	}

	// A system user already in use.
	in := convertForm()
	in["username"] = "u_cust"
	_, err := conv.Convert(ctx, findFullSite(t, h, 50), in, admin)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Fields["username"] != "The system user u_cust is in use. Choose another name." {
		t.Fatalf("error = %+v", err)
	}

	// Invisible to the actor: reseller 2 does not see solo's addon.
	reseller := loadUser(t, h.db, 2)
	other := findFullSite(t, h, 60)
	if _, err := conv.Convert(ctx, other, convertForm(), reseller); err == nil {
		t.Fatal("an invisible addon converted")
	} else if msg := userErrorMsg(t, err); msg != "That website does not exist." {
		t.Fatalf("error = %q", msg)
	}
	// The account was not created for any of the guard failures.
	var n int
	if err := h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE username = 'addonuser'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("guard failures created a login")
	}
}

func userErrorMsg(t *testing.T, err error) string {
	t.Helper()
	var ue *UserError
	if !errors.As(err, &ue) {
		t.Fatalf("error = %v, want *UserError", err)
	}
	return ue.What
}

func TestConvertFailureUndoes(t *testing.T) {
	h, conv := newConversion(t)
	ctx := context.Background()
	h.agent.failActions = map[string]error{"ftp.move_site": errors.New("agent down")}
	admin := loadUser(t, h.db, 1)
	site := findFullSite(t, h, 50)

	_, err := conv.Convert(ctx, site, convertForm(), admin)
	if err == nil {
		t.Fatal("convert succeeded despite the agent failure")
	}
	// The websites are back on their previous account, parents included.
	for _, id := range []int64{50, 51} {
		w := findFullSite(t, h, id)
		if w.SystemUser != "u_cust" || !w.OwnerID.Valid || w.OwnerID.Int64 != 3 {
			t.Fatalf("site %d after undo = user %q owner %+v", id, w.SystemUser, w.OwnerID)
		}
	}
	if p := findFullSite(t, h, 50).ParentID; !p.Valid || p.Int64 != 10 {
		t.Fatalf("addon parent after undo = %+v, want 10", p)
	}
	// The login was removed.
	var n int
	if err := h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE username = 'addonuser'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("the new login survived the undo")
	}
	// Ownership never moved.
	if got := ownerOf(t, h, "SELECT owner_id FROM databases WHERE id = 50"); got != 3 {
		t.Fatalf("addon_db owner = %d, want 3", got)
	}
	if got := ownerOf(t, h, "SELECT owner_id FROM dns_zones WHERE id = 50"); got != 3 {
		t.Fatalf("dns owner = %d, want 3", got)
	}
	// The undo re-applied the cron schedule for the old system user.
	if n := h.agent.callCount("cron.apply"); n < 1 {
		t.Fatalf("cron.apply = %d, want >= 1 (undo)", n)
	}
	a := lastAuditOf(t, h.audit, "account.convert_addon")
	if a.result != "failure" || a.details["username"] != "addonuser" {
		t.Fatalf("audit = %+v", a)
	}
	if !strings.Contains(strings.ToLower(a.details["error"].(string)), "agent down") {
		t.Fatalf("error detail = %v", a.details["error"])
	}
}

func TestConvertPreview(t *testing.T) {
	h, conv := newConversion(t)
	ctx := context.Background()
	site := findFullSite(t, h, 50)

	pv, err := conv.Preview(ctx, site)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Websites) != 2 || pv.Websites[0].Domain != "addon.example" || pv.Websites[1].Domain != "app.addon.example" {
		t.Fatalf("websites = %+v", pv.Websites)
	}
	if len(pv.Databases) != 1 || pv.Databases[0] != "addon_db" {
		t.Fatalf("databases = %v", pv.Databases)
	}
	if !pv.DNS || !pv.Mail {
		t.Fatalf("dns/mail = %v/%v, want both", pv.DNS, pv.Mail)
	}
	if pv.Cron != 1 || pv.Workers != 1 {
		t.Fatalf("cron/workers = %d/%d", pv.Cron, pv.Workers)
	}
}
