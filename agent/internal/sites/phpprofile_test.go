package sites

import "testing"

func TestDisableFunctionsFor(t *testing.T) {
	// Legacy: empty profile keeps the exact old behaviour.
	if got := DisableFunctionsFor(PHPSettings{Harden: true}); got != legacyHardenList {
		t.Errorf("legacy harden = %q", got)
	}
	if got := DisableFunctionsFor(PHPSettings{Harden: false}); got != "" {
		t.Errorf("legacy no-harden = %q", got)
	}
	// Recommended disables the full dangerous set from the brief.
	rec := DisableFunctionsFor(PHPSettings{Profile: ProfileRecommended})
	for _, fn := range []string{"exec", "system", "shell_exec", "passthru", "popen", "proc_open", "proc_get_status", "proc_terminate", "pcntl_exec"} {
		if !contains(rec, fn) {
			t.Errorf("recommended missing %s (%q)", fn, rec)
		}
	}
	// Balanced keeps process-control functions enabled.
	bal := DisableFunctionsFor(PHPSettings{Profile: ProfileBalanced})
	if contains(bal, "pcntl_exec") || contains(bal, "proc_get_status") || !contains(bal, "exec") {
		t.Errorf("balanced = %q", bal)
	}
	// Compatibility disables nothing.
	if got := DisableFunctionsFor(PHPSettings{Profile: ProfileCompatibility}); got != "" {
		t.Errorf("compatibility = %q", got)
	}
	// Custom: cleaned, de-duplicated, sorted, junk dropped.
	cust := DisableFunctionsFor(PHPSettings{Profile: ProfileCustom, DisableFunctions: "System, exec,exec, ; rm -rf, shell_exec"})
	if cust != "exec,shell_exec,system" {
		t.Errorf("custom = %q", cust)
	}
}

func TestNormalizeRejectsProfile(t *testing.T) {
	s := Spec{Domain: "a.example.com", User: "sh_user", Kind: "php", PHPVersion: "8.3", PHP: PHPSettings{Profile: "wide-open"}}
	if err := s.Normalize("/home/nimvor", "/var/lib/nimvor-agent"); err == nil {
		t.Fatal("an invalid profile must be rejected")
	}
	ok := Spec{Domain: "a.example.com", User: "sh_user", Kind: "php", PHPVersion: "8.3", PHP: PHPSettings{Profile: ProfileRecommended}}
	if err := ok.Normalize("/home/nimvor", "/var/lib/nimvor-agent"); err != nil {
		t.Fatalf("recommended rejected: %v", err)
	}
}

func contains(csv, fn string) bool {
	for _, p := range splitComma(csv) {
		if p == fn {
			return true
		}
	}
	return false
}

func splitComma(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}
