package hosting

import (
	"strings"
	"testing"
)

// recNameRe must accept exactly what PHP's DnsService record-name pattern
// accepts. The Go port had "(\*\.?)" (a mandatory star) where PHP has "(\*\.)?",
// so every ordinary name (www, mail, ...) was rejected. The expectations below are
// the output of PHP 8.3's preg_match on the same names.
func TestRecordNamePatternMatchesPHP(t *testing.T) {
	cases := map[string]bool{
		"www": true, "mail": true, "*.dev": true, "a.b.c": true, "_dmarc": true, "_sip._tcp": true,
		"-bad": false, "bad-": false, "a..b": false, "*": false, "*.": false, "*dev": false,
		"UPPER": false, "x": true, "a-b": true, "*.a.b": true, "a_b": true, "1.2": true, "9x": true,
		strings.Repeat("a", 63): true, strings.Repeat("a", 64): false,
	}
	for name, want := range cases {
		if got := recNameRe.MatchString(name); got != want {
			t.Errorf("recNameRe(%q) = %v, PHP says %v", name, got, want)
		}
	}
}
