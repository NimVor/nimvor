// Package mailmgr hosts email for domains: Postfix receives and sends,
// Dovecot stores mailboxes (IMAP) and OpenDKIM signs outgoing mail.
//
// WhaCP keeps its data in one JSON file and renders from it the lookup
// tables Postfix reads (texthash, no postmap needed), Dovecot's user list
// and OpenDKIM's key and signing tables, all in its own folder. The
// distribution's packages are configured once, after confirmation:
// Postfix with postconf, Dovecot with a drop-in file (plus disabling its
// system-account login), OpenDKIM from WhaCP's own unit and configuration.
package mailmgr

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Data is WhaCP's mail configuration.
type Data struct {
	Hostname string `json:"hostname"`
	// Dovecot24 selects Dovecot 2.4's user-file fields.
	Dovecot24 bool                `json:"dovecot24,omitempty"`
	Domains   map[string]Domain   `json:"domains"`
	Mailboxes map[string]Mailbox  `json:"mailboxes"`
	Aliases   map[string][]string `json:"aliases"`
	// Suspended domains: their mailboxes cannot log in (IMAP, POP3, SMTP
	// submission, webmail); mail is still delivered.
	Suspended []string `json:"suspended,omitempty"`
	Spam      Spam     `json:"spam"`
}

// Spam holds the spam filter (Rspamd) settings.
type Spam struct {
	Enabled bool `json:"enabled"`
	// Scores at which a message is marked (filed into Junk) or refused.
	AddHeader float64 `json:"add_header"`
	Reject    float64 `json:"reject"`
	// Domains whose accounts turned the filter off (not scanned) or the
	// Spam Box off (marked mail stays in the inbox).
	NoFilterDomains []string `json:"no_filter_domains,omitempty"`
	NoJunkDomains   []string `json:"no_junk_domains,omitempty"`
}

// Domain is a hosted mail domain.
type Domain struct {
	Selector string `json:"dkim_selector"`
	// DKIMPublic is the TXT value to publish at <selector>._domainkey.
	DKIMPublic string `json:"dkim_public"`
}

// Mailbox is one mailbox (never returned with its hash).
type Mailbox struct {
	Hash    string `json:"hash"`
	QuotaMB int64  `json:"quota_mb"`
}

func newData() Data {
	return Data{Domains: map[string]Domain{}, Mailboxes: map[string]Mailbox{}, Aliases: map[string][]string{}}
}

func parseData(b []byte) (Data, error) {
	d := newData()
	if len(b) == 0 {
		return d, nil
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return d, err
	}
	if d.Domains == nil {
		d.Domains = map[string]Domain{}
	}
	if d.Mailboxes == nil {
		d.Mailboxes = map[string]Mailbox{}
	}
	if d.Aliases == nil {
		d.Aliases = map[string][]string{}
	}
	return d, nil
}

var (
	localRe  = regexp.MustCompile(`^[a-z0-9]([a-z0-9._+-]{0,62}[a-z0-9])?$`)
	domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
)

// SplitAddress validates a hosted address (lower-case local@domain).
func SplitAddress(addr string) (local, domain string, err error) {
	addr = strings.ToLower(strings.TrimSpace(addr))
	local, domain, ok := strings.Cut(addr, "@")
	if !ok || !localRe.MatchString(local) || strings.Contains(local, "..") || !domainRe.MatchString(domain) || len(addr) > 254 {
		return "", "", fmt.Errorf("%q is not a valid address (letters, digits and . _ + - before the @)", addr)
	}
	return local, domain, nil
}

// ValidDomain checks a mail domain name.
func ValidDomain(d string) bool { return len(d) <= 253 && domainRe.MatchString(d) }

// validDestination checks an alias target (any domain).
func validDestination(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && a.Name == "" && len(s) <= 254 && !strings.ContainsAny(s, " ,:\t\r\n\"")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Rendered files, relative to the mail folder.
type files map[string]string

const header = "# Managed by WhaCP (email). Do not edit: changes are overwritten.\n"

// render produces every derived file.
func (d Data) render(dir, vmail string) files {
	var domains, mailboxes, aliases, senders, users, keyTable, signing strings.Builder
	for _, b := range []*strings.Builder{&domains, &mailboxes, &aliases, &senders, &users, &keyTable, &signing} {
		b.WriteString(header)
	}
	for _, dom := range sortedKeys(d.Domains) {
		fmt.Fprintf(&domains, "%s OK\n", dom)
		sel := d.Domains[dom].Selector
		fmt.Fprintf(&keyTable, "%s._domainkey.%s %s:%s:%s/dkim/%s.private\n", sel, dom, dom, sel, dir, dom)
		fmt.Fprintf(&signing, "*@%s %s._domainkey.%s\n", dom, sel, dom)
	}
	for _, addr := range sortedKeys(d.Mailboxes) {
		m := d.Mailboxes[addr]
		local, dom, _ := strings.Cut(addr, "@")
		fmt.Fprintf(&mailboxes, "%s %s/%s/\n", addr, dom, local)
		fmt.Fprintf(&senders, "%s %s\n", addr, addr)
		quota := ""
		if m.QuotaMB > 0 {
			quota = fmt.Sprintf("userdb_quota_rule=*:storage=%dM", m.QuotaMB)
			if d.Dovecot24 {
				quota = fmt.Sprintf("userdb_quota_storage_size=%dM", m.QuotaMB)
			}
		}
		hash := m.Hash
		if slices.Contains(d.Suspended, dom) {
			// No password matches a "!"-prefixed hash, so every login fails
			// (IMAP, POP3, webmail and SMTP AUTH through Dovecot); the userdb
			// entry stays, so mail is still delivered.
			hash = "!" + hash
			quota = strings.TrimSpace(quota + " nologin=y reason=Suspended")
		}
		// user:password:uid:gid:gecos:home:shell:extra
		fmt.Fprintf(&users, "%s:{SHA512-CRYPT}%s::::%s/%s/%s::%s\n", addr, hash, vmail, dom, local, quota)
	}
	for _, src := range sortedKeys(d.Aliases) {
		fmt.Fprintf(&aliases, "%s %s\n", src, strings.Join(d.Aliases[src], ","))
		// A mailbox may send as an alias that forwards to it.
		var owners []string
		for _, dst := range d.Aliases[src] {
			if _, ok := d.Mailboxes[dst]; ok {
				owners = append(owners, dst)
			}
		}
		if len(owners) > 0 && !strings.HasPrefix(src, "@") {
			fmt.Fprintf(&senders, "%s %s\n", src, strings.Join(owners, ","))
		}
	}
	return files{
		"domains": domains.String(), "mailboxes": mailboxes.String(), "aliases": aliases.String(),
		"senders": senders.String(), "users": users.String(),
		"dkim/KeyTable": keyTable.String(), "dkim/SigningTable": signing.String(),
	}
}

// newDKIM creates an RSA-2048 key: the private PEM and the TXT value.
func newDKIM() (privPEM []byte, txt string, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, "", err
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, "", err
	}
	priv := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return priv, "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(pub), nil
}
