package http

// Database-user routes (DatabasesController's user methods + grant), all
// databases.manage. The PHP re-renders the page with per-field errors on
// validation failure; the Go port follows this area's established
// flash-and-redirect convention (databasesStore/databasesDrop).

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// dbUserRow is one row of the databases page's Users tab: the login, the
// databases it may use, and the databases it can still be assigned to.
type dbUserRow struct {
	User      hosting.DBUser
	Databases []hosting.Grant
	Free      []hosting.Database
}

// dbUsersBack ports DatabasesController::back: the return_to target must be a
// databases page, else the fallback is /databases.
var dbReturnToRe = regexp.MustCompile(`^/(databases|sites/[0-9]+/databases)$`)

func (s *Server) dbUsersBack(r *http.Request) string {
	if to := r.FormValue("return_to"); dbReturnToRe.MatchString(to) {
		return to
	}
	return "/databases"
}

// secretFlash is the one-shot "shown once" credential flash. The PHP flashes
// a map (title/user/password); the sealed-cookie session stores strings, so
// it is JSON-encoded. Only the databases page pulls it.
type secretFlash struct {
	Title    string `json:"title"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// flashSecret flashes the shown-once credential (PHP session->flash('secret')).
func (s *Server) flashSecret(sess *session.Session, title, user, password string) {
	if b, err := json.Marshal(secretFlash{Title: title, User: user, Password: password}); err == nil {
		sess.Flash("secret", string(b))
	}
}

// formOn is the form-side filter_var($v, FILTER_VALIDATE_BOOL) for checkbox
// values ("1" when checked, "" or "0" when not).
func formOn(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

// dbUserErrorFor maps a hosting error to the PHP controller's flash message:
// the not-found case (notFound, the per-route wording), the no-stored-
// password case, validation field errors, and friendly user errors. ok is
// false for unexpected errors (logged + fallback).
func dbUserErrorFor(err error, notFound string) (string, bool) {
	var ue *hosting.UserError
	var ve *hosting.ValidationError
	switch {
	case errors.Is(err, hosting.ErrDBUserNotFound), errors.Is(err, hosting.ErrDatabaseNotFound):
		return notFound, true
	case errors.Is(err, hosting.ErrNoStoredDBPassword):
		return "No stored password for this user. Reset it to get a new one.", true
	case errors.As(err, &ve):
		for _, m := range ve.Fields {
			return m, true
		}
		return "Please correct the form.", true
	case errors.As(err, &ue):
		return ue.What, true
	}
	return "", false
}

// dbUsersCreate creates a database login (generated password, flashed once;
// kept encrypted when the form asks). CSRF + databases.manage. Mirrors
// DatabasesController::createUser (route dbusers.store).
func (s *Server) dbUsersCreate(w http.ResponseWriter, r *http.Request) {
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
	back := s.dbUsersBack(r)
	svc := s.databaseService(r, user)
	host, err := svc.DBUserHostFrom(r.FormValue("host_choice"), r.FormValue("host_ip"), r.FormValue("host"), user)
	if err != nil { // a validation error (hostChoice/hostIP are never looked up)
		sess.Flash("error", dbUserErrorFor(err, "")[0])
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	username, password, err := svc.CreateDBUser(r.Context(), s.currentServerID(r), user, r.FormValue("username"), host, formOn(r.FormValue("store_password")))
	if err != nil {
		if msg, ok := dbUserErrorFor(err, ""); ok {
			sess.Flash("error", msg)
		} else {
			s.log.Error("create database user", "error", err)
			sess.Flash("error", "The database user could not be created.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	s.flashSecret(sess, "Database user created", username, password)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// dbUserPassword generates a new password for a login (flashed once).
// Mirrors DatabasesController::password (route dbusers.password).
func (s *Server) dbUserPassword(w http.ResponseWriter, r *http.Request) {
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
	back := s.dbUsersBack(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	username, password, err := s.databaseService(r, user).
		ResetDBUserPassword(r.Context(), s.currentServerID(r), user, id, formOn(r.FormValue("store_password")))
	if err != nil {
		if msg, ok := dbUserErrorFor(err, "User not found."); ok {
			sess.Flash("error", msg)
		} else {
			s.log.Error("reset database user password", "error", err)
			sess.Flash("error", "The password could not be reset.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	s.flashSecret(sess, "New password", username, password)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// dbUserReveal decrypts and flashes the stored password of a login. Mirrors
// DatabasesController::reveal (route dbusers.reveal).
func (s *Server) dbUserReveal(w http.ResponseWriter, r *http.Request) {
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
	back := s.dbUsersBack(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	username, password, err := s.databaseService(r, user).
		RevealDBUserPassword(r.Context(), s.currentServerID(r), user, id)
	if err != nil {
		if msg, ok := dbUserErrorFor(err, "User not found."); ok {
			sess.Flash("error", msg)
		} else {
			s.log.Error("reveal database user password", "error", err)
			sess.Flash("error", "The stored password could not be shown.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	s.flashSecret(sess, "Stored password", username, password)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// dbUserDelete removes a database login. Mirrors DatabasesController::deleteUser
// (route dbusers.delete).
func (s *Server) dbUserDelete(w http.ResponseWriter, r *http.Request) {
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
	back := s.dbUsersBack(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.databaseService(r, user).DropDBUser(r.Context(), s.currentServerID(r), user, id); err != nil {
		if msg, ok := dbUserErrorFor(err, "User not found."); ok {
			sess.Flash("error", msg)
		} else {
			s.log.Error("delete database user", "error", err)
			sess.Flash("error", "The database user could not be removed.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Database user removed.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// dbUserAssign grants a login access to one of the caller's databases
// (privileges default to "all"). Mirrors DatabasesController::assign (route
// dbusers.assign).
func (s *Server) dbUserAssign(w http.ResponseWriter, r *http.Request) {
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
	back := s.dbUsersBack(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	dbID, err := strconv.ParseInt(r.FormValue("database_id"), 10, 64)
	if err != nil {
		sess.Flash("error", "Database or user not found.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	priv := r.FormValue("privileges")
	if priv == "" {
		priv = "all" // $request->string('privileges', 'all')
	}
	dbName, username, err := s.databaseService(r, user).
		GrantDBAccess(r.Context(), s.currentServerID(r), user, dbID, id, &priv)
	if err != nil {
		if msg, ok := dbUserErrorFor(err, "Database or user not found."); ok {
			sess.Flash("error", msg)
		} else {
			s.log.Error("assign database user", "error", err)
			sess.Flash("error", "The database access could not be updated.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	sess.Flash("success", username+" may now use "+dbName+".")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// databasesGrant sets (or, with privileges "none", revokes) a login's access
// to one database. Mirrors DatabasesController::grant (route databases.grant).
func (s *Server) databasesGrant(w http.ResponseWriter, r *http.Request) {
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
	back := s.dbUsersBack(r)
	dbID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	userID, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil {
		sess.Flash("error", "Database or user not found.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	var priv *string
	if v := r.FormValue("privileges"); v != "none" { // 'none' => null => revoke
		p := v
		priv = &p
	}
	if _, _, err := s.databaseService(r, user).
		GrantDBAccess(r.Context(), s.currentServerID(r), user, dbID, userID, priv); err != nil {
		if msg, ok := dbUserErrorFor(err, "Database or user not found."); ok {
			sess.Flash("error", msg)
		} else {
			s.log.Error("grant database access", "error", err)
			sess.Flash("error", "The privileges could not be updated.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Privileges updated.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// dbUserAddHost lets a login also connect from another host (a separate
// account with the same access, new password flashed once). Mirrors
// DatabasesController::addHost (route dbusers.host).
func (s *Server) dbUserAddHost(w http.ResponseWriter, r *http.Request) {
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
	back := s.dbUsersBack(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	svc := s.databaseService(r, user)
	host, err := svc.DBUserHostFrom(r.FormValue("host_choice"), r.FormValue("host_ip"), r.FormValue("host"), user)
	if err != nil { // a validation error (hostChoice/hostIP are never looked up)
		sess.Flash("error", dbUserErrorFor(err, "")[0])
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	username, password, err := svc.AddDBUserHost(r.Context(), s.currentServerID(r), user, id, host, formOn(r.FormValue("store_password")))
	if err != nil {
		if msg, ok := dbUserErrorFor(err, "Database user not found."); ok {
			sess.Flash("error", msg)
		} else {
			s.log.Error("add database user host", "error", err)
			sess.Flash("error", "The remote access could not be added.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	title := "Remote access added: " + username + " from " + host
	if host == "%" {
		title = "Remote access added: " + username + " from any host"
	}
	s.flashSecret(sess, title, username, password)
	http.Redirect(w, r, back, http.StatusSeeOther)
}
