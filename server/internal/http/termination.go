package http

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// terminateConfirmWord is what an administrator types to confirm terminating
// accounts (TerminationService::CONFIRM_WORD).
const terminateConfirmWord = "TERMINATE"

// termAccount is what terminating one account would remove.
type termAccount struct {
	ID                                                      int64
	Username, Role, Scope                                   string
	Websites                                                []string
	MailDomains, Mailboxes, Databases, Zones, CustomersLeft int
	Customers                                               []int64
}

// terminationPreview lists what terminating the accounts removes
// (TerminationService::preview), in the order given.
func (s *Server) terminationPreview(r *http.Request, ids []int64) []termAccount {
	ctx := r.Context()
	repo := identity.NewRepository(s.db)
	count := func(q string, id int64) int {
		var n int
		_ = s.db.QueryRowContext(ctx, q, id).Scan(&n)
		return n
	}
	seen := map[int64]bool{}
	var out []termAccount
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		u, err := repo.Find(ctx, id)
		if err != nil || u == nil {
			continue
		}
		a := termAccount{ID: id, Username: u.Username, Role: u.RoleName, Scope: u.RoleScope,
			MailDomains: count(`SELECT COUNT(*) FROM mail_domains WHERE owner_id = ?`, id),
			Mailboxes:   count(`SELECT COUNT(*) FROM mail_mailboxes m JOIN mail_domains d ON d.id = m.domain_id WHERE d.owner_id = ?`, id),
			Databases:   count(`SELECT COUNT(*) FROM databases WHERE owner_id = ?`, id),
			Zones:       count(`SELECT COUNT(*) FROM dns_zones WHERE owner_id = ?`, id)}
		if rows, err := s.db.QueryContext(ctx, `SELECT domain FROM websites WHERE owner_id = ? ORDER BY domain`, id); err == nil {
			for rows.Next() {
				var d string
				if rows.Scan(&d) == nil {
					a.Websites = append(a.Websites, d)
				}
			}
			rows.Close()
		}
		if rows, err := s.db.QueryContext(ctx, `SELECT id FROM users WHERE parent_id = ?`, id); err == nil {
			for rows.Next() {
				var c int64
				if rows.Scan(&c) == nil {
					a.Customers = append(a.Customers, c)
				}
			}
			rows.Close()
		}
		out = append(out, a)
	}
	return out
}

func postedIDs(r *http.Request) []int64 {
	_ = r.ParseForm()
	var ids []int64
	for _, k := range []string{"ids[]", "ids"} {
		for _, v := range r.PostForm[k] {
			if id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && id > 0 {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func (s *Server) terminateData(r *http.Request, accounts []termAccount) map[string]any {
	selected := map[int64]bool{}
	for _, a := range accounts {
		selected[a.ID] = true
	}
	for i := range accounts {
		for _, c := range accounts[i].Customers {
			if !selected[c] {
				accounts[i].CustomersLeft++
			}
		}
	}
	sess := sessionFrom(r.Context())
	return map[string]any{
		"CSRF": session.CSRFToken(sess), "User": userFrom(r.Context()), "Title": "Terminate accounts",
		"Accounts": accounts, "Word": terminateConfirmWord, "Count": len(accounts),
		"Error": pullFlashKey(sess, "error"), "Results": nil,
	}
}

// accountsTerminateConfirm POST /accounts/terminate/confirm — what terminating
// the selected accounts would remove (AccountAdminController::confirmTerminate).
func (s *Server) accountsTerminateConfirm(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	if !hosting.IsAll(userFrom(r.Context())) {
		sess.Flash("error", "Only administrators can terminate accounts.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	accounts := s.terminationPreview(r, postedIDs(r))
	if len(accounts) == 0 {
		sess.Flash("error", "Select the accounts to terminate first.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	s.render(w, "accounts_terminate", s.terminateData(r, accounts))
}

type termResult struct {
	Username, Message string
	OK                bool
}

// accountsTerminate POST /accounts/terminate — terminates the confirmed
// accounts, customers before their reseller, one after another; an account
// whose removal fails part-way is kept (with what is left) and reported, and
// the others continue (TerminationService::terminate).
func (s *Server) accountsTerminate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	actor := userFrom(r.Context())
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	if !hosting.IsAll(actor) {
		sess.Flash("error", "Only administrators can terminate accounts.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	accounts := s.terminationPreview(r, postedIDs(r))
	expected, _ := strconv.Atoi(r.PostFormValue("count"))
	if strings.TrimSpace(r.PostFormValue("confirm")) != terminateConfirmWord || expected != len(accounts) || len(accounts) == 0 {
		sess.Flash("error", "Type "+terminateConfirmWord+" to confirm terminating the selected accounts.")
		if len(accounts) == 0 {
			http.Redirect(w, r, "/users", http.StatusSeeOther)
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.render(w, "accounts_terminate", s.terminateData(r, accounts))
		return
	}
	selected := map[int64]bool{}
	for _, a := range accounts {
		selected[a.ID] = true
	}
	// Customers before their reseller.
	sort.SliceStable(accounts, func(i, j int) bool { return len(accounts[i].Customers) == 0 && len(accounts[j].Customers) != 0 })
	repo := identity.NewRepository(s.db)
	uid := actor.ID
	var results []termResult
	for _, a := range accounts {
		if a.Scope != "own" {
			results = append(results, termResult{a.Username, "Not a hosting account (staff accounts are removed under Users).", false})
			continue
		}
		left := 0
		for _, c := range a.Customers {
			if !selected[c] {
				left++
			}
		}
		if left > 0 {
			results = append(results, termResult{a.Username, fmt.Sprintf("The reseller still has %d customer(s) that were not selected. Terminate or move them first.", left), false})
			continue
		}
		if a.ID == actor.ID {
			results = append(results, termResult{a.Username, "You cannot terminate your own account. Ask another administrator to remove it.", false})
			continue
		}
		target, err := repo.Find(r.Context(), a.ID)
		if err != nil || target == nil {
			results = append(results, termResult{a.Username, "The account no longer exists.", false})
			continue
		}
		if _, err := s.terminateAccount(r, actor, target); err != nil {
			msg := problemMessage(err)
			results = append(results, termResult{a.Username, "Stopped: " + msg + " The account and what was not yet removed are kept; terminate it again to continue.", false})
			s.audit.Record(r.Context(), audit.Entry{Action: "account.terminate", TargetType: "user", TargetID: strconv.FormatInt(a.ID, 10), Result: audit.Failure,
				Details: map[string]any{"username": a.Username, "error": msg}, UserID: &uid, Username: actor.Username})
			continue
		}
		results = append(results, termResult{a.Username, fmt.Sprintf("Terminated: %d website(s), %d mailbox(es), %d database(s), %d DNS zone(s).", len(a.Websites), a.Mailboxes, a.Databases, a.Zones), true})
		s.audit.Record(r.Context(), audit.Entry{Action: "account.terminate", TargetType: "user", TargetID: strconv.FormatInt(a.ID, 10), Result: audit.Success,
			Details: map[string]any{"username": a.Username, "websites": a.Websites}, UserID: &uid, Username: actor.Username})
	}
	data := s.terminateData(r, nil)
	data["Results"] = results
	s.render(w, "accounts_terminate", data)
}

// problemMessage is the user-facing text of a service error.
func problemMessage(err error) string {
	return hosting.ProblemText(err)
}
