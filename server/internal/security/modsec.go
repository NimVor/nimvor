package security

// This file ports the static (agent-independent) half of Services
// ModSecurityService: the website modes, the custom-rules file format
// (single directives with their continuation lines and the comment lines
// right above them), rule-ID parsing, and a website's setting as reported in
// the agent's status. The agent-driven half (status, configure, the site and
// events calls, the background installations) rides with the http handlers.

import (
	"regexp"
	"strconv"
	"strings"
)

// msTrimRight is PHP's rtrim() with its default character list (the parser's
// rtrim($line)).
func msTrimRight(s string) string { return strings.TrimRight(s, " \t\n\r\x00\x0B\x0C") }

// ModsecModes is the website firewall's modes, in form order (MODES).
var ModsecModes = [][2]string{
	{"off", "Off"},
	{"detect", "Detection only (log, do not block)"},
	{"on", "On (block attacks)"},
}

// ModsecModeLabel is the mode's label, or the key itself (PHP's
// MODES[$mode] ?? $mode).
func ModsecModeLabel(mode string) string {
	for _, m := range ModsecModes {
		if m[0] == mode {
			return m[1]
		}
	}
	return mode
}

// ModsecModeKnown reports whether the mode is one of the three.
func ModsecModeKnown(mode string) bool {
	for _, m := range ModsecModes {
		if m[0] == mode {
			return true
		}
	}
	return false
}

// ModsecVendorIDs is the rule vendor set a website can leave out.
var ModsecVendorIDs = []string{"crs", "custom"}

// ModsecRule is one custom rule of the rules file (splitRules' element).
type ModsecRule struct {
	Text    string
	Comment string
	ID      *int
	Phase   string
	Action  string
	Msg     string
}

var (
	// describeRule's four lookups (the agent's CheckCustom pins IDs to
	// 10000–99999; the parser only reports what is there).
	msIDRe     = regexp.MustCompile(`\bid:(\d+)`)
	msPhaseRe  = regexp.MustCompile(`\bphase:(\w+)`)
	msActionRe = regexp.MustCompile(`\b(deny|block|drop|allow|pass|redirect)\b`)
	msMsgRe    = regexp.MustCompile(`\bmsg:'([^']*)'`)
)

// msDescribeRule is describeRule: the rule's ID, phase, action and message,
// as shown in the rules table.
func msDescribeRule(text, comment string) ModsecRule {
	r := ModsecRule{Text: text, Comment: comment}
	if m := msIDRe.FindStringSubmatch(text); m != nil {
		if id, err := strconv.Atoi(m[1]); err == nil {
			r.ID = &id
		}
	}
	if m := msPhaseRe.FindStringSubmatch(text); m != nil {
		r.Phase = m[1]
	}
	if m := msActionRe.FindStringSubmatch(text); m != nil {
		r.Action = m[1]
	}
	if m := msMsgRe.FindStringSubmatch(text); m != nil {
		r.Msg = m[1]
	}
	return r
}

// ModsecSplitRules is splitRules: the custom rules file as single rules —
// each directive (with its backslash continuation lines) and the comment
// lines right above it; a blank line ends a comment run.
func ModsecSplitRules(file string) []ModsecRule {
	lines := strings.Split(strings.ReplaceAll(file, "\r\n", "\n"), "\n")
	var (
		rules   []ModsecRule
		comment []string
		cur     []string
	)
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if cur != nil {
			cur = append(cur, msTrimRight(line))
			if !strings.HasSuffix(msTrimRight(line), "\\") {
				rules = append(rules, msDescribeRule(strings.Join(cur, "\n"), strings.Join(comment, "\n")))
				cur = nil
				comment = nil
			}
			continue
		}
		if t == "" {
			comment = nil
			continue
		}
		if strings.HasPrefix(t, "#") {
			comment = append(comment, strings.TrimSpace(strings.TrimLeft(t, "#")))
			continue
		}
		if strings.HasSuffix(msTrimRight(line), "\\") {
			cur = []string{msTrimRight(line)}
			continue
		}
		rules = append(rules, msDescribeRule(msTrimRight(line), strings.Join(comment, "\n")))
		comment = nil
	}
	if cur != nil {
		rules = append(rules, msDescribeRule(strings.Join(cur, "\n"), strings.Join(comment, "\n")))
	}
	if rules == nil {
		return []ModsecRule{}
	}
	return rules
}

// ModsecJoinRules is joinRules: the rules back to the file (each with its
// comment lines above it, one blank line between rules, trailing newline).
func ModsecJoinRules(rules []ModsecRule) string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		c := strings.TrimSpace(r.Comment)
		if c != "" {
			parts := make([]string, 0, len(strings.Split(c, "\n")))
			for _, l := range strings.Split(c, "\n") {
				parts = append(parts, "# "+l)
			}
			out = append(out, strings.Join(parts, "\n")+"\n"+strings.TrimSpace(r.Text))
		} else {
			out = append(out, strings.TrimSpace(r.Text))
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n\n") + "\n"
}

// ModsecNextRuleID is nextRuleId: a free rule ID for a new custom rule
// (10000–99999).
func ModsecNextRuleID(rules []ModsecRule) int {
	max := 0
	used := false
	for _, r := range rules {
		if r.ID != nil {
			if !used || *r.ID > max {
				max = *r.ID
			}
			used = true
		}
	}
	n := 10001
	if used {
		n = max + 1
	}
	if n > 99999 {
		return 10000
	}
	if n < 10001 {
		return 10001
	}
	return n
}

var msRuleIDRe = regexp.MustCompile(`^\d{1,7}$`)

// ModsecParseIDs is parseIds: "942100, 941100 920350" into unique IDs. The
// error is the validation message ("\"X\" is not a rule ID.").
func ModsecParseIDs(text string) ([]int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return []int{}, nil
	}
	seen := map[int]bool{}
	var ids []int
	for _, p := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ',' || r == ';'
	}) {
		if !msRuleIDRe.MatchString(p) {
			return nil, &ModsecError{Message: `"` + p + `" is not a rule ID.`}
		}
		v, _ := strconv.Atoi(p)
		if v < 1 {
			return nil, &ModsecError{Message: `"` + p + `" is not a rule ID.`}
		}
		if !seen[v] {
			seen[v] = true
			ids = append(ids, v)
		}
	}
	return ids, nil
}

// ModsecError is a UserFacingException from the ModSecurity service (the
// caller flashes its message and redirects back).
type ModsecError struct{ Message string }

func (e *ModsecError) Error() string { return e.Message }

// ModsecSiteSetting is a website's setting from the agent's status
// (sites[domain]: mode, the excluded vendors, the rules turned off).
type ModsecSiteSetting struct {
	Mode          string
	Exclude       []string
	DisabledRules []int
}

// ModsecSettingFor is siteSetting: the website's setting (off / no
// exclusions / no rules off when the agent reports none for it).
func ModsecSettingFor(sites any, domain string) ModsecSiteSetting {
	s := ModsecSiteSetting{Mode: "off", Exclude: []string{}, DisabledRules: []int{}}
	m, ok := sites.(map[string]any)
	if !ok {
		return s
	}
	v, ok := m[domain]
	if !ok {
		return s
	}
	rm, ok := v.(map[string]any)
	if !ok {
		return s
	}
	if mode, ok := rm["mode"].(string); ok && mode != "" {
		s.Mode = mode
	}
	if list, ok := rm["exclude"].([]any); ok {
		for _, e := range list {
			if str, ok := e.(string); ok {
				s.Exclude = append(s.Exclude, str)
			}
		}
	}
	if list, ok := rm["disabled_rules"].([]any); ok {
		for _, e := range list {
			if n, ok := e.(float64); ok {
				s.DisabledRules = append(s.DisabledRules, int(n))
			}
		}
	}
	return s
}

// ModsecVendorName is the vendor's display name (the page's map lookup).
func ModsecVendorName(vendors []ModsecVendorInfo, id string) string {
	for _, v := range vendors {
		if v.ID == id {
			return v.Name
		}
	}
	return id
}

// ModsecVendorInfo is one rule vendor of the agent's status (id, name,
// description, the rule-ID range).
type ModsecVendorInfo struct {
	ID          string
	Name        string
	Description string
	FirstID     int
	LastID      int
}

