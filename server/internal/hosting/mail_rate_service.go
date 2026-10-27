// This file ports the agent calls of MailRateService: read the state,
// save the settings (re-applying the PHP websites when the limits turn on
// for the first time) and let a blocked account send again.
package hosting

import (
	"context"
	"database/sql"
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

// RateAlerts notifies the accounts that reached their outgoing mail limit in
// the last day (MailRateService::alerts): administrators, and the owner of a
// website using the account. notify stores one notification and reports
// whether it was new. One line per new alert.
func RateAlerts(ctx context.Context, ag AgentCaller, sites *WebsiteRepository, serverID int64, now time.Time,
	notify func(ctx context.Context, key string, websiteID, ownerID sql.NullInt64, title, body, link string) bool) ([]string, error) {
	st, err := RateRead(ctx, ag)
	if err != nil {
		if strings.Contains(err.Error(), "unknown_action") {
			return nil, nil
		}
		return nil, err
	}
	if !st.Config.Enabled || st.Usage == nil {
		return nil, nil
	}
	var out []string
	for _, ev := range st.Usage.Events {
		if ev.Time < now.Unix()-86400 {
			continue
		}
		who := ev.Name
		if who == "" {
			who = fmt.Sprintf("UID %d", ev.UID)
		}
		title := "Mail sending limit reached: " + who
		body := fmt.Sprintf("The website account %s sent mail to %d recipients within the hour; more mail from it is deferred until the next hour (or until an administrator lets it send again). A sudden increase can mean a compromised website sending spam.", who, ev.Limit)
		var used []SiteOwner
		if ev.Name != "" {
			used, _ = sites.UsingSystemUser(ctx, serverID, ev.Name)
		}
		key := fmt.Sprintf("mailrate:%d:%d:%d", serverID, ev.UID, ev.Time)
		if !notify(ctx, key, sql.NullInt64{}, sql.NullInt64{}, title, body, "/mail/admin/sending-limits") {
			continue
		}
		out = append(out, title)
		// The website's owner hears of it too.
		for _, s := range used {
			if s.OwnerID != nil {
				notify(ctx, fmt.Sprintf("%s:owner:%d", key, *s.OwnerID),
					sql.NullInt64{Int64: s.ID, Valid: true}, sql.NullInt64{Int64: *s.OwnerID, Valid: true}, "Your website reached its mail sending limit",
					fmt.Sprintf("Mail sent from your website to more than %d recipients within an hour is deferred until the next hour. If you did not send this mail, your website may have been compromised: contact your hosting provider.", ev.Limit), "")
				break
			}
		}
	}
	return out, nil
}
