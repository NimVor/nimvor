package avmgr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func TestParse(t *testing.T) {
	out := `/home/whacp/a.test/public_html/shell.php: Php.Webshell.Generic FOUND
/home/whacp/a.test/public_html/x y.js: Js.Trojan.Agent-1 FOUND
/etc/passwd: Fake.Sig FOUND

----------- SCAN SUMMARY -----------
Known viruses: 8700000
Scanned files: 42
Infected files: 2
Total errors: 1
`
	res := Parse(strings.NewReader(out), "/home/whacp/a.test")
	if res.Scanned != 42 || res.Errors != 1 || len(res.Infected) != 2 {
		t.Fatalf("parse: %+v", res)
	}
	if res.Infected[0] != (Finding{Path: "public_html/shell.php", Signature: "Php.Webshell.Generic"}) || res.Infected[1].Path != "public_html/x y.js" {
		t.Errorf("findings: %+v", res.Infected)
	}
}

func TestScanRunsAsTheWebsiteAndQuarantinesInside(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	call := func(params string) (any, error) {
		a, _ := r.Lookup("av.scan")
		return a.Handler(context.Background(), json.RawMessage(params))
	}
	site := `"domain":"a.test","user":"example","root":"/home/whacp/a.test"`
	root := filepath.Join(h.Base, "home/whacp/a.test")
	os.MkdirAll(filepath.Join(root, "public_html/sub"), 0o755)
	h.Runner.Unavailable["clamscan"] = true
	if _, err := call(`{` + site + `}`); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("without ClamAV: %v", err)
	}
	delete(h.Runner.Unavailable, "clamscan")

	os.WriteFile(filepath.Join(root, "public_html/shell.php"), []byte("<?php evil"), 0o644)
	os.WriteFile(filepath.Join(root, "public_html/sub/bad.js"), []byte("evil"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "etc/secret"), []byte("root only"), 0o600)
	// A symlink reported as infected, and a path outside the website.
	os.Symlink(filepath.Join(h.Base, "etc/secret"), filepath.Join(root, "public_html/link.php"))
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id != "clamscan" {
			return "", nil
		}
		dir := args[len(args)-1]
		return dir + "/public_html/shell.php: Php.Webshell FOUND\n" +
			dir + "/public_html/sub/bad.js: Js.Bad FOUND\n" +
			dir + "/public_html/link.php: Link.Bad FOUND\n" +
			filepath.Join(h.Base, "etc/secret") + ": Outside FOUND\n" +
			"Scanned files: 3\nTotal errors: 0\n", &os.PathError{Op: "exit", Path: "status 1"}
	}
	// Any failure other than exit status 1 ("infected files found") fails the job.
	res, err := call(`{` + site + `,"quarantine":true}`)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string))
	if job.Status != "failed" {
		t.Fatalf("a failed scan must fail the job: %+v", job)
	}
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		dir := args[len(args)-1]
		for _, a := range args {
			if strings.HasPrefix(a, "--exclude-dir=") && !strings.Contains(a, QuarantineDir) {
				t.Errorf("quarantine not excluded: %s", a)
			}
		}
		return dir + "/public_html/shell.php: Php.Webshell FOUND\n" +
			dir + "/public_html/sub/bad.js: Js.Bad FOUND\n" +
			dir + "/public_html/link.php: Link.Bad FOUND\n" +
			filepath.Join(h.Base, "etc/secret") + ": Outside FOUND\n" +
			"Known viruses: 8700000\nScanned files: 3\nTotal errors: 0\n", nil
	}
	res, err = call(`{` + site + `,"quarantine":true}`)
	if err != nil {
		t.Fatal(err)
	}
	job, _ = h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string))
	if job.Status != "succeeded" {
		t.Fatalf("job: %+v", job)
	}
	o := h.Runner.Opts[len(h.Runner.Opts)-1]
	if o.As == nil || o.As.UID < 1000 {
		t.Fatalf("clamscan must run as the website's account: %+v", o.As)
	}
	b, _ := json.Marshal(job.Result)
	var got Result
	json.Unmarshal(b, &got)
	if got.Scanned != 3 || len(got.Infected) != 3 {
		t.Fatalf("result: %+v", got)
	}
	moved := map[string]bool{}
	for _, f := range got.Infected {
		moved[f.Path] = f.Quarantined
	}
	if !moved["public_html/shell.php"] || !moved["public_html/sub/bad.js"] || moved["public_html/link.php"] {
		t.Errorf("quarantined: %v", moved)
	}
	if _, err := os.Lstat(filepath.Join(root, "public_html/shell.php")); !os.IsNotExist(err) {
		t.Error("the infected file is still in place")
	}
	matches, _ := filepath.Glob(filepath.Join(root, QuarantineDir, "*", "public_html/sub/bad.js"))
	if len(matches) != 1 {
		t.Fatalf("not in quarantine: %v", matches)
	}
	if info, _ := os.Stat(filepath.Dir(matches[0])); info.Sys().(*syscall.Stat_t).Uid != uint32(h.UID) || info.Mode().Perm() != 0o700 {
		t.Errorf("quarantine folder: %v uid %d", info.Mode(), info.Sys().(*syscall.Stat_t).Uid)
	}
	if info, _ := os.Stat(matches[0]); info.Mode().Perm() != 0 {
		t.Errorf("quarantined file mode %o", info.Mode().Perm())
	}
	if b, _ := os.ReadFile(filepath.Join(h.Base, "etc/secret")); string(b) != "root only" {
		t.Error("the file outside the website was touched")
	}
	if _, err := os.Lstat(filepath.Join(root, "public_html/link.php")); err != nil {
		t.Error("the symlink was moved or followed")
	}

	// No summary (or no signatures loaded): nothing was checked, not "clean".
	saved := h.Runner.Handler
	h.Runner.Handler = func(string, execx.Opts, []string) (string, error) { return "Scanned files: 0\n", nil }
	res, _ = call(`{` + site + `}`)
	if job, _ = h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string)); job.Status != "failed" || !strings.Contains(job.Error, "no virus signatures") {
		t.Fatalf("without signatures: %+v", job)
	}
	h.Runner.Handler = saved

	// A quarantine folder that is a symlink out of the website is not used.
	os.RemoveAll(filepath.Join(root, QuarantineDir))
	os.MkdirAll(filepath.Join(h.Base, "etc/elsewhere"), 0o755)
	os.Symlink(filepath.Join(h.Base, "etc/elsewhere"), filepath.Join(root, QuarantineDir))
	os.WriteFile(filepath.Join(root, "public_html/shell.php"), []byte("<?php evil"), 0o644)
	res, _ = call(`{` + site + `,"quarantine":true}`)
	h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string))
	if entries, _ := os.ReadDir(filepath.Join(h.Base, "etc/elsewhere")); len(entries) != 0 {
		t.Errorf("quarantined outside the website: %v", entries)
	}

	for _, bad := range []string{`{"domain":"a.test","user":"root","root":"/home/whacp/a.test"}`, `{"domain":"a.test","user":"example","root":"/etc"}`} {
		if _, err := call(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}

	// Path-scoped scan: only the named file is handed to clamscan, and a
	// traversal path is refused.
	os.WriteFile(filepath.Join(root, "public_html/up.php"), []byte("<?php uploaded"), 0o644)
	var scanned []string
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "clamscan" {
			scanned = args
		}
		return "Known viruses: 8700000\nScanned files: 1\nTotal errors: 0\n", nil
	}
	res, err = call(`{` + site + `,"paths":["public_html/up.php"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if job, _ = h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string)); job.Status != "succeeded" {
		t.Fatalf("path scan: %+v", job)
	}
	wantTarget := filepath.Join(root, "public_html/up.php")
	hasTarget, hasRoot := false, false
	for _, a := range scanned {
		if a == wantTarget {
			hasTarget = true
		}
		if a == root {
			hasRoot = true
		}
	}
	if !hasTarget || hasRoot {
		t.Fatalf("path scan passed wrong targets: %v", scanned)
	}
	for _, bad := range []string{
		`{` + site + `,"paths":["../../etc/secret"]}`,
		`{` + site + `,"paths":["public_html/../../../etc/secret"]}`,
		`{` + site + `,"paths":[""]}`,
	} {
		if _, err := call(bad); err == nil {
			t.Errorf("accepted traversal path: %s", bad)
		}
	}
}

// Rocky Linux 9 report: the page said "No signatures downloaded yet" while
// they were there. The status ran clamscan --version as root, which the
// runner refuses; it now reads the database header (no clamscan).
func TestStatusReadsSignatureHeader(t *testing.T) {
	h := testhost.New(t)
	os.MkdirAll(filepath.Join(h.Base, "var/lib/clamav"), 0o755)
	os.MkdirAll(filepath.Join(h.Base, "usr/bin"), 0o755)
	head := "ClamAV-VDB:07 Oct 2026 08-24 -0400:28146:355728:90:X:X:svc.clamav-publisher:1759840000"
	os.WriteFile(filepath.Join(h.Base, "var/lib/clamav/daily.cld"), append([]byte(head), make([]byte, 512-len(head))...), 0o644)
	h.Respond = func(id string, a []string) (string, error, bool) {
		if id == "dpkg-query" {
			return "clamav\tii \t1.4.3+dfsg-0ubuntu0.24.04.1\n", nil, true
		}
		return "", nil, false
	}
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("av.status")
	res, err := a.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["installed"] != true || m["version"] != "ClamAV 1.4.3/28146/2026-10-07" {
		t.Fatalf("status %v", m)
	}
	if h.Runner.Called("clamscan --version") {
		t.Fatal("ran clamscan as root")
	}
	if v, d, ok := ParseHeader(head); !ok || v != 28146 || d != "2026-10-07" {
		t.Fatalf("header: %d %s %v", v, d, ok)
	}
	if _, _, ok := ParseHeader("garbage"); ok {
		t.Fatal("garbage accepted")
	}
}
