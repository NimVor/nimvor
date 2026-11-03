package pgmgr

import (
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	for _, ok := range []string{"shop", "shop_db1", "_x", "a", strings.Repeat("a", 63)} {
		if err := ValidName("name", ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Shop", "1shop", "shop-db", "shop db", "shop;drop", "a\"b", "ünï", strings.Repeat("a", 64),
		"postgres", "template0", "template1", "public", "nimvor_admin", "nimvor_x", "pg_monitor", "pg_x", "none"} {
		if err := ValidName("name", bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestValidPassword(t *testing.T) {
	if err := ValidPassword("p", "short"); err == nil {
		t.Error("short password accepted")
	}
	if err := ValidPassword("p", "has a\x00nul and is long"); err == nil {
		t.Error("control character accepted")
	}
	if err := ValidPassword("p", "a perfectly fine passphrase!"); err != nil {
		t.Errorf("good password refused: %v", err)
	}
}

func TestQuoteNeverBreaksOut(t *testing.T) {
	if got := Quote(`a"b`); got != `"a""b"` {
		t.Errorf("quote = %s", got)
	}
}

func TestValidLevel(t *testing.T) {
	for _, l := range []Level{LevelNone, LevelReadOnly, LevelReadWrite, LevelAll} {
		if err := ValidLevel("level", l); err != nil {
			t.Error(err)
		}
	}
	if err := ValidLevel("level", "superuser"); err == nil {
		t.Error("unknown level accepted")
	}
}

func TestTargetConfigRules(t *testing.T) {
	// A remote server without TLS settings is refused; the local default needs none.
	if _, err := (Target{Host: "db.example.test", User: "u", Password: "p"}).config("postgres"); err == nil {
		t.Error("remote without ssl_mode accepted")
	}
	if _, err := (Target{Host: "db.example.test", User: "u", Password: "p", SSLMode: "verify-full", CAPEM: "not a pem"}).config("postgres"); err == nil {
		t.Error("bad CA accepted")
	}
	if _, err := (Target{Host: "db.example.test", User: "u", Password: "p", SSLMode: "bogus"}).config("postgres"); err == nil {
		t.Error("unknown ssl_mode accepted")
	}
	c, err := (Target{User: "u", Password: "p"}).config("postgres")
	if err != nil || c.Host != "127.0.0.1" || c.Port != 5432 || c.TLSConfig != nil {
		t.Errorf("local default: %+v %v", c, err)
	}
	c, err = (Target{Host: "db.example.test", Port: 6432, User: "u", Password: "p", SSLMode: "require"}).config("postgres")
	if err != nil || c.TLSConfig == nil || c.Port != 6432 {
		t.Errorf("require: %+v %v", c, err)
	}
}
