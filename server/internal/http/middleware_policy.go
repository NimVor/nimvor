package http

// Sign-in policies enforced on every authenticated request, ported from
// Middleware\Authenticate: mandatory two-factor roles, password age and the
// step-up password confirmation for sensitive routes.

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
)

// stepUpRoutes need a password confirmed within the last 15 minutes
// (Authenticate::STEP_UP).
var stepUpRoutes = map[string]bool{
	"users.store": true, "users.update": true, "users.delete": true, "users.2fa.reset": true,
	"accounts.store": true,
	"roles.store":    true, "roles.update": true, "roles.2fa": true, "roles.delete": true,
	"tokens.store": true, "security.policies.save": true,
	// SSH keys of administrators grant a root-capable login.
	"security.ssh.key_add": true, "security.ssh.key_generate": true, "security.ssh.key_remove": true,
	// Runs the installer as root and restarts everything.
	"whacp.update.start": true,
}

// policySetupAllowed: pages a user who must set up two-factor sign-in (or
// change an expired password) can still reach.
func policySetupAllowed(routeName, path string) bool {
	switch routeName {
	case "profile", "profile.password", "logout":
		return true
	}
	if strings.HasPrefix(routeName, "profile.2fa.") {
		return true
	}
	return path == "/profile" || path == "/profile/password" || strings.HasPrefix(path, "/profile/two-factor/")
}

func (s *Server) policyRefuse(w http.ResponseWriter, r *http.Request, message, redirect string) {
	if wantsJSON(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "error": map[string]string{"message": message}})
		return
	}
	sessionFrom(r.Context()).Flash("error", message)
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// requirePolicies must run inside requireAuth.
func (s *Server) requirePolicies(routeName string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFrom(r.Context())
		if user == nil {
			next(w, r)
			return
		}
		ctx := r.Context()
		allowed := policySetupAllowed(routeName, r.URL.Path)
		if user.RoleRequires2FA && !allowed {
			on, err := s.twoFactor.Enabled(ctx, user.ID)
			if err == nil && !on {
				var keys int
				err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?`, user.ID).Scan(&keys)
				on = keys > 0
			}
			if err != nil {
				s.log.Error("two-factor policy", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if !on {
				s.policyRefuse(w, r, "Your role requires two-factor sign-in. Set it up to continue.", "/profile#two-factor")
				return
			}
		}
		if !allowed {
			maxAge, _ := s.settings.Int(ctx, "policy_password_age_days", 0)
			if maxAge > 0 {
				changed := time.Time{}
				if user.PasswordChangedAt.Valid {
					changed = parseStoredTime(user.PasswordChangedAt.String)
				}
				if time.Since(changed) > time.Duration(maxAge)*24*time.Hour {
					s.policyRefuse(w, r, fmt.Sprintf("Your password is older than %d days. Choose a new password to continue.", maxAge), "/profile#password")
					return
				}
			}
		}
		if stepUpRoutes[routeName] && !s.auth.IsConfirmed(sessionFrom(ctx)) {
			message := "Confirm your password to continue, then submit again."
			back := "/"
			if ref, err := url.Parse(r.Header.Get("Referer")); err == nil && ref.Path != "" {
				back = ref.Path
				if ref.RawQuery != "" {
					back += "?" + ref.RawQuery
				}
			}
			target := "/confirm-password?return=" + url.QueryEscape(safeReturnPath(back))
			s.audit.Record(ctx, audit.Entry{
				UserID: &user.ID, Username: user.Username, Action: "security.step_up_required",
				TargetType: "route", TargetID: r.URL.Path, Result: audit.Denied, IP: clientIP(r),
			})
			if wantsJSON(r) {
				writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "error": map[string]string{"message": message}, "redirect": target})
				return
			}
			sessionFrom(ctx).Flash("error", message)
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// parseStoredTime reads the timestamp formats found in the shared database
// (RFC 3339 from the panel, "Y-m-d H:i:s" from SQLite defaults); unparseable
// values count as the epoch, i.e. "very old".
func parseStoredTime(v string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}
