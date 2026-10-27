package hosting

import (
	"context"
	"database/sql"
	"net"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// SiteProvisioner sets up DNS for a new website when this server hosts DNS
// (SiteProvisioner): a domain under a zone of the same account gets an A
// record in that zone; any other gets its own zone (A, www, SPF, DMARC and CAA
// following the account's options) and, when this server hosts email, the
// domain's email with its DKIM key and MX record. Nothing happens when the
// account uses its registrar's name servers. Failures are reported and never
// undo the website.
type SiteProvisioner struct {
	db       *sql.DB
	serverID int64
	dns      *DnsService
	dnsRepo  *DnsRepository
	mail     *MailService
	mailRepo *MailRepository
	opts     *AccountOptionStore
	cfg      *settings.Repository
	audit    Auditor
}

// NewSiteProvisioner builds the provisioner.
func NewSiteProvisioner(db *sql.DB, serverID int64, dns *DnsService, dnsRepo *DnsRepository, mail *MailService, mailRepo *MailRepository,
	opts *AccountOptionStore, cfg *settings.Repository, audit Auditor) *SiteProvisioner {
	return &SiteProvisioner{db: db, serverID: serverID, dns: dns, dnsRepo: dnsRepo, mail: mail, mailRepo: mailRepo, opts: opts, cfg: cfg, audit: audit}
}

// systemActor acts on the website's behalf (it is already access-checked).
var systemActor = &identity.User{ID: 0, Username: "system", RoleScope: "all"}

// Provision returns what was done, for the audit log.
func (p *SiteProvisioner) Provision(ctx context.Context, site *Website) []string {
	domain := strings.ToLower(site.Domain)
	var owner *int64
	if site.OwnerID.Valid {
		o := site.OwnerID.Int64
		owner = &o
	}
	opts, err := p.opts.Get(ctx, owner)
	if err != nil {
		return nil
	}
	if site.ParentID.Valid {
		var parent string
		_ = p.db.QueryRowContext(ctx, `SELECT domain FROM websites WHERE id = ?`, site.ParentID.Int64).Scan(&parent)
		if parent != "" && strings.HasSuffix(domain, "."+parent) {
			return nil // subdomains: the parent's zone, from the Subdomains page
		}
	}
	if opts.DNSRegistrarNS || !p.dnsReady(ctx) {
		return nil
	}
	var done []string
	fail := func(err error) []string {
		done = append(done, "DNS set-up failed: "+problem(err))
		p.report(ctx, site, done)
		return done
	}
	var zoneID int64
	var zoneOwner sql.NullInt64
	err = p.db.QueryRowContext(ctx, `SELECT id, owner_id FROM dns_zones WHERE server_id = ? AND name = ?`, p.serverID, domain).Scan(&zoneID, &zoneOwner)
	switch {
	case err == nil:
		if opts.DNSOverwrite && sameOwner(zoneOwner, owner) {
			zone, zerr := p.dnsRepo.Zone(ctx, p.serverID, zoneID, systemActor)
			if zerr != nil || zone == nil {
				return fail(zerr)
			}
			if derr := p.dns.Delete(ctx, zone, domain); derr != nil {
				return fail(derr)
			}
			done = append(done, "replaced the existing zone")
		} else {
			return nil // kept as it is (linked to the website when created)
		}
	case err != sql.ErrNoRows:
		return fail(err)
	}
	parentID, parentName, mine := p.parentZone(ctx, domain, owner)
	if parentID != 0 && !mine {
		return done // under another account's zone: never touched
	}
	if parentID != 0 {
		label := strings.TrimSuffix(domain, "."+parentName)
		if p.addA(ctx, parentID, label) {
			done = append(done, "A record "+label+" in "+parentName)
		}
		p.report(ctx, site, done)
		return done
	}
	in := map[string]string{"name": domain, "website_id": strconv.FormatInt(site.ID, 10), "template": "1", "spf": "0"}
	if opts.DNSSPF {
		in["spf"] = "1"
	}
	// DMARC needs SPF and DKIM.
	if opts.DNSDMARC && opts.DNSSPF && opts.DNSDKIM {
		in["dmarc"] = "1"
	}
	if _, err := p.dns.Create(ctx, p.serverID, in, systemActor); err != nil {
		return fail(err)
	}
	done = append(done, "DNS zone")
	// Mail received elsewhere (backup or remote exchanger): no mailboxes here.
	if opts.DNSDKIM && opts.MailRouting != "remote" && opts.MailRouting != "backup" {
		more, err := p.email(ctx, site)
		done = append(done, more...)
		if err != nil {
			done = append(done, "DNS set-up failed: "+problem(err))
		}
	}
	p.report(ctx, site, done)
	return done
}

func (p *SiteProvisioner) report(ctx context.Context, site *Website, done []string) {
	if len(done) == 0 || p.audit == nil {
		return
	}
	p.audit(ctx, "website.provision", "website", strconv.FormatInt(site.ID, 10), "success", map[string]any{"domain": site.Domain, "done": done})
}

func sameOwner(zone sql.NullInt64, owner *int64) bool {
	if !zone.Valid {
		return owner == nil
	}
	return owner != nil && *owner == zone.Int64
}

func (p *SiteProvisioner) dnsReady(ctx context.Context) bool {
	if ns, _ := p.dns.Nameservers(ctx); len(ns) == 0 {
		return false
	}
	st, err := p.dns.Status(ctx)
	return err == nil && st != nil && st.Configured && st.Running
}

// parentZone is the closest zone above the domain and whether it belongs to
// the same account (0 when there is none).
func (p *SiteProvisioner) parentZone(ctx context.Context, domain string, owner *int64) (int64, string, bool) {
	parts := strings.Split(domain, ".")
	for i := 1; i < len(parts)-1; i++ {
		name := strings.Join(parts[i:], ".")
		var id int64
		var o sql.NullInt64
		if err := p.db.QueryRowContext(ctx, `SELECT id, owner_id FROM dns_zones WHERE server_id = ? AND name = ?`, p.serverID, name).Scan(&id, &o); err == nil {
			return id, name, sameOwner(o, owner)
		}
	}
	return 0, "", false
}

// addA adds the website's A record to a parent zone unless the label has one.
func (p *SiteProvisioner) addA(ctx context.Context, zoneID int64, label string) bool {
	ip, _ := p.cfg.String(ctx, "dns_default_ipv4", "")
	ip = strings.TrimSpace(ip)
	if net.ParseIP(ip) == nil || strings.Count(ip, ".") != 3 {
		return false
	}
	var n int
	_ = p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dns_records WHERE zone_id = ? AND name = ?`, zoneID, label).Scan(&n)
	if n > 0 {
		return false
	}
	zone, err := p.dnsRepo.Zone(ctx, p.serverID, zoneID, systemActor)
	if err != nil || zone == nil {
		return false
	}
	err = p.dns.SaveRecord(ctx, zone, nil, map[string]string{"name": label, "type": "A", "value": ip, "ttl": "0"}, false)
	return err == nil
}

// email hosts the domain's email when this server runs a mail server: its DKIM
// key, and MX and DKIM records in the zone.
func (p *SiteProvisioner) email(ctx context.Context, site *Website) ([]string, error) {
	st, err := p.mail.Status(ctx)
	if err != nil || st == nil || !st.Configured {
		return nil, nil
	}
	if d, _ := p.mailRepo.DomainForWebsite(ctx, site.ID); d != nil {
		return nil, nil
	}
	if _, err := p.mail.AddDomain(ctx, p.serverID, map[string]string{"website_id": strconv.FormatInt(site.ID, 10)}, systemActor); err != nil {
		return nil, err
	}
	d, err := p.mailRepo.DomainForWebsite(ctx, site.ID)
	if err != nil || d == nil {
		return nil, err
	}
	if _, err := p.mail.PublishDNS(ctx, p.serverID, d, st.Hostname, systemActor); err != nil {
		return nil, err
	}
	return []string{"email with DKIM"}, nil
}
