package redismgr

import (
	"encoding/base64"
	"testing"
)

func TestEscapeGlobAndPrefix(t *testing.T) {
	if got := escapeGlob(`a*b?[c]\d`); got != `a\*b\?\[c\]\\d` {
		t.Errorf("escapeGlob = %q", got)
	}
	r := browseReq{DB: 0, Prefix: "shop:"}
	if _, err := r.decodeKey(base64.StdEncoding.EncodeToString([]byte("shop:x"))); err != nil {
		t.Error(err)
	}
	for _, k := range []string{"other:x", "", "sho"} {
		if _, err := r.decodeKey(base64.StdEncoding.EncodeToString([]byte(k))); err == nil {
			t.Errorf("%q accepted", k)
		}
	}
	if _, err := r.decodeKey("!!notbase64"); err == nil {
		t.Error("bad base64 accepted")
	}
	for _, bad := range []browseReq{{DB: -1}, {DB: 256}, {Prefix: "a\nb"}} {
		if bad.check() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if b := bin([]byte("hé")); b.Binary || b.Text != "hé" {
		t.Errorf("text: %+v", b)
	}
	if b := bin([]byte{0, 'a'}); !b.Binary || b.Text != "" {
		t.Errorf("binary: %+v", b)
	}
}
