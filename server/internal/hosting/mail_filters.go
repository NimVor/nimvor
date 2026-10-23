package hosting

// This file ports the per-mailbox e-mail filters and autoresponder
// (MailFilterService): the agent keeps the filters (rendered to Sieve), the
// panel validates the forms and stores the audit.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// MailFilterParts is the message parts a condition may match (MailFilterService::
// PARTS), in form order.
var MailFilterParts = []struct {
	Key   string
	Label string
}{
	{"from", "From"},
	{"to", "To"},
	{"cc", "Cc"},
	{"reply-to", "Reply-To"},
	{"subject", "Subject"},
	{"list-id", "List-Id"},
	{"body", "Body"},
}

// MailFilterMatches is the condition comparisons (MailFilterService::MATCHES).
var MailFilterMatches = []struct {
	Key   string
	Label string
}{
	{"contains", "contains"},
	{"not-contains", "does not contain"},
	{"is", "is"},
	{"not-is", "is not"},
	{"begins", "begins with"},
	{"not-begins", "does not begin with"},
	{"ends", "ends with"},
	{"not-ends", "does not end with"},
}

// MailFilterActions is what a matching filter may do (MailFilterService::
// ACTIONS).
var MailFilterActions = []struct {
	Key   string
	Label string
}{
	{"fileinto", "Move to folder"},
	{"redirect", "Forward to"},
	{"redirect_copy", "Send a copy to"},
	{"mark_read", "Mark as read"},
	{"discard", "Delete"},
	{"stop", "Stop processing more filters"},
}

// mailFilterLabel looks a part/match/action label up (the key itself when
// unknown, as the PHP's ?? fallback).
func mailFilterLabel(list []struct {
	Key   string
	Label string
}, key string) string {
	for _, o := range list {
		if o.Key == key {
			return o.Label
		}
	}
	return key
}

// MailFilterRule matches one part of a message (the agent's Rule).
type MailFilterRule struct {
	Part  string `json:"part"`
	Match string `json:"match"`
	Value string `json:"value"`
}

// MailFilterAction is what a matching filter does (the agent's FilterAction).
type MailFilterAction struct {
	Type  string `json:"type"`
	Value string `json:"value,omitempty"`
}

// MailFilter is one e-mail filter (the agent's Filter).
type MailFilter struct {
	Name    string             `json:"name"`
	Enabled bool               `json:"enabled"`
	Any     bool               `json:"any"`
	Rules   []MailFilterRule   `json:"rules"`
	Actions []MailFilterAction `json:"actions"`
}

// MailResponder is the autoresponder (the agent's Responder).
type MailResponder struct {
	Enabled bool   `json:"enabled"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Days    int    `json:"days"`
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
}

// MailFilterData is the agent's mail.filters_get data.
type MailFilterData struct {
	Filters   []MailFilter  `json:"filters"`
	Responder MailResponder `json:"responder"`
}

// MailFilterMailbox returns one mailbox of the domain with its address
// (MailFilterService::mailbox); "The mailbox no longer exists." when it is
// not there.
func (s *MailService) MailFilterMailbox(ctx context.Context, d *MailDomainFull, id int64) (*Mailbox, string, error) {
	m, err := s.repo.Mailbox(ctx, d.ID, id)
	if err != nil {
		return nil, "", err
	}
	if m == nil {
		return nil, "", &UserError{What: "The mailbox no longer exists."}
	}
	return m, m.Local + "@" + d.Domain, nil
}

// MailFilterGet reads the mailbox's filters and autoresponder from the agent
// (MailFilterService::get).
func (s *MailService) MailFilterGet(ctx context.Context, address string) (*MailFilterData, error) {
	res, err := s.agent.Call(ctx, "mail.filters_get", map[string]any{"address": address})
	if err != nil {
		return nil, err
	}
	var out MailFilterData
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	if out.Filters == nil {
		out.Filters = []MailFilter{}
	}
	if out.Responder.Days == 0 {
		out.Responder.Days = 1
	}
	return &out, nil
}

// MailFilterFromForm is MailFilterService::fromForm: name, any, rules[i]
// (part|match|value) and actions[i] (type|value) from the flat form (the
// rules[i][part]-shaped keys); empty rows are ignored.
func MailFilterFromForm(in map[string]string) (*MailFilter, error) {
	rules := []MailFilterRule{}
	for i := 0; i < 10; i++ {
		prefix := fmt.Sprintf("rules[%d][", i)
		value := strings.TrimSpace(in[prefix+"value]"])
		if value == "" {
			continue
		}
		rules = append(rules, MailFilterRule{
			Part:  in[prefix+"part]"],
			Match: in[prefix+"match]"],
			Value: value,
		})
	}
	actions := []MailFilterAction{}
	for i := 0; i < 10; i++ {
		prefix := fmt.Sprintf("actions[%d][", i)
		typ := in[prefix+"type]"]
		if typ == "" {
			continue
		}
		actions = append(actions, MailFilterAction{Type: typ, Value: strings.TrimSpace(in[prefix+"value]"])})
	}
	errs := map[string]string{}
	if strings.TrimSpace(in["name"]) == "" {
		errs["name"] = "Name the filter."
	}
	if len(rules) == 0 {
		errs["rules"] = "Add at least one condition."
	}
	if len(actions) == 0 {
		errs["actions"] = "Add at least one action."
	}
	if errs != nil {
		return nil, &ValidationError{Fields: errs}
	}
	return &MailFilter{
		Name:    strings.TrimSpace(in["name"]),
		Enabled: in["enabled"] != "0", // the hidden field posts 1/0; absent = on
		Any:     in["any"] == "1",
		Rules:   rules,
		Actions: actions,
	}, nil
}

// ResponderFromForm is MailFilterService::responderFromForm.
func ResponderFromForm(in map[string]string) MailResponder {
	days := 1
	if v, err := strconv.Atoi(in["days"]); err == nil {
		days = v
	}
	if days < 1 {
		days = 1
	}
	if days > 30 {
		days = 30
	}
	return MailResponder{
		Enabled: in["enabled"] == "1",
		Subject: strings.TrimSpace(in["subject"]),
		Body:    in["body"],
		Days:    days,
		Start:   strings.TrimSpace(in["start"]),
		End:     strings.TrimSpace(in["end"]),
	}
}

// MailFilterSave sends the mailbox's filters + autoresponder to the agent
// (MailFilterService::save), which renders them to its Sieve script.
func (s *MailService) MailFilterSave(ctx context.Context, address string, filters []MailFilter, r MailResponder, what string) error {
	if filters == nil {
		filters = []MailFilter{}
	}
	for i := range filters {
		if filters[i].Rules == nil {
			filters[i].Rules = []MailFilterRule{}
		}
		if filters[i].Actions == nil {
			filters[i].Actions = []MailFilterAction{}
		}
	}
	if _, err := s.agent.Call(ctx, "mail.filters_save", map[string]any{
		"address":   address,
		"filters":   filters,
		"responder": r,
	}); err != nil {
		return err
	}
	if s.auditlog != nil {
		s.auditlog(ctx, "mail.filters", "mailbox", address, "success",
			map[string]any{"change": what, "filters": len(filters), "autoresponder": r.Enabled})
	}
	return nil
}

// FilterSummary renders a filter's conditions and actions for the list (the
// PHP view's inline implode).
func FilterSummary(f MailFilter) (when, then string) {
	parts := make([]string, 0, len(f.Rules))
	for _, r := range f.Rules {
		parts = append(parts, mailFilterLabel(MailFilterParts, r.Part)+" "+mailFilterLabel(MailFilterMatches, r.Match)+` “`+r.Value+`”`)
	}
	join := " and "
	if f.Any {
		join = " or "
	}
	when = strings.Join(parts, join)
	dos := make([]string, 0, len(f.Actions))
	for _, a := range f.Actions {
		line := mailFilterLabel(MailFilterActions, a.Type)
		if a.Value != "" {
			line += " " + a.Value
		}
		dos = append(dos, line)
	}
	then = strings.Join(dos, ", ")
	return when, then
}
