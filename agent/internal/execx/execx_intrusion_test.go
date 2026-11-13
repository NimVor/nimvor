package execx

import "testing"

func TestSetfaclAndAugenrulesAllowlist(t *testing.T) {
	ok := [][]string{
		{"-m", "u:www-data:---", "/usr/bin/nc.openbsd"},
		{"-x", "u:www-data", "/usr/bin/nc.openbsd"},
		{"-m", "u:shop:---", "/opt/tools/scan"},
	}
	for _, a := range ok {
		if !setfaclAllowed(a) {
			t.Errorf("refused %v", a)
		}
	}
	bad := [][]string{
		{},
		{"-m", "u:www-data:rwx", "/usr/bin/nc"},          // grants instead of denying
		{"-m", "u:root:---", "/usr/bin/nc"},              // never root
		{"-m", "g:www-data:---", "/usr/bin/nc"},          // users only
		{"-R", "-m", "u:www-data:---", "/usr/bin"},       // no recursion
		{"-m", "u:www-data:---", "/home/nimvor/x"},       // not a system program directory
		{"-m", "u:www-data:---", "/usr/bin/../../etc/x"}, // no traversal
		{"-m", "u:www-data:---", "usr/bin/nc"},           // absolute only
		{"-m", "u:Bad Name:---", "/usr/bin/nc"},
		{"-m", "u:www-data:---", "/usr/bin/nc\n"},
		{"-b", "u:www-data:---", "/usr/bin/nc"},
		{"-m", "u:www-data:---", "/usr/bin/nc", "/etc/shadow"}, // one path only
	}
	for _, a := range bad {
		if setfaclAllowed(a) {
			t.Errorf("allowed %v", a)
		}
	}
	if err := checkArgs("augenrules", Opts{}, []string{"--load"}); err != nil {
		t.Errorf("augenrules --load: %v", err)
	}
	for _, a := range [][]string{{}, {"-s"}, {"--load", "-x"}, {"--check"}} {
		if err := checkArgs("augenrules", Opts{}, a); err == nil {
			t.Errorf("augenrules %v allowed", a)
		}
	}
}
