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
