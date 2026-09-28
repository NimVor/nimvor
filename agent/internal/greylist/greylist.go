// Package greylist is WhaCP's triplet greylisting: a Postfix policy
// service that defers the first message of each (sender IP, from, to)
// triplet and accepts a retry after the initial deferral period.
//
// It runs as its own unprivileged, socket-activated systemd service
// ("whacp-agent greylist-policy", DynamicUser) and never as the agent.
// The agent writes its configuration and reads reports over a control
// socket that only root and the service user can open.
package greylist

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
)

// Config is the greylisting configuration (written by the agent).
type Config struct {
	Enabled bool `json:"enabled"`
	// Minutes.
	Deferral int `json:"deferral"` // initial deferral period
	Resend   int `json:"resend"`   // resend acceptance period after the deferral
	Expire   int `json:"expire"`   // how long an accepted triplet is remembered
	// Senders whose SPF record passes for the client address are not deferred.
	SPFBypass bool `json:"spf_bypass"`
	// New providers in WhaCP's list are trusted when they first appear.
	AutoTrustNew bool                      `json:"auto_trust_new"`
	Trusted      []TrustedHost             `json:"trusted"`
	Providers    map[string]*ProviderState `json:"providers"`
}

// TrustedHost is an address or range that is never deferred.
type TrustedHost struct {
	Range   string `json:"range"` // address or CIDR
	Comment string `json:"comment"`
}

// ProviderState is the administrator's choice for a known provider and
// its address ranges (from its SPF record).
type ProviderState struct {
	Trust      bool     `json:"trust"`
	AutoUpdate bool     `json:"auto_update"`
	Ranges     []string `json:"ranges,omitempty"`
	Updated    int64    `json:"updated,omitempty"` // unix time of the last range update
	Checked    int64    `json:"checked,omitempty"` // unix time of the last attempt
	Error      string   `json:"error,omitempty"`   // last update error
}

// Provider is a known mail provider; its ranges come from these SPF records.
type Provider struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	SPF  []string `json:"spf"`
}

// Providers is WhaCP's list of common mail providers.
var Providers = []Provider{
	{"google", "Google (Gmail, Workspace)", []string{"_spf.google.com"}},
	{"microsoft", "Microsoft (Outlook.com, Microsoft 365)", []string{"spf.protection.outlook.com"}},
	{"yahoo", "Yahoo and AOL", []string{"_spf.mail.yahoo.com"}},
	{"apple", "Apple iCloud", []string{"icloud.com"}},
	{"amazonses", "Amazon SES", []string{"amazonses.com"}},
	{"zoho", "Zoho Mail", []string{"zoho.com"}},
	{"fastmail", "Fastmail", []string{"spf.messagingengine.com"}},
	{"proton", "Proton Mail", []string{"_spf.protonmail.ch"}},
	{"mailgun", "Mailgun", []string{"mailgun.org"}},
	{"sendgrid", "SendGrid", []string{"sendgrid.net"}},
	{"mandrill", "Mailchimp Transactional (Mandrill)", []string{"spf.mandrillapp.com"}},
	{"postmark", "Postmark", []string{"spf.mtasv.net"}},
	{"brevo", "Brevo (Sendinblue)", []string{"spf.sendinblue.com"}},
	{"gmx", "GMX, WEB.DE and mail.com", []string{"gmx.net"}},
	{"yandex", "Yandex Mail", []string{"_spf.yandex.net"}},
}

// Defaults for a new configuration (minutes).
const (
	DefaultDeferral = 5
	DefaultResend   = 240
	DefaultExpire   = 4320
	// MaxTriplets bounds memory and the state file.
	MaxTriplets = 200000
)

// Normalize fills defaults and merges the provider list: providers new to
// this configuration are trusted when AutoTrustNew is on.
func (c *Config) Normalize() {
	if c.Deferral <= 0 {
		c.Deferral = DefaultDeferral
	}
	if c.Resend <= 0 {
		c.Resend = DefaultResend
	}
	if c.Expire <= 0 {
		c.Expire = DefaultExpire
	}
	if c.Providers == nil {
		c.Providers = map[string]*ProviderState{}
	}
	for _, p := range Providers {
		if c.Providers[p.ID] == nil {
			c.Providers[p.ID] = &ProviderState{Trust: c.AutoTrustNew, AutoUpdate: true}
		}
	}
	if c.Trusted == nil {
		c.Trusted = []TrustedHost{}
	}
}

// Validate checks the values an administrator sets.
func (c *Config) Validate() error {
	switch {
	case c.Deferral < 1 || c.Deferral > 1440:
		return fmt.Errorf("initial deferral: 1 to 1440 minutes")
	case c.Resend < 1 || c.Resend > 10080:
		return fmt.Errorf("resend acceptance period: 1 to 10080 minutes")
	case c.Expire < 60 || c.Expire > 525600:
		return fmt.Errorf("record expiration: 60 to 525600 minutes")
	case len(c.Trusted) > 1000:
		return fmt.Errorf("at most 1000 trusted hosts")
	}
	seen := map[string]bool{}
	for i := range c.Trusted {
		t := &c.Trusted[i]
		p, err := ParseRange(t.Range)
		if err != nil {
			return fmt.Errorf("trusted host %q: an IP address or range such as 192.0.2.0/24", t.Range)
		}
		t.Range = p.String()
		if p.IsSingleIP() {
			t.Range = p.Addr().String()
		}
		if seen[t.Range] {
			return fmt.Errorf("trusted host %s is listed twice", t.Range)
		}
		seen[t.Range] = true
		t.Comment = strings.TrimSpace(t.Comment)
		if len(t.Comment) > 200 || strings.ContainsAny(t.Comment, "\x00\r\n") {
			return fmt.Errorf("trusted host %s: a comment of up to 200 characters on one line", t.Range)
		}
	}
	for id := range c.Providers {
		if !knownProvider(id) {
			delete(c.Providers, id)
		}
	}
	return nil
}

func knownProvider(id string) bool {
	for _, p := range Providers {
		if p.ID == id {
			return true
		}
	}
	return false
}

// ParseRange reads an address or CIDR range (masked).
func ParseRange(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		if p.Addr().Is4In6() {
			return netip.Prefix{}, fmt.Errorf("mapped address")
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("bad address")
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// Triplet is one greylisting record.
type Triplet struct {
	IP       string `json:"ip"`
	From     string `json:"from"`
	To       string `json:"to"`
	Deferred int    `json:"deferred"`
	Accepted int    `json:"accepted"`
	Created  int64  `json:"created"`
	// Retries are deferred until BlockExpire and accepted until MustRetry;
	// an accepted triplet is kept until RecordExpire (refreshed on use).
	BlockExpire  int64 `json:"block_expire"`
	MustRetry    int64 `json:"must_retry"`
	RecordExpire int64 `json:"record_expire"`
}

// Passed reports whether the triplet was accepted.
func (t *Triplet) Passed() bool { return t.Accepted > 0 }

func key(ip, from, to string) string { return ip + "\x00" + from + "\x00" + to }

// Store holds the triplets.
type Store struct {
	mu    sync.Mutex
	m     map[string]*Triplet
	dirty bool
}

// NewStore returns an empty store.
func NewStore() *Store { return &Store{m: map[string]*Triplet{}} }

// Decision is what the policy service answers.
type Decision struct {
	Defer   bool
	Wait    time.Duration // until a retry is accepted
	Reason  string        // why mail passed without greylisting
	Triplet *Triplet
}

// Check records a delivery attempt of the triplet and decides.
func (s *Store) Check(cfg *Config, ip, from, to string, now time.Time) Decision {
	from, to = strings.ToLower(from), strings.ToLower(to)
	if from == "" {
		from = "<>"
	}
	k := key(ip, from, to)
	n := now.Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.m[k]
	if t != nil && n > t.RecordExpire {
		t = nil // expired: start again
	}
	if t == nil {
		if len(s.m) >= MaxTriplets {
			s.evictLocked(n)
		}
		t = &Triplet{IP: ip, From: from, To: to, Created: n}
		t.BlockExpire = n + int64(cfg.Deferral)*60
		t.MustRetry = t.BlockExpire + int64(cfg.Resend)*60
		t.RecordExpire = t.MustRetry
		s.m[k] = t
	}
	s.dirty = true
	if !t.Passed() && n < t.BlockExpire {
		t.Deferred++
		c := *t
		return Decision{Defer: true, Wait: time.Duration(t.BlockExpire-n) * time.Second, Triplet: &c}
	}
	// A retry within the acceptance window, or a triplet seen before.
	t.Accepted++
	t.RecordExpire = n + int64(cfg.Expire)*60
	c := *t
	return Decision{Triplet: &c}
}

// evictLocked removes expired triplets, then the oldest ones, until there
// is room.
func (s *Store) evictLocked(now int64) {
	for k, t := range s.m {
		if now > t.RecordExpire {
			delete(s.m, k)
		}
	}
	if len(s.m) < MaxTriplets {
		return
	}
	all := make([]*Triplet, 0, len(s.m))
	for _, t := range s.m {
		all = append(all, t)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].RecordExpire < all[j].RecordExpire })
	for _, t := range all[:len(all)-MaxTriplets*9/10] {
		delete(s.m, key(t.IP, t.From, t.To))
	}
}

// Purge removes expired triplets and returns how many.
func (s *Store) Purge(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, t := range s.m {
		if now.Unix() > t.RecordExpire {
			delete(s.m, k)
			n++
		}
	}
	if n > 0 {
		s.dirty = true
	}
	return n
}

// Delete removes the given triplets (all of them when keys is empty and
// all is set).
func (s *Store) Delete(triplets []Triplet, all bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	if all {
		n = len(s.m)
		s.m = map[string]*Triplet{}
	} else {
		for _, t := range triplets {
			k := key(t.IP, t.From, t.To)
			if _, ok := s.m[k]; ok {
				delete(s.m, k)
				n++
			}
		}
	}
	if n > 0 {
		s.dirty = true
	}
	return n
}

// Snapshot copies all triplets.
func (s *Store) Snapshot() []Triplet {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Triplet, 0, len(s.m))
	for _, t := range s.m {
		out = append(out, *t)
	}
	return out
}

// Marshal returns the state to save, or nil when nothing changed.
func (s *Store) Marshal(force bool) []byte {
	s.mu.Lock()
	if !s.dirty && !force {
		s.mu.Unlock()
		return nil
	}
	s.dirty = false
	all := make([]*Triplet, 0, len(s.m))
	for _, t := range s.m {
		all = append(all, t)
	}
	b, _ := json.Marshal(all)
	s.mu.Unlock()
	return b
}

// Load reads saved state (expired triplets are dropped).
func (s *Store) Load(b []byte, now time.Time) error {
	var all []*Triplet
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range all {
		if t == nil || now.Unix() > t.RecordExpire || len(s.m) >= MaxTriplets {
			continue
		}
		s.m[key(t.IP, t.From, t.To)] = t
	}
	return nil
}

// Status describes a triplet at a moment: deferring (block not expired),
// waiting (retry accepted now), passed, or expired.
func Status(t Triplet, now int64) string {
	switch {
	case now > t.RecordExpire:
		return "expired"
	case t.Passed():
		return "passed"
	case now < t.BlockExpire:
		return "deferring"
	}
	return "waiting"
}

// ReportQuery selects triplets for the report.
type ReportQuery struct {
	Search string `json:"search"`
	Status string `json:"status"` // deferring, waiting, passed, expired or ""
	Sort   string `json:"sort"`   // a column name
	Desc   bool   `json:"desc"`
	Page   int    `json:"page"`
	Per    int    `json:"per"`
}

// ReportRow is a triplet with its status.
type ReportRow struct {
	Triplet
	Status string `json:"status"`
}

// Report filters, sorts and pages triplets.
func Report(all []Triplet, q ReportQuery, now time.Time) (rows []ReportRow, total int, counts map[string]int) {
	n := now.Unix()
	counts = map[string]int{"deferring": 0, "waiting": 0, "passed": 0, "expired": 0}
	search := strings.ToLower(strings.TrimSpace(q.Search))
	for _, t := range all {
		st := Status(t, n)
		counts[st]++
		if q.Status != "" && st != q.Status {
			continue
		}
		if search != "" && !strings.Contains(t.IP, search) && !strings.Contains(t.From, search) && !strings.Contains(t.To, search) {
			continue
		}
		rows = append(rows, ReportRow{Triplet: t, Status: st})
	}
	less := map[string]func(a, b *ReportRow) bool{
		"ip":            func(a, b *ReportRow) bool { return a.IP < b.IP },
		"from":          func(a, b *ReportRow) bool { return a.From < b.From },
		"to":            func(a, b *ReportRow) bool { return a.To < b.To },
		"deferred":      func(a, b *ReportRow) bool { return a.Deferred < b.Deferred },
		"accepted":      func(a, b *ReportRow) bool { return a.Accepted < b.Accepted },
		"created":       func(a, b *ReportRow) bool { return a.Created < b.Created },
		"block_expire":  func(a, b *ReportRow) bool { return a.BlockExpire < b.BlockExpire },
		"must_retry":    func(a, b *ReportRow) bool { return a.MustRetry < b.MustRetry },
		"record_expire": func(a, b *ReportRow) bool { return a.RecordExpire < b.RecordExpire },
	}
	f, ok := less[q.Sort]
	if !ok {
		f, q.Desc = less["created"], true
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := &rows[i], &rows[j]
		if f(a, b) == f(b, a) {
			return key(a.IP, a.From, a.To) < key(b.IP, b.From, b.To)
		}
		if q.Desc {
			return f(b, a)
		}
		return f(a, b)
	})
	total = len(rows)
	if q.Per <= 0 || q.Per > 500 {
		q.Per = 50
	}
	if q.Page < 1 {
		q.Page = 1
	}
	start := (q.Page - 1) * q.Per
	if start >= total {
		return []ReportRow{}, total, counts
	}
	end := min(start+q.Per, total)
	return rows[start:end], total, counts
}
