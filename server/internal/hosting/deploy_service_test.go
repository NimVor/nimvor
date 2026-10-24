package hosting

// Unit tests for the pure logic of DeployService: the repository form
// validation (repo/branch/path/hooks), the generated token + secret
// lengths, and the webhook signature helpers.

import (
	"regexp"
	"strings"
	"testing"
)

var deployHexRe = regexp.MustCompile(`^[a-f0-9]+$`)

func TestDeployValidateRepo(t *testing.T) {
	s := &DeployService{}
	ok := []string{
		"https://github.com/owner/repo.git",
		"https://gitlab.example.com:8443/group/sub/repo.git",
		"git@github.com:owner/repo.git",
		"ssh://git@github.com:22/owner/repo.git",
	}
	for _, repo := range ok {
		if _, err := s.Validate(map[string]string{"repo": repo, "path": "app"}); err != nil {
			t.Errorf("Validate(%q) = %v, want ok", repo, err)
		}
	}
	bad := []string{
		"",
		"http://github.com/owner/repo.git",        // http, not https
		"ftp://github.com/owner/repo.git",         // unknown scheme
		"https://github.com/owner/../repo.git",    // path traversal
		"git@github.com:owner/..%2Frepo.git",      // traversal-ish
		"https://user:pass@github.com/o/repo.git", // credentials not allowed
		"not a url",
	}
	for _, repo := range bad {
		if _, err := s.Validate(map[string]string{"repo": repo, "path": "app"}); err == nil {
			t.Errorf("Validate(%q) = ok, want error", repo)
		}
	}
}

func TestDeployValidateBranch(t *testing.T) {
	s := &DeployService{}
	cases := map[string]string{"main": "main", "release/1.0": "release/1.0", "v1.2.3": "v1.2.3"}
	for in, want := range cases {
		v, err := s.Validate(map[string]string{"repo": "https://x/y.git", "branch": in, "path": "app"})
		if err != nil {
			t.Errorf("Validate branch %q = %v, want ok", in, err)
			continue
		}
		if v.Branch != want {
			t.Errorf("branch %q = %q", in, v.Branch)
		}
	}
	// Empty -> defaults to main.
	v, err := s.Validate(map[string]string{"repo": "https://x/y.git", "path": "app"})
	if err != nil || v.Branch != "main" {
		t.Errorf("empty branch = %q %v, want main", v.Branch, err)
	}
	for _, bad := range []string{"-weird", "a..b", "has space", strings.Repeat("b", 101)} {
		if _, err := s.Validate(map[string]string{"repo": "https://x/y.git", "branch": bad, "path": "app"}); err == nil {
			t.Errorf("Validate branch %q = ok, want error", bad)
		}
	}
}

func TestDeployValidatePath(t *testing.T) {
	s := &DeployService{}
	cases := map[string]bool{
		"app":        true,
		"web/app":    true,
		"/app/":      true, // trimmed
		".hidden":    true, // dot segments are fine as long as not "."/".."
		"app/.env":   true,
		"":           false,
		".":          false,
		"..":         false,
		"./app":      false,
		"app/..":     false,
		"logs":       false,
		"tmp":        false,
		"backups":    false,
		".ssh":       false,
		"../etc":     false,
		"app/../etc": false,
		"a b":        false,
	}
	for in, wantOK := range cases {
		_, err := s.Validate(map[string]string{"repo": "https://x/y.git", "path": in})
		if (err == nil) != wantOK {
			t.Errorf("Validate path %q: err=%v, want ok=%v", in, err, wantOK)
		}
	}
	// A reserved name only matters as the FIRST segment.
	if _, err := s.Validate(map[string]string{"repo": "https://x/y.git", "path": "app/logs"}); err != nil {
		t.Errorf("Validate path 'app/logs' = %v, want ok", err)
	}
}

func TestDeployValidateHooks(t *testing.T) {
	s := &DeployService{}
	base := map[string]string{"repo": "https://x/y.git", "path": "app"}

	v, err := s.Validate(withHooks(base, "composer install\n\nphp artisan migrate\n"))
	if err != nil {
		t.Fatalf("hooks validate = %v", err)
	}
	if len(v.Hooks) != 2 || v.Hooks[0] != "composer install" || v.Hooks[1] != "php artisan migrate" {
		t.Errorf("hooks = %v", v.Hooks)
	}

	// Blank lines only -> no hooks.
	if v, err := s.Validate(withHooks(base, "  \n\t\n")); err != nil || len(v.Hooks) != 0 {
		t.Errorf("blank hooks = %v %v", v.Hooks, err)
	}

	// More than 20 commands.
	many := strings.Repeat("echo x\n", 21)
	if _, err := s.Validate(withHooks(base, many)); err == nil {
		t.Error("21 hooks = ok, want error")
	}

	// A hook over 2000 chars.
	if _, err := s.Validate(withHooks(base, strings.Repeat("x", 2100))); err == nil {
		t.Error("over-long hook = ok, want error")
	}

	// The auto_deploy flag only parses "1".
	if v, err := s.Validate(withHooks(map[string]string{"repo": "https://x/y.git", "path": "app", "auto_deploy": "1"}, "")); err != nil || !v.AutoDeploy {
		t.Errorf("auto_deploy=1 = %v %v, want true", v, err)
	}
	if v, err := s.Validate(withHooks(map[string]string{"repo": "https://x/y.git", "path": "app"}, "")); err != nil || v.AutoDeploy {
		t.Errorf("auto_deploy missing = %v %v, want false", v, err)
	}
}

func withHooks(base map[string]string, hooks string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	out["hooks"] = hooks
	return out
}

func TestDeployTokens(t *testing.T) {
	if got := deployToken(); len(got) != 40 || !deployHexRe.MatchString(got) {
		t.Errorf("deployToken() = %q, want 40 hex", got)
	}
	if got := deploySecret(); len(got) != 48 || !deployHexRe.MatchString(got) {
		t.Errorf("deploySecret() = %q, want 48 hex", got)
	}
	// Tokens are random.
	if deployToken() == deployToken() {
		t.Error("deployToken() not random")
	}
}

func TestDeployWebhookSignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := []byte("s3cr3t")
	sig := hmacSHA256(body, secret)
	if len(sig) != 64 || !deployHexRe.MatchString(sig) {
		t.Errorf("hmacSHA256() = %q, want 64 hex", sig)
	}
	if !hmacEqual(sig, sig) {
		t.Error("hmacEqual equal mismatch")
	}
	if hmacEqual(sig, "deadbeef") {
		t.Error("hmacEqual distinct match")
	}
	// A different body must not verify.
	if hmacEqual(hmacSHA256(body, secret), hmacSHA256([]byte(`{"ref":"other"}`), secret)) {
		t.Error("different bodies produced equal signatures")
	}
}
