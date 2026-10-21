package hosting

import (
	"context"
	"database/sql"
	"errors"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

func accountForm(username string) map[string]string {
	return map[string]string{
		"username":              username,
		"email":                 username + "@example.test",
		"display_name":          username,
		"password":              testPassword,
		"password_confirmation": testPassword,
	}
}

func TestAccountCreateCustomer(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	admin := loadUser(t, h.db, 1)

	acct, err := h.accounts.Create(ctx, accountForm("newacct"), admin)
	if err != nil {
		t.Fatal(err)
	}
	if acct.Username != "newacct" || acct.WebsiteID.Valid || acct.Password.Valid {
		t.Fatalf("account = %+v", acct)
	}
	u, err := identity.NewRepository(h.db).Find(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if u.RoleName != "customer" || !u.IsActive {
		t.Fatalf("user = %+v", u)
	}
	// Hosting options stored with the (all-off) form defaults.
	var raw string
	if err := h.db.QueryRowContext(ctx, "SELECT options FROM account_options WHERE user_id = ?", acct.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var stored storedOptions
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.MailRouting == nil || *stored.MailRouting != "auto" || stored.DNSDKIM == nil || *stored.DNSDKIM {
		t.Fatalf("options = %s", raw)
	}
	a := lastAuditOf(t, h.audit, "account.create")
	if a.result != "success" || a.details["username"] != "newacct" || a.details["reseller"] != false {
		t.Fatalf("audit = %+v", a)
	}
}

func TestAccountCreateResellerOnlyForAdmins(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	reseller := loadUser(t, h.db, 2)

	in := accountForm("newreseller")
	in["reseller"] = "1"
	_, err := h.accounts.Create(ctx, in, reseller)
	if msg := validationField(t, err, "reseller"); msg != "Only administrators create resellers." {
		t.Fatalf("message = %q", msg)
	}
	admin := loadUser(t, h.db, 1)
	acct, err := h.accounts.Create(ctx, in, admin)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := identity.NewRepository(h.db).Find(ctx, acct.ID)
	if u.RoleName != "reseller" {
		t.Fatalf("role = %q, want reseller", u.RoleName)
	}
}

func TestAccountCreateUsernameRules(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	admin := loadUser(t, h.db, 1)
	const want = "Use 3–31 lowercase letters, digits, - or _, starting with a letter (also the website's system account)."
	for _, username := range []string{"root", "www-data", "a", "9bad", "has.dot", "this-name-is-definitely-way-too-long-36"} {
		_, err := h.accounts.Create(ctx, accountForm(username), admin)
		if msg := validationField(t, err, "username"); msg != want {
			t.Fatalf("%q: message = %q", username, msg)
		}
	}
	// Upper case is normalized, not rejected.
	if _, err := h.accounts.Create(ctx, accountForm("MIXEDcase"), admin); err != nil {
		t.Fatalf("mixed case should normalize: %v", err)
	}
}

func TestAccountCreateGenerate(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	admin := loadUser(t, h.db, 1)
	in := accountForm("genpass")
	in["generate"] = "1"
	delete(in, "password")
	delete(in, "password_confirmation")

	acct, err := h.accounts.Create(ctx, in, admin)
	if err != nil {
		t.Fatal(err)
	}
	if !acct.Password.Valid || len(acct.Password.String) != 20 {
		t.Fatalf("generated password = %+v", acct.Password)
	}
	hash, err := identity.NewRepository(h.db).PasswordHash(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !identity.VerifyPassword(hash, acct.Password.String) {
		t.Fatal("generated password does not verify")
	}
}

func TestAccountCreateWithDomain(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	admin := loadUser(t, h.db, 1)
	in := accountForm("webacct")
	in["domain"] = "webacct.example."
	in["php_version"] = "8.3.1"

	acct, err := h.accounts.Create(ctx, in, admin)
	if err != nil {
		t.Fatal(err)
	}
	if !acct.WebsiteID.Valid {
		t.Fatalf("account = %+v, want a website", acct)
	}
	var sysUser string
	var owner sql.NullInt64
	if err := h.db.QueryRowContext(ctx, "SELECT system_user, owner_id FROM websites WHERE id = ?", acct.WebsiteID.Int64).Scan(&sysUser, &owner); err != nil {
		t.Fatal(err)
	}
	if sysUser != "webacct" || !owner.Valid || owner.Int64 != acct.ID {
		t.Fatalf("website system_user=%q owner=%+v", sysUser, owner)
	}
	// www alias on by default.
	var n int
	if err := h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM domains WHERE website_id = ? AND name = 'www.webacct.example'", acct.WebsiteID.Int64).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("www alias rows = %d", n)
	}
	if h.agent.callCount("sites.apply") != 1 {
		t.Fatalf("sites.apply calls = %d", h.agent.callCount("sites.apply"))
	}
}

func TestAccountCreateWebsiteFailureDeletesLogin(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	h.agent.failActions = map[string]error{"sites.apply": errors.New("agent down")}
	admin := loadUser(t, h.db, 1)
	in := accountForm("failacct")
	in["domain"] = "failacct.example"
	in["php_version"] = "8.3.1"

	_, err := h.accounts.Create(ctx, in, admin)
	if err == nil {
		t.Fatal("create succeeded despite the agent failure")
	}
	var n int
	if err := h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE username = 'failacct'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("the login was not removed after the website failure")
	}
	a := lastAuditOf(t, h.audit, "account.create")
	if a.result != "failure" || a.target != "failacct" || a.details["domain"] != "failacct.example" {
		t.Fatalf("audit = %+v", a)
	}
}

func TestAccountOptionsFrom(t *testing.T) {
	h := newAccountHarness(t)
	svc := h.accounts

	out := svc.OptionsFrom(map[string]string{
		"dns_dkim": "1", "dns_spf": "1", "dns_dmarc": "1",
		"mail_routing": "local",
	})
	if out["dns_dmarc"] != true || out["mail_routing"] != "local" {
		t.Fatalf("out = %v", out)
	}
	// DMARC requires DKIM and SPF.
	out = svc.OptionsFrom(map[string]string{"dns_dmarc": "1", "dns_spf": "1"})
	if out["dns_dmarc"] != false {
		t.Fatalf("dmarc without dkim = %v", out)
	}
	// Invalid mail routing falls back to auto.
	out = svc.OptionsFrom(map[string]string{"mail_routing": "bogus"})
	if out["mail_routing"] != "auto" {
		t.Fatalf("mail_routing = %v", out)
	}
}

func TestAccountDomainPreValidation(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	admin := loadUser(t, h.db, 1)
	// cust.example already exists: the domain check fails before anything
	// is created.
	in := accountForm("taken")
	in["domain"] = "cust.example"
	_, err := h.accounts.Create(ctx, in, admin)
	validationField(t, err, "domain")
	var n int
	if err := h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE username = 'taken'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("a user was created despite the domain validation failure")
	}
}

func TestGeneratedPassword(t *testing.T) {
	p, err := GeneratedPassword()
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 20 {
		t.Fatalf("length = %d", len(p))
	}
	for _, r := range p {
		if !strings.ContainsRune(passwordChars, r) {
			t.Fatalf("character %q outside the alphabet", r)
		}
	}
	q, _ := GeneratedPassword()
	if p == q {
		t.Fatal("two generated passwords are identical")
	}
}
