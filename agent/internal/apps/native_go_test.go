package apps

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Unit tests of the Cloudreve and Gopeed installs: the website account's shell and systemd are simulated, the
// downloads are served locally.

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func cloudreveTarGz(program []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		body []byte
	}{{"LICENSE", []byte("GPL")}, {"cloudreve", program}, {"README.md", []byte("hi")}} {
		tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg})
		tw.Write(f.body)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func gopeedZip(program []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("gopeed-web-v1.9.3-linux-amd64/gopeed")
	w.Write(program)
	zw.Close()
	return buf.Bytes()
}

type goFixture struct {
	srv       *httptest.Server
	program   []byte
	archive   []byte
	name      string
	sums      string // checksums.txt body (Cloudreve)
	digest    string // the digest GitHub reports (Gopeed)
	noSums    bool
	assetName string
}

func newCloudreve(t *testing.T) *goFixture {
	t.Helper()
	f := &goFixture{program: []byte("\x7fELF fake cloudreve")}
	f.archive = cloudreveTarGz(f.program)
	f.name = "cloudreve_4.19.1_linux_" + goArch() + ".tar.gz"
	f.sums = sum(f.archive) + "  " + f.name + "\n"
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := f.srvURL() + "/cloudreve/Cloudreve/releases/download/4.19.1/"
		switch r.URL.Path {
		case "/repos/cloudreve/Cloudreve/releases/latest":
			assets := fmt.Sprintf(`{"name":%q,"browser_download_url":%q}`, f.name, base+f.name)
			if !f.noSums {
				assets += fmt.Sprintf(`,{"name":"checksums.txt","browser_download_url":%q}`, base+"checksums.txt")
			}
			fmt.Fprintf(w, `{"tag_name":"4.19.1","draft":false,"prerelease":false,"assets":[%s]}`, assets)
		case "/cloudreve/Cloudreve/releases/download/4.19.1/" + f.name:
			w.Write(f.archive)
		case "/cloudreve/Cloudreve/releases/download/4.19.1/checksums.txt":
			fmt.Fprint(w, f.sums)
		default:
			http.NotFound(w, r)
		}
	}))
	f.wire(t)
	return f
}

func newGopeed(t *testing.T) *goFixture {
	t.Helper()
	f := &goFixture{program: []byte("\x7fELF fake gopeed")}
	f.archive = gopeedZip(f.program)
	f.name = "gopeed-web-v1.9.3-linux-" + goArch() + ".zip"
	f.digest = "sha256:" + sum(f.archive)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := f.srvURL() + "/GopeedLab/gopeed/releases/download/v1.9.3/"
		switch r.URL.Path {
		case "/repos/GopeedLab/gopeed/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v1.9.3","draft":false,"prerelease":false,"assets":[{"name":%q,"browser_download_url":%q,"digest":%q}]}`, f.name, base+f.name, f.digest)
		case "/GopeedLab/gopeed/releases/download/v1.9.3/" + f.name:
			w.Write(f.archive)
		default:
			http.NotFound(w, r)
		}
	}))
	f.wire(t)
	return f
}

func (f *goFixture) srvURL() string { return f.srv.URL }

func (f *goFixture) wire(t *testing.T) {
	t.Cleanup(f.srv.Close)
	oldAPI, oldDL := githubAPI, githubDownloads
	githubAPI, githubDownloads = f.srv.URL, f.srv.URL+"/"
	t.Cleanup(func() { githubAPI, githubDownloads = oldAPI, oldDL })
}

func (f *goFixture) sources() Sources { return Sources{Client: f.srv.Client()} }

func cloudreveRequest() Request {
	return Request{App: "cloudreve", Domain: "files.test", User: "example", Root: "/home/nimvor/shop.test", Path: "cloud", URL: "https://files.test", Port: 3111, ServiceSlug: "files-cloudreve"}
}

func gopeedRequest() Request {
	return Request{App: "gopeed", Domain: "dl.test", User: "example", Root: "/home/nimvor/shop.test", Path: "gopeed", URL: "https://dl.test", Port: 3112, ServiceSlug: "dl-gopeed",
		AdminUser: "owner", AdminPassword: "correct-horse-battery-9"}
}

func TestCloudreveInstallsVerifiesAndSupervises(t *testing.T) {
	h, _ := setup(t)
	f := newCloudreve(t)
	health := stubHealth(t, nil)
	cred, _, _ := h.Env.Credential("example")
	var log strings.Builder
	res, err := Install(context.Background(), h.Env, f.sources(), cloudreveRequest(), cred, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if res["version"] != "4.19.1" || res["strategy"] != "binary-service" || res["port"] != 3111 {
		t.Errorf("result: %+v", res)
	}
	svc := res["service"].(map[string]any)
	if !strings.Contains(svc["command"].(string), "bin/cloudreve' -w -c ") || !strings.Contains(svc["command"].(string), "data/conf.ini'") {
		t.Errorf("service command: %v", svc["command"])
	}
	for i, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "sh -c") {
			if o := h.Runner.Opts[i]; o.As == nil || o.As.UID == 0 {
				t.Errorf("%q did not run as the website account", c)
			}
		}
	}
	stdins := strings.Join(h.Runner.Stdins, "\n---\n")
	if !strings.Contains(stdins, string(f.program)) {
		t.Error("the verified program was not what was written")
	}
	for _, want := range []string{"[System]", "Mode = master", "Listen = 127.0.0.1:3111", "SessionSecret = ", "HashIDSalt = "} {
		if !strings.Contains(stdins, want) {
			t.Errorf("conf.ini lacks %q", want)
		}
	}
	// The secrets are random, 64 characters, and never reach the log or the result.
	i := strings.Index(stdins, "SessionSecret = ")
	secret := strings.Fields(stdins[i+len("SessionSecret = "):])[0]
	if len(secret) != 64 || strings.Contains(log.String(), secret) || strings.Contains(fmt.Sprint(res), secret) {
		t.Errorf("secret handling: %q", secret)
	}
	if *health != 1 {
		t.Errorf("health checks: %d", *health)
	}
	if !strings.Contains(log.String(), "register it right away") {
		t.Errorf("the log does not say the first account becomes the administrator:\n%s", log.String())
	}
}

func TestCloudreveRefusesWhatCannotBeVerified(t *testing.T) {
	for name, tweak := range map[string]func(f *goFixture){
		"a wrong checksum":     func(f *goFixture) { f.sums = strings.Repeat("0", 64) + "  " + f.name + "\n" },
		"a file not listed":    func(f *goFixture) { f.sums = sum(f.archive) + "  other.tar.gz\n" },
		"no checksums.txt":     func(f *goFixture) { f.noSums = true },
		"a bad checksum entry": func(f *goFixture) { f.sums = "nothex  " + f.name + "\n" },
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := setup(t)
			f := newCloudreve(t)
			tweak(f)
			stubHealth(t, nil)
			cred, _, _ := h.Env.Credential("example")
			if _, err := Install(context.Background(), h.Env, f.sources(), cloudreveRequest(), cred, &strings.Builder{}); err == nil {
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

func TestGopeedInstallsWithItsOwnLogin(t *testing.T) {
	h, _ := setup(t)
	f := newGopeed(t)
	stubHealth(t, nil)
	cred, _, _ := h.Env.Credential("example")
	var log strings.Builder
	req := gopeedRequest()
	res, err := Install(context.Background(), h.Env, f.sources(), req, cred, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if res["version"] != "1.9.3" || res["admin_user"] != "owner" {
		t.Errorf("result: %+v", res)
	}
	stdins := strings.Join(h.Runner.Stdins, "\n---\n")
	if !strings.Contains(stdins, string(f.program)) {
		t.Error("the verified program was not what was written")
	}
	for _, want := range []string{`"address": "127.0.0.1"`, `"port": 3112`, `"username": "owner"`, `"password": "` + req.AdminPassword + `"`, `"storageDir": "`} {
		if !strings.Contains(stdins, want) {
			t.Errorf("config.json lacks %s", want)
		}
	}
	// The password lives only in the 0600 configuration file: not in the command, the service's environment, the
	// log or the result the panel receives.
	svc := res["service"].(map[string]any)
	for what, text := range map[string]string{"command": fmt.Sprint(svc["command"]), "env": fmt.Sprint(svc["env"]), "log": log.String(), "result": fmt.Sprint(res)} {
		if strings.Contains(text, req.AdminPassword) {
			t.Errorf("the password is in the %s", what)
		}
	}
	if !strings.Contains(fmt.Sprint(svc["command"]), "bin/gopeed' -c ") {
		t.Errorf("command: %v", svc["command"])
	}
	for _, c := range h.Runner.Calls {
		if strings.Contains(c, req.AdminPassword) {
			t.Errorf("the password is in a command line: %s", c)
		}
	}
	// config.json is written with umask 077.
	found := false
	for _, c := range h.Runner.Calls {
		if strings.Contains(c, "umask 077 && cat > config.json") {
			found = true
		}
	}
	if !found {
		t.Errorf("config.json was not written private: %v", h.Runner.Calls)
	}
}

func TestGopeedRefusesWhatGitHubDoesNotVouchFor(t *testing.T) {
	for name, digest := range map[string]string{"no digest": "", "a wrong digest": "sha256:" + strings.Repeat("0", 64), "a digest that is not sha256": "md5:abc"} {
		t.Run(name, func(t *testing.T) {
			h, _ := setup(t)
			f := newGopeed(t)
			f.digest = digest
			stubHealth(t, nil)
			cred, _, _ := h.Env.Credential("example")
			if _, err := Install(context.Background(), h.Env, f.sources(), gopeedRequest(), cred, &strings.Builder{}); err == nil {
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

func TestGoAppsCleanUpWhenNotHealthy(t *testing.T) {
	h, _ := setup(t)
	f := newGopeed(t)
	stubHealth(t, fmt.Errorf("no answer"))
	cred, _, _ := h.Env.Credential("example")
	var log strings.Builder
	_, err := Install(context.Background(), h.Env, f.sources(), gopeedRequest(), cred, &log)
	if err == nil || !strings.Contains(err.Error(), "not healthy") || !strings.Contains(log.String(), "Cleanup: removed") {
		t.Fatalf("err = %v\n%s", err, log.String())
	}
}

func TestProgramFromArchiveTakesOnlyTheNamedFile(t *testing.T) {
	h, _ := setup(t)
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := dir + "/" + name
		if err := osWriteFile(p, b); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p, err := programFromArchive(h.Env, "targz", write("a.tgz", cloudreveTarGz([]byte("PROG"))), "cloudreve"); err != nil || readFile(t, p) != "PROG" {
		t.Fatalf("targz: %v", err)
	}
	if p, err := programFromArchive(h.Env, "zip", write("a.zip", gopeedZip([]byte("PROG2"))), "gopeed"); err != nil || readFile(t, p) != "PROG2" {
		t.Fatalf("zip: %v", err)
	}
	if _, err := programFromArchive(h.Env, "zip", write("b.zip", gopeedZip([]byte("X"))), "missing"); err == nil {
		t.Error("a missing member was accepted")
	}
	if _, err := programFromArchive(h.Env, "targz", write("c.tgz", []byte("not an archive")), "cloudreve"); err == nil {
		t.Error("garbage was accepted")
	}
}

func TestGopeedRequestNeedsALogin(t *testing.T) {
	h, _ := setup(t)
	for _, tweak := range []func(r *Request){
		func(r *Request) { r.AdminUser = "" },
		func(r *Request) { r.AdminUser = "a b" },
		func(r *Request) { r.AdminPassword = "short" },
		func(r *Request) { r.AdminPassword = "has-a-newline-here\nx" },
	} {
		r := gopeedRequest()
		tweak(&r)
		if err := r.validate(h.Env); err == nil {
			t.Errorf("accepted %+v", r)
		}
	}
	r := gopeedRequest()
	if err := r.validate(h.Env); err != nil {
		t.Errorf("a good request: %v", err)
	}
}

func osWriteFile(p string, b []byte) error { return os.WriteFile(p, b, 0o600) }

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
