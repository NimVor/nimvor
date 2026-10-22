package hosting

// Unit tests for the SiteMongoService's name scoping: the account prefix,
// the scoped() prefixing (never doubled), the assertOwned() guard, and the
// backup archive-name parsing.

import (
	"strings"
	"testing"
)

func mongoTestSite() *Website {
	return &Website{ID: 10, Domain: "bob.example", SystemUser: "u_bob"}
}

func TestSiteMongoPrefix(t *testing.T) {
	s := &SiteMongoService{}
	if got := s.Prefix(mongoTestSite()); got != "u_bob_" {
		t.Errorf("Prefix = %q", got)
	}
	// The prefix is lower-cased.
	site := &Website{ID: 1, SystemUser: "U_Alice"}
	if got := s.Prefix(site); got != "u_alice_" {
		t.Errorf("Prefix = %q", got)
	}
}

func TestSiteMongoScoped(t *testing.T) {
	s := &SiteMongoService{}
	site := mongoTestSite()
	cases := map[string]string{
		"shop":      "u_bob_shop",
		" Shop ":    "u_bob_shop", // trimmed + lower-cased
		"u_bob_x":   "u_bob_x",   // a typed prefix is not doubled
		"U_BOB_SHOP": "u_bob_shop",
	}
	for in, want := range cases {
		got, err := s.scoped(site, in)
		if err != nil {
			t.Errorf("scoped(%q) = %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("scoped(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"", "   "} {
		if _, err := s.scoped(site, in); err == nil {
			t.Errorf("scoped(%q) = ok, want 'Enter a name.'", in)
		}
	}
}

func TestSiteMongoAssertOwned(t *testing.T) {
	s := &SiteMongoService{}
	site := mongoTestSite()
	for _, name := range []string{"u_bob_shop", "U_BOB_SHOP"} {
		if err := s.assertOwned(site, name); err != nil {
			t.Errorf("assertOwned(%q) = %v", name, err)
		}
	}
	for _, name := range []string{"other_app", "u_bob", "bob_u_shop", "_u_bob_shop"} {
		if err := s.assertOwned(site, name); err == nil {
			t.Errorf("assertOwned(%q) = ok, want refused", name)
		}
	}
}

func TestSiteMongoBackupDb(t *testing.T) {
	s := &SiteMongoService{}
	site := mongoTestSite()
	cases := map[string]string{
		"20261020-093000_u_bob_shop.archive.gz": "u_bob_shop",
		"20261020-093000_u_bob_shop_data.archive.gz": "u_bob_shop_data", // underscores in the db
	}
	for file, want := range cases {
		got, err := s.backupDb(site, file)
		if err != nil {
			t.Errorf("backupDb(%q) = %v", file, err)
			continue
		}
		if got != want {
			t.Errorf("backupDb(%q) = %q, want %q", file, got, want)
		}
	}
	for _, file := range []string{
		"",
		"garbage.gz",
		"20261020-093000_.archive.gz",                       // empty db
		"20261020-093000_" + strings.Repeat("x", 90) + ".archive.gz", // db too long
		"20261020-093000_other_app.archive.gz",              // not this account's db
	} {
		if _, err := s.backupDb(site, file); err == nil {
			t.Errorf("backupDb(%q) = ok, want refused", file)
		}
	}
}
