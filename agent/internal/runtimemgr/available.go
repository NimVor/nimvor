package runtimemgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Version is one installable runtime version offered to the administrator.
type Version struct {
	Version   string `json:"version"`
	Label     string `json:"label"`     // e.g. "LTS Jod", "Current", "Stable"
	Installed bool   `json:"installed"` // already under the runtimes directory
}

// Available is the installable versions, per runtime. A source that could
// not be reached leaves its list empty and sets its error.
type AvailableList struct {
	Node        []Version `json:"node"`
	Python      []Version `json:"python"`
	NodeError   string    `json:"node_error,omitempty"`
	PythonError string    `json:"python_error,omitempty"`
}

const (
	maxNodeLines   = 4 // newest LTS lines offered
	minPythonMinor = 9 // oldest Python 3.x offered
)

var pythonAssetRe = regexp.MustCompile(`^cpython-(3\.[0-9]+\.[0-9]+)\+[0-9]+-(.+)-install_only\.tar\.gz$`)

func runtimeLabel(kind string) string {
	if kind == "python" {
		return "Python"
	}
	return "Node.js"
}

// pythonTriple is the python-build-standalone target for this server.
func pythonTriple() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64-unknown-linux-gnu"
	case "arm64":
		return "aarch64-unknown-linux-gnu"
	}
	return ""
}

// semver parses "a.b.c" for ordering; ok is false for anything else.
func semver(v string) (a, b, c int, ok bool) {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return
	}
	var n [3]int
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil {
			return
		}
		n[i] = x
	}
	return n[0], n[1], n[2], true
}

func newer(x, y string) bool {
	xa, xb, xc, _ := semver(x)
	ya, yb, yc, _ := semver(y)
	if xa != ya {
		return xa > ya
	}
	if xb != yb {
		return xb > yb
	}
	return xc > yc
}

// Available fetches the stable versions that can be installed.
func Available(ctx context.Context, env *host.Env, src Sources) AvailableList {
	out := AvailableList{Node: []Version{}, Python: []Version{}}
	if nodeArch() != "" {
		v, err := availableNode(ctx, env, src)
		if err != nil {
			out.NodeError = err.Error()
		} else {
			out.Node = v
		}
	} else {
		out.NodeError = "This server's CPU architecture has no Node.js build."
	}
	if pythonTriple() != "" {
		v, err := availablePython(ctx, env, src)
		if err != nil {
			out.PythonError = err.Error()
		} else {
			out.Python = v
		}
	} else {
		out.PythonError = "This server's CPU architecture has no Python build."
	}
	return out
}

// availableNode lists the newest release of each recent LTS line, plus the
// newest Current release when it is not already an LTS one.
func availableNode(ctx context.Context, env *host.Env, src Sources) ([]Version, error) {
	body, err := fetchText(ctx, src.Client, strings.TrimRight(src.NodeBaseURL, "/")+"/index.json")
	if err != nil {
		return nil, fmt.Errorf("The Node.js release list could not be fetched (%v).", err)
	}
	var rel []struct {
		Version string          `json:"version"`
		LTS     json.RawMessage `json:"lts"`
		Files   []string        `json:"files"`
	}
	if err := json.Unmarshal([]byte(body), &rel); err != nil {
		return nil, fmt.Errorf("The Node.js release list was not understood.")
	}
	want := "linux-" + nodeArch()
	bestByMajor := map[int]Version{}
	var current *Version
	var majors []int
	for _, r := range rel {
		a, _, _, ok := semver(r.Version)
		if !ok || !hasString(r.Files, want) {
			continue
		}
		ver := strings.TrimPrefix(r.Version, "v")
		label := ""
		var name string
		if json.Unmarshal(r.LTS, &name) == nil && name != "" {
			label = "LTS " + name
		}
		if cur, seen := bestByMajor[a]; seen {
			if !newer(ver, cur.Version) {
				continue
			}
		} else {
			majors = append(majors, a)
		}
		bestByMajor[a] = Version{Version: ver, Label: label}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(majors)))
	var out []Version
	lts := 0
	for i, m := range majors {
		v := bestByMajor[m]
		if v.Label == "" {
			if i == 0 {
				v.Label = "Current"
				current = &v
			}
			continue
		}
		if lts < maxNodeLines {
			out = append(out, v)
			lts++
		}
	}
	if current != nil {
		out = append([]Version{*current}, out...)
	}
	for i := range out {
		out[i].Installed = env.Root.Exists(filepath.Join(host.RuntimesDir, "node", out[i].Version))
	}
	if out == nil {
		out = []Version{}
	}
	return out, nil
}

func hasString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

type pyRelease struct {
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func fetchPythonRelease(ctx context.Context, src Sources) (*pyRelease, error) {
	body, err := fetchText(ctx, src.Client, src.PythonReleaseURL)
	if err != nil {
		return nil, fmt.Errorf("The Python release list could not be fetched (%v).", err)
	}
	var rel pyRelease
	if err := json.Unmarshal([]byte(body), &rel); err != nil {
		return nil, fmt.Errorf("The Python release list was not understood.")
	}
	return &rel, nil
}

// availablePython lists the newest patch release of each Python 3.x minor
// version in the current python-build-standalone release.
func availablePython(ctx context.Context, env *host.Env, src Sources) ([]Version, error) {
	rel, err := fetchPythonRelease(ctx, src)
	if err != nil {
		return nil, err
	}
	best := map[string]string{} // minor -> newest patch
	for _, a := range rel.Assets {
		m := pythonAssetRe.FindStringSubmatch(a.Name)
		if m == nil || m[2] != pythonTriple() {
			continue
		}
		_, minor, _, ok := semver(m[1])
		if !ok || minor < minPythonMinor {
			continue
		}
		key := m[1][:strings.LastIndex(m[1], ".")]
		if cur, seen := best[key]; !seen || newer(m[1], cur) {
			best[key] = m[1]
		}
	}
	out := []Version{}
	for _, v := range best {
		out = append(out, Version{Version: v, Label: "Stable", Installed: env.Root.Exists(filepath.Join(host.RuntimesDir, "python", v))})
	}
	sort.Slice(out, func(i, j int) bool { return newer(out[i].Version, out[j].Version) })
	return out, nil
}

// installPython downloads a prebuilt CPython from python-build-standalone,
// verifies it against the release's SHA256SUMS and unpacks it to
// <runtimes>/python/<version>.
func installPython(ctx context.Context, env *host.Env, src Sources, version string, log io.Writer) (*Runtime, error) {
	rel, err := fetchPythonRelease(ctx, src)
	if err != nil {
		return nil, actions.Failed("The Python release list could not be fetched.", err.Error())
	}
	var file, sumsURL string
	for _, a := range rel.Assets {
		if a.Name == "SHA256SUMS" {
			sumsURL = a.URL
		}
		if m := pythonAssetRe.FindStringSubmatch(a.Name); m != nil && m[1] == version && m[2] == pythonTriple() {
			file = a.Name
			if err := httpsOnly(a.URL); err != nil {
				return nil, actions.Failed("The Python download address was refused.", err.Error())
			}
		}
	}
	if file == "" {
		return nil, actions.Failed("Python "+version+" is not available for this server.", "no build named "+version+" in the current release")
	}
	if sumsURL == "" {
		return nil, actions.Failed("The Python checksums could not be found.", "the release has no SHA256SUMS")
	}
	var url string
	for _, a := range rel.Assets {
		if a.Name == file {
			url = a.URL
		}
	}
	fmt.Fprintf(log, "Downloading Python %s (%s)…\n", version, pythonTriple())
	tarball, sum, err := download(ctx, env, src.Client, url, log)
	if err != nil {
		return nil, actions.Failed("The Python download failed.", err.Error())
	}
	defer os.Remove(tarball)
	sums, err := fetchText(ctx, src.Client, sumsURL)
	if err != nil {
		return nil, actions.Failed("The Python checksums could not be fetched.", err.Error())
	}
	want := checksumFor(sums, file)
	if want == "" {
		return nil, actions.Failed("Python "+version+" was not found for this server.", "no checksum for "+file)
	}
	if want != sum {
		return nil, actions.Failed("The Python download did not match its published checksum.", "")
	}
	fmt.Fprintf(log, "Verified. Unpacking…\n")
	destDir := filepath.Join(host.RuntimesDir, "python")
	if _, err := env.Writer.MkdirAll(destDir, 0o755, agentfs.RootOwner); err != nil {
		return nil, err
	}
	real, err := env.Root.Path(filepath.Join(destDir, version))
	if err != nil {
		return nil, err
	}
	tmp := real + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := extractTarGz(tarball, tmp, "python/"); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, actions.Failed("The Python archive could not be unpacked.", err.Error())
	}
	if err := os.Rename(tmp, real); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, actions.Failed("The Python runtime could not be placed.", err.Error())
	}
	fmt.Fprintf(log, "Installed Python %s.\n", version)
	return &Runtime{Kind: "python", Version: version, Bin: filepath.Join(destDir, version, "bin"), Binary: filepath.Join(destDir, version, "bin", "python3"), Managed: true}, nil
}

func httpsOnly(u string) error {
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://127.0.0.1") {
		return nil
	}
	return fmt.Errorf("not an https address")
}
