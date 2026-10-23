// This file ports the greylisting data model (MailGreylistController and
// the agent's greylist package). The agent keeps the configuration; every
// change sends the whole of it.
package hosting

import (
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// GreylistConfig is the agent's greylisting configuration.
type GreylistConfig struct {
	Enabled      bool                              `json:"enabled"`
	Deferral     int                               `json:"deferral"` // minutes
	Resend       int                               `json:"resend"`   // minutes
	Expire       int                               `json:"expire"`   // minutes
	SPFBypass    bool                              `json:"spf_bypass"`
	AutoTrustNew bool                              `json:"auto_trust_new"`
	Trusted      []GreylistHost                    `json:"trusted"`
	Providers    map[string]*GreylistProviderState `json:"providers"`
}

// GreylistHost is an address or range that is never deferred.
type GreylistHost struct {
	Range   string `json:"range"`
	Comment string `json:"comment"`
}

// GreylistProviderState is the administrator's choice for a known provider
// and its address ranges (from the provider's SPF record).
type GreylistProviderState struct {
	Trust      bool     `json:"trust"`
	AutoUpdate bool     `json:"auto_update"`
	Ranges     []string `json:"ranges,omitempty"`
	Updated    int64    `json:"updated,omitempty"`
	Checked    int64    `json:"checked,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// GreylistProvider is a known mail provider.
type GreylistProvider struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	SPF  []string `json:"spf"`
}

// GreylistServiceState is the service state in the agent's responses.
type GreylistServiceState struct {
	Active  bool `json:"active"`
	Socket  bool `json:"socket"`
	Postfix bool `json:"postfix"`
}

// GreylistState is the data of mail.greylist_get (and of
// mail.greylist_set / mail.greylist_update_providers).
type GreylistState struct {
	Config     *GreylistConfig        `json:"config"`
	Providers  []GreylistProvider     `json:"providers"`
	Service    GreylistServiceState   `json:"service"`
	Configured bool                   `json:"configured"`
}

// GreylistTriplet is one greylisting record.
type GreylistTriplet struct {
	IP           string `json:"ip"`
	From         string `json:"from"`
	To           string `json:"to"`
	Deferred     int    `json:"deferred"`
	Accepted     int    `json:"accepted"`
	Created      int64  `json:"created"`
	BlockExpire  int64  `json:"block_expire"`
	MustRetry    int64  `json:"must_retry"`
	RecordExpire int64  `json:"record_expire"`
}

// GreylistReportRow is a triplet with its status.
type GreylistReportRow struct {
	GreylistTriplet
	Status string `json:"status"`
}

// GreylistReport is the data of mail.greylist_report.
type GreylistReport struct {
	Running bool                `json:"running"`
	Rows    []GreylistReportRow `json:"rows"`
	Total   int                 `json:"total"`
	Counts  map[string]int      `json:"counts"`
}

// GreylistStatus is one report status.
type GreylistStatus struct {
	Key   string
	Label string
}

// GreylistStatuses are the report's statuses, in the view's order
// (MailGreylistController::STATUSES).
var GreylistStatuses = []GreylistStatus{
	{Key: "deferring", Label: "Deferring"},
	{Key: "waiting", Label: "Retry accepted"},
	{Key: "passed", Label: "Passed"},
	{Key: "expired", Label: "Expired"},
}

// GreylistSorts are the report's sortable columns
// (MailGreylistController::SORTS).
var GreylistSorts = []string{"ip", "from", "to", "deferred", "accepted", "created", "block_expire", "must_retry", "record_expire"}

// Defaults for a new configuration (minutes).
const (
	GreylistDefaultDeferral = 5
	GreylistDefaultResend   = 240
	GreylistDefaultExpire   = 4320
)

// GreylistParams builds the mail.greylist_set request from the current
// configuration (MailGreylistController::params).
func GreylistParams(cfg *GreylistConfig) map[string]any {
	providers := map[string]map[string]bool{}
	trusted := []map[string]string{}
	if cfg != nil {
		for id, p := range cfg.Providers {
			if p == nil {
				providers[id] = map[string]bool{}
				continue
			}
			providers[id] = map[string]bool{"trust": p.Trust, "auto_update": p.AutoUpdate}
		}
		for _, t := range cfg.Trusted {
			trusted = append(trusted, map[string]string{"range": t.Range, "comment": t.Comment})
		}
	}
	return map[string]any{
		"enabled":        cfg != nil && cfg.Enabled,
		"deferral":       greylistParamInt(cfg, cfgDeferral, GreylistDefaultDeferral),
		"resend":         greylistParamInt(cfg, cfgResend, GreylistDefaultResend),
		"expire":         greylistParamInt(cfg, cfgExpire, GreylistDefaultExpire),
		"spf_bypass":     cfg != nil && cfg.SPFBypass,
		"auto_trust_new": cfg != nil && cfg.AutoTrustNew,
		"trusted":        trusted,
		"providers":      providers,
	}
}

func cfgDeferral(c *GreylistConfig) int {
	if c == nil {
		return 0
	}
	return c.Deferral
}

func cfgResend(c *GreylistConfig) int {
	if c == nil {
		return 0
	}
	return c.Resend
}

func cfgExpire(c *GreylistConfig) int {
	if c == nil {
		return 0
	}
	return c.Expire
}

func greylistParamInt(c *GreylistConfig, v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// GreylistValidateRange checks a trusted host's address or range (the agent
// re-parses it; this gives the form a friendlier message).
func GreylistValidateRange(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return &UserError{What: "Enter an IP address or range."}
	}
	if len(s) > 50 {
		return &UserError{What: "The address or range is too long."}
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		p, err := netip.ParsePrefix(s)
		if err != nil || p.Addr().Is4In6() {
			return &UserError{What: "Enter an IP address or range such as 192.0.2.0/24."}
		}
		return nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return &UserError{What: "Enter an IP address or range such as 192.0.2.0/24."}
	}
	return nil
}

// GreylistAgo renders the provider's last range update (the PHP view's
// $ago: minutes under an hour, hours under two days, days otherwise).
func GreylistAgo(updated int64, now time.Time) string {
	if updated <= 0 {
		return "never"
	}
	s := now.Unix() - updated
	if s < 0 {
		s = 0
	}
	switch {
	case s < 3600:
		return strconv.FormatInt(s/60, 10) + " min ago"
	case s < 172800:
		return strconv.FormatInt(s/3600, 10) + " h ago"
	default:
		return strconv.FormatInt(s/86400, 10) + " days ago"
	}
}

// GreylistCountsTotal is the status bar's "N record(s)" (the sum of the
// per-status counts, the PHP view's array_sum of the counts).
func (r *GreylistReport) GreylistCountsTotal() int {
	if r == nil {
		return 0
	}
	n := 0
	for _, v := range r.Counts {
		n += v
	}
	return n
}
