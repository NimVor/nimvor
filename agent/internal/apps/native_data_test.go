package apps

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Unit tests of the ClickHouse install: the account's shell and systemd are simulated, GitHub is local.

type dataFixture struct {
	goFixture
}

// newDataRelease serves one release of repo with one file; digest "" reports none.
func newDataRelease(t *testing.T, repo, tag, name string, body []byte, digest string) *dataFixture {
	t.Helper()
	f := &dataFixture{}
	f.program = body
	f.name = name
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := f.srvURL() + "/" + repo + "/releases/download/" + tag + "/"
		switch r.URL.Path {
		case "/repos/" + repo + "/releases/latest":
			d := ""
			if digest != "" {
				d = fmt.Sprintf(`,"digest":%q`, digest)
			}
			fmt.Fprintf(w, `{"tag_name":%q,"draft":false,"prerelease":false,"assets":[{"name":%q,"browser_download_url":%q%s}]}`, tag, name, base+name, d)
		case "/" + repo + "/releases/download/" + tag + "/" + name:
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	f.wire(t)
	return f
}

func clickhouseTgz(program []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		body []byte
	}{{"clickhouse-common-static-26.8.48.11/usr/share/doc/README", []byte("hi")}, {"clickhouse-common-static-26.8.48.11/usr/bin/clickhouse", program}} {
		tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg})
		tw.Write(f.body)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func clickhouseRequest() Request {
	return Request{App: "clickhouse", Domain: "ch.test", User: "example", Root: "/home/nimvor/shop.test", Path: "ch", URL: "https://ch.test", Port: 3114, ServiceSlug: "ch-clickhouse",
		AdminUser: "analyst", AdminPassword: "correct-horse-battery-9"}
}

func TestClickHouseInstallsWithLoopbackOnlyAndAHashedPassword(t *testing.T) {
	h, _ := setup(t)
	prog := []byte("\x7fELF fake clickhouse")
	archive := clickhouseTgz(prog)
	f := newDataRelease(t, "ClickHouse/ClickHouse", "v26.8.48.11-lts", "clickhouse-common-static-26.8.48.11-"+goArch()+".tgz", archive, "sha256:"+sum(archive))
	stubHealth(t, nil)
	cred, _, _ := h.Env.Credential("example")
	var log strings.Builder
	req := clickhouseRequest()
	res, err := Install(context.Background(), h.Env, f.sources(), req, cred, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if res["version"] != "26.8.48.11-lts" || res["admin_user"] != "analyst" {
		t.Errorf("result: %+v", res)
	}
	stdins := strings.Join(h.Runner.Stdins, "\n---\n")
	if !strings.Contains(stdins, string(prog)) {
		t.Error("the program from the archive was not what was written")
	}
	for _, want := range []string{"<listen_host>127.0.0.1</listen_host>", "<http_port>3114</http_port>", "/ch/data/</path>", "<analyst>", "<password_sha256_hex>" + sum([]byte(req.AdminPassword)) + "</password_sha256_hex>", "<ip>127.0.0.1</ip>"} {
		if !strings.Contains(stdins, want) {
			t.Errorf("the configuration lacks %s", want)
		}
	}
	for _, not := range []string{"<tcp_port>", "<mysql_port>", "<interserver_http_port>", "<listen_host>0.0.0.0"} {
		if strings.Contains(stdins, not) {
			t.Errorf("the configuration has %s", not)
		}
	}
	svc := res["service"].(map[string]any)
	if !strings.Contains(fmt.Sprint(svc["command"]), "bin/clickhouse' server --config-file=") {
		t.Errorf("command: %v", svc["command"])
	}
	for what, text := range map[string]string{"command": fmt.Sprint(svc["command"]), "env": fmt.Sprint(svc["env"]), "log": log.String(), "result": fmt.Sprint(res)} {
		if strings.Contains(text, req.AdminPassword) {
			t.Errorf("the password is in the %s", what)
		}
	}
}

func TestDataAppsRefuseWhatGitHubDoesNotVouchFor(t *testing.T) {
	for name, digest := range map[string]string{"no digest": "", "a wrong digest": "sha256:" + strings.Repeat("0", 64)} {
		t.Run(name, func(t *testing.T) {
			h, _ := setup(t)
			archive := clickhouseTgz([]byte("\x7fELF"))
			f := newDataRelease(t, "ClickHouse/ClickHouse", "v26.8.48.11-lts", "clickhouse-common-static-26.8.48.11-"+goArch()+".tgz", archive, digest)
			stubHealth(t, nil)
			cred, _, _ := h.Env.Credential("example")
			if _, err := Install(context.Background(), h.Env, f.sources(), clickhouseRequest(), cred, &strings.Builder{}); err == nil {
				t.Fatal("installed")
			}
			for _, c := range h.Runner.Calls {
				if strings.HasPrefix(c, "sh -c") {
					t.Fatalf("something was written before the refusal: %v", h.Runner.Calls)
				}
			}
		})
	}
}

func TestDataAppsSayWhichFilesTheyDidSeeAndCleanUp(t *testing.T) {
	h, _ := setup(t)
	f := newDataRelease(t, "ClickHouse/ClickHouse", "v26.8.48.11-lts", "something-else.zip", []byte("x"), "sha256:"+sum([]byte("x")))
	stubHealth(t, nil)
	cred, _, _ := h.Env.Credential("example")
	_, err := Install(context.Background(), h.Env, f.sources(), clickhouseRequest(), cred, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "files seen: something-else.zip") {
		t.Fatalf("err = %v", err)
	}

	archive2 := clickhouseTgz([]byte("\x7fELF fake clickhouse"))
	f2 := newDataRelease(t, "ClickHouse/ClickHouse", "v26.8.48.11-lts", "clickhouse-common-static-26.8.48.11-"+goArch()+".tgz", archive2, "sha256:"+sum(archive2))
	stubHealth(t, fmt.Errorf("no answer"))
	h2, _ := setup(t)
	cred2, _, _ := h2.Env.Credential("example")
	var log strings.Builder
	if _, err := Install(context.Background(), h2.Env, f2.sources(), clickhouseRequest(), cred2, &log); err == nil || !strings.Contains(err.Error(), "not healthy") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(log.String(), "Cleanup: removed") {
		t.Errorf("no cleanup:\n%s", log.String())
	}
}

func TestDataAppRequestsAreValidated(t *testing.T) {
	h, _ := setup(t)
	for name, mod := range map[string]func(*Request){
		"clickhouse: short password": func(r *Request) { r.App = "clickhouse"; r.AdminPassword = "short" },
		"clickhouse: xml in name":    func(r *Request) { r.App = "clickhouse"; r.AdminUser = "a><b" },
		"clickhouse: dotted name":    func(r *Request) { r.App = "clickhouse"; r.AdminUser = "ana.lyst" },
		"clickhouse: newline": func(r *Request) {
			r.App = "clickhouse"
			r.AdminUser = "analyst"
			r.AdminPassword = "correct-horse\nbattery"
		},
	} {
		r := clickhouseRequest()
		mod(&r)
		r.AllowTesting = true
		if err := r.validate(h.Env); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
