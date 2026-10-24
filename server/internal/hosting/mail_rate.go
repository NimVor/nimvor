// This file ports the outgoing mail limits data model (MailRateController
// and MailRateService). The agent's policy service counts the recipients
// per website account and hour; the panel shows this hour's use and the
// recent limit events.
package hosting

import (
	"sort"
	"strconv"
	"strings"
)

// RateMax is the largest limit in recipients per hour
// (MailRateService::MAX).
const RateMax = 1000000

// RateConfig is the outgoing mail limits as the agent reports them
// (mailRateSettings: the per-account limits by account name).
type RateConfig struct {
	Enabled  bool           `json:"enabled"`
	PerHour  int            `json:"per_hour"`
	Accounts map[string]int `json:"accounts"`
}

// RateAccountUsage is this hour's use of one website account.
type RateAccountUsage struct {
	UID     int    `json:"uid"`
	Name    string `json:"name"`
	Sent    int    `json:"sent"`
	Limit   int    `json:"limit"`
	Blocked bool   `json:"blocked"`
}

// RateEvent records an account reaching its limit.
type RateEvent struct {
	UID   int    `json:"uid"`
	Name  string `json:"name"`
	Time  int64  `json:"time"`
	Limit int    `json:"limit"`
}

// RateUsage is this hour's counts and the recent limit events.
type RateUsage struct {
	Hour     int64              `json:"hour"`
	Accounts []RateAccountUsage `json:"accounts"`
	Events   []RateEvent        `json:"events"`
}

// RateServiceState is the service state in the agent's responses.
type RateServiceState struct {
	Active  bool `json:"active"`
	Postfix bool `json:"postfix"`
}

// RateState is the data of mail.rate_get.
type RateState struct {
	Config     RateConfig       `json:"config"`
	Accounts   []string         `json:"accounts"`
	Service    RateServiceState `json:"service"`
	Usage      *RateUsage       `json:"usage"`
	Configured bool             `json:"configured"`
}

// RateValidate checks the administrator's values (MailRateService::validate)
// and returns the configuration to save.
//
// enabled is the raw "enabled" form value ("1" when the switch is on),
// perHour the "per_hour" input, accounts the "accounts[name]" inputs and
// known the server's website accounts.
func RateValidate(enabled, perHour string, accounts map[string]string, known []string) (map[string]any, string) {
	var errs []string
	per, err := strconv.Atoi(strings.TrimSpace(perHour))
	if err != nil || per < 0 || per > RateMax {
		errs = append(errs, "Enter a number between 0 (no limit) and "+strconv.Itoa(RateMax)+".")
		per = 0
	}
	out := map[string]int{}
	for _, name := range sortedKeys(accounts) {
		if strings.TrimSpace(accounts[name]) == "" {
			continue // the default applies
		}
		if !containsStr(known, name) {
			errs = append(errs, name+" is not a website account.")
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(accounts[name]))
		if err != nil || n < 0 || n > RateMax {
			errs = append(errs, "Enter a number between 0 (no limit) and "+strconv.Itoa(RateMax)+" for "+name+".")
			continue
		}
		out[name] = n
	}
	if len(errs) > 0 {
		return nil, strings.Join(errs, " ")
	}
	return map[string]any{
		"enabled":  enabled == "1",
		"per_hour": per,
		"accounts": out,
	}, ""
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
