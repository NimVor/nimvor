// Package web renders the Go panel's HTML with html/template (contextual
// auto-escaping on by default). Templates are embedded in the binary. During
// the migration only the login and a minimal authenticated shell are ported;
// the full view set is carried over page by page in later phases. All pages
// are CSP-safe: no inline <script> or <style> — behaviour lives in the reused
// /assets/js, exactly as the PHP panel requires (SecurityHeaders CSP).
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/*.html
var files embed.FS

// Templates is the parsed template set.
type Templates struct{ t *template.Template }

var weekDays = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

// weekday is the PHP BackupScheduler::WEEKDAYS lookup (0 = Sunday).
func weekday(n int) string {
	if n < 0 || n > 6 {
		return ""
	}
	return weekDays[n]
}

// stamp renders an RFC3339 timestamp the way the PHP views show them
// (substr(..., 0, 16): "2026-10-23 10:15"); "—" when it is not one.
func stamp(iso string) string {
	if iso == "" {
		return "—"
	}
	if t, err := time.Parse(time.RFC3339, iso); err == nil {
		return t.Format("2006-01-02 15:04")
	}
	return iso
}

// dict builds a map from key/value pairs for passing several values to a
// subtemplate ({{template "x" (dict "a" 1 "b" 2)}}).
func dict(kv ...any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[fmt.Sprint(kv[i])] = kv[i+1]
	}
	return m
}

// bytes renders a size like the PHP format_bytes (one decimal, 1024-based).
func bytes(v int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	f := float64(v)
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return strconv.FormatInt(v, 10) + " " + units[0]
	}
	return strconv.FormatFloat(f, 'f', 1, 64) + " " + units[i]
}

// New parses the embedded templates.
func New() (*Templates, error) {
	t, err := template.New("whacp").Funcs(template.FuncMap{
		// PHP's strtoupper / strtolower in the badge labels.
		"upper":   strings.ToUpper,
		"lower":   strings.ToLower,
		"weekday": weekday,
		"bytes":   bytes,
		"stamp":   stamp,
		"dict":    dict,
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
