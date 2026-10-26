package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// The panel's CSP (script-src/style-src 'self') refuses inline scripts and
// style attributes; no template may contain them.
func TestTemplatesHaveNoInlineScriptOrStyle(t *testing.T) {
	inlineScript := regexp.MustCompile(`<script(\s[^>]*)?>`)
	styleAttr := regexp.MustCompile(`\sstyle="`)
	err := fs.WalkDir(files, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, _ := fs.ReadFile(files, p)
		for _, m := range inlineScript.FindAllString(string(b), -1) {
			if !strings.Contains(m, "src=") {
				t.Errorf("%s: inline <script>", p)
			}
		}
		if styleAttr.Match(b) {
			t.Errorf("%s: inline style attribute", p)
		}
		if strings.Contains(string(b), "<style") {
			t.Errorf("%s: <style> block", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
