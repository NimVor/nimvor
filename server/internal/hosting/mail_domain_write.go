package hosting

// This file ports the mail domain page's write side (MailController::
// saveMailbox / deleteMailbox / saveAlias / deleteAlias / publishDns /
// delete, MailFilterService::setRouting and the DNS checker): the mailbox
// and forwarder CRUD, the per-domain mail routing, adding the mail records
// to the panel's DNS zone and stopping hosting a domain.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// mailLocalRe is the local part of an address (MailService::LOCAL): letters
// and digits with . _ + -, starting and ending with a letter or digit, up to
// 63 characters.
var mailLocalRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._+-]{0,62}[a-z0-9])?$`)

// MailRoutingOptions is the domain's mail routing choices (MailFilterService::
// ROUTING), labels included.
var MailRoutingOptions = []struct {
	Value string
	Label string
}{
	{"local", "Local mail exchanger"},
	{"backup", "Backup mail exchanger"},
	{"remote", "Remote mail exchanger"},
}

// MailRoutingHelp is the routing card's explanation per choice.
func MailRoutingHelp(routing string) string {
	switch routing {
	case "local":
		return "This server receives the domain's mail into its mailboxes."
	case "backup":
		return "This server only queues the domain's mail for its primary mail server (a lower-priority MX)."
	case "remote":
		return "Another server receives the domain's mail; mail from this server to the domain goes there."
	}
	return ""
}

// SaveMailbox creates or changes a mailbox (MailService::saveMailbox). It
// returns the generated password when the form asked for one ("" otherwise).
// input carries local, password, generate and quota_mb.
func (s *MailService) SaveMailbox(ctx context.Context, d *MailDomainFull, mailboxID *int64, input map[string]string) (string, error) {
	var existing *Mailbox
	if mailboxID != nil {
		m, err := s.repo.Mailbox(ctx, d.ID, *mailboxID)
		if err != nil {
			return "", err
		}
		if m == nil {
			return "", &UserError{What: "The mailbox no longer exists."}
		}
		existing = m
	}
	local := ""
	if existing != nil {
		local = existing.Local
	} else {
		local = strings.ToLower(strings.TrimSpace(input["local"]))
	}
	if !mailLocalRe.MatchString(local) || strings.Contains(local, "..") {
		return "", &ValidationError{Fields: map[string]string{"local": "Use letters, digits and . _ + - (not at the start or end)."}}
	}
	password := input["password"]
	generated := ""
	if input["generate"] != "" {
		// The policy's generator always meets it; ':' is excluded because
		// the webmail password is a sign-in credential, not a key.
		p, err := s.policy.Generate(ctx, 20, ":")
		if err != nil {
			return "", err
		}
		password = p
		generated = p
	}
	if password != "" && (len(password) < 10 || len(password) > 128 || strings.Contains(password, ":")) {
		return "", &ValidationError{Fields: map[string]string{"password": "Use 10–128 characters (no colon), or generate one."}}
	}
	// Webmail sign-ins use this password: the server's password policy
	// applies to typed-in passwords (generated ones meet it by construction).
	if password != "" && generated == "" {
		if weak, err := s.policy.Check(ctx, password, ""); err != nil {
			return "", err
		} else if weak != "" {
			return "", &ValidationError{Fields: map[string]string{"password": weak}}
		}
	}
	if existing == nil && password == "" {
		return "", &ValidationError{Fields: map[string]string{"password": "Enter a password or generate one."}}
	}
	quota := 0
	if v := strings.TrimSpace(input["quota_mb"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			quota = n
		}
	}
	owner := nullToPtr(d.OwnerID)
	if max, err := s.limits.Value(ctx, owner, "max_email_quota_mb"); err != nil {
		return "", err
	} else if max > 0 {
		// The plan caps each mailbox; "unlimited" becomes the cap.
		if quota > max {
			return "", &ValidationError{Fields: map[string]string{"quota_mb": fmt.Sprintf("Your plan allows at most %d MB per mailbox.", max)}}
		}
		if quota == 0 {
			quota = max
		}
	}
	if existing == nil {
		if err := s.limits.AssertCanCreate(ctx, "email_accounts", owner, 1); err != nil {
			return "", err
		}
		n, err := s.repo.MailboxCount(ctx, d.ID)
		if err != nil {
			return "", err
		}
		if n >= MaxMailboxes {
			return "", &UserError{What: "This domain has the maximum number of mailboxes."}
		}
		taken, err := s.repo.LocalTaken(ctx, d.ID, local)
		if err != nil {
			return "", err
		}
		if taken {
			return "", &ValidationError{Fields: map[string]string{"local": "This address exists already."}}
		}
	}
	address := local + "@" + d.Domain
	params := map[string]any{"address": address, "quota_mb": quota}
	if password != "" {
		params["password"] = password
	}
	if _, err := s.agent.Call(ctx, "mail.mailbox_save", params, 60*time.Second); err != nil {
		return "", err
	}
	if existing == nil {
		if _, err := s.repo.InsertMailbox(ctx, d.ID, local, quota); err != nil {
			return "", err
		}
	} else if err := s.repo.UpdateMailboxQuota(ctx, existing.ID, quota); err != nil {
		return "", err
	}
	action := "mail.mailbox_create"
	if existing != nil {
		action = "mail.mailbox_update"
	}
	if s.auditlog != nil {
		s.auditlog(ctx, action, "mail_domain", fmt.Sprint(d.ID), "success",
			map[string]any{"address": address, "quota_mb": quota, "password_changed": password != ""})
	}
	return generated, nil
}

// DeleteMailbox removes a mailbox after the address was typed (MailService::
// deleteMailbox); a mailbox the agent does not know (404) is removed anyway.
func (s *MailService) DeleteMailbox(ctx context.Context, d *MailDomainFull, id int64, confirm string) error {
	m, err := s.repo.Mailbox(ctx, d.ID, id)
	if err != nil {
		return err
	}
	if m == nil {
		return &UserError{What: "The mailbox no longer exists."}
	}
	address := m.Local + "@" + d.Domain
	if strings.ToLower(strings.TrimSpace(confirm)) != address {
		return &ValidationError{Fields: map[string]string{"confirm": "Type the address to confirm."}}
	}
	_, err = s.agent.Call(ctx, "mail.mailbox_delete", map[string]any{"address": address, "confirm": address}, 60*time.Second)
	if err != nil {
		var ae *agent.Error
		if !errors.As(err, &ae) || ae.Status != 404 {
			return err
		}
	}
	if err := s.repo.DeleteMailboxRow(ctx, m.ID); err != nil {
		return err
	}
	if s.auditlog != nil {
		s.auditlog(ctx, "mail.mailbox_delete", "mail_domain", fmt.Sprint(d.ID), "success", map[string]any{"address": address})
	}
	return nil
}

// SaveAlias creates or re-points a forwarder (MailService::saveAlias). input
// carries alias_local ('*' = catch-all, "" = the domain's other addresses)
// and destinations (1–50 comma/space/semicolon separated addresses).
func (s *MailService) SaveAlias(ctx context.Context, d *MailDomainFull, input map[string]string) error {
	local := strings.ToLower(strings.TrimSpace(input["alias_local"]))
	if local == "*" {
		local = ""
	} else if !mailLocalRe.MatchString(local) || strings.Contains(local, "..") {
		return &ValidationError{Fields: map[string]string{"alias_local": "Use letters, digits and . _ + -, or * for all other addresses."}}
	}
	dests := []string{}
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(input["destinations"], func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) {
		a := strings.ToLower(strings.TrimSpace(part))
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		dests = append(dests, a)
	}
	if len(dests) == 0 || len(dests) > 50 {
		return &ValidationError{Fields: map[string]string{"destinations": "Enter 1–50 addresses."}}
	}
	for _, a := range dests {
		if !emailRe.MatchString(a) {
			return &ValidationError{Fields: map[string]string{"destinations": a + " is not an email address."}}
		}
	}
	if local != "" {
		// Re-pointing an existing alias is fine; a mailbox with the address
		// may not also be an alias.
		if mbTaken, err := s.mailboxHasLocal(ctx, d.ID, local); err != nil {
			return err
		} else if mbTaken {
			return &ValidationError{Fields: map[string]string{"alias_local": "A mailbox has this address."}}
		}
	}
	source := local + "@" + d.Domain
	if _, err := s.agent.Call(ctx, "mail.alias_save", map[string]any{"source": source, "destinations": dests}, 60*time.Second); err != nil {
		return err
	}
	if err := s.repo.UpsertAlias(ctx, d.ID, local, strings.Join(dests, ", ")); err != nil {
		return err
	}
	if s.auditlog != nil {
		s.auditlog(ctx, "mail.alias_save", "mail_domain", fmt.Sprint(d.ID), "success",
			map[string]any{"source": source, "destinations": dests})
	}
	return nil
}

// mailboxHasLocal reports whether a mailbox of the domain uses the local part.
func (s *MailService) mailboxHasLocal(ctx context.Context, domainID int64, local string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail_mailboxes WHERE domain_id = ? AND local = ?`, domainID, local).Scan(&n)
	return n > 0, err
}

// DeleteAlias removes a forwarder (MailService::deleteAlias); one the agent
// does not know (404) is removed anyway.
func (s *MailService) DeleteAlias(ctx context.Context, d *MailDomainFull, id int64) error {
	a, err := s.repo.Alias(ctx, d.ID, id)
	if err != nil {
		return err
	}
	if a == nil {
		return &UserError{What: "The alias no longer exists."}
	}
	source := a.Local + "@" + d.Domain
	_, err = s.agent.Call(ctx, "mail.alias_delete", map[string]any{"source": source})
	if err != nil {
		var ae *agent.Error
		if !errors.As(err, &ae) || ae.Status != 404 {
			return err
		}
	}
	if err := s.repo.DeleteAliasRow(ctx, a.ID); err != nil {
		return err
	}
	if s.auditlog != nil {
		s.auditlog(ctx, "mail.alias_delete", "mail_domain", fmt.Sprint(d.ID), "success", map[string]any{"source": source})
	}
	return nil
}

// SetRouting changes the domain's mail routing (MailFilterService::setRouting).
func (s *MailService) SetRouting(ctx context.Context, d *MailDomainFull, routing string) error {
	ok := false
	for _, r := range MailRoutingOptions {
		if r.Value == routing {
			ok = true
			break
		}
	}
	if !ok {
		return &UserError{What: "Choose local, backup or remote."}
	}
	if _, err := s.agent.Call(ctx, "mail.routing_set", map[string]any{"domain": d.Domain, "routing": routing}); err != nil {
		return err
	}
	if err := s.repo.UpdateDomainRouting(ctx, d.ID, routing); err != nil {
		return err
	}
	if s.auditlog != nil {
		s.auditlog(ctx, "mail.routing", "mail_domain", fmt.Sprint(d.ID), "success",
			map[string]any{"domain": d.Domain, "routing": routing})
	}
	return nil
}

// PublishDNS adds the domain's mail records (MX, SPF, DKIM, DMARC) to the
// panel's DNS zone for the domain (MailService::publishDns). Records already
// present — and a different SPF or DMARC record — are kept; it returns how
// many were added.
func (s *MailService) PublishDNS(ctx context.Context, serverID int64, d *MailDomainFull, mailHost string, u *identity.User) (int, error) {
	zones, err := s.dnsRepo.Zones(ctx, serverID, u)
	if err != nil {
		return 0, err
	}
	var zoneID int64
	for _, z := range zones {
		if z.Name == d.Domain {
			zoneID = z.ID
			break
		}
	}
	if zoneID == 0 {
		return 0, &UserError{
			What: "There is no DNS zone for " + d.Domain + " on this panel.",
			Fix:  "Create the zone under DNS, or add the records at your DNS provider.",
		}
	}
	zone, err := s.dnsRepo.Zone(ctx, serverID, zoneID, u)
	if err != nil {
		return 0, err
	}
	if zone == nil {
		return 0, &UserError{What: "There is no DNS zone for " + d.Domain + " on this panel.",
			Fix: "Create the zone under DNS, or add the records at your DNS provider."}
	}
	existing, err := s.dnsRepo.Records(ctx, zone.ID)
	if err != nil {
		return 0, err
	}
	added := 0
	for _, r := range s.DNSRecords(d, mailHost) {
		present := false
		for _, e := range existing {
			// A different SPF is the site's own policy (and a DMARC record
			// may already carry the domain's policy).
			sameSpf := r.Type == "TXT" && strings.HasPrefix(r.Value, "v=spf1") && strings.HasPrefix(e.Value, "v=spf1")
			if e.Name == r.Name && e.Type == r.Type &&
				(r.Type != "TXT" || sameSpf || e.Value == r.Value || strings.HasPrefix(e.Value, "v=DMARC1")) {
				present = true
				break
			}
		}
		if present {
			continue
		}
		in := map[string]string{"type": r.Type, "name": r.Name, "ttl": "0", "value": r.Value}
		if r.Type == "MX" {
			in["priority"] = strconv.Itoa(r.Priority)
		}
		if err := s.dns.SaveRecord(ctx, zone, nil, in, false); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}

// RemoveDomain stops hosting e-mail for a domain after the name was typed
// (MailService::removeDomain); its mailboxes and aliases must be gone first,
// and a domain the agent does not know (404) is removed anyway.
func (s *MailService) RemoveDomain(ctx context.Context, d *MailDomainFull, confirm string) error {
	if strings.ToLower(strings.TrimSpace(confirm)) != d.Domain {
		return &ValidationError{Fields: map[string]string{"confirm": "Type the domain to confirm."}}
	}
	left, err := s.repo.MailboxOrAliasCount(ctx, d.ID)
	if err != nil {
		return err
	}
	if left > 0 {
		return &UserError{What: "Delete the domain's mailboxes and aliases first."}
	}
	_, err = s.agent.Call(ctx, "mail.domain_remove", map[string]any{"domain": d.Domain, "confirm": d.Domain})
	if err != nil {
		var ae *agent.Error
		if !errors.As(err, &ae) || ae.Status != 404 {
			return err
		}
	}
	if err := s.repo.DeleteDomainRow(ctx, d.ID); err != nil {
		return err
	}
	if s.auditlog != nil {
		s.auditlog(ctx, "mail.domain_remove", "mail_domain", fmt.Sprint(d.ID), "success", map[string]any{"domain": d.Domain})
	}
	return nil
}

// DnsCheckResult is the domain page's public-DNS checker answer (the agent's
// mail.auth_check data).
type DnsCheckResult struct {
	Checks    []DnsCheck `json:"checks"`
	Simulated bool       `json:"simulated,omitempty"`
}

// DnsCheck is one public-DNS check (mail.auth_check's AuthCheck).
type DnsCheck struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Status   string `json:"status"`
	Found    string `json:"found,omitempty"`
	Expected string `json:"expected,omitempty"`
	Detail   string `json:"detail"`
}

// DnsCheck checks the domain's public DNS for e-mail (MailController::
// dnsCheck): the agent looks up MX, SPF, DKIM, DMARC, the host name and the
// reverse DNS of the given server IP.
func (s *MailService) DnsCheck(ctx context.Context, d *MailDomainFull, mailHost, ip string) (*DnsCheckResult, error) {
	params := map[string]any{
		"domain":        d.Domain,
		"hostname":      mailHost,
		"dkim_selector": d.DkimSelector,
		"dkim_txt":      d.DkimTxt,
		"routing":       d.Routing,
	}
	if ip != "" {
		params["ips"] = []string{ip}
	} else {
		params["ips"] = []string{}
	}
	res, err := s.agent.Call(ctx, "mail.auth_check", params, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var out DnsCheckResult
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
