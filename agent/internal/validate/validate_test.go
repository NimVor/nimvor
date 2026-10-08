package validate

import "testing"

func TestDomain(t *testing.T) {
	for _, ok := range []string{"example.com", "a.b-c.example.co.uk", "xn--bcher-kva.example"} {
		if err := Domain("d", ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "localhost", "Example.com", "-a.com", "a-.com", "*.example.com", "a..com", "10.0.0.1", "exa mple.com", "a.com;rm", "a.com\n"} {
		if Domain("d", bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestWithin(t *testing.T) {
	if err := Within("p", "/home/whacp", "/home/whacp/a.com/public_html"); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"/home/whacpx/a", "/home/whacp/../etc", "/etc", "relative", "/home/whacp/a;b", "/home/whacp/a\"b"} {
		if Within("p", "/home/whacp", bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestIdentifiers(t *testing.T) {
	if Username("u", "root") == nil || Username("u", "Bad") == nil || Username("u", "shop_1") != nil {
		t.Error("username rules")
	}
	if DBIdentifier("d", "shop_db") != nil || DBIdentifier("d", "a`b") == nil || DBIdentifier("d", "mysql") == nil {
		t.Error("db identifier rules")
	}
	if PHPVersion("v", "8.3") != nil || PHPVersion("v", "8.3; rm") == nil {
		t.Error("php version rules")
	}
	if Password("p", "abc def ghi jkl", 8) == nil || Password("p", "Str0ng!Pass#1", 8) != nil {
		t.Error("password rules")
	}
}

func TestCron(t *testing.T) {
	for _, ok := range []string{"*/5 * * * *", "0 3 * * 1-5", "@daily", "15,45 0-23/2 1 1 0"} {
		if err := CronSchedule("s", ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"* * * *", "61 * * * *", "* * * * * *", "a * * * *", "@sometimes"} {
		if CronSchedule("s", bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestGit(t *testing.T) {
	for _, ok := range []string{"https://github.com/o/r.git", "git@github.com:o/r.git", "ssh://git@host:2222/o/r"} {
		if err := GitURL("u", ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"file:///etc", "http://x/y", "https://x/../y", "--upload-pack=x", "ext::sh -c id"} {
		if GitURL("u", bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if GitRef("b", "-f") == nil || GitRef("b", "main") != nil {
		t.Error("ref rules")
	}
}

func TestBuildAccountNameIsReserved(t *testing.T) {
	if Username("user", "whacp-build") == nil {
		t.Fatal("whacp-build accepted as a website account")
	}
}

// RPM names with capitals are packages too (ImageMagick on RHEL).
func TestPackageNames(t *testing.T) {
	for _, ok := range []string{"nginx", "php8.3-fpm", "libstdc++6", "ImageMagick", "perl-Net_SSLeay", "clamav-update"} {
		if err := Package("p", ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"-y", "--nogpgcheck", "a b", "x;rm", "a/b", "*", "", "a"} {
		if Package("p", bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// TestAdversarialInputs drives the GA agent-RPC audit's attack vectors
// through every validator that guards a file name, identifier, path or
// command argument. None of these must be accepted where it could become a
// traversal, an injected argument or a shell command. The validators are
// the agent's input boundary: an action decodes JSON, validates with these,
// and only then builds an allowlisted command — so proving rejection here
// proves user input cannot reach an arbitrary path or shell.
func TestAdversarialInputs(t *testing.T) {
	long := ""
	for i := 0; i < 5000; i++ {
		long += "a"
	}
	// Vectors that must never pass a name/identifier/path validator.
	vectors := []string{
		"../etc/passwd", "../../root", "a/../../b", "..", "./../x",
		"/etc/shadow", "/root/.ssh/authorized_keys", // absolute
		"a\x00b", "a\x00", "\x00", // null bytes
		"a\nb", "a\r\nb", "line1\nline2", // newlines
		"a;id", "a|id", "a&&id", "a`id`", "a$(id)", // shell metacharacters + command substitution
		"a${HOME}", "a$HOME", "a~root", // environment / tilde expansion
		"a*", "a?", "a[bc]", "*", // wildcards
		"a b", "a\tb", // whitespace splitting
		"café", "日本語", "а", "‮evil", // unexpected Unicode (incl. RTL override)
		"-rf", "--force", "-", "--", // argument injection
		long,    // very long
		"", " ", // empty / blank
	}
	named := map[string]func(string) error{
		"Domain":       func(s string) error { return Domain("f", s) },
		"Username":     func(s string) error { return Username("f", s) },
		"DBIdentifier": func(s string) error { return DBIdentifier("f", s) },
		"PHPVersion":   func(s string) error { return PHPVersion("f", s) },
		"Unit":         func(s string) error { return Unit("f", s) },
		"Package":      func(s string) error { return Package("f", s) },
		"Slug":         func(s string) error { return Slug("f", s) },
		"GitRef":       func(s string) error { return GitRef("f", s) },
	}
	for name, fn := range named {
		for _, v := range vectors {
			if fn(v) == nil {
				t.Errorf("%s accepted adversarial input %q", name, v)
			}
		}
	}

	// Paths. Within and RelPath keep a path inside its base; they do not
	// reject every unusual character, because ordinary Linux file names may
	// contain spaces, Unicode and even shell glob characters, and WhaCP
	// passes paths as argv (no shell) — so "a|id" is an inert file name, not
	// a command. What they MUST reject is traversal, escaping the base, and
	// the characters that could break the configuration files WhaCP
	// generates or a command line: NUL, CR, LF, and ; { } " ' $ ` \.
	base := "/home/whacp/site.example.com"
	dangerous := "\x00\n\r;{}\"'$`\\"
	for _, v := range vectors {
		mustReject := hasDotDot(v) || containsAny(v, dangerous)
		// Within: an absolute vector is tried as-is (and must stay in base);
		// a relative one is joined under the base.
		p := v
		if !isAbsLike(v) {
			p = base + "/" + v
		}
		withinReject := mustReject || (isAbsLike(v) && !within(base, p))
		if withinReject && Within("f", base, p) == nil {
			t.Errorf("Within accepted %q (as %q)", v, p)
		}
		// RelPath rejects a leading slash, control characters and traversal.
		if (containsAny(v, "\x00\n\r") || hasDotDot(v) || isAbsLike(v)) && RelPath("f", v) == nil {
			t.Errorf("RelPath accepted %q", v)
		}
	}

	// Command and Password deliberately allow metacharacters (a user's cron
	// line runs as their own unprivileged account; a password is opaque),
	// but both must still reject control characters, and Password rejects
	// spaces and non-ASCII.
	for _, v := range []string{"a\x00b", "a\nb", "a\r\nb"} {
		if Command("f", v) == nil {
			t.Errorf("Command accepted control character in %q", v)
		}
		if Password("f", v+"xxxxxxxx", 8) == nil {
			t.Errorf("Password accepted control character in %q", v)
		}
	}
	if Password("f", "with space 1", 8) == nil || Password("f", "café-pass-1", 8) == nil {
		t.Error("Password accepted a space or non-ASCII")
	}
	if Command("f", long+long) == nil {
		t.Error("Command accepted an over-long value")
	}

	// Known-good values still pass (the boundary rejects attacks, not use).
	good := map[string]func(string) error{
		"Domain:example.com":   func(s string) error { return Domain("f", "example.com") },
		"Username:shop_1":      func(s string) error { return Username("f", "shop_1") },
		"DBIdentifier:shop_db": func(s string) error { return DBIdentifier("f", "shop_db") },
		"PHPVersion:8.3":       func(s string) error { return PHPVersion("f", "8.3") },
		"Unit:php8.3-fpm":      func(s string) error { return Unit("f", "php8.3-fpm.service") },
		"Package:php8.3-fpm":   func(s string) error { return Package("f", "php8.3-fpm") },
		"Slug:queue-worker":    func(s string) error { return Slug("f", "queue-worker") },
		"GitRef:main":          func(s string) error { return GitRef("f", "release/1.0") },
		"RelPath:wp/uploads":   func(s string) error { return RelPath("f", "wp-content/uploads") },
	}
	for name, fn := range good {
		if err := fn(""); err != nil {
			t.Errorf("legitimate input %s rejected: %v", name, err)
		}
	}
}

func isAbsLike(s string) bool { return len(s) > 0 && s[0] == '/' }

// within reports whether the cleaned path p stays inside base.
func within(base, p string) bool {
	if hasDotDot(p) {
		return false
	}
	return p == base || len(p) > len(base) && p[:len(base)+1] == base+"/"
}

func containsAny(s, set string) bool {
	for _, r := range s {
		for _, c := range set {
			if r == c {
				return true
			}
		}
	}
	return false
}

func hasDotDot(v string) bool {
	for _, part := range splitSlash(v) {
		if part == ".." {
			return true
		}
	}
	return false
}

func splitSlash(v string) []string {
	out := []string{}
	cur := ""
	for _, r := range v {
		if r == '/' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}
