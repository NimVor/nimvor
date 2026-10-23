// This file ports the agent calls of MailRateService: read the state,
// save the settings (re-applying the PHP websites when the limits turn on
// for the first time) and let a blocked account send again.
package hosting

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RateRead calls mail.rate_get.
func RateRead(ctx context.Context, ag AgentCaller) (*RateState, error) {
	raw, err := ag.Call(ctx, "mail.rate_get", nil, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var st RateState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("mail limits state: %w", err)
	}
	return &st, nil
}

// RateSave applies the settings (MailRateService::save). Turning the limits
// on for the first time re-applies every PHP website, so its PHP uses
// WhaCP's counting sendmail; when a website cannot be re-applied the limits
// stay off and the failed sites are named in a UserError.
func RateSave(ctx context.Context, ag AgentCaller, cfg map[string]any, wasEnabled bool, sites []Website, reapply func(ctx context.Context, site *Website) error) error {
	enabled, _ := cfg["enabled"].(bool)
	if enabled && !wasEnabled {
		failed := []string{}
		for _, site := range sites {
			if site.Kind != "php" {
				continue
			}
			if err := reapply(ctx, &site); err != nil {
				failed = append(failed, site.Domain+" ("+err.Error()+")")
			}
		}
		if len(failed) > 0 {
			return &UserError{
				What: "The limits were not turned on: these websites could not be updated to send mail through WhaCP: " + strings.Join(failed, "; ") + ". Fix them (Websites → the website → Save) and try again.",
			}
		}
	}
	accounts := map[string]int{}
	if a, ok := cfg["accounts"].(map[string]int); ok {
		accounts = a
	}
	perHour, _ := cfg["per_hour"].(int)
	_, err := ag.Call(ctx, "mail.rate_set", map[string]any{
		"enabled": enabled, "per_hour": perHour, "accounts": accounts,
	}, 180*time.Second)
	return err
}

// RateReset lets a website account (or all with an empty name) send again
// this hour (MailRateService::reset).
func RateReset(ctx context.Context, ag AgentCaller, account string) error {
	_, err := ag.Call(ctx, "mail.rate_reset", map[string]any{"account": account}, 60*time.Second)
	return err
}
