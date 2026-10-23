package http

// This file ports the per-mailbox e-mail filters + autoresponder page
// (MailFiltersController::show / save / toggle / delete / responder).

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// filterRow is one filter of the list, summarized for the table.
type filterRow struct {
	Index   int
	Name    string
	Enabled bool
	When    string
	Then    string
}

// filterRuleRow / filterActionRow are the modal's padded form rows.
type filterRuleRow struct {
	Part  string
	Match string
	Value string
}

type filterActionRow struct {
	Type  string
	Value string
}

// filterModal is the new/edit filter window (4 condition rows, 3 action
// rows, padded as the PHP view).
type filterModal struct {
	Index   int // -1 for the new-filter window
	IsNew   bool
	Title   string
	Name    string
	Any     bool
	Enabled bool
	Rules   []filterRuleRow
	Actions []filterActionRow
}

func filterModalFor(f *hosting.MailFilter, index int) filterModal {
	rules := make([]filterRuleRow, 4)
	for i := 0; i < 4; i++ {
		if f != nil && i < len(f.Rules) {
			rules[i] = filterRuleRow{Part: f.Rules[i].Part, Match: f.Rules[i].Match, Value: f.Rules[i].Value}
		} else {
			rules[i] = filterRuleRow{Part: "subject", Match: "contains"}
		}
	}
	acts := make([]filterActionRow, 3)
	if f != nil {
		for i := 0; i < 3 && i < len(f.Actions); i++ {
			acts[i] = filterActionRow{Type: f.Actions[i].Type, Value: f.Actions[i].Value}
		}
	}
	m := filterModal{Index: index, Rules: rules, Actions: acts}
	if f == nil {
		m.IsNew = true
		m.Title = "New filter"
		m.Enabled = true
	} else {
		m.Title = "Edit filter"
		m.Name = f.Name
		m.Any = f.Any
		m.Enabled = f.Enabled
	}
	return m
}

// mailFiltersTarget loads the route's domain + mailbox; ok is false after
// the "not found" redirect / 404 page is sent.
func (s *Server) mailFiltersTarget(w http.ResponseWriter, r *http.Request, user *identity.User) (*hosting.MailDomainFull, *hosting.Mailbox, string, bool) {
	d, err := hosting.NewMailRepository(s.db).Domain(r.Context(), s.currentServerID(r), pathID(r), user)
	if err != nil {
		s.log.Error("load mail domain", "error", err)
		s.showNotFound(w)
		return nil, nil, "", false
	}
	if d == nil {
		sess := sessionFrom(r.Context())
		sess.Flash("error", "Mail domain not found.")
		http.Redirect(w, r, "/mail", http.StatusSeeOther)
		return nil, nil, "", false
	}
	mbID, err := strconv.ParseInt(r.PathValue("mailbox"), 10, 64)
	if err != nil {
		s.showNotFound(w)
		return nil, nil, "", false
	}
	mbox, address, err := s.siteMailService(r, user).MailFilterMailbox(r.Context(), d, mbID)
	if err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			sess := sessionFrom(r.Context())
			sess.Flash("error", userFacingMsg(ue))
			http.Redirect(w, r, "/mail", http.StatusSeeOther)
			return nil, nil, "", false
		}
		s.log.Error("load mailbox", "domain", d.ID, "error", err)
		s.showNotFound(w)
		return nil, nil, "", false
	}
	return d, mbox, address, true
}

// filtersBack is the mailbox's filters page (the redirect target).
func filtersBack(d *hosting.MailDomainFull, mbox *hosting.Mailbox) string {
	return fmt.Sprintf("/mail/domains/%d/mailboxes/%d/filters", d.ID, mbox.ID)
}

// mailFiltersShow renders the mailbox's filters + autoresponder
// (MailFiltersController::show, route mail.filters).
func (s *Server) mailFiltersShow(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	d, mbox, address, ok := s.mailFiltersTarget(w, r, user)
	if !ok {
		return
	}
	svc := s.siteMailService(r, user)
	var data *hosting.MailFilterData
	agentErr := ""
	if got, err := svc.MailFilterGet(r.Context(), address); err != nil {
		agentErr = userFacingMsgFrom(err)
		s.log.Error("read filters", "domain", d.ID, "mailbox", mbox.ID, "error", err)
	} else {
		data = got
	}
	rows := []filterRow{}
	modals := []filterModal{filterModalFor(nil, -1)}
	if data != nil {
		for n, f := range data.Filters {
			when, then := hosting.FilterSummary(f)
			rows = append(rows, filterRow{Index: n, Name: f.Name, Enabled: f.Enabled, When: when, Then: then})
			modals = append(modals, filterModalFor(&f, n))
		}
	}
	s.render(w, "mail_filters", map[string]any{
		"User":        user,
		"CSRF":        session.CSRFToken(sess),
		"Domain":      d,
		"Mailbox":     mbox,
		"Address":     address,
		"Filters":     rows,
		"FilterCount": len(rows),
		"Modals":      modals,
		"Responder":   responderView(data),
		"AgentErr":    agentErr,
		"Parts":       hosting.MailFilterParts,
		"Matches":     hosting.MailFilterMatches,
		"Actions":     hosting.MailFilterActions,
		"Success":     pullFlashKey(sess, "success"),
		"Error":       pullFlashKey(sess, "error"),
	})
}

// responderView is the autoresponder form's values (the defaults when the
// agent reply is missing).
func responderView(data *hosting.MailFilterData) hosting.MailResponder {
	if data == nil {
		return hosting.MailResponder{Days: 1}
	}
	return data.Responder
}

// mailFilterSave creates or edits a filter (MailFiltersController::save). A
// refused form redirects back with the messages (PHP's change()).
func (s *Server) mailFilterSave(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	d, mbox, address, ok := s.mailFiltersTarget(w, r, user)
	if !ok {
		return
	}
	back := filtersBack(d, mbox)
	svc := s.siteMailService(r, user)
	data, err := svc.MailFilterGet(r.Context(), address)
	if err != nil {
		s.flashFilterFailure(sess, back, "The filters could not be read.", err)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	filter, err := hosting.MailFilterFromForm(postValues(r))
	if err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			sess.Flash("error", strings.Join(fieldErrorList(ve), " "))
		} else {
			s.log.Error("parse filter", "domain", d.ID, "error", err)
			sess.Flash("error", "The filter could not be saved.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	list := data.Filters
	if v := r.PostFormValue("index"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n < len(list) {
			list[n] = *filter
		} else {
			list = append(list, *filter)
		}
	} else {
		list = append(list, *filter)
	}
	if err := svc.MailFilterSave(r.Context(), address, list, data.Responder, "filter saved"); err != nil {
		s.flashFilterFailure(sess, back, "The filter could not be saved.", err)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Filter “"+filter.Name+"” saved.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// mailFilterToggle turns a filter on or off (MailFiltersController::toggle).
func (s *Server) mailFilterToggle(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	d, mbox, address, ok := s.mailFiltersTarget(w, r, user)
	if !ok {
		return
	}
	back := filtersBack(d, mbox)
	svc := s.siteMailService(r, user)
	data, err := svc.MailFilterGet(r.Context(), address)
	if err != nil {
		s.flashFilterFailure(sess, back, "The filters could not be read.", err)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 0 || n >= len(data.Filters) {
		sess.Flash("error", "The filter no longer exists.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	data.Filters[n].Enabled = !data.Filters[n].Enabled
	if err := svc.MailFilterSave(r.Context(), address, data.Filters, data.Responder, "filter toggled"); err != nil {
		s.flashFilterFailure(sess, back, "The filter could not be changed.", err)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	state := "disabled"
	if data.Filters[n].Enabled {
		state = "enabled"
	}
	sess.Flash("success", "Filter “"+data.Filters[n].Name+"” "+state+".")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// mailFilterDelete removes a filter (MailFiltersController::delete).
func (s *Server) mailFilterDelete(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	d, mbox, address, ok := s.mailFiltersTarget(w, r, user)
	if !ok {
		return
	}
	back := filtersBack(d, mbox)
	svc := s.siteMailService(r, user)
	data, err := svc.MailFilterGet(r.Context(), address)
	if err != nil {
		s.flashFilterFailure(sess, back, "The filters could not be read.", err)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 0 || n >= len(data.Filters) {
		sess.Flash("error", "The filter no longer exists.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	name := data.Filters[n].Name
	data.Filters = append(data.Filters[:n], data.Filters[n+1:]...)
	if err := svc.MailFilterSave(r.Context(), address, data.Filters, data.Responder, "filter deleted"); err != nil {
		s.flashFilterFailure(sess, back, "The filter could not be deleted.", err)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Filter “"+name+"” deleted.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// mailResponder saves the autoresponder (MailFiltersController::responder).
func (s *Server) mailResponder(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	d, mbox, address, ok := s.mailFiltersTarget(w, r, user)
	if !ok {
		return
	}
	back := filtersBack(d, mbox)
	svc := s.siteMailService(r, user)
	data, err := svc.MailFilterGet(r.Context(), address)
	if err != nil {
		s.flashFilterFailure(sess, back, "The filters could not be read.", err)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	resp := hosting.ResponderFromForm(postValues(r))
	if err := svc.MailFilterSave(r.Context(), address, data.Filters, resp, "autoresponder"); err != nil {
		s.flashFilterFailure(sess, back, "The autoresponder could not be saved.", err)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	state := "off"
	if resp.Enabled {
		state = "on"
	}
	sess.Flash("success", "Autoresponder "+state+".")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// flashFilterFailure maps the filters-side errors the way PHP's change():
// user-facing ones get their message, everything else a fallback (logged).
func (s *Server) flashFilterFailure(sess *session.Session, back, fallback string, err error) {
	var ue *hosting.UserError
	if errors.As(err, &ue) {
		sess.Flash("error", userFacingMsg(ue))
		return
	}
	s.log.Error("mail filters", "back", back, "error", err)
	sess.Flash("error", fallback)
}

// userFacingMsgFrom is userFacingMsg for a non-nil error (the agent's
// message when it is a user-facing agent failure, else a generic one).
func userFacingMsgFrom(err error) string {
	var ue *hosting.UserError
	if errors.As(err, &ue) {
		return userFacingMsg(ue)
	}
	return "The mail server could not be reached."
}

// fieldErrorList is a ValidationError's messages in the PHP's field order
// (name, then the rest by key), for the joined flash message.
func fieldErrorList(ve *hosting.ValidationError) []string {
	out := []string{}
	for _, key := range []string{"name", "rules", "actions"} {
		if m, ok := ve.Fields[key]; ok {
			out = append(out, m)
		}
	}
	for key, m := range ve.Fields {
		if key != "name" && key != "rules" && key != "actions" {
			out = append(out, m)
		}
	}
	return out
}
