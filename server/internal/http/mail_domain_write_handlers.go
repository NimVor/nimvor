package http

// This file ports the mail domain page's write side: the mailbox CRUD
// (create, change quota/password, delete), the forwarder CRUD, the mail
// routing, the DNS publish + public-DNS check and stopping hosting a domain
// (MailController::saveMailbox / deleteMailbox / saveAlias / deleteAlias /
// publishDns / dnsCheck / delete, MailFilterController::routing).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// mailDomainMailboxRow is one mailbox of the domain page (usage + quota,
// pre-rendered for the table; the edit form needs the raw values too).
type mailDomainMailboxRow struct {
	ID           int64
	Local        string
	Address      string
	Used         string
	Quota        string
	QuotaUnlimit bool
	QuotaMB      int
	Pct          int // -1 when the quota is 0
	Status       string
}

// mailDomainEdit is the re-render extras the write handlers add on a refused
// save (the page with the field messages, the submitted values and the modal
// open, as PHP's backWithErrors).
type mailDomainEdit struct {
	Errors      map[string]string
	Old         map[string]string
	MailboxOpen bool  // open the create-mailbox modal
	AliasOpen   bool  // open the add-forwarder modal
	Edit        int64 // open the inline edit form for this mailbox
}

// mailDomainOf loads the route's mail domain for the user; nil (and the
// "not found" redirect already sent) when it does not exist for them.
func (s *Server) mailDomainOf(w http.ResponseWriter, r *http.Request, user *identity.User) *hosting.MailDomainFull {
	d, err := hosting.NewMailRepository(s.db).Domain(r.Context(), s.currentServerID(r), pathIDOrZero(r), user)
	if err != nil {
		s.log.Error("load mail domain", "error", err)
		s.showNotFound(w)
		return nil
	}
	if d == nil {
		sess := sessionFrom(r.Context())
		sess.Flash("error", "Mail domain not found.")
		http.Redirect(w, r, "/mail", http.StatusSeeOther)
		return nil
	}
	return d
}

// mailDomainData builds the domain page's shared data (MailController::show's
// view variables).
func (s *Server) mailDomainData(ctx context.Context, r *http.Request, user *identity.User, d *hosting.MailDomainFull) map[string]any {
	sess := sessionFrom(ctx)
	svc := s.siteMailService(r, user)
	status, err := svc.Status(ctx)
	if err != nil {
		status = nil
	}
	host := ""
	sizes := map[string]int64{}
	webmailURL := ""
	if status != nil {
		host = status.Hostname
		for _, m := range status.Mailboxes {
			sizes[m.Address] = m.Bytes
		}
		if status.Webmail.Enabled && status.Webmail.Hostname != "" {
			webmailURL = "https://" + status.Webmail.Hostname + "/"
		}
	}
	repo := hosting.NewMailRepository(s.db)
	mailboxes, err := repo.MailboxList(ctx, d.ID)
	if err != nil {
		s.log.Error("list mailboxes", "domain", d.ID, "error", err)
	}
	aliases, err := repo.AliasList(ctx, d.ID)
	if err != nil {
		s.log.Error("list aliases", "domain", d.ID, "error", err)
	}
	records := svc.DNSRecords(d, host)
	var site *hosting.Website
	if d.WebsiteID.Valid {
		site, _ = hosting.NewWebsiteRepository(s.db).FindFull(ctx, s.currentServerID(r), d.WebsiteID.Int64, user)
	}
	rows := make([]mailDomainMailboxRow, 0, len(mailboxes))
	for _, m := range mailboxes {
		addr := m.Local + "@" + d.Domain
		used := sizes[addr]
		quota := int64(m.QuotaMB) * 1048576
		pct := -1
		st := "Active"
		if m.QuotaMB > 0 {
			pct = int(used * 100 / quota)
			if pct > 100 {
				pct = 100
			}
			if pct >= 100 {
				st = "Full"
			} else if pct >= 90 {
				st = "Almost full"
			}
		}
		rows = append(rows, mailDomainMailboxRow{
			ID: m.ID, Local: m.Local, Address: addr,
			Used: formatBytes(used), Quota: formatBytes(quota),
			QuotaUnlimit: m.QuotaMB == 0, QuotaMB: m.QuotaMB, Pct: pct, Status: st,
		})
	}
	var secret *secretFlash
	if raw, _ := sess.PullFlash("secret"); raw != "" {
		var sf secretFlash
		if json.Unmarshal([]byte(raw), &sf) == nil {
			secret = &sf
		}
	}
	return map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"Domain":     d,
		"Mailboxes":  rows,
		"Aliases":    aliases,
		"Records":    records,
		"Host":       host,
		"WebmailURL": webmailURL,
		"Site":       site,
		"Secret":     secret,
		"Success":    pullFlashKey(sess, "success"),
		"Error":      pullFlashKey(sess, "error"),
	}
}

// renderMailDomain draws the domain page, with the re-render extras when a
// save was refused.
func (s *Server) renderMailDomain(w http.ResponseWriter, r *http.Request, user *identity.User, d *hosting.MailDomainFull, edit mailDomainEdit) {
	data := s.mailDomainData(r.Context(), r, user, d)
	data["Errors"] = edit.Errors
	data["Old"] = edit.Old
	data["MailboxOpen"] = edit.MailboxOpen
	data["AliasOpen"] = edit.AliasOpen
	data["Edit"] = edit.Edit
	data["Admin"] = s.can(r, "server.manage")
	data["RoutingHelp"] = hosting.MailRoutingHelp(d.Routing)
	data["MailboxCount"] = len(data["Mailboxes"].([]mailDomainMailboxRow))
	s.render(w, "mail_domain", data)
}

// oldMailForm is the submitted values kept for the re-rendered form (the
// secrets and the token are dropped, as PHP's backWithErrors).
func oldMailForm(vals map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range vals {
		if k == "_token" || k == "password" || k == "secret_key" || k == "private_key" || k == "client_secret" {
			continue
		}
		out[k] = v
	}
	return out
}

// mailMailboxSave creates a mailbox (POST /mail/domains/{id}/mailboxes) or
// changes one (POST /mail/domains/{id}/mailboxes/{mailbox}) —
// MailController::saveMailbox / updateMailbox.
func (s *Server) mailMailboxSave(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	d := s.mailDomainOf(w, r, user)
	if d == nil {
		return
	}
	var mboxID *int64
	editID := int64(0)
	if m := r.PathValue("mailbox"); m != "" {
		if id, err := strconv.ParseInt(m, 10, 64); err == nil {
			mboxID = &id
			editID = id
		}
	}
	vals := postValues(r)
	generated, err := s.siteMailService(r, user).SaveMailbox(r.Context(), d, mboxID, vals)
	if err != nil {
		var ve *hosting.ValidationError
		var ue *hosting.UserError
		switch {
		case errors.As(err, &ve):
			s.renderMailDomain(w, r, user, d, mailDomainEdit{
				Errors: ve.Fields, Old: oldMailForm(vals), MailboxOpen: mboxID == nil, Edit: editID,
			})
			return
		case errors.As(err, &ue):
			sess.Flash("error", userFacingMsg(ue))
		default:
			s.log.Error("save mailbox", "domain", d.ID, "error", err)
			sess.Flash("error", "The mailbox could not be saved.")
		}
		http.Redirect(w, r, fmt.Sprintf("/mail/domains/%d", d.ID), http.StatusSeeOther)
		return
	}
	if generated != "" {
		if mboxID != nil {
			s.flashSecret(sess, "Mailbox password", "the mailbox", generated)
		} else {
			s.flashSecret(sess, "Mailbox password", strings.ToLower(strings.TrimSpace(vals["local"]))+"@"+d.Domain, generated)
		}
	}
	if mboxID == nil {
		sess.Flash("success", "Mailbox "+strings.ToLower(strings.TrimSpace(vals["local"]))+"@"+d.Domain+" created.")
	} else {
		sess.Flash("success", "Mailbox saved.")
	}
	http.Redirect(w, r, fmt.Sprintf("/mail/domains/%d", d.ID), http.StatusSeeOther)
}

// mailMailboxDelete removes a mailbox after its address was typed
// (MailController::deleteMailbox).
func (s *Server) mailMailboxDelete(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	d := s.mailDomainOf(w, r, user)
	if d == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("mailbox"), 10, 64)
	if err != nil {
		s.showNotFound(w)
		return
	}
	if err := s.siteMailService(r, user).DeleteMailbox(r.Context(), d, id, r.PostFormValue("confirm")); err != nil {
		var ve *hosting.ValidationError
		var ue *hosting.UserError
		switch {
		case errors.As(err, &ve):
			sess.Flash("error", firstValidationError(ve))
		case errors.As(err, &ue):
			sess.Flash("error", userFacingMsg(ue))
		default:
			s.log.Error("delete mailbox", "domain", d.ID, "mailbox", id, "error", err)
			sess.Flash("error", "The mailbox could not be deleted.")
		}
		http.Redirect(w, r, fmt.Sprintf("/mail/domains/%d", d.ID), http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Mailbox deleted. Its mail was moved to the server's trash folder.")
	http.Redirect(w, r, fmt.Sprintf("/mail/domains/%d", d.ID), http.StatusSeeOther)
}

// mailAliasSave creates or re-points a forwarder (MailController::saveAlias).
func (s *Server) mailAliasSave(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	d := s.mailDomainOf(w, r, user)
	if d == nil {
		return
	}
	vals := postValues(r)
	if err := s.siteMailService(r, user).SaveAlias(r.Context(), d, vals); err != nil {
		var ve *hosting.ValidationError
		var ue *hosting.UserError
		switch {
		case errors.As(err, &ve):
			s.renderMailDomain(w, r, user, d, mailDomainEdit{
				Errors: ve.Fields, Old: oldMailForm(vals), AliasOpen: true,
			})
			return
		case errors.As(err, &ue):
			sess.Flash("error", userFacingMsg(ue))
		default:
			s.log.Error("save alias", "domain", d.ID, "error", err)
			sess.Flash("error", "The forwarder could not be saved.")
		}
		http.Redirect(w, r, fmt.Sprintf("/mail/domains/%d", d.ID), http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Alias saved.")
	http.Redirect(w, r, fmt.Sprintf("/mail/domains/%d", d.ID), http.StatusSeeOther)
}

// mailAliasDelete removes a forwarder (MailController::deleteAlias).
func (s *Server) mailAliasDelete(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	d := s.mailDomainOf(w, r, user)
	if d == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("alias"), 10, 64)
	if err != nil {
		s.showNotFound(w)
		return
	}
	if err := s.siteMailService(r, user).DeleteAlias(r.Context(), d, id); err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			sess.Flash("error", userFacingMsg(ue))
		} else {
			s.log.Error("delete alias", "domain", d.ID, "alias", id, "error", err)
			sess.Flash("error", "The forwarder could not be deleted.")
		}
		http.Redirect(w, r, fmt.Sprintf("/mail/domains/%d", d.ID), http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Alias deleted.")
	http.Redirect(w, r, fmt.Sprintf("/mail/domains/%d", d.ID), http.StatusSeeOther)
}

// mailRouting changes the domain's mail routing (MailFilterController::routing).
func (s *Server) mailRouting(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	d := s.mailDomainOf(w, r, user)
	if d == nil {
		return
	}
	routing := r.PostFormValue("routing")
	if err := s.siteMailService(r, user).SetRouting(r.Context(), d, routing); err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			sess.Flash("error", userFacingMsg(ue))
		} else {
			s.log.Error("set mail routing", "domain", d.ID, "error", err)
			sess.Flash("error", "The mail routing could not be changed.")
		}
		http.Redirect(w, r, fmt.Sprintf("/mail/domains/%d#routing", d.ID), http.StatusSeeOther)
		return
	}
	label := "local"
	for _, o := range hosting.MailRoutingOptions {
		if o.Value == routing {
			label = o.Label
			break
		}
	}
	sess.Flash("success", "Mail routing: "+label+".")
	http.Redirect(w, r, fmt.Sprintf("/mail/domains/%d#routing", d.ID), http.StatusSeeOther)
}

// mailPublishDns adds the mail records to the domain's DNS zone on this panel
// (MailController::publishDns).
func (s *Server) mailPublishDns(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	d := s.mailDomainOf(w, r, user)
	if d == nil {
		return
	}
	svc := s.siteMailService(r, user)
	status, err := svc.Status(r.Context())
	host := ""
	if err == nil && status != nil {
		host = status.Hostname
	}
	added, err := svc.PublishDNS(r.Context(), s.currentServerID(r), d, host, user)
	if err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			sess.Flash("error", userFacingMsg(ue))
		} else {
			s.log.Error("publish mail dns", "domain", d.ID, "error", err)
			sess.Flash("error", "The DNS zone could not be updated.")
		}
		http.Redirect(w, r, fmt.Sprintf("/mail/domains/%d", d.ID), http.StatusSeeOther)
		return
	}
	if added > 0 {
		sess.Flash("success", fmt.Sprintf("%d record(s) added to the DNS zone.", added))
	} else {
		sess.Flash("success", "The DNS zone already has these records.")
	}
	http.Redirect(w, r, fmt.Sprintf("/mail/domains/%d", d.ID), http.StatusSeeOther)
}

// mailDnsCheck is the domain page's public-DNS checker (MailController::
// dnsCheck, JSON for the whacp.js button).
func (s *Server) mailDnsCheck(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	d, err := hosting.NewMailRepository(s.db).Domain(r.Context(), s.currentServerID(r), pathIDOrZero(r), user)
	if err != nil || d == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Mail domain not found."}`))
		return
	}
	svc := s.siteMailService(r, user)
	status, err := svc.Status(r.Context())
	host := ""
	if err == nil && status != nil {
		host = status.Hostname
	}
	ip := ""
	if ov := s.siteOverview(r, user); ov != nil {
		if v, _ := ov.ServerIP(r.Context()); v != "" {
			ip = v
		}
	}
	res, err := svc.DnsCheck(r.Context(), d, host, ip)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"The check could not be run."}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if b, err := json.Marshal(res); err == nil {
		_, _ = w.Write(b)
	}
}

// mailDomainDelete stops hosting e-mail for the domain after its name was
// typed (MailController::delete).
func (s *Server) mailDomainDelete(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	d := s.mailDomainOf(w, r, user)
	if d == nil {
		return
	}
	if err := s.siteMailService(r, user).RemoveDomain(r.Context(), d, r.PostFormValue("confirm")); err != nil {
		var ve *hosting.ValidationError
		var ue *hosting.UserError
		switch {
		case errors.As(err, &ve):
			sess.Flash("error", firstValidationError(ve))
		case errors.As(err, &ue):
			sess.Flash("error", userFacingMsg(ue))
		default:
			s.log.Error("remove mail domain", "domain", d.ID, "error", err)
			sess.Flash("error", "Email could not be stopped for the domain.")
		}
		http.Redirect(w, r, fmt.Sprintf("/mail/domains/%d", d.ID), http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Email is no longer hosted for "+d.Domain+".")
	http.Redirect(w, r, "/mail", http.StatusSeeOther)
}
