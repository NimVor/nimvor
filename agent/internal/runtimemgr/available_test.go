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

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func pythonTarball(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "python/bin/", Mode: 0o755, Typeflag: tar.TypeDir})
	body := "#!py\n"
	tw.WriteHeader(&tar.Header{Name: "python/bin/python3", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestAvailableListsStableNodeAndPython(t *testing.T) {
	if nodeArch() == "" || pythonTriple() == "" {
		t.Skip("no build for this architecture")
	}
	h := testhost.New(t)
	os.MkdirAll(filepath.Join(h.Base, "opt/whacp/runtimes/node/22.9.0/bin"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "opt/whacp/runtimes/node/22.9.0/bin/node"), []byte("x"), 0o755)

	file := "linux-" + nodeArch()
	index := fmt.Sprintf(`[
	 {"version":"v23.1.0","lts":false,"files":["%[1]s"]},
	 {"version":"v22.9.0","lts":"Jod","files":["%[1]s"]},
	 {"version":"v22.8.0","lts":false,"files":["%[1]s"]},
	 {"version":"v20.18.0","lts":"Iron","files":["%[1]s"]},
	 {"version":"v20.17.0","lts":"Iron","files":["%[1]s"]},
	 {"version":"v19.9.0","lts":false,"files":["%[1]s"]},
	 {"version":"v18.20.4","lts":"Hydrogen","files":["osx-x64"]}
	]`, file)
	tri := pythonTriple()
	py := fmt.Sprintf(`{"assets":[
	 {"name":"cpython-3.13.0+20241016-%[1]s-install_only.tar.gz","browser_download_url":"https://x/a"},
	 {"name":"cpython-3.12.7+20241016-%[1]s-install_only.tar.gz","browser_download_url":"https://x/b"},
	 {"name":"cpython-3.12.6+20241016-%[1]s-install_only.tar.gz","browser_download_url":"https://x/c"},
	 {"name":"cpython-3.8.20+20241016-%[1]s-install_only.tar.gz","browser_download_url":"https://x/d"},
	 {"name":"cpython-3.11.10+20241016-%[1]s-install_only_stripped.tar.gz","browser_download_url":"https://x/e"},
	 {"name":"cpython-3.10.15+20241016-other-install_only.tar.gz","browser_download_url":"https://x/f"}
	]}`, tri)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dist/index.json":
			w.Write([]byte(index))
		case "/py":
			w.Write([]byte(py))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	got := Available(context.Background(), h.Env, Sources{NodeBaseURL: srv.URL + "/dist", PythonReleaseURL: srv.URL + "/py", Client: srv.Client()})
	if got.NodeError != "" || got.PythonError != "" {
		t.Fatalf("errors: %+v", got)
	}
	var nodes []string
	for _, v := range got.Node {
		nodes = append(nodes, v.Version)
	}
	if fmt.Sprint(nodes) != "[23.1.0 22.9.0 20.18.0]" {
		t.Fatalf("node versions: %v", nodes)
	}
	if got.Node[0].Label != "Current" || got.Node[1].Label != "LTS Jod" || !got.Node[1].Installed || got.Node[2].Installed {
		t.Fatalf("node labels/installed: %+v", got.Node)
	}
	var pys []string
	for _, v := range got.Python {
		pys = append(pys, v.Version)
	}
	if fmt.Sprint(pys) != "[3.13.0 3.12.7]" {
		t.Fatalf("python versions: %v", pys)
	}

	// An unreachable source reports an error but still returns the other list.
	bad := Available(context.Background(), h.Env, Sources{NodeBaseURL: srv.URL + "/missing", PythonReleaseURL: srv.URL + "/py", Client: srv.Client()})
	if bad.NodeError == "" || len(bad.Python) != 2 {
		t.Fatalf("partial failure: %+v", bad)
	}
}

func TestInstallPythonVerifiesAndUnpacks(t *testing.T) {
	if pythonTriple() == "" {
		t.Skip("no Python build for this architecture")
	}
	h := testhost.New(t)
	tarball := pythonTarball(t)
	sum := sha256.Sum256(tarball)
	name := "cpython-3.12.7+20241016-" + pythonTriple() + "-install_only.tar.gz"
	for _, good := range []bool{true, false} {
		want := hex.EncodeToString(sum[:])
		if !good {
			want = "0000000000000000000000000000000000000000000000000000000000000000"
		}
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/rel":
				fmt.Fprintf(w, `{"assets":[{"name":%q,"browser_download_url":%q},{"name":"SHA256SUMS","browser_download_url":%q}]}`, name, srv.URL+"/tar", srv.URL+"/sums")
			case "/tar":
				w.Write(tarball)
			case "/sums":
				fmt.Fprintf(w, "%s  %s\n", want, name)
			}
		}))
		reg := actions.NewRegistry(true)
		RegisterWith(reg, h.Env, Sources{NodeBaseURL: srv.URL, PythonReleaseURL: srv.URL + "/rel", Client: srv.Client()})
		inst, _ := reg.Lookup("runtime.install")
		res, err := inst.Handler(context.Background(), json.RawMessage(`{"kind":"python","version":"3.12.7"}`))
		if err != nil {
			t.Fatal(err)
		}
		job, _ := h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string))
		installed := h.Exists("opt/whacp/runtimes/python/3.12.7/bin/python3")
		if good && (job.Status != "succeeded" || !installed) {
			t.Fatalf("good install: %+v installed=%v", job, installed)
		}
		if !good && (job.Status != "failed" || installed) {
			t.Fatalf("bad checksum must fail: %+v installed=%v", job, installed)
		}
		if good {
			if bin, err := BinDir(h.Env, "python:3.12.7"); err != nil || bin != "/opt/whacp/runtimes/python/3.12.7/bin" {
				t.Fatalf("BinDir: %q %v", bin, err)
			}
			if _, err := inst.Handler(context.Background(), json.RawMessage(`{"kind":"python","version":"3.12.7"}`)); err == nil {
				t.Error("re-install must be refused")
			}
		}
		srv.Close()
		if good {
			os.RemoveAll(filepath.Join(h.Base, "opt/whacp/runtimes/python"))
		}
	}
}
