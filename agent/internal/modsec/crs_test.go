package modsec

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/txn"
)

// crsArchive builds a release-like archive; extra adds hostile entries.
func crsArchive(t *testing.T, extra bool) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name, body string, typ byte, link string) {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: typ, Linkname: link})
		if typ == tar.TypeReg {
			tw.Write([]byte(body))
		}
	}
	add("coreruleset-4.18.0/crs-setup.conf.example", "SecAction \"id:900990,phase:1,pass,nolog,setvar:tx.crs_setup_version=4180\"\n", tar.TypeReg, "")
	for i := 0; i < 12; i++ {
		add(fmt.Sprintf("coreruleset-4.18.0/rules/REQUEST-9%02d-TEST.conf", i), "# rule\n", tar.TypeReg, "")
	}
	add("coreruleset-4.18.0/rules/php-errors.data", "error\n", tar.TypeReg, "")
	add("coreruleset-4.18.0/rules/REQUEST-900-EXCLUSION-RULES-BEFORE-CRS.conf.example", "# before\n", tar.TypeReg, "")
	add("coreruleset-4.18.0/LICENSE", "license\n", tar.TypeReg, "")
	if extra {
		add("coreruleset-4.18.0/rules/../../../etc/evil.conf", "evil", tar.TypeReg, "")
		add("/etc/abs.conf", "evil", tar.TypeReg, "")
		add("coreruleset-4.18.0/rules/link.conf", "", tar.TypeSymlink, "/etc/shadow")
		add("coreruleset-4.18.0/rules/sub/deep.conf", "x", tar.TypeReg, "")
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestCRSFilesKeepsOnlyRules(t *testing.T) {
	files, err := CRSFiles(crsArchive(t, true))
	if err != nil {
		t.Fatal(err)
	}
	for name := range files {
		if strings.Contains(name, "..") || strings.Contains(name, "evil") || strings.Contains(name, "link") || strings.Contains(name, "sub/") || name == "LICENSE" {
			t.Errorf("kept %s", name)
		}
	}
	if _, ok := files["crs-setup.conf"]; !ok {
		t.Fatal("setup")
	}
	if _, ok := files["rules/REQUEST-900-EXCLUSION-RULES-BEFORE-CRS.conf"]; !ok {
		t.Fatal("exclusion example not renamed")
	}
	if _, ok := files["rules/php-errors.data"]; !ok {
		t.Fatal("data file")
	}
	if _, err := CRSFiles([]byte("not a gzip")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func crsServer(t *testing.T, body []byte) CRSSource {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	t.Cleanup(srv.Close)
	return CRSSource{URL: srv.URL + "/crs.tar.gz", SHA256: sum(body), Client: srv.Client()}
}

// No package (e.g. AlmaLinux): the release, versioned; on in detection
// only; a second run changes nothing.
func TestInstallCRSFromRelease(t *testing.T) {
	m := newMachine(t)
	m.Runner.Unavailable["apt-get"] = true
	archive := crsArchive(t, false)
	res, err := InstallCRS(context.Background(), m.Env, crsServer(t, archive), 30, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	c := FindCRS(m.Env)
	if !c.Available || c.Version != CRSVersion || c.Rules != CRSRoot+"/4.18.0/rules" || c.before == "" {
		t.Fatalf("%+v", c)
	}
	st := Load(m.Env)
	if !st.Config.CRS.Enabled || !st.Config.DetectionOnly || res["method"] != "crs (release)" {
		t.Fatalf("%+v %v", st.Config, res)
	}
	if res, _ := InstallCRS(context.Background(), m.Env, crsServer(t, archive), 30, io.Discard); res["changed"] != false {
		t.Fatalf("%v", res)
	}
}

func TestInstallCRSRollsBackOnABadDownload(t *testing.T) {
	m := newMachine(t)
	m.Runner.Unavailable["apt-get"] = true
	before := m.state()
	src := crsServer(t, crsArchive(t, false))
	src.SHA256 = strings.Repeat("a", 64)
	if _, err := InstallCRS(context.Background(), m.Env, src, 30, io.Discard); err == nil || !strings.Contains(err.Error(), "checksum") || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("%v", err)
	}
	if m.state() != before || FindCRS(m.Env).Available {
		t.Fatal("something was left")
	}
	if tx := lastTx(t, m); tx.Status != txn.RolledBack {
		t.Fatalf("%+v", tx)
	}
}

// With a package: the package, recorded (a rollback removes it).
func TestInstallCRSFromPackage(t *testing.T) {
	m := newMachine(t)
	orig := m.Respond
	m.Respond = func(id string, args []string) (string, error, bool) {
		if id == "apt-get" && contains(args, "install") && contains(args, "modsecurity-crs") {
			m.installed["modsecurity-crs"] = true
			os.MkdirAll(m.path("/usr/share/modsecurity-crs/rules"), 0o755)
			os.WriteFile(m.path("/usr/share/modsecurity-crs/rules/REQUEST-942-APPLICATION-ATTACK-SQLI.conf"), []byte("#"), 0o644)
			os.MkdirAll(m.path("/etc/modsecurity/crs"), 0o755)
			os.WriteFile(m.path("/etc/modsecurity/crs/crs-setup.conf"), []byte("setvar:tx.crs_setup_version=335"), 0o644)
			return "", nil, true
		}
		return orig(id, args)
	}
	res, err := InstallCRS(context.Background(), m.Env, CRSSource{}, 30, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res["method"] != "crs (package)" || !FindCRS(m.Env).Available {
		t.Fatalf("%v", res)
	}
	tx := lastTx(t, m)
	if len(tx.Changes) == 0 || tx.Changes[0].Op != "package" || tx.Changes[0].Name != "modsecurity-crs" {
		t.Fatalf("%+v", tx.Changes)
	}
}

// Detection only: a website set to block is rendered detecting until
// blocking is allowed.
func TestDetectionOnlyBeforeBlocking(t *testing.T) {
	f := newFixture(t)
	if _, err := f.call(t, "modsec.configure", `{"enabled":true,"detection_only":true,"crs":{"enabled":true}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.call(t, "modsec.site", `{"domain":"shop.test","mode":"on"}`); err != nil {
		t.Fatal(err)
	}
	site := f.h.Read("etc/nginx/whacp-modsec/sites/shop.test.conf")
	if !strings.Contains(site, "SecRuleEngine DetectionOnly") {
		t.Fatalf("blocking before it was allowed:\n%s", site)
	}
	if _, err := f.call(t, "modsec.configure", `{"enabled":true,"detection_only":false,"crs":{"enabled":true}}`); err != nil {
		t.Fatal(err)
	}
	if site := f.h.Read("etc/nginx/whacp-modsec/sites/shop.test.conf"); strings.Contains(site, "DetectionOnly") {
		t.Fatalf("still detection only:\n%s", site)
	}
}

// The state is updated under the actions' lock: a website's mode being
// changed at the same time is not overwritten with an older copy.
func TestInstallCRSWaitsForOtherStateChanges(t *testing.T) {
	m := newMachine(t)
	m.Runner.Unavailable["apt-get"] = true
	src := crsServer(t, crsArchive(t, false))
	mu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := InstallCRS(context.Background(), m.Env, src, 30, io.Discard)
		done <- err
	}()
	select {
	case err := <-done:
		mu.Unlock()
		t.Fatalf("finished while another change held the state: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	mu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
