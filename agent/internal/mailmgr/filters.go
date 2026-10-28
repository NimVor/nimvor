package mailmgr

import (
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Email filters and the autoresponder of a mailbox become its personal
// Sieve script (~/.dovecot.sieve in the mailbox folder), generated from
// structured, validated rules: users never write Sieve themselves.

// Rule matches one part of a message.
type Rule struct {
	Part  string `json:"part"`  // from, to, cc, reply-to, subject, list-id, body
	Match string `json:"match"` // contains, is, begins, ends (not- prefix negates)
	Value string `json:"value"`
}

// FilterAction is what a matching filter does.
type FilterAction struct {
	Type  string `json:"type"`            // fileinto, redirect, redirect_copy, discard, mark_read, stop
	Value string `json:"value,omitempty"` // folder or address
}

// Filter is one email filter.
type Filter struct {
	Name    string         `json:"name"`
	Enabled bool           `json:"enabled"`
	Any     bool           `json:"any"` // any rule (anyof) instead of all rules
	Rules   []Rule         `json:"rules"`
	Actions []FilterAction `json:"actions"`
}

// Responder is the autoresponder (Sieve vacation).
type Responder struct {
	Enabled bool   `json:"enabled"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Days    int    `json:"days"`            // one reply per sender per this many days
	Start   string `json:"start,omitempty"` // YYYY-MM-DD, optional
	End     string `json:"end,omitempty"`
}

var (
	ruleParts   = []string{"from", "to", "cc", "reply-to", "subject", "list-id", "body"}
	ruleMatches = []string{"contains", "is", "begins", "ends", "not-contains", "not-is", "not-begins", "not-ends"}
	actionTypes = []string{"fileinto", "redirect", "redirect_copy", "discard", "mark_read", "stop"}
	folderRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{0,63}$`)
	dateRe      = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

func cleanLine(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	return s, len(s) <= max && !strings.ContainsAny(s, "\x00\r\n")
}

// ValidateFilters checks filters and the autoresponder.
func ValidateFilters(filters []Filter, r *Responder) error {
	if len(filters) > 50 {
		return fmt.Errorf("at most 50 filters")
	}
	for i := range filters {
		f := &filters[i]
		name, ok := cleanLine(f.Name, 100)
		if !ok || name == "" {
			return fmt.Errorf("filter %d: a name of up to 100 characters", i+1)
		}
		f.Name = name
		if len(f.Rules) == 0 || len(f.Rules) > 10 {
			return fmt.Errorf("%s: 1 to 10 conditions", name)
		}
		for j := range f.Rules {
			ru := &f.Rules[j]
			v, ok := cleanLine(ru.Value, 200)
			if !ok || v == "" || !slices.Contains(ruleParts, ru.Part) || !slices.Contains(ruleMatches, ru.Match) {
				return fmt.Errorf("%s: condition %d needs a part, a comparison and a value (up to 200 characters)", name, j+1)
			}
			ru.Value = v
		}
		if len(f.Actions) == 0 || len(f.Actions) > 5 {
			return fmt.Errorf("%s: 1 to 5 actions", name)
		}
		for j := range f.Actions {
			a := &f.Actions[j]
			if !slices.Contains(actionTypes, a.Type) {
				return fmt.Errorf("%s: unknown action %q", name, a.Type)
			}
			a.Value = strings.TrimSpace(a.Value)
			switch a.Type {
			case "fileinto":
				if !folderRe.MatchString(a.Value) {
					return fmt.Errorf("%s: a folder name of letters, digits, spaces, _ and - (up to 64)", name)
				}
			case "redirect", "redirect_copy":
				addr, err := mail.ParseAddress(a.Value)
				if err != nil || addr.Name != "" || !validDestination(addr.Address) {
					return fmt.Errorf("%s: %q is not an email address", name, a.Value)
				}
				a.Value = strings.ToLower(addr.Address)
			default:
				a.Value = ""
			}
		}
	}
	if r != nil {
		subject, ok := cleanLine(r.Subject, 200)
		if !ok {
			return fmt.Errorf("autoresponder: a subject of up to 200 characters")
		}
		r.Subject = subject
		r.Body = strings.ReplaceAll(strings.TrimSpace(r.Body), "\r\n", "\n")
		if len(r.Body) > 4000 || strings.Contains(r.Body, "\x00") {
			return fmt.Errorf("autoresponder: a message of up to 4000 characters")
		}
		if r.Enabled && r.Body == "" {
			return fmt.Errorf("autoresponder: write the reply message")
		}
		if r.Days < 1 || r.Days > 30 {
			r.Days = 1
		}
		for _, d := range []string{r.Start, r.End} {
			if d == "" {
				continue
			}
			if _, err := time.Parse("2006-01-02", d); err != nil || !dateRe.MatchString(d) {
				return fmt.Errorf("autoresponder: dates as YYYY-MM-DD")
			}
		}
		if r.Start != "" && r.End != "" && r.End < r.Start {
			return fmt.Errorf("autoresponder: the end date is before the start date")
		}
	}
	return nil
}

// sieveString quotes s for Sieve.
func sieveString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// matchPattern escapes Sieve :matches wildcards in a literal value.
func matchPattern(s string) string {
	return strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`).Replace(s)
}

// RenderSieve produces the personal Sieve script of a mailbox, or "" when
// there is nothing to do.
func RenderSieve(address string, filters []Filter, r *Responder) string {
	need := map[string]bool{}
	var body strings.Builder
	for _, f := range filters {
		if !f.Enabled {
			continue
		}
		var tests []string
		for _, ru := range f.Rules {
			neg := strings.HasPrefix(ru.Match, "not-")
			m := strings.TrimPrefix(ru.Match, "not-")
			comp, val := ":"+m, sieveString(ru.Value)
			switch m {
			case "begins":
				comp, val = ":matches", sieveString(matchPattern(ru.Value)+"*")
			case "ends":
				comp, val = ":matches", sieveString("*"+matchPattern(ru.Value))
			}
			var t string
			switch ru.Part {
			case "from", "to", "cc", "reply-to":
				t = fmt.Sprintf(`address :all %s %s %s`, comp, sieveString(ru.Part), val)
			case "body":
				need["body"] = true
				t = fmt.Sprintf(`body :text %s %s`, comp, val)
			default:
				t = fmt.Sprintf(`header %s %s %s`, comp, sieveString(ru.Part), val)
			}
			if neg {
				t = "not " + t
			}
			tests = append(tests, t)
		}
		op := "allof"
		if f.Any {
			op = "anyof"
		}
		fmt.Fprintf(&body, "# %s\nif %s (%s) {\n", strings.NewReplacer("\n", " ", "\r", " ").Replace(f.Name), op, strings.Join(tests, ", "))
		// Flags apply to the actions after them (RFC 5232): set them first.
		acts := slices.Clone(f.Actions)
		slices.SortStableFunc(acts, func(x, y FilterAction) int {
			return boolInt(y.Type == "mark_read") - boolInt(x.Type == "mark_read")
		})
		for _, a := range acts {
			switch a.Type {
			case "fileinto":
				need["fileinto"], need["mailbox"] = true, true
				fmt.Fprintf(&body, "  fileinto :create %s;\n", sieveString(a.Value))
			case "redirect":
				fmt.Fprintf(&body, "  redirect %s;\n", sieveString(a.Value))
			case "redirect_copy":
				need["copy"] = true
				fmt.Fprintf(&body, "  redirect :copy %s;\n", sieveString(a.Value))
			case "discard":
				body.WriteString("  discard;\n")
			case "mark_read":
				need["imap4flags"] = true
				body.WriteString("  addflag \"\\\\Seen\";\n")
			case "stop":
				body.WriteString("  stop;\n")
			}
		}
		body.WriteString("}\n")
	}
	if r != nil && r.Enabled {
		need["vacation"] = true
		vac := fmt.Sprintf("vacation :days %d :subject %s :from %s %s;\n", r.Days, sieveString(r.Subject), sieveString(address), sieveString(r.Body))
		var when []string
		if r.Start != "" {
			when = append(when, fmt.Sprintf(`currentdate :value "ge" "date" %s`, sieveString(r.Start)))
		}
		if r.End != "" {
			when = append(when, fmt.Sprintf(`currentdate :value "le" "date" %s`, sieveString(r.End)))
		}
		body.WriteString("# autoresponder\n")
		if when != nil {
			need["date"], need["relational"] = true, true
			fmt.Fprintf(&body, "if allof (%s) {\n  %s}\n", strings.Join(when, ", "), vac)
		} else {
			body.WriteString(vac)
		}
	}
	if body.Len() == 0 {
		return ""
	}
	var exts []string
	for _, x := range []string{"fileinto", "mailbox", "copy", "imap4flags", "body", "vacation", "date", "relational"} {
		if need[x] {
			exts = append(exts, sieveString(x))
		}
	}
	head := "# Managed by NimVor (email filters and autoresponder). Do not edit: changes are overwritten.\n"
	if exts != nil {
		head += "require [" + strings.Join(exts, ", ") + "];\n"
	}
	return head + body.String()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
