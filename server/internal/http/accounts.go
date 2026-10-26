package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// accountsCreate GET /accounts/new — new-account form page. Renders the
// real form (domain, username, contact, password, plan, reseller/www
// toggles) backed by hosting.AccountService::create.
// Mirrors AccountsController::create (accounts.create, users.manage).
func (s *Server) accountsCreate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	data := map[string]any{
		"CSRF":        session.CSRFToken(sess),
		"User":        user,
		"Title":       "New account",
		"Plans":       nil,
		"Error":       "",
		"Success":     "",
		"Stub":        false,
		"CanReseller": hosting.IsAll(user),
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	plans, err := hosting.NewPlanRepository(s.db).Visible(r.Context(), user)
	if err == nil {
		data["Plans"] = plans
	}
	s.render(w, "accounts_create", data)
}

// accountsStore POST /accounts — creates a new hosting account. Wires to
// hosting.AccountService::create which provisions the panel login, plan,
// hosting options, initial website (with www alias), DNS zone and SSL,
// rolling back the login if website creation fails.
// Mirrors AccountsController::store.
func (s *Server) accountsStore(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	actor := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	svc := s.accountService(r, actor)
	acct, err := svc.Create(r.Context(), postValues(r), actor)
	if err != nil {
		var ve *hosting.ValidationError
		msg := err.Error()
		if errors.As(err, &ve) {
			for _, m := range ve.Fields {
				msg = m
				break
			}
		}
		sess.Flash("error", msg)
		http.Redirect(w, r, "/accounts/new", http.StatusSeeOther)
		return
	}
	if acct.Password.Valid {
		sess.Flash("success", "Account "+acct.Username+" created. Generated password: "+acct.Password.String)
	} else {
		sess.Flash("success", "Account "+acct.Username+" created.")
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// accountService builds hosting.AccountService with per-request audit hooks
// (same pattern as websiteService/websiteAuditHook).
func (s *Server) accountService(r *http.Request, user *identity.User) *hosting.AccountService {
	users := identity.NewRepository(s.db)
	roles := authz.NewRoleRepository(s.db)
	plans := hosting.NewPlanRepository(s.db)
	limits := hosting.NewPlanLimits(s.db)
	usvc := hosting.NewUserService(users, roles, s.policy, plans, limits, s.accountAuditHook(r, user))
	ws := hosting.NewWebsiteService(
		hosting.NewWebsiteRepository(s.db), s.agent, s.features, limits,
		hosting.NewDnsRepository(s.db), s.settings, s.websiteAuditHook(r, user),
		s.currentServerID(r),
	)
	return hosting.NewAccountService(usvc, users, ws, hosting.NewAccountOptionStore(s.db), s.db, s.accountAuditHook(r, user))
}

func (s *Server) accountAuditHook(r *http.Request, user *identity.User) hosting.Auditor {
	return func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
		if targetType == "" {
			targetType = "user"
		}
		uid := user.ID
		s.audit.Record(ctx, audit.Entry{
			UserID:     &uid,
			Username:   user.Username,
			Action:     action,
			TargetType: targetType,
			TargetID:   target,
			Result:     result,
			IP:         clientIP(r),
			UserAgent:  r.UserAgent(),
			Details:    details,
		})
	}
}

// accountsBandwidth GET /accounts/bandwidth — bandwidth usage report per
// account. Calls agent `accounts.bandwidth` and renders the result; if
// the agent action is unavailable it surfaces the error on the page so
// the operator can see what's missing. Mirrors
// AccountAdminController::bandwidth.
func (s *Server) accountsBandwidth(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Account bandwidth",
		"Status":     nil,
		"Stub":       false,
		"Error":      "",
		"Success":    "",
		"AgentError": nil,
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	if raw, err := s.agent.Call(r.Context(), "accounts.bandwidth", nil, 60*time.Second); err == nil {
		var parsed any
		_ = json.Unmarshal(raw, &parsed)
		data["Status"] = parsed
	} else {
		data["AgentError"] = err.Error()
	}
	s.render(w, "accounts_bandwidth", data)
}

// accountsTerminate POST /accounts/terminate — starts termination of a
// hosting account. Administrators only; takes a single user_id (the PHP
// controller supports bulk checkboxes from the users list, but Go keeps
// it single-account for now to avoid building the confirm page and
// JobTracker queue). Mirrors AccountAdminController::confirmTerminate.
// terminateConfirmWord is what an administrator types to confirm terminating a
// hosting account (the PHP panel's TERMINATE confirmation).
const terminateConfirmWord = "TERMINATE"

func (s *Server) accountsTerminate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	actor := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	if !hosting.IsAll(actor) {
		sess.Flash("error", "Only administrators can terminate hosting accounts.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil || id <= 0 {
		sess.Flash("error", "Select the account to terminate first.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	repo := identity.NewRepository(s.db)
	target, err := repo.FindByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || target == nil {
		sess.Flash("error", "The account no longer exists.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if target.RoleScope != "own" {
		sess.Flash("error", "Staff accounts are removed from the Users page, not terminated.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if target.ID == actor.ID {
		sess.Flash("error", "You cannot terminate your own account. Ask another administrator to remove it.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	// Stash the target id in session for the confirm step (matches PHP
	// rendering a confirm page; we avoid building a new template by
	// carrying the pending id and re-flashing errors on /users).
	sess.Set("_terminate_pending_id", strconv.FormatInt(id, 10))
	sess.Set("_terminate_pending_user", target.Username)
	sess.Flash("error", "Type \""+terminateConfirmWord+"\" on the confirmation screen to terminate "+target.Username+".")
	http.Redirect(w, r, "/accounts/terminate/confirm", http.StatusSeeOther)
}

// accountsTerminateConfirmGet GET /accounts/terminate/confirm — renders
// the confirmation form for a pending termination. A separate GET page
// (rather than re-rendering from a POST) keeps the flow bookmarkable.
func (s *Server) accountsTerminateConfirmGet(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	actor := userFrom(r.Context())
	if !hosting.IsAll(actor) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	v := sess.Get("_terminate_pending_id")
	if v == "" {
		sess.Flash("error", "Select the accounts to terminate first.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	pendingID, err := strconv.ParseInt(v, 10, 64)
	if err != nil || pendingID <= 0 {
		clearTerminatePending(sess)
		sess.Flash("error", "Select the accounts to terminate first.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	user := sess.Get("_terminate_pending_user")
	username := user
	if username == "" {
		sess.Flash("error", "Select the accounts to terminate first.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.render(w, "accounts_terminate", map[string]any{
		"CSRF":     session.CSRFToken(sess),
		"User":     actor,
		"Title":    "Confirm termination",
		"Username": username,
		"Word":     terminateConfirmWord,
		"Error":    flashStr(sess, "error"),
	})
}

// accountsTerminateConfirm POST /accounts/terminate/confirm — performs
// the termination when the admin types TERMINATE and the count matches.
// Mirrors AccountAdminController::terminate / TerminationService::terminate
// for the single-account case.
func (s *Server) accountsTerminateConfirm(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	actor := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	if !hosting.IsAll(actor) {
		sess.Flash("error", "Only administrators can terminate hosting accounts.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	v := sess.Get("_terminate_pending_id")
	if v == "" {
		sess.Flash("error", "Select the accounts to terminate first.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	pendingID, perr := strconv.ParseInt(v, 10, 64)
	pendingUser := sess.Get("_terminate_pending_user")
	username := pendingUser
	confirm := strings.TrimSpace(r.FormValue("confirm"))
	if confirm != terminateConfirmWord || perr != nil || pendingID <= 0 || username == "" {
		sess.Flash("error", "Type \""+terminateConfirmWord+"\" to confirm terminating the selected account.")
		http.Redirect(w, r, "/accounts/terminate/confirm", http.StatusSeeOther)
		return
	}
	repo := identity.NewRepository(s.db)
	target, err := repo.FindByID(r.Context(), pendingID)
	if errors.Is(err, sql.ErrNoRows) || target == nil {
		clearTerminatePending(sess)
		sess.Flash("error", "The account no longer exists.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if target.RoleScope != "own" {
		clearTerminatePending(sess)
		sess.Flash("error", "Staff accounts are removed from the Users page, not terminated.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if target.ID == actor.ID {
		clearTerminatePending(sess)
		sess.Flash("error", "You cannot terminate your own account. Ask another administrator to remove it.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	msg, terr := s.terminateAccount(r, actor, target)
	clearTerminatePending(sess)
	if terr != nil {
		uid := actor.ID
		s.audit.Record(r.Context(), audit.Entry{
			Action: "account.terminate", TargetType: "user", TargetID: strconv.FormatInt(target.ID, 10),
			Result: audit.Failure, Details: map[string]any{"username": target.Username, "error": terr.Error()},
			UserID: &uid, Username: actor.Username,
		})
		sess.Flash("error", "Termination stopped: "+terr.Error()+" The account and anything not yet removed are kept.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func clearTerminatePending(sess *session.Session) {
	sess.Remove("_terminate_pending_id")
	sess.Remove("_terminate_pending_user")
}

// terminateAccount removes the hosting account's websites, databases,
// DNS zones (website-free ones), mail domains and finally the user row.
// Mirrors TerminationService::one for the single-account, single-server
// case. On-server teardown of mail/databases is a best-effort follow-up;
// websites are fully removed through WebsiteService.Delete.
func (s *Server) terminateAccount(r *http.Request, actor *identity.User, target *identity.User) (string, error) {
	ctx := r.Context()
	ws := s.websiteService(r, actor)
	sitesRepo := hosting.NewWebsiteRepository(s.db)
	// Children first, parents last (child sites must be removed before their
	// parent addon domain so nginx -t always sees a consistent config).
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id FROM websites WHERE owner_id = ? ORDER BY parent_id IS NULL, id`,
		target.ID)
	if err != nil {
		return "", err
	}
	type siteRow struct{ id, server int64 }
	var sites []siteRow
	for rows.Next() {
		var r siteRow
		if err := rows.Scan(&r.id, &r.server); err != nil {
			rows.Close()
			return "", err
		}
		sites = append(sites, r)
	}
	rows.Close()

	webCount := 0
	var warns []string
	for _, r := range sites {
		site, err := sitesRepo.FindFull(ctx, r.server, r.id, nil)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return "", fmt.Errorf("load site %d: %w", r.id, err)
		}
		ww, derr := ws.Delete(ctx, site, true, true)
		if derr != nil {
			return "", fmt.Errorf("delete website %s: %w", site.Domain, derr)
		}
		webCount++
		warns = append(warns, ww...)
	}
	// Databases and database users: delete panel rows. On-server DROP is
	// done as a best-effort follow-up (not required for safety since the
	// panel no longer references them, and the agent requires a server
	// context we already built for sites above).
	var dbCount int
	{
		drows, err := s.db.QueryContext(ctx, `SELECT id FROM databases WHERE owner_id = ?`, target.ID)
		if err == nil {
			var ids []int64
			for drows.Next() {
				var id int64
				if err := drows.Scan(&id); err != nil {
					break
				}
				ids = append(ids, id)
			}
			drows.Close()
			for _, id := range ids {
				_, _ = s.db.ExecContext(ctx, `DELETE FROM databases WHERE id = ?`, id)
				dbCount++
			}
		}
		dur, err := s.db.QueryContext(ctx, `SELECT id FROM database_users WHERE owner_id = ?`, target.ID)
		if err == nil {
			// Read the ids and close the cursor before deleting: with one pooled
			// connection the DELETE would wait forever on the cursor's connection.
			var duIDs []int64
			for dur.Next() {
				var id int64
				if err := dur.Scan(&id); err != nil {
					break
				}
				duIDs = append(duIDs, id)
			}
			dur.Close()
			for _, id := range duIDs {
				_, _ = s.db.ExecContext(ctx, `DELETE FROM database_users WHERE id = ?`, id)
			}
		}
	}
	// DNS zones not tied to a website (website zones are already dropped
	// by WebsiteService.Delete).
	var zoneCount int
	{
		zrows, err := s.db.QueryContext(ctx, `SELECT id FROM dns_zones WHERE owner_id = ? AND website_id IS NULL`, target.ID)
		if err == nil {
			var ids []int64
			for zrows.Next() {
				var id int64
				if err := zrows.Scan(&id); err != nil {
					break
				}
				ids = append(ids, id)
			}
			zrows.Close()
			for _, id := range ids {
				_, _ = s.db.ExecContext(ctx, `DELETE FROM dns_records WHERE zone_id = ?`, id)
				_, _ = s.db.ExecContext(ctx, `DELETE FROM dns_zones WHERE id = ?`, id)
				zoneCount++
			}
		}
	}
	// Mail domains, mailboxes, aliases.
	{
		mrows, err := s.db.QueryContext(ctx, `SELECT id FROM mail_domains WHERE owner_id = ?`, target.ID)
		if err == nil {
			var ids []int64
			for mrows.Next() {
				var id int64
				if err := mrows.Scan(&id); err != nil {
					break
				}
				ids = append(ids, id)
			}
			mrows.Close()
			for _, id := range ids {
				_, _ = s.db.ExecContext(ctx, `DELETE FROM mail_aliases WHERE domain_id = ?`, id)
				_, _ = s.db.ExecContext(ctx, `DELETE FROM mail_mailboxes WHERE domain_id = ?`, id)
				_, _ = s.db.ExecContext(ctx, `DELETE FROM mail_domains WHERE id = ?`, id)
			}
		}
	}
	// WebAuthn credentials, API tokens, limit overrides.
	_, _ = s.db.ExecContext(ctx, `DELETE FROM webauthn_credentials WHERE user_id = ?`, target.ID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE user_id = ?`, target.ID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM limit_overrides WHERE user_id = ?`, target.ID)
	// Finally the user row.
	if err := identity.NewRepository(s.db).Delete(ctx, target.ID); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("%s terminated: %d website(s), %d database(s), %d DNS zone(s) removed.", target.Username, webCount, dbCount, zoneCount)
	if len(warns) > 0 {
		msg += " Warnings: " + strings.Join(warns, " ")
	}
	return msg, nil
}
