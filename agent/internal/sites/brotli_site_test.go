package sites

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func brotliSpec(domain string, brotli *bool) Spec {
	s := spec()
	s.Domain, s.Aliases = domain, nil
	s.Cache = &CachePolicy{DynamicMode: "app", Brotli: brotli}
	return s
}

func boolp(b bool) *bool { return &b }

func TestBrotliOffIsWrittenOnlyForAWebsiteThatTurnedItOffAndOnlyWithTheModule(t *testing.T) {
	render := func(brotli *bool, module bool) string {
		s := brotliSpec("a.test", brotli)
		if err := s.Normalize(testRoot, testState); err != nil {
			t.Fatal(err)
		}
		l := policyLayout
		l.BrotliModule = module
		return RenderNginx(s, l)
	}
	if conf := render(boolp(false), true); strings.Count(conf, "    brotli off;\n") < 1 {
		t.Errorf("a website that turned Brotli off has no \"brotli off;\":\n%s", conf)
	}
	for name, conf := range map[string]string{
		"default (nil)":  render(nil, true),
		"enabled":        render(boolp(true), true),
		"module missing": render(boolp(false), false),
	} {
		if strings.Contains(conf, "brotli") {
			t.Errorf("%s: the configuration mentions brotli:\n%s", name, conf)
		}
	}
	// No cache policy at all: nothing.
	s := spec()
	if err := s.Normalize(testRoot, testState); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(RenderNginx(s, Layout{Socket: "/s", LogDir: "/l", ACMERoot: "/a", BrotliModule: true}), "brotli") {
		t.Error("a website without a cache policy mentions brotli")
	}
}

// With the real Brotli module loaded for the whole server, one website opts out and another keeps the default.
func TestAWebsiteCanOptOutOfBrotliWithARealNginx(t *testing.T) {
	var mod string
	for _, d := range []string{"/usr/lib/nginx/modules", "/usr/lib64/nginx/modules", "/usr/share/nginx/modules"} {
		if m, _ := filepath.Glob(filepath.Join(d, "ngx_http_brotli_filter_module.so")); len(m) > 0 {
			mod = m[0]
		}
	}
	if mod == "" {
		t.Skip("the Nginx Brotli module is not installed")
	}
	text := strings.Repeat("NimVor compresses text responses. ", 200)
	be := &backend{handle: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		fmt.Fprint(w, text)
	}}
	on, off := brotliSpec("on.test", nil), brotliSpec("off.test", boolp(false))
	for _, s := range []*Spec{&on, &off} {
		if err := s.Normalize(testRoot, testState); err != nil {
			t.Fatal(err)
		}
	}
	tuning := "brotli on;\nbrotli_comp_level 5;\nbrotli_types text/css;\ngzip on;\ngzip_types text/css;\n"
	dir, _ := os.MkdirTemp("", "nimvor-tune-")
	t.Cleanup(func() { os.RemoveAll(dir) })
	os.WriteFile(filepath.Join(dir, "tuning.conf"), []byte(tuning), 0o644)
	addr := startNginxWith(t, []Spec{on, off}, be, nginxOpts{loadModule: mod, brotli: true, httpExtra: "    include " + filepath.Join(dir, "tuning.conf") + ";\n"})
	get := func(host string) (string, int) {
		req, _ := http.NewRequest("GET", "http://"+addr+"/page.css", nil)
		req.Host = host
		req.Header.Set("Accept-Encoding", "br, gzip")
		res, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.Header.Get("Content-Encoding"), len(b)
	}
	// The PHP location answers /page.css through index.php (the test backend), as a front controller would.
	if enc, _ := get("on.test"); enc != "br" {
		t.Errorf("the default website: encoding %q, want br", enc)
	}
	if enc, _ := get("off.test"); enc != "gzip" {
		t.Errorf("the website that opted out: encoding %q, want gzip (Brotli off, gzip stays)", enc)
	}
}

// Applying a website: "brotli off;" is written when the module is loaded, and left out (so Nginx stays valid) when not.
func TestApplyWritesBrotliOffOnlyWhenTheModuleIsLoaded(t *testing.T) {
	for _, module := range []bool{true, false} {
		h := testhost.New(t)
		h.Respond = func(id string, args []string) (string, error, bool) {
			if module && id == "nginx" && len(args) > 0 && args[0] == "-V" {
				return "nginx version: nginx/1.24.0\nconfigure arguments: --with-http_v2_module --add-module=/build/ngx_brotli\n", nil, true
			}
			return "", nil, false
		}
		m := &Manager{Env: h.Env}
		s := brotliSpec("new.test", boolp(false))
		if _, err := m.Apply(context.Background(), s); err != nil {
			t.Fatal(err)
		}
		has := strings.Contains(h.Read("etc/nginx/sites-available/nimvor-new.test.conf"), "brotli off;")
		if has != module {
			t.Errorf("module loaded=%v but \"brotli off;\" written=%v", module, has)
		}
	}
}
