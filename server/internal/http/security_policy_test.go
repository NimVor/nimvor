package http

// This suite drives the security policy configuration (SecurityPolicyController):
// the policy items page (the verified-IP switch, the per-role 2FA requirement,
// the password strength switch, the password age), the verified-IP list (forget),
// and the password strength configuration page — the scope/role gates (403 page),
// the SettingsService::update validation, the settings persistence, the audits,
// the verified-IP seeding and the flashes.

import (
	"database/sql"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// policyEnv seeds an administrator (role 1, scope all), a customer (role 2,
// scope own, no security.manage) and an operator (role 3, scope all,
// security.manage but not the admin role — the role-admin gate) and the
// three roles. The fake agent answers the pages' status calls.
func policyEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES
		(1,'admin','Administrator','all',''),(2,'customer','Customer','own',''),(3,'ops','Operator','all','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'security.view','sv','security'),(3,'security.manage','sm','security')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES
		(1,1),(1,2),(1,3),(2,1),(2,2),(3,1),(3,2),(3,3)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','',''),
		(3,'carol','c@x','Carol',?,3,1,'2026-01-01T00:00:00Z','','')`,
		loginHash, loginHash, loginHash)

	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sa, socket, tokenFile := startSitesAgent(t, false)
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler(), sa, db
}

func setting(t *testing.T, db *sql.DB, key, def string) string {
	t.Helper()
	var v sql.NullString
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return def
		}
		t.Fatal(err)
	}
	return v.String
}

func TestPoliciesPageAdmin(t *testing.T) {
	h, _, _ := policyEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/policies", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Configure Security Policies",
		"Security Policy Items",
		"Limit logins to verified IP addresses",
		"Two-Factor Authentication: Google Authenticator",
		"Password Strength",
		"Password Age",
		"Your verified IP addresses",
		"None yet.",
		// The default policy (strength on: 65 / length 12, no counts).
		"At least 12 characters; strength 65 or more.",
		">Save<",
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestPoliciesPageScopeDenied(t *testing.T) {
	h, _, _ := policyEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/security/policies", bob)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("GET (bob) = %d, want 403", rr.Code)
	}
	if !pageHas(rr.Body.String(), "Only administrators manage security policies.") {
		t.Error("the 403 page is missing the policy message")
	}
}

func TestSavePolicies(t *testing.T) {
	h, _, db := policyEnv(t)
	// Bob's last sign-in address — seeding counts it as verified.
	if _, err := db.Exec(`UPDATE users SET last_login_ip = '198.51.100.7' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	// The administrator changed the password recently, so the age policy
	// saved below does not lock them out of the page that follows.
	if _, err := db.Exec(`UPDATE users SET password_changed_at = ? WHERE id = 1`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/policies")
	rr := postForm(h, "/security/policies", url.Values{
		"_token":                   {token},
		"policy_verified_ips":      {"1"},
		"policy_password_strength": {"1"},
		"policy_password_age_days": {"30"},
		"two_factor_roles[]":       {"2"},
	}, admin)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/security/policies" {
		t.Fatalf("save = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/policies",
		"Security policies saved. Each user&#39;s last sign-in address (2) counts as verified.") // bob's seeded one + the administrator's own login just made

	if got := setting(t, db, "policy_verified_ips", "false"); got != "true" {
		t.Errorf("policy_verified_ips = %s, want true", got)
	}
	if got := setting(t, db, "policy_password_age_days", "0"); got != "30" {
		t.Errorf("policy_password_age_days = %s, want 30", got)
	}
	// The strength switch was already on — unchanged, not re-written.
	var strengthUpdated string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'policy_password_strength'`).Scan(&strengthUpdated); err != sql.ErrNoRows {
		if err != nil {
			t.Fatal(err)
		}
		t.Errorf("policy_password_strength was re-written although unchanged: %s", strengthUpdated)
	}

	var twoFactor int
	if err := db.QueryRow(`SELECT require_two_factor FROM roles WHERE id = 2`).Scan(&twoFactor); err != nil || twoFactor != 1 {
		t.Errorf("role 2 require_two_factor = %d (err %v), want 1", twoFactor, err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM roles WHERE require_two_factor = 1`).Scan(&n); err != nil || n != 1 {
		t.Errorf("roles requiring 2FA = %d (err %v), want 1", n, err)
	}

	// The actor's current address and bob's last sign-in address were verified.
	var ips string
	if err := db.QueryRow(`SELECT ip FROM login_ips WHERE user_id = 1 AND ip = '203.0.113.50'`).Scan(&ips); err != nil {
		t.Errorf("the actor's address was not verified: %v", err)
	}
	if err := db.QueryRow(`SELECT ip FROM login_ips WHERE user_id = 2 AND ip = '198.51.100.7'`).Scan(&ips); err != nil {
		t.Errorf("bob's last sign-in address was not seeded: %v", err)
	}

	var target, details string
	if err := db.QueryRow(`SELECT target_id, details FROM audit_logs WHERE action = 'settings.update'`).Scan(&target, &details); err != nil {
		t.Fatalf("settings.update audit row: %v", err)
	}
	if target != "panel" ||
		!strings.Contains(details, `"policy_verified_ips":{"from":false,"to":true}`) ||
		// The redactor (same key pattern as PHP's Redactor::SENSITIVE_KEY) hides any
		// key containing "pass", including this non-secret day count.
		!strings.Contains(details, `"policy_password_age_days":"[REDACTED]"`) {
		t.Errorf("settings.update audit = %s / %s", target, details)
	}
	if err := db.QueryRow(`SELECT target_id, details FROM audit_logs WHERE action = 'role.two_factor_policy'`).Scan(&target, &details); err != nil {
		t.Fatalf("role.two_factor_policy audit row: %v", err)
	}
	if target != "2" || !strings.Contains(details, `"role":"customer"`) || !strings.Contains(details, `"required":true`) {
		t.Errorf("role.two_factor_policy audit = %s / %s", target, details)
	}
}

func TestSavePoliciesRoleGate(t *testing.T) {
	h, _, _ := policyEnv(t)
	// Carol: scope all + security.manage, but not the admin role — read-only.
	carol := loginAsUser(t, h, "carol")
	rr := get(t, h, "/security/policies", carol)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET (carol) = %d, want 200 (read-only)", rr.Code)
	}
	if pageHas(rr.Body.String(), ">Save<") {
		t.Error("a non-admin sees the Save button")
	}
	token, carol := siteFormToken(t, h, carol, "/security/policies")
	rr = postForm(h, "/security/policies", url.Values{
		"_token":                   {token},
		"policy_verified_ips":      {"1"},
		"policy_password_strength": {"1"},
		"policy_password_age_days": {"0"},
	}, carol)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("POST (carol) = %d, want the 403 page (not a flash)", rr.Code)
	}
	if !pageHas(rr.Body.String(), "Only administrators manage security policies.") {
		t.Error("the 403 page is missing the policy message")
	}
}

func TestSavePoliciesInvalidAge(t *testing.T) {
	h, _, db := policyEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/policies")
	rr := postForm(h, "/security/policies", url.Values{
		"_token":                   {token},
		"policy_verified_ips":      {"1"},
		"policy_password_strength": {"1"},
		"policy_password_age_days": {"9999"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/policies",
		"Enter a whole number between 0 and 3650.")
	// Nothing persisted: the failed save does not touch the other values.
	if got := setting(t, db, "policy_verified_ips", "false"); got != "false" {
		t.Errorf("a failed save persisted policy_verified_ips = %s", got)
	}
}

func TestForgetPolicyIp(t *testing.T) {
	h, _, db := policyEnv(t)
	if _, err := db.Exec(`INSERT INTO login_ips (id,user_id,ip,verified_at,last_used_at)
		VALUES (1,3,'198.51.100.9','2026-01-02T00:00:00Z','2026-01-03T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	// Carol (scope all, non-admin): the forget route has no role gate, and
	// the form is shown even without the Save button.
	carol := loginAsUser(t, h, "carol")
	rr := get(t, h, "/security/policies", carol)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET (carol) = %d", rr.Code)
	}
	if !pageHas(rr.Body.String(), "198.51.100.9") {
		t.Fatal("carol's verified address is not listed")
	}
	if !pageHas(rr.Body.String(), "Forget") {
		t.Error("the forget form is missing for a read-only user")
	}
	token, carol := siteFormToken(t, h, carol, "/security/policies")
	rr = postForm(h, "/security/policies/ips/forget", url.Values{
		"_token": {token},
		"id":     {"1"},
	}, carol)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/security/policies#verified-ips" {
		t.Fatalf("forget = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(carol, rr), "/security/policies",
		"The address is no longer verified.")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM login_ips`).Scan(&n); err != nil || n != 0 {
		t.Errorf("login_ips rows = %d (err %v), want 0", n, err)
	}
}

func TestStrengthPageAdmin(t *testing.T) {
	h, _, _ := policyEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/password-strength", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Password Strength Configuration",
		"Password strength",
		"Number of total characters",
		"Number of special characters",
		"Number of numeric characters",
		"Number of alphabet characters",
		"Number of uppercase characters",
		"Number of lowercase characters",
		"65 or more counts as strong.",
		"Strength examples",
		"password1234",
		"sunflower2024",
		"Sunflower!2024",
		"correct-horse-battery-staple",
		`class="wcp-state is-ok">On`,
		">Save<",
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestStrengthPageScopeDenied(t *testing.T) {
	h, _, _ := policyEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/security/password-strength", bob)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("GET (bob) = %d, want 403", rr.Code)
	}
	if !pageHas(rr.Body.String(), "Only administrators manage security policies.") {
		t.Error("the 403 page is missing the policy message")
	}
}

func TestSaveStrength(t *testing.T) {
	h, _, db := policyEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/password-strength")
	rr := postForm(h, "/security/password-strength", url.Values{
		"_token":                {token},
		"password_min_strength": {"66"},
		"password_min_length":   {"14"},
		"password_min_special":  {"1"},
		"password_min_numeric":  {"2"},
		"password_min_alpha":    {"3"},
		"password_min_upper":    {"1"},
		"password_min_lower":    {"2"},
	}, admin)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/security/password-strength" {
		t.Fatalf("save = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/password-strength",
		"Password strength requirements saved. They apply to passwords set from now on.")

	for key, want := range map[string]string{
		"password_min_strength": "66",
		"password_min_length":   "14",
		"password_min_special":  "1",
		"password_min_numeric":  "2",
		"password_min_alpha":    "3",
		"password_min_upper":    "1",
		"password_min_lower":    "2",
	} {
		if got := setting(t, db, key, "0"); got != want {
			t.Errorf("%s = %s, want %s", key, got, want)
		}
	}
	var target, details string
	if err := db.QueryRow(`SELECT target_id, details FROM audit_logs WHERE action = 'settings.update'`).Scan(&target, &details); err != nil {
		t.Fatalf("settings.update audit row: %v", err)
	}
	if target != "panel" ||
		!strings.Contains(details, `"password_min_strength":{"from":65,"to":66}`) ||
		!strings.Contains(details, `"password_min_length":{"from":12,"to":14}`) {
		t.Errorf("settings.update audit = %s / %s", target, details)
	}
}

func TestSaveStrengthValidation(t *testing.T) {
	h, _, db := policyEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/password-strength")
	rr := postForm(h, "/security/password-strength", url.Values{
		"_token":                {token},
		"password_min_strength": {"200"},
		"password_min_length":   {"5"},
		"password_min_special":  {"0"},
		"password_min_numeric":  {"0"},
		"password_min_alpha":    {"0"},
		"password_min_upper":    {"0"},
		"password_min_lower":    {"0"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save = %d", rr.Code)
	}
	// All the errors at once, in the form's field order.
	flashAt(t, h, refreshCookie(admin, rr), "/security/password-strength",
		"Enter a whole number between 0 and 100. Enter a whole number between 12 and 128.")
	if got := setting(t, db, "password_min_strength", "65"); got != "65" {
		t.Errorf("a failed save persisted password_min_strength = %s", got)
	}
}
