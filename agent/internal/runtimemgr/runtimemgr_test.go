package runtimemgr

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// nodeTarball builds a minimal node-v<ver>-linux-<arch>.tar.gz with a bin/node
// and an npx symlink, as the real release has.
func nodeTarball(t *testing.T, top string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write := func(name string, mode int64, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: top + name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	dir := func(name string) {
		tw.WriteHeader(&tar.Header{Name: top + name, Mode: 0o755, Typeflag: tar.TypeDir})
	}
	dir("bin/")
	write("bin/node", 0o755, "#!node\n")
	// A relative symlink that stays inside the tree (npx -> ../lib/node_modules/npm/bin/npx-cli.js).
	dir("lib/")
	dir("lib/node_modules/")
	write("lib/node_modules/marker", 0o644, "x")
	if err := tw.WriteHeader(&tar.Header{Name: top + "bin/npx", Linkname: "../lib/node_modules/marker", Typeflag: tar.TypeSymlink}); err != nil {
		t.Fatal(err)
	}
	// An entry that tries to escape must be rejected by the extractor.
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestInstallVerifiesAndUnpacksNode(t *testing.T) {
	if nodeArch() == "" {
		t.Skip("no Node build for this architecture")
	}
	h := testhost.New(t)
	version := "20.17.0"
	top := fmt.Sprintf("node-v%s-linux-%s/", version, nodeArch())
	tarball := nodeTarball(t, top)
	sum := sha256.Sum256(tarball)
	sums := hex.EncodeToString(sum[:]) + "  " + fmt.Sprintf("node-v%s-linux-%s.tar.gz", version, nodeArch()) + "\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v"+version+"/SHASUMS256.txt":
			w.Write([]byte(sums))
		case r.URL.Path == "/v"+version+"/node-v"+version+"-linux-"+nodeArch()+".tar.gz":
			w.Write(tarball)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	r := actions.NewRegistry(true)
	RegisterWith(r, h.Env, Sources{NodeBaseURL: srv.URL, Client: srv.Client()})

	inst, _ := r.Lookup("runtime.install")
	res, err := inst.Handler(context.Background(), json.RawMessage(`{"kind":"node","version":"`+version+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	job, _ := h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string))
	if job.Status != "succeeded" {
		t.Fatalf("install job: %+v", job)
	}
	node := filepath.Join(h.Base, "opt/nimvor/runtimes/node", version, "bin/node")
	if _, err := os.Stat(node); err != nil {
		t.Fatalf("node binary not installed: %v", err)
	}
	if fi, _ := os.Lstat(filepath.Join(h.Base, "opt/nimvor/runtimes/node", version, "bin/npx")); fi == nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("npx symlink not created")
	}
	if h.Exists("opt/nimvor/runtimes/node/" + version + ".tmp") {
		t.Error("the temporary unpack folder was left behind")
	}

	// It now shows up in the status, and resolves as a worker runtime.
	stA, _ := r.Lookup("runtime.status")
	sres, _ := stA.Handler(context.Background(), json.RawMessage(`{}`))
	st := sres.(Status)
	if len(st.Node) != 1 || st.Node[0].Version != version || !st.Node[0].Managed {
		t.Fatalf("status node: %+v", st.Node)
	}
	if bin, err := BinDir(h.Env, "node:"+version); err != nil || bin != filepath.Join("/opt/nimvor/runtimes/node", version, "bin") {
		t.Fatalf("BinDir: %q %v", bin, err)
	}
	if _, err := BinDir(h.Env, "node:19.9.9"); err == nil {
		t.Error("a missing runtime must not resolve")
	}
	if bin, err := BinDir(h.Env, "system"); err != nil || bin != "" {
		t.Errorf("system runtime should be empty: %q %v", bin, err)
	}

	// Installing it again is refused; a bad checksum fails the job.
	if _, err := inst.Handler(context.Background(), json.RawMessage(`{"kind":"node","version":"`+version+`"}`)); err == nil {
		t.Error("re-installing an existing runtime was not refused")
	}

	// Remove takes it away.
	rm, _ := r.Lookup("runtime.remove")
	if _, err := rm.Handler(context.Background(), json.RawMessage(`{"kind":"node","version":"`+version+`"}`)); err != nil {
		t.Fatal(err)
	}
	if h.Exists("opt/nimvor/runtimes/node/" + version) {
		t.Error("runtime folder not removed")
	}
}

func TestInstallRejectsBadChecksumAndInput(t *testing.T) {
	if nodeArch() == "" {
		t.Skip("no Node build for this architecture")
	}
	h := testhost.New(t)
	version := "20.17.0"
	top := fmt.Sprintf("node-v%s-linux-%s/", version, nodeArch())
	tarball := nodeTarball(t, top)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v"+version+"/SHASUMS256.txt" {
			// A wrong checksum.
			w.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000  node-v" + version + "-linux-" + nodeArch() + ".tar.gz\n"))
			return
		}
		w.Write(tarball)
	}))
	defer srv.Close()
	r := actions.NewRegistry(true)
	RegisterWith(r, h.Env, Sources{NodeBaseURL: srv.URL, Client: srv.Client()})
	inst, _ := r.Lookup("runtime.install")

	res, _ := inst.Handler(context.Background(), json.RawMessage(`{"kind":"node","version":"`+version+`"}`))
	job, _ := h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string))
	if job.Status != "failed" {
		t.Fatalf("a bad checksum must fail the install: %+v", job)
	}
	if h.Exists("opt/nimvor/runtimes/node/" + version) {
		t.Error("a runtime with a bad checksum was left installed")
	}

	for _, bad := range []string{
		`{"kind":"node","version":"latest"}`,
		`{"kind":"node","version":"20"}`,
		`{"kind":"python","version":"3.12"}`,
		`{"kind":"node","version":"../../etc"}`,
	} {
		if _, err := inst.Handler(context.Background(), json.RawMessage(bad)); err == nil {
			t.Errorf("accepted bad install input: %s", bad)
		}
	}
}

func TestStatusReportsSystemInterpreters(t *testing.T) {
	h := testhost.New(t)
	os.MkdirAll(filepath.Join(h.Base, "usr/bin"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "usr/bin/python3.12"), []byte("#!py\n"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "usr/bin/node"), []byte("#!node\n"), 0o755)
	r := actions.NewRegistry(true)
	RegisterWith(r, h.Env, DefaultSources)
	stA, _ := r.Lookup("runtime.status")
	res, _ := stA.Handler(context.Background(), json.RawMessage(`{}`))
	st := res.(Status)
	var py, node bool
	for _, s := range st.System {
		if s.Kind == "python" && s.Version == "3.12" {
			py = true
		}
		if s.Kind == "node" {
			node = true
		}
	}
	if !py || !node {
		t.Fatalf("system interpreters not reported: %+v", st.System)
	}

	// A Python site may select a system interpreter by version; BinDir
	// resolves it to the directory holding python<version>.
	if bin, err := BinDir(h.Env, "python:3.12"); err != nil || bin != "/usr/bin" {
		t.Fatalf("system python BinDir: %q %v", bin, err)
	}
	if _, err := BinDir(h.Env, "python:3.99"); err == nil {
		t.Fatal("a missing system python should be an error")
	}
}
