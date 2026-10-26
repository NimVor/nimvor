package web

import (
	bytesPkg "bytes"
	"html/template"
	"testing"
)

// list is the constructor templates use for literal ranges; text/template's
// own "slice" is the slicing builtin and fails on string operands, which made
// mail_admin_server.html (and mail_admin_reports.html) error on every render.
func TestListFuncBuildsRangeLiteral(t *testing.T) {
	tpl, err := template.New("t").Funcs(template.FuncMap{"list": list}).
		Parse(`{{range $x := (list "postfix" "dovecot")}}[{{$x}}]{{end}}`)
	if err != nil {
		t.Fatal(err)
	}
	var out bytesPkg.Buffer
	if err := tpl.Execute(&out, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "[postfix][dovecot]" {
		t.Fatalf("got %q", out.String())
	}
}

// The embedded templates must keep parsing with the registered helpers (a
// misspelled or unregistered function is a start-up failure, not a page 500).
func TestEmbeddedTemplatesParse(t *testing.T) {
	if _, err := New(); err != nil {
		t.Fatal(err)
	}
}
