// This file ports the agent calls of MailGreylistController: read the
// state, apply a change (read the current configuration, transform the
// mail.greylist_set parameters, send the whole result), update the
// provider ranges, read the report and clean up records.
package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
)

// GreylistAudit records through the shared audit hook (action, result,
// details).
type GreylistAudit func(action, result string, details map[string]any)

// GreylistRead calls mail.greylist_get.
func GreylistRead(ctx context.Context, ag AgentCaller) (*GreylistState, error) {
	raw, err := ag.Call(ctx, "mail.greylist_get", nil, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var st GreylistState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("greylisting state: %w", err)
	}
	return &st, nil
}

// GreylistChange applies a change to the current configuration
// (MailGreylistController::change): the current configuration is read, the
// change is applied to the mail.greylist_set parameters and the whole
// result is sent. A user-facing error from the change itself or the agent
// is audited as a failure and returned for the flash; the fallback message
// is used for anything else.
func GreylistChange(ctx context.Context, ag AgentCaller, audit GreylistAudit, action string, details map[string]any, change func(map[string]any) (map[string]any, error), fallback string) (string, error) {
	fail := func(msg string) (string, error) {
		d := map[string]any{}
		for k, v := range details {
			d[k] = v
		}
		d["error"] = msg
		audit(action, "failure", d)
		return "", errors.New(msg)
	}
	st, err := GreylistRead(ctx, ag)
	if err != nil {
		return fail(fallback)
	}
	params, err := change(GreylistParams(st.Config))
	if err != nil {
		msg := fallback
		var ue *UserError
		if errors.As(err, &ue) && ue.What != "" {
			msg = ue.What
		}
		return fail(msg)
	}
	if _, err := ag.Call(ctx, "mail.greylist_set", params, 180*time.Second); err != nil {
		return fail(agentMessageOr(err, fallback))
	}
	audit(action, "success", details)
	return "", nil
}

// agentMessageOr is the agent's user-facing message, or the fallback.
func agentMessageOr(err error, fallback string) string {
	ae := &agent.Error{}
	if errors.As(err, &ae) && ae.Message != "" {
		return ae.Message
	}
	return fallback
}

// GreylistUpdateProviders refreshes the provider address ranges from their
// SPF records (MailGreylistController::updateProviders).
func GreylistUpdateProviders(ctx context.Context, ag AgentCaller, audit GreylistAudit, details map[string]any) (string, error) {
	if _, err := ag.Call(ctx, "mail.greylist_update_providers", map[string]any{"all": true}, 300*time.Second); err != nil {
		audit("mail.greylist_update_providers", "failure", details)
		return agentMessageOr(err, "The provider address ranges could not be updated."), err
	}
	audit("mail.greylist_update_providers", "success", details)
	return "", nil
}

// GreylistCleanup removes greylisting records (MailGreylistController::cleanup):
// op is purge, delete or delete_all; triplets (at most 1000) for delete.
// It returns the number of removed records.
func GreylistCleanup(ctx context.Context, ag AgentCaller, op string, triplets []GreylistTriplet) (int, string, error) {
	if len(triplets) > 1000 {
		triplets = triplets[:1000]
	}
	raw, err := ag.Call(ctx, "mail.greylist_cleanup", map[string]any{
		"op": op, "triplets": triplets,
	}, 60*time.Second)
	if err != nil {
		return 0, agentMessageOr(err, "The greylisting records could not be removed."), err
	}
	var res struct {
		Removed int `json:"removed"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, "The greylisting report came back unreadable.", err
	}
	return res.Removed, "", nil
}
