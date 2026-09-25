package dbmgr

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/mysqlwire"
)

func TestLiteral(t *testing.T) {
	for in, want := range map[string]string{
		`null`: "NULL", `"it's"`: `'it\'s'`, `"a\\b"`: `'a\\b'`, `{"hex":"cafe"}`: "X'cafe'", `{"hex":""}`: "''", `{"default":true}`: "DEFAULT", `"NULL"`: "'NULL'",
	} {
		got, err := literal(json.RawMessage(in))
		if err != nil || got != want {
			t.Errorf("literal(%s) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{`{"hex":"zz"}`, `{"hex":"' OR 1"}`, `5`, `[]`, `{}`} {
		if _, err := literal(json.RawMessage(bad)); err == nil {
			t.Errorf("literal(%s) accepted", bad)
		}
	}
}

func TestCell(t *testing.T) {
	text := mysqlwire.Column{Type: 0xfd, Charset: 45}
	bin := mysqlwire.Column{Type: 0xfc, Charset: 63}
	v := func(s string) *[]byte { b := []byte(s); return &b }
	if cell(text, nil, 10) != nil || cell(text, v("héllo"), 10) != "héllo" {
		t.Fatal("plain text")
	}
	m := cell(text, v(strings.Repeat("é", 10)), 5).(map[string]any)
	if m["text"] != "éé" || m["size"] != 20 || m["truncated"] != true {
		t.Fatalf("truncated text cut inside a character: %+v", m)
	}
	m = cell(bin, v("\x00\xff"), 10).(map[string]any)
	if m["hex"] != "00ff" || m["truncated"] != false {
		t.Fatalf("binary: %+v", m)
	}
	if m := cell(text, v("\xff\xfe"), 10).(map[string]any); m["hex"] != "fffe" {
		t.Fatalf("invalid UTF-8 must be hex: %+v", m)
	}
}

func TestWhereNeedsTheExactKey(t *testing.T) {
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	tb := tableInfo{Columns: []string{"a", "b", "c"}, Key: []string{"a", "b"}}
	w, err := tb.where(map[string]json.RawMessage{"a": raw(`"1"`), "b": raw(`{"hex":"00"}`)})
	if err != nil || w != " WHERE `a` = '1' AND `b` = X'00'" {
		t.Fatalf("where = %q, %v", w, err)
	}
	for _, k := range []map[string]json.RawMessage{
		{"a": raw(`"1"`)},
		{"a": raw(`"1"`), "b": raw(`"2"`), "c": raw(`"3"`)},
		{"a": raw(`null`), "b": raw(`"2"`)},
	} {
		if _, err := tb.where(k); err == nil {
			t.Errorf("where(%v) accepted", k)
		}
	}
	if _, err := (tableInfo{Columns: []string{"a"}}).where(map[string]json.RawMessage{"a": raw(`"1"`)}); err == nil {
		t.Error("table without a key accepted")
	}
}

func TestBrowserAccountAndReservedNames(t *testing.T) {
	a := BrowserAccount("shop_db")
	if !strings.HasPrefix(a, BrowserPrefix) || len(a) > 32 || a == BrowserAccount("shop_db2") {
		t.Fatalf("account = %q", a)
	}
	if err := checkUser("WHACP_B_x", "localhost"); err == nil {
		t.Fatal("reserved prefix accepted")
	}
	if err := checkTable("bad\x00name"); err == nil || checkTable("ok-name with space") != nil {
		t.Fatal("table name check")
	}
}
