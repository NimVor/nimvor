// Package web renders the Go panel's HTML with html/template (contextual
// auto-escaping on by default). Templates are embedded in the binary. During
// the migration only the login and a minimal authenticated shell are ported;
// the full view set is carried over page by page in later phases. All pages
// are CSP-safe: no inline <script> or <style> — behaviour lives in the reused
// /assets/js, exactly as the PHP panel requires (SecurityHeaders CSP).
package web

import (
	"embed"
	"html/template"
	"io"
	"strings"
)

//go:embed templates/*.html
var files embed.FS

// Templates is the parsed template set.
type Templates struct{ t *template.Template }

// New parses the embedded templates.
func New() (*Templates, error) {
	t, err := template.New("whacp").Funcs(template.FuncMap{
		// PHP's strtoupper / strtolower in the badge labels.
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
	}).ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Templates{t: t}, nil
}

// Render writes a named template to w.
func (t *Templates) Render(w io.Writer, name string, data any) error {
	return t.t.ExecuteTemplate(w, name, data)
}
