// Package runtimemgr installs extra Node.js runtimes side by side under
// /opt/whacp/runtimes and reports the Node.js and Python runtimes available
// on the server, so a website's workers can run on a chosen version without
// touching the system packages.
//
// A Node.js runtime is the official linux tarball from nodejs.org, verified
// against that release's SHASUMS256.txt before it is unpacked. It is placed
// at /opt/whacp/runtimes/node/<version>; nothing is run as root to install
// it, and a worker selects it by name (the launcher prepends its bin folder
// to PATH).
package runtimemgr

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Sources says where Node.js runtimes are downloaded from.
type Sources struct {
	NodeBaseURL string // e.g. https://nodejs.org/dist
	Client      *http.Client
}

// DefaultSources are the official download locations.
var DefaultSources = Sources{NodeBaseURL: "https://nodejs.org/dist", Client: &http.Client{Timeout: 15 * time.Minute}}

const maxDownload = 120 << 20 // a Node tarball is ~30 MB; cap well above that

var (
	// A WhaCP-managed runtime version (a folder name under the runtimes dir).
	versionRe = regexp.MustCompile(`^[0-9][0-9A-Za-z.]{0,31}$`)
	// An exact Node.js release, e.g. 20.17.0.
	nodeVersionRe = regexp.MustCompile(`^(0|[1-9][0-9]?)\.(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})$`)
	// System Python interpreters to look for.
	pythonMinor = []string{"3.8", "3.9", "3.10", "3.11", "3.12", "3.13", "3.14"}
)

// Runtime is one installed language runtime.
type Runtime struct {
	Kind    string `json:"kind"`    // "node" or "python"
	Version string `json:"version"` // folder name, e.g. "20.17.0" or "3.12"
	Bin     string `json:"bin"`     // the bin directory
	Binary  string `json:"binary"`  // the interpreter path
	Managed bool   `json:"managed"` // installed by WhaCP (vs. the system's own)
}

// Status is the runtimes available on the server.
type Status struct {
	Arch   string    `json:"arch"`   // the Node dist arch, "" when unsupported
	Node   []Runtime `json:"node"`   // managed Node.js runtimes
	Python []Runtime `json:"python"` // managed Python runtimes
	System []Runtime `json:"system"` // the system's own node and python interpreters
}

// nodeArch maps the Go architecture to the one in nodejs.org file names.
func nodeArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	case "arm":
		return "armv7l"
	case "ppc64le":
		return "ppc64le"
	case "s390x":
		return "s390x"
	}
	return ""
}

// List reports the runtimes on the server: the WhaCP-managed ones (folders
// under the runtimes directory) and the system's own node and python.
func List(env *host.Env) Status {
	st := Status{Arch: nodeArch(), Node: []Runtime{}, Python: []Runtime{}, System: []Runtime{}}
	st.Node = managed(env, "node", "bin/node")
	st.Python = managed(env, "python", "bin/python3")
	// The system's own interpreters (already on PATH; shown for reference).
	if env.Root.Exists("/usr/bin/node") || env.Root.Exists("/usr/bin/nodejs") {
		bin := "/usr/bin/node"
		if !env.Root.Exists(bin) {
			bin = "/usr/bin/nodejs"
		}
		st.System = append(st.System, Runtime{Kind: "node", Version: "system", Bin: "/usr/bin", Binary: bin})
	}
	for _, m := range pythonMinor {
		if p := "/usr/bin/python" + m; env.Root.Exists(p) {
			st.System = append(st.System, Runtime{Kind: "python", Version: m, Bin: "/usr/bin", Binary: p})
		}
	}
	return st
}

// managed lists the version folders under <runtimes>/<kind> whose expected
// interpreter exists.
func managed(env *host.Env, kind, rel string) []Runtime {
	out := []Runtime{}
	dir := filepath.Join(host.RuntimesDir, kind)
	real, err := env.Root.Path(dir)
	if err != nil {
		return out
	}
	ents, err := os.ReadDir(real)
	if err != nil {
		return out
	}
	for _, e := range ents {
		if !e.IsDir() || !versionRe.MatchString(e.Name()) {
			continue
		}
		bin := filepath.Join(dir, e.Name(), filepath.Dir(rel))
		binary := filepath.Join(dir, e.Name(), rel)
		if !env.Root.Exists(binary) {
			continue
		}
		out = append(out, Runtime{Kind: kind, Version: e.Name(), Bin: bin, Binary: binary, Managed: true})
	}
	return out
}

// BinDir resolves a worker's runtime selection ("node:<v>", "python:<v>" or
// "" / "system") to a bin directory to prepend to PATH. "" and "system"
// return an empty directory (no change). An unknown or not-installed runtime
// is an error so a worker is never silently started on the wrong one.
func BinDir(env *host.Env, selection string) (string, error) {
	if selection == "" || selection == "system" {
		return "", nil
	}
	kind, ver, ok := strings.Cut(selection, ":")
	if !ok || (kind != "node" && kind != "python") || !versionRe.MatchString(ver) {
		return "", fmt.Errorf("runtime: use node:<version>, python:<version> or system")
	}
	for _, rt := range managed(env, kind, map[string]string{"node": "bin/node", "python": "bin/python3"}[kind]) {
		if rt.Version == ver {
			return rt.Bin, nil
		}
	}
	return "", fmt.Errorf("the %s %s runtime is not installed", kind, ver)
}

// Register adds the runtime actions.
func Register(r *actions.Registry, env *host.Env) {
	RegisterWith(r, env, DefaultSources)
}

// RegisterWith is Register with explicit download sources (for tests).
func RegisterWith(r *actions.Registry, env *host.Env, src Sources) {
	r.Register(actions.Action{Name: "runtime.status", Description: "List the Node.js and Python runtimes available on the server (WhaCP-managed and the system's own).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return List(env), nil
	}})
	r.Register(actions.Action{Name: "runtime.install", Mutating: true, Description: "Install a Node.js runtime (an official nodejs.org release, verified) side by side under the runtimes directory (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Kind    string `json:"kind"`
			Version string `json:"version"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.Kind != "node" {
			return nil, actions.InvalidParams("kind: only Node.js runtimes can be installed here; install Python versions under Server → Packages")
		}
		if !nodeVersionRe.MatchString(in.Version) {
			return nil, actions.InvalidParams("version: an exact Node.js release, e.g. 20.17.0")
		}
		if nodeArch() == "" {
			return nil, actions.Conflict("This server's CPU architecture has no Node.js build.")
		}
		dest := filepath.Join(host.RuntimesDir, "node", in.Version)
		if env.Root.Exists(dest) {
			return nil, actions.Conflict("Node.js " + in.Version + " is already installed.")
		}
		j := env.Jobs.Start("runtime.install", "node:"+in.Version, func(ctx context.Context, log io.Writer) (any, error) {
			rt, err := installNode(ctx, env, src, in.Version, log)
			if err != nil {
				return nil, err
			}
			return rt, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "runtime.remove", Mutating: true, Description: "Remove a WhaCP-managed runtime.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Kind    string `json:"kind"`
			Version string `json:"version"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.Kind != "node" && in.Kind != "python" {
			return nil, actions.InvalidParams("kind: must be node or python")
		}
		if !versionRe.MatchString(in.Version) {
			return nil, actions.InvalidParams("version: invalid")
		}
		dir := filepath.Join(host.RuntimesDir, in.Kind, in.Version)
		real, err := env.Root.Path(dir)
		if err != nil {
			return nil, err
		}
		base, err := env.Root.Path(host.RuntimesDir)
		if err != nil {
			return nil, err
		}
		// Never delete anything outside the runtimes directory.
		if real != filepath.Join(base, in.Kind, in.Version) || !strings.HasPrefix(real, base+string(os.PathSeparator)) {
			return nil, actions.InvalidParams("version: invalid")
		}
		if !env.Root.IsDir(dir) {
			return nil, actions.NotFound("That runtime is not installed.")
		}
		if err := os.RemoveAll(real); err != nil {
			return nil, actions.Failed("The runtime could not be removed.", err.Error())
		}
		return map[string]any{"removed": in.Kind + ":" + in.Version}, nil
	}})
}

// installNode downloads a Node.js release, verifies it against the release's
// SHASUMS256.txt, and unpacks it to <runtimes>/node/<version>.
func installNode(ctx context.Context, env *host.Env, src Sources, version string, log io.Writer) (*Runtime, error) {
	arch := nodeArch()
	name := fmt.Sprintf("node-v%s-linux-%s", version, arch)
	base := strings.TrimRight(src.NodeBaseURL, "/") + "/v" + version
	fmt.Fprintf(log, "Downloading Node.js %s (%s)…\n", version, arch)
	file, sum, err := download(ctx, env, src.Client, base+"/"+name+".tar.gz", log)
	if err != nil {
		return nil, actions.Failed("The Node.js download failed.", err.Error())
	}
	defer os.Remove(file)
	sums, err := fetchText(ctx, src.Client, base+"/SHASUMS256.txt")
	if err != nil {
		return nil, actions.Failed("The Node.js checksums could not be fetched.", err.Error())
	}
	want := checksumFor(sums, name+".tar.gz")
	if want == "" {
		return nil, actions.Failed("Node.js "+version+" was not found for this server.", "no checksum for "+name)
	}
	if want != sum {
		return nil, actions.Failed("The Node.js download did not match its published checksum.", "")
	}
	fmt.Fprintf(log, "Verified. Unpacking…\n")
	destDir := filepath.Join(host.RuntimesDir, "node")
	if _, err := env.Writer.MkdirAll(destDir, 0o755, agentfs.RootOwner); err != nil {
		return nil, err
	}
	real, err := env.Root.Path(filepath.Join(destDir, version))
	if err != nil {
		return nil, err
	}
	tmp := real + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := extractTarGz(file, tmp, name+"/"); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, actions.Failed("The Node.js archive could not be unpacked.", err.Error())
	}
	if err := os.Rename(tmp, real); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, actions.Failed("The Node.js runtime could not be placed.", err.Error())
	}
	fmt.Fprintf(log, "Installed Node.js %s.\n", version)
	return &Runtime{Kind: "node", Version: version, Bin: filepath.Join(destDir, version, "bin"), Binary: filepath.Join(destDir, version, "bin", "node"), Managed: true}, nil
}

// download fetches url to a temp file under the state dir and returns the
// file path and its SHA-256 (hex).
func download(ctx context.Context, env *host.Env, client *http.Client, url string, log io.Writer) (string, string, error) {
	dir := filepath.Join(env.Cfg.StateDir, "downloads")
	if _, err := env.Writer.MkdirAll(dir, 0o700, agentfs.RootOwner); err != nil {
		return "", "", err
	}
	real, err := env.Root.Path(dir)
	if err != nil {
		return "", "", err
	}
	f, err := os.CreateTemp(real, "node-*.tar.gz")
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "WhaCP")
	res, err := client.Do(req)
	if err != nil {
		os.Remove(f.Name())
		return "", "", fmt.Errorf("download failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		os.Remove(f.Name())
		return "", "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, maxDownload+1))
	if err != nil || n > maxDownload {
		os.Remove(f.Name())
		return "", "", fmt.Errorf("download failed or too large")
	}
	return f.Name(), hex.EncodeToString(h.Sum(nil)), nil
}

func fetchText(ctx context.Context, client *http.Client, url string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "WhaCP")
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return string(b), err
}

// checksumFor returns the SHA-256 for file in a SHASUMS256.txt body ("<hex>  <file>").
func checksumFor(sums, file string) string {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && fields[1] == file && len(fields[0]) == 64 {
			return fields[0]
		}
	}
	return ""
}

// maxUnpack bounds the unpacked size of a runtime archive.
const maxUnpack = 1 << 30 // 1 GiB

// extractTarGz unpacks a .tar.gz into dest, stripping the leading strip
// path component. Entries must stay inside dest; a symlink may only point
// inside dest; devices and other special files are skipped.
func extractTarGz(archive, dest, strip string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(hdr.Name)), strip)
		if rel == hdr.Name || rel == "" || rel == "." {
			continue // not under the expected top folder (or the folder itself)
		}
		if strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") || strings.HasPrefix(rel, "/") {
			return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if target != dest && !strings.HasPrefix(target, dest+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if total += hdr.Size; total > maxUnpack {
				return fmt.Errorf("the archive is larger than %d bytes", maxUnpack)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			w, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o755)
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, io.LimitReader(tr, maxUnpack)); err != nil {
				w.Close()
				return err
			}
			w.Close()
		case tar.TypeSymlink:
			// The link target must resolve inside dest.
			link := hdr.Linkname
			var resolved string
			if filepath.IsAbs(link) {
				resolved = filepath.Clean(link)
			} else {
				resolved = filepath.Join(filepath.Dir(target), link)
			}
			if resolved != dest && !strings.HasPrefix(resolved, dest+string(os.PathSeparator)) {
				return fmt.Errorf("unsafe symlink in archive: %s -> %s", hdr.Name, link)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(link, target); err != nil {
				return err
			}
		default:
			// Hard links, devices, fifos: skipped.
		}
	}
	return nil
}
