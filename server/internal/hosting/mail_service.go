package hosting

// This file ports the MailService halves the site pages and the domain page
// need: the agent's mail status, hosting a domain (agent DKIM key + the
// domains row + the spam-options sync) and the DNS records a hosted domain
// must publish. The mailbox/alias write routes ride with the mail batch.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// MailService hosts e-mail domains and reads the mail server's state.
type MailService struct {
	db       *sql.DB
	repo     *MailRepository
	agent    AgentCaller
	sites    *WebsiteRepository
	options  *AccountOptionStore
	auditlog Auditor
}

// NewMailService builds the service.
func NewMailService(db *sql.DB, repo *MailRepository, agent AgentCaller, sites *WebsiteRepository,
	options *AccountOptionStore, auditlog Auditor) *MailService {
	return &MailService{db: db, repo: repo, agent: agent, sites: sites, options: options, auditlog: auditlog}
}

// MailStatus is the agent's mail.status data.
type MailStatus struct {
	Installed  bool        `json:"installed"`
	Configured bool        `json:"configured"`
	Hostname   string      `json:"hostname"`
	Running    MailRunning `json:"running"`
	Spam       MailSpam    `json:"spam"`
	Webmail    MailWebmail `json:"webmail"`
	Mailboxes  []struct {
		Address string `json:"address"`
		QuotaMB int    `json:"quota_mb"`
		Bytes   int64  `json:"bytes"`
	} `json:"mailboxes"`
}

// MailRunning is the per-component running state of mail.status.
type MailRunning struct {
	Postfix  bool `json:"postfix"`
	Dovecot  bool `json:"dovecot"`
	OpenDKIM bool `json:"opendkim"`
}

// MailWebmail is the agent's webmail state.
type MailWebmail struct {
	Enabled  bool   `json:"enabled"`
	Hostname string `json:"hostname"`
}

// MailSpam is the server's configured spam settings (mail.status.spam).
type MailSpam struct {
	Enabled   bool
	AddHeader *float64
	Reject    *float64
}

// Status asks the agent for the mail server's state (nil when unreachable,
// as in the PHP's status()).
func (s *MailService) Status(ctx context.Context) (*MailStatus, error) {
	res, err := s.agent.Call(ctx, "mail.status", nil)
	if err != nil {
		return nil, err
	}
	var st MailStatus
	if err := json.Unmarshal(res, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// AddDomain hosts e-mail for a domain (MailService::addDomain): the agent
// creates the DKIM key, the panel stores the domain, and the account's spam
// options are re-applied to cover the new domain.
func (s *MailService) AddDomain(ctx context.Context, serverID int64, input map[string]string, u *identity.User) (int64, error) {
	var site *Website
	if v := input["website_id"]; v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			found, err := s.sites.Find(ctx, serverID, id, u)
			if err != nil && !errors.Is(err, ErrWebsiteNotFound) {
				return 0, err
			}
			if found == nil {
				return 0, &ValidationError{Fields: map[string]string{"website_id": "Choose one of your websites."}}
			}
			site = found
		}
	}
	domain := strings.ToLower(strings.TrimSpace(input["domain"]))
	if domain == "" && site != nil {
		domain = strings.ToLower(site.Domain)
	}
	if !IsAll(u) && (site == nil || strings.ToLower(site.Domain) != domain) {
		return 0, &ValidationError{Fields: map[string]string{"website_id": "Choose the website whose domain receives email."}}
	}
	if !dnsDomainRe.MatchString(domain) {
		return 0, &ValidationError{Fields: map[string]string{"domain": "Enter a domain name such as example.com."}}
	}
	// Email for a website's domain belongs to that website (and so to its
	// owner), whether or not the form named the website.
	if site == nil && IsAll(u) {
		if found, err := s.sites.FindByDomain(ctx, serverID, domain); err == nil {
			site = found
		}
	}
	if taken, err := s.repo.DomainTaken(ctx, serverID, domain); err != nil {
		return 0, err
	} else if taken {
		return 0, &ValidationError{Fields: map[string]string{"domain": "Email is already hosted for this domain."}}
	}
	res, err := s.agent.Call(ctx, "mail.domain_add", map[string]any{"domain": domain}, 60*time.Second)
	if err != nil {
		return 0, err
	}
	var out struct {
		DkimSelector string `json:"dkim_selector"`
		DkimTxt      string `json:"dkim_txt"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return 0, err
	}
	var websiteIDPtr, ownerIDPtr *int64
	if site != nil {
		websiteIDPtr = &site.ID
		if site.OwnerID.Valid {
			o := site.OwnerID.Int64
			ownerIDPtr = &o
		}
	} else if !IsAll(u) {
		id := u.ID
		ownerIDPtr = &id
	}
	id, err := s.repo.InsertDomain(ctx, serverID, domain, websiteIDPtr, ownerIDPtr, out.DkimSelector, out.DkimTxt)
	if err != nil {
		return 0, err
	}
	if ownerIDPtr != nil {
		s.SyncSpam(ctx, serverID)
	}
	if s.auditlog != nil {
		s.auditlog(ctx, "mail.domain_add", "mail_domain", fmt.Sprint(id), "success", map[string]any{"domain": domain})
	}
	return id, nil
}

// spamExceptions is the spam-filter off / spam-box off domain lists
// (MailService::spamExceptions).
func (s *MailService) spamExceptions(ctx context.Context, serverID int64) (noFilter, noJunk []string, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT domain, owner_id FROM mail_domains WHERE server_id = ? ORDER BY domain`, serverID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var domain string
		var owner sql.NullInt64
		if err := rows.Scan(&domain, &owner); err != nil {
			return nil, nil, err
		}
		opts, err := s.options.Get(ctx, nullToPtr(owner))
		if err != nil {
			return nil, nil, err
		}
		if !opts.SpamFilter {
			noFilter = append(noFilter, domain)
		}
		if !opts.SpamBox {
			noJunk = append(noJunk, domain)
		}
	}
	return noFilter, noJunk, rows.Err()
}

// SyncSpam applies the accounts' spam options to the server (MailService::
// syncSpam); best effort, false when it could not be applied.
func (s *MailService) SyncSpam(ctx context.Context, serverID int64) bool {
	st, err := s.Status(ctx)
	if err != nil || st == nil || !st.Configured {
		return true
	}
	noFilter, noJunk, err := s.spamExceptions(ctx, serverID)
	if err != nil {
		return false
	}
	if noFilter == nil {
		noFilter = []string{}
	}
	if noJunk == nil {
		noJunk = []string{}
	}
	addHeader, reject := 6.0, 15.0
	if st.Spam.AddHeader != nil {
		addHeader = *st.Spam.AddHeader
	}
	if st.Spam.Reject != nil {
		reject = *st.Spam.Reject
	}
	_, err = s.agent.Call(ctx, "mail.spam_configure", map[string]any{
		"enabled":           st.Spam.Enabled,
		"add_header":        addHeader,
		"reject":            reject,
		"no_filter_domains": noFilter,
		"no_junk_domains":   noJunk,
	}, 120*time.Second)
	return err == nil
}

// MailDNSRecord is one record a hosted domain must publish.
type MailDNSRecord struct {
	Name     string
	Type     string
	Value    string
	Priority int
	Note     string
}

// DNSRecords is the list the domain page shows (MailService::dnsRecords).
func (s *MailService) DNSRecords(d *MailDomainFull, mailHost string) []MailDNSRecord {
	var out []MailDNSRecord
	if mailHost != "" {
		out = append(out, MailDNSRecord{"@", "MX", mailHost + ".", 10, "Mail for the domain goes to this server."})
	}
	out = append(out, MailDNSRecord{"@", "TXT", "v=spf1 a mx ~all", 0, "SPF: this server may send for the domain."})
	if d.DkimSelector != "" {
		out = append(out, MailDNSRecord{d.DkimSelector + "._domainkey", "TXT", d.DkimTxt, 0, "DKIM: the public key for signatures."})
	}
	out = append(out, MailDNSRecord{"_dmarc", "TXT", "v=DMARC1; p=none; rua=mailto:postmaster@" + d.Domain, 0, "DMARC: start with p=none and tighten once mail flows."})
	return out
}

// nullToPtr turns a NULL id into a nil pointer (AccountOptionStore.Get takes
// *int64).
func nullToPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	id := n.Int64
	return &id
}
