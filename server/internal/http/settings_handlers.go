package http

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// settingsMailTest POST /settings/mail/test — sends a short test email to
// the current user's address via the internal SMTP Mailer.
// Mirrors SettingsController::testMail (settings.mail.test, settings.manage).
func (s *Server) settingsMailTest(w http.ResponseWriter, r *http.Request) {
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
	to := strings.TrimSpace(user.Email)
	if to == "" {
		sess.Flash("error", "Your account has no email address on file; set one on your profile first.")
		http.Redirect(w, r, "/settings#email", http.StatusSeeOther)
		return
	}
	uid := user.ID
	if err := s.mailer.Send(r.Context(), []string{to}, "WhaCP test message", "This is a test message from WhaCP.\n\nIf you can read it, outgoing email works.\n"); err != nil {
		s.audit.Record(r.Context(), audit.Entry{
			Action: "settings.mail_test", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
			Result: audit.Failure, Details: map[string]any{"to": to, "error": err.Error()},
			UserID: &uid, Username: user.Username,
		})
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/settings#email", http.StatusSeeOther)
		return
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "settings.mail_test", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Success, Details: map[string]any{"to": to},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Test message sent to "+to+".")
	http.Redirect(w, r, "/settings#email", http.StatusSeeOther)
}

// --- helpers ------------------------------------------------------------

func hashFor(tab string) string {
	if !isSafeTab(tab) {
		return ""
	}
	return "#" + tab
}

func isSafeTab(t string) bool {
	if t == "" {
		return false
	}
	if len(t) > 20 {
		return false
	}
	for _, c := range t {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func isPublicIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	nums := make([]int, 4)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return false
		}
		nums[i] = n
	}
	if nums[0] == 10 || nums[0] == 127 {
		return false
	}
	if nums[0] == 172 && nums[1] >= 16 && nums[1] <= 31 {
		return false
	}
	if nums[0] == 192 && nums[1] == 168 {
		return false
	}
	if nums[0] == 169 && nums[1] == 254 {
		return false
	}
	return true
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
