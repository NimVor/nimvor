package security

import (
	"reflect"
	"testing"
)

func TestModsecSplitRules(t *testing.T) {
	// Comment lines above a rule, a backslash continuation, a blank line
	// ending a comment run, and a dangling continuation at the end of the
	// file (splitRules' rules, in order).
	file := "# block xmlrpc\n" +
		"SecRule REQUEST_URI \"@beginsWith /xmlrpc.php\" \\\n" +
		"    \"id:10001,phase:1,deny,status:403,log,msg:'xmlrpc blocked'\"\n" +
		"\n" +
		"# an orphan comment that belongs to nothing\n" +
		"\n" +
		"SecAction \"id:10002,phase:1,nolog,pass,secmark='EXECCMD'\"\n" +
		"# a multi-line comment\n" +
		"# second line of the comment\n" +
		"SecRule ARGS \"@rx evil\" \\\n"
	rules := ModsecSplitRules(file)
	if len(rules) != 3 {
		t.Fatalf("rules = %d, want 3: %+v", len(rules), rules)
	}
	if rules[0].Comment != "block xmlrpc" {
		t.Errorf("rule 0 comment = %q", rules[0].Comment)
	}
	if rules[0].Text != "SecRule REQUEST_URI \"@beginsWith /xmlrpc.php\" \\\n    \"id:10001,phase:1,deny,status:403,log,msg:'xmlrpc blocked'\"" {
		t.Errorf("rule 0 text = %q", rules[0].Text)
	}
	if rules[0].ID == nil || *rules[0].ID != 10001 {
		t.Errorf("rule 0 id = %v", rules[0].ID)
	}
	if rules[0].Phase != "1" || rules[0].Action != "deny" || rules[0].Msg != "xmlrpc blocked" {
		t.Errorf("rule 0 describe = %q/%q/%q", rules[0].Phase, rules[0].Action, rules[0].Msg)
	}
	// The orphan comment ends with the blank line: rule 1 has none.
	if rules[1].Comment != "" {
		t.Errorf("rule 1 comment = %q (want none — the blank line ended the run)", rules[1].Comment)
	}
	if rules[1].Action != "pass" {
		t.Errorf("rule 1 action = %q", rules[1].Action)
	}
	if rules[2].Comment != "a multi-line comment\nsecond line of the comment" {
		t.Errorf("rule 2 comment = %q", rules[2].Comment)
	}
	// The dangling continuation swallows the file's final (empty) line, so the
	// text keeps a trailing newline — verified against PHP splitRules.
	if rules[2].Text != "SecRule ARGS \"@rx evil\" \\\n" {
		t.Errorf("rule 2 text (dangling continuation kept) = %q", rules[2].Text)
	}
	if rules[2].ID != nil {
		t.Errorf("rule 2 id = %v (want nil)", rules[2].ID)
	}
}

func TestModsecSplitRulesEmpty(t *testing.T) {
	if rules := ModsecSplitRules(""); len(rules) != 0 {
		t.Errorf("empty file = %d rules", len(rules))
	}
	if rules := ModsecSplitRules("# only comments\n\n   \n"); len(rules) != 0 {
		t.Errorf("comments only = %d rules", len(rules))
	}
}

func TestModsecJoinRules(t *testing.T) {
	rules := []ModsecRule{
		{Text: "SecRule REQUEST_URI \"@rx a\" \"id:10001,phase:1,deny\"", Comment: "block a"},
		{Text: "SecAction \"id:10002,phase:1,pass\""},
	}
	got := ModsecJoinRules(rules)
	want := "# block a\nSecRule REQUEST_URI \"@rx a\" \"id:10001,phase:1,deny\"\n\nSecAction \"id:10002,phase:1,pass\"\n"
	if got != want {
		t.Errorf("join = %q, want %q", got, want)
	}
	// Round trip: split(join(rules)) keeps text and comment.
	back := ModsecSplitRules(got)
	if len(back) != 2 || back[0].Text != rules[0].Text || back[0].Comment != "block a" || back[1].Comment != "" {
		t.Errorf("round trip = %+v", back)
	}
	if ModsecJoinRules(nil) != "" {
		t.Error("join of nothing must be empty")
	}
}

func TestModsecNextRuleID(t *testing.T) {
	id := func(v int) *int { return &v }
	cases := []struct {
		ids  []*int
		want int
	}{
		{nil, 10001},
		{[]*int{id(10001)}, 10002},
		{[]*int{id(10000)}, 10001}, // the free range starts at 10001
		{[]*int{id(10000), id(10005)}, 10006},
		{[]*int{id(99999)}, 10000}, // wraps
	}
	for _, c := range cases {
		rules := make([]ModsecRule, 0, len(c.ids))
		for _, v := range c.ids {
			rules = append(rules, ModsecRule{ID: v})
		}
		if got := ModsecNextRuleID(rules); got != c.want {
			t.Errorf("nextRuleId(%v) = %d, want %d", c.ids, got, c.want)
		}
	}
}

func TestModsecParseIDs(t *testing.T) {
	got, err := ModsecParseIDs("942100, 941100 920350;942100")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int{942100, 941100, 920350}) {
		t.Errorf("ids = %v", got)
	}
	if got, err := ModsecParseIDs("  \n "); err != nil || len(got) != 0 {
		t.Errorf("blank = %v, %v", got, err)
	}
	for _, bad := range []string{"abc", "0", "12345678"} {
		if _, err := ModsecParseIDs(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		} else if err.Error() != `"`+bad+`" is not a rule ID.` {
			t.Errorf("%q: error = %q", bad, err)
		}
	}
}

func TestModsecModeLabel(t *testing.T) {
	if ModsecModeLabel("detect") != "Detection only (log, do not block)" {
		t.Error("detect label")
	}
	if ModsecModeLabel("bogus") != "bogus" {
		t.Error("unknown mode is its own label")
	}
	if ModsecModeKnown("detect") != true || ModsecModeKnown("bogus") != false {
		t.Error("ModeKnown")
	}
}

func TestModsecSettingFor(t *testing.T) {
	sites := map[string]any{
		"bob.example": map[string]any{
			"mode":           "on",
			"exclude":        []any{"custom"},
			"disabled_rules": []any{942100.0, 920350.0},
		},
	}
	s := ModsecSettingFor(sites, "bob.example")
	if s.Mode != "on" || !reflect.DeepEqual(s.Exclude, []string{"custom"}) || !reflect.DeepEqual(s.DisabledRules, []int{942100, 920350}) {
		t.Errorf("setting = %+v", s)
	}
	s = ModsecSettingFor(sites, "alice.example")
	if s.Mode != "off" || len(s.Exclude) != 0 || len(s.DisabledRules) != 0 {
		t.Errorf("absent site = %+v (want the off defaults)", s)
	}
	s = ModsecSettingFor(nil, "bob.example")
	if s.Mode != "off" {
		t.Errorf("nil sites = %+v", s)
	}
}
