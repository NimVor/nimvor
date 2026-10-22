package http

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
	"github.com/mchkhawarshahzad/whacp/server/internal/twofactor"
)

// sessionTwoFactorSetup is the session key holding the not-yet-enabled TOTP
// secret during setup (ProfileController::SETUP_KEY).
const sessionTwoFactorSetup = "_profile_2fa_setup"

// verifyCurrentPassword checks the form's current-password field against the
// signed-in user (ProfileController::passwordOk).
func (s *Server) verifyCurrentPassword(ctx context.Context, user *identity.User, password string) bool {
	hash, err := identity.NewRepository(s.db).PasswordHash(ctx, user.ID)
	return err == nil && hash != "" && identity.VerifyPassword(hash, password)
}

// flashRedirect flashes a message and redirects (303) to loc.
func (s *Server) flashRedirect(w http.ResponseWriter, r *http.Request, sess *session.Session, loc, flashKey, msg string) {
	sess.Flash(flashKey, msg)
	http.Redirect(w, r, loc, http.StatusSeeOther)
}

func (s *Server) twoFactorError(w http.ResponseWriter, r *http.Request, sess *session.Session, loc, msg string) {
	s.flashRedirect(w, r, sess, loc, "error", msg)
}

func (s *Server) twoFactorSuccess(w http.ResponseWriter, r *http.Request, sess *session.Session, loc, msg string) {
	s.flashRedirect(w, r, sess, loc, "success", msg)
}

// profileTwoFactorSetup starts the setup: a fresh secret is staged in the
// session and the profile page renders it. Mirrors
// ProfileController::twoFactorSetup (route "profile.2fa.setup").
func (s *Server) profileTwoFactorSetup(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	_ = r.ParseForm()
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	enabled, err := s.twoFactor.Enabled(r.Context(), user.ID)
	if err != nil {
		s.log.Error("2fa status", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if enabled {
		s.twoFactorError(w, r, sess, "/profile", "Two-factor sign-in is already on.")
		return
	}
	secret, err := twofactor.NewSecret()
	if err != nil {
		s.log.Error("2fa new secret", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sess.Set(sessionTwoFactorSetup, secret)
	http.Redirect(w, r, "/profile#two-factor", http.StatusSeeOther)
}

// profileTwoFactorEnable turns two-factor on after the current password and a
// valid authenticator code. Mirrors ProfileController::twoFactorEnable
// (route "profile.2fa.enable").
func (s *Server) profileTwoFactorEnable(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	secret := sess.Get(sessionTwoFactorSetup)
	if secret == "" {
		s.twoFactorError(w, r, sess, "/profile", "Start the setup again.")
		return
	}
	if !s.verifyCurrentPassword(r.Context(), user, r.FormValue("password")) {
		s.twoFactorError(w, r, sess, "/profile#two-factor", "Your current password is not correct.")
		return
	}
	codes, err := s.twoFactor.Enable(r.Context(), user.ID, secret, clip(r.FormValue("code"), 40))
	if err != nil {
		if errors.Is(err, twofactor.ErrBadCode) {
			s.twoFactorError(w, r, sess, "/profile#two-factor", "That code is not valid. Check that your phone shows the right time and try the newest code.")
			return
		}
		s.log.Error("2fa enable", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sess.Remove(sessionTwoFactorSetup)
	sess.Flash("recovery_codes", strings.Join(codes, "\n"))
	s.audit.Record(r.Context(), audit.Entry{UserID: &user.ID, Username: user.Username, Action: "user.two_factor_enabled", TargetType: "user", TargetID: strconv.FormatInt(user.ID, 10), Result: audit.Success, IP: clientIP(r)})
	s.twoFactorSuccess(w, r, sess, "/profile#two-factor", "Two-factor sign-in is on.")
}

// profileTwoFactorDisable turns two-factor off (current password required).
// Mirrors ProfileController::twoFactorDisable (route "profile.2fa.disable").
func (s *Server) profileTwoFactorDisable(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	if !s.verifyCurrentPassword(r.Context(), user, r.FormValue("password")) {
		s.twoFactorError(w, r, sess, "/profile#two-factor", "Your current password is not correct.")
		return
	}
	if err := s.twoFactor.Disable(r.Context(), user.ID); err != nil {
		s.log.Error("2fa disable", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sess.Remove(sessionTwoFactorSetup)
	s.audit.Record(r.Context(), audit.Entry{UserID: &user.ID, Username: user.Username, Action: "user.two_factor_disabled", TargetType: "user", TargetID: strconv.FormatInt(user.ID, 10), Result: audit.Success, IP: clientIP(r)})
	s.twoFactorSuccess(w, r, sess, "/profile#two-factor", "Two-factor sign-in is off.")
}

// profileTwoFactorRecovery regenerates the recovery codes (current password
// required); the old codes stop working. Mirrors
// ProfileController::twoFactorRecovery (route "profile.2fa.recovery").
func (s *Server) profileTwoFactorRecovery(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	enabled, err := s.twoFactor.Enabled(r.Context(), user.ID)
	if err != nil {
		s.log.Error("2fa status", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !enabled {
		s.twoFactorError(w, r, sess, "/profile", "Two-factor sign-in is off.")
		return
	}
	if !s.verifyCurrentPassword(r.Context(), user, r.FormValue("password")) {
		s.twoFactorError(w, r, sess, "/profile#two-factor", "Your current password is not correct.")
		return
	}
	codes, err := s.twoFactor.RegenerateRecovery(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, twofactor.ErrNotEnabled) {
			s.twoFactorError(w, r, sess, "/profile", "Two-factor sign-in is off.")
			return
		}
		s.log.Error("2fa regenerate recovery", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sess.Flash("recovery_codes", strings.Join(codes, "\n"))
	s.audit.Record(r.Context(), audit.Entry{UserID: &user.ID, Username: user.Username, Action: "user.two_factor_recovery", TargetType: "user", TargetID: strconv.FormatInt(user.ID, 10), Result: audit.Success, IP: clientIP(r)})
	s.twoFactorSuccess(w, r, sess, "/profile#two-factor", "New recovery codes created; the old ones no longer work.")
}
