package http

// This file ports Security → Configure Security Policies and Password
// Strength Configuration (SecurityPolicyController): the policy switches
// (verified IPs, password strength, password age), the per-role 2FA
// requirement, the user's verified-IP list (forget) and the password
// strength minimums. The pages are all-scope (403 otherwise); the saves are
// all-scope + role admin (403); forgetting an IP is any user's own list.
// The settings persist through the settings table (the PHP
// SettingsService::update validation for these keys), audited like PHP.

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/security"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// policyDenied is the controller's deny(): the all-scope gate, plus the
// role-admin gate for changes.
func (s *Server) policyDenied(w http.ResponseWriter, user *identity.User) {
	renderStatus(w, s, "error", map[string]any{
		"Title":   "Access denied",
		"Message": "Only administrators manage security policies.",
	}, http.StatusForbidden)
}

// formBool is SettingsService's filter_var(FILTER_VALIDATE_BOOL) for the
// form's checkbox values (1/0 from the hidden+checkbox pair).
func formBool(v string) bool {
	switch strings.ToLower(v) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

// policyInt is SettingsService's int validation: a whole number in
// [min..max], the "Enter a whole number between …" message otherwise.
func policyInt(v string, min, max int) (int, string) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < min || n > max {
		return 0, fmt.Sprintf("Enter a whole number between %d and %d.", min, max)
	}
	return n, ""
}

// policies (GET) — SecurityPolicyController::policies.
func (s *Server) policiesPage(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.policyDenied(w, user)
		return
	}
	ctx := r.Context()
	sess := sessionFrom(r.Context())
	roles, err := s.roles.All(ctx)
	if err != nil {
		s.log.Error("list roles", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	myIps, err := s.loginIPs.List(ctx, user.ID)
	if err != nil {
		s.log.Error("list verified ips", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	rules, err := s.policy.Describe(ctx)
	if err != nil {
		s.log.Error("describe policy", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	verifiedIPs, _ := s.settings.Bool(ctx, "policy_verified_ips", false)
	strengthOn, _ := s.settings.Bool(ctx, "policy_password_strength", true)
	ageDays, _ := s.settings.Int(ctx, "policy_password_age_days", 0)
	ok, _ := s.authorizer().Can(ctx, user, "security.manage")
	data := map[string]any{
		"User":        user,
		"CSRF":        session.CSRFToken(sess),
		"Pending":     s.securityPending(ctx),
		"CanManage":   ok && user.RoleName == "admin",
		"Roles":       roles,
		"MyIps":       myIps,
		"Rules":       rules,
		"VerifiedIPs": verifiedIPs,
		"StrengthOn":  strengthOn,
		"AgeDays":     ageDays,
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	s.render(w, "security_policies", data)
}

// savePolicies (POST) — SecurityPolicyController::savePolicies: the three
// policy settings (validated like SettingsService::update), the per-role 2FA
// requirement, and — when the verified-IP policy is turned on — the seeding
// of each user's last sign-in address.
func (s *Server) savePolicies(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	if !hosting.IsAll(user) || user.RoleName != "admin" {
		s.policyDenied(w, user)
		return
	}
	ctx := r.Context()
	to := "/security/policies"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}

	newIPs := formBool(r.FormValue("policy_verified_ips"))
	newStrength := formBool(r.FormValue("policy_password_strength"))
	ageDays, ageErr := policyInt(r.FormValue("policy_password_age_days"), 0, 3650)
	if ageErr != "" {
		fail(ageErr)
		return
	}

	wasOn, _ := s.settings.Bool(ctx, "policy_verified_ips", false)
	// Persist the changed values (SettingsService::update: only the changes,
	// audited as one settings.update with the from/to pairs).
	changed := map[string]any{}
	commitBool := func(key string, def, want bool) {
		old, _ := s.settings.Bool(ctx, key, def)
		if old != want && s.settings.Set(ctx, key, want, user.ID) == nil {
			changed[key] = map[string]any{"from": old, "to": want}
		}
	}
	commitInt := func(key string, def, want int) {
		old, _ := s.settings.Int(ctx, key, def)
		if old != want && s.settings.Set(ctx, key, want, user.ID) == nil {
			changed[key] = map[string]any{"from": old, "to": want}
		}
	}
	commitBool("policy_verified_ips", false, newIPs)
	commitBool("policy_password_strength", true, newStrength)
	commitInt("policy_password_age_days", 0, ageDays)
	if len(changed) > 0 {
		s.audit.Record(ctx, audit.Entry{
			UserID:     &user.ID,
			Username:   user.Username,
			Action:     "settings.update",
			TargetType: "settings",
			TargetID:   "panel",
			Result:     audit.Success,
			IP:         clientIP(r),
			UserAgent:  r.UserAgent(),
			Details:    map[string]any{"changed": changed},
		})
	}

	// The per-role 2FA requirement (RoleService::setRequireTwoFactor,
	// audited per role).
	required := map[int64]bool{}
	for _, raw := range r.Form["two_factor_roles[]"] {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			required[id] = true
		}
	}
	roles, err := s.roles.All(ctx)
	if err != nil {
		s.log.Error("list roles", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	for _, role := range roles {
		want := required[role.ID]
		if want != role.RequireTwoFactor && s.roles.SetRequireTwoFactor(ctx, role.ID, want) == nil {
			s.audit.Record(ctx, audit.Entry{
				UserID:     &user.ID,
				Username:   user.Username,
				Action:     "role.two_factor_policy",
				TargetType: "role",
				TargetID:   strconv.FormatInt(role.ID, 10),
				Result:     audit.Success,
				IP:         clientIP(r),
				UserAgent:  r.UserAgent(),
				Details:    map[string]any{"role": role.Name, "required": want},
			})
		}
	}

	// Turning the verified-IP policy on: nobody is locked out — the actor's
	// current address and every user's last sign-in address count as
	// verified.
	note := ""
	if !wasOn && newIPs {
		if err := s.loginIPs.Remember(ctx, user.ID, clientIP(r)); err != nil {
			s.log.Error("remember login ip", "error", err)
		}
		if n, err := s.loginIPs.SeedFromLastLogins(ctx); err != nil {
			s.log.Error("seed login ips", "error", err)
		} else {
			note = fmt.Sprintf(" Each user's last sign-in address (%d) counts as verified.", n)
		}
	}
	sess.Flash("success", "Security policies saved."+note)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// forgetPolicyIP (POST /security/policies/ips/forget) — any user forgets one
// of their own verified addresses (no scope or role gate, as in PHP).
func (s *Server) forgetPolicyIP(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	// PHP's (int) cast: a malformed id is 0 and forgets nothing.
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err := s.loginIPs.Forget(r.Context(), user.ID, id); err != nil {
		s.log.Error("forget login ip", "error", err)
	}
	sess.Flash("success", "The address is no longer verified.")
	http.Redirect(w, r, "/security/policies#verified-ips", http.StatusSeeOther)
}

// strengthField is one row of the password strength form.
type strengthField struct {
	Key   string
	Label string
	Min   int
	Max   int
	Value int
	Help  string
}

// strengthDef is one password-minimum key's SettingsService definition.
type strengthDef struct {
	Key   string
	Label string
	Min   int
	Max   int
	Def   int
	Help  string
}

// strengthDefs mirrors SettingsService::definitions for the seven
// password-minimum keys (label, bounds, help — in the view's field order).
func strengthDefs() []strengthDef {
	return []strengthDef{
		{"password_min_strength", "Password strength", 0, 100, 65, "0–100, from the length and the kinds of characters used."},
		{"password_min_length", "Number of total characters", 12, 128, 12, "At least 12."},
		{"password_min_special", "Number of special characters", 0, 32, 0, "Characters other than letters and digits."},
		{"password_min_numeric", "Number of numeric characters", 0, 32, 0, "0–9."},
		{"password_min_alpha", "Number of alphabet characters", 0, 32, 0, "Letters, upper or lower case."},
		{"password_min_upper", "Number of uppercase characters", 0, 32, 0, "A–Z."},
		{"password_min_lower", "Number of lowercase characters", 0, 32, 0, "a–z."},
	}
}

// strength (GET) — SecurityPolicyController::strength.
func (s *Server) strengthPage(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.policyDenied(w, user)
		return
	}
	ctx := r.Context()
	sess := sessionFrom(r.Context())
	fields := []strengthField{}
	for _, d := range strengthDefs() {
		v, _ := s.settings.Int(ctx, d.Key, d.Def)
		fields = append(fields, strengthField{Key: d.Key, Label: d.Label, Min: d.Min, Max: d.Max, Value: v, Help: d.Help})
	}
	strengthOn, _ := s.settings.Bool(ctx, "policy_password_strength", true)
	rules, err := s.policy.Describe(ctx)
	if err != nil {
		s.log.Error("describe policy", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	examples := []struct {
		Password string
		Strength int
	}{
		{"password1234", security.Strength("password1234")},
		{"sunflower2024", security.Strength("sunflower2024")},
		{"Sunflower!2024", security.Strength("Sunflower!2024")},
		{"correct-horse-battery-staple", security.Strength("correct-horse-battery-staple")},
	}
	ok, _ := s.authorizer().Can(ctx, user, "security.manage")
	data := map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"Pending":    s.securityPending(ctx),
		"CanManage":  ok && user.RoleName == "admin",
		"Fields":     fields,
		"StrengthOn": strengthOn,
		"Rules":      rules,
		"Examples":   examples,
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	s.render(w, "security_password_strength", data)
}

// saveStrength (POST) — SecurityPolicyController::saveStrength: the seven
// minimums (validated like SettingsService::update — all the errors at
// once), then the changed values + the settings.update audit.
func (s *Server) saveStrength(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	if !hosting.IsAll(user) || user.RoleName != "admin" {
		s.policyDenied(w, user)
		return
	}
	ctx := r.Context()
	to := "/security/password-strength"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}

	defs := strengthDefs()
	values := make([]int, len(defs))
	errs := []string{}
	for i, d := range defs {
		v, err := policyInt(r.FormValue(d.Key), d.Min, d.Max)
		if err != "" {
			errs = append(errs, err)
			continue
		}
		values[i] = v
	}
	if len(errs) > 0 {
		fail(strings.Join(errs, " "))
		return
	}

	changed := map[string]any{}
	for i, d := range defs {
		old, _ := s.settings.Int(ctx, d.Key, d.Def)
		if old != values[i] && s.settings.Set(ctx, d.Key, values[i], user.ID) == nil {
			changed[d.Key] = map[string]any{"from": old, "to": values[i]}
		}
	}
	if len(changed) > 0 {
		s.audit.Record(ctx, audit.Entry{
			UserID:     &user.ID,
			Username:   user.Username,
			Action:     "settings.update",
			TargetType: "settings",
			TargetID:   "panel",
			Result:     audit.Success,
			IP:         clientIP(r),
			UserAgent:  r.UserAgent(),
			Details:    map[string]any{"changed": changed},
		})
	}
	sess.Flash("success", "Password strength requirements saved. They apply to passwords set from now on.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}
