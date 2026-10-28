package modsec

import (
	"slices"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// Nginx before 1.21.5 builds only against the original PCRE library.
func TestBuildPackagesPCRE(t *testing.T) {
	h := testhost.New(t) // Ubuntu fixture (apt names)
	old, _, _ := buildPackages(h.Env, "1.18.0")
	if !slices.Contains(old, "libpcre3-dev") || !slices.Contains(old, "libpcre2-dev") {
		t.Fatalf("Nginx 1.18: %v", old)
	}
	cur, _, _ := buildPackages(h.Env, "1.24.0")
	if slices.Contains(cur, "libpcre3-dev") {
		t.Fatalf("Nginx 1.24 needs only PCRE2: %v", cur)
	}
	for _, c := range []struct {
		a, b string
		less bool
	}{{"1.20.1", "1.21.5", true}, {"1.21.5", "1.21.5", false}, {"1.26.3", "1.21.5", false}, {"1.21.4", "1.21.5", true}, {"1.9.0", "1.21.5", true}} {
		if versionLess(c.a, c.b) != c.less {
			t.Errorf("%s < %s = %v", c.a, c.b, !c.less)
		}
	}
}
