package modsec

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
	"github.com/NimVor/nimvor/agent/internal/txn"
)

// The OWASP Core Rule Set is installed separately from the connector: the
// distribution's package when there is one, otherwise the pinned CRS
// release (checksum-verified), unpacked by the agent (rule and data files
// only) into a versioned folder. It is a transaction like the connector's
// (same records, rolled back on failure, rollback in order). After it is
// installed, ModSecurity starts in detection only: websites set to block
// only log until an administrator allows blocking.

// Pinned Core Rule Set release (SHA-256 of the release's minimal archive).
const (
	CRSVersion = "4.18.0"
	crsSHA256  = "08b37e6616bba1573da3c568e67a2b09ca287797dfcbf08ec7b82d98505b2ad1"
	// CRSRoot holds releases NimVor installed (one folder per version).
	CRSRoot    = "/usr/local/lib/nimvor/coreruleset"
	maxCRSFile = 8 << 20
)

// CRSSource is where the release comes from (tests replace it).
type CRSSource struct {
	URL    string
	SHA256 string
	Client *http.Client
}

// DefaultCRSSource is the official release.
var DefaultCRSSource = CRSSource{
	URL:    "https://github.com/coreruleset/coreruleset/releases/download/v" + CRSVersion + "/coreruleset-" + CRSVersion + "-minimal.tar.gz",
	SHA256: crsSHA256,
	Client: DefaultSources.Client,
}

// nimvorCRS finds a release NimVor installed (the newest).
func nimvorCRS(e *host.Env) CRS {
	dirs, _ := e.Root.Glob(filepath.Join(CRSRoot, "*", "rules"))
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, rules := range dirs {
		base := filepath.Dir(rules)
		setup := filepath.Join(base, "crs-setup.conf")
		if confs, _ := e.Root.Glob(filepath.Join(rules, "*.conf")); len(confs) == 0 || !e.Root.Exists(setup) {
			continue
		}
		c := CRS{Available: true, Rules: rules, Setup: setup, Version: filepath.Base(base)}
		if p := filepath.Join(rules, "REQUEST-900-EXCLUSION-RULES-BEFORE-CRS.conf"); e.Root.Exists(p) {
			c.before = p
		}
		if p := filepath.Join(rules, "RESPONSE-999-EXCLUSION-RULES-AFTER-CRS.conf"); e.Root.Exists(p) {
			c.after = p
		}
		return c
	}
	return CRS{}
}

// CRSFiles unpacks a CRS release archive: rule and data files, the setup
// example (as crs-setup.conf) and the exclusion examples; nothing outside
// the release folder, no links, nothing else.
func CRSFiles(archive []byte) (map[string][]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Clean(h.Name)
		parts := strings.SplitN(name, "/", 2)
		if len(parts) != 2 || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
			continue
		}
		rel := parts[1]
		switch {
		case rel == "crs-setup.conf.example":
			rel = "crs-setup.conf"
		case strings.HasPrefix(rel, "rules/") && strings.Count(rel, "/") == 1 && (strings.HasSuffix(rel, ".conf") || strings.HasSuffix(rel, ".data") || strings.HasSuffix(rel, ".conf.example")):
			rel = strings.TrimSuffix(rel, ".example")
		default:
			continue
		}
		if h.Size > maxCRSFile {
			return nil, fmt.Errorf("%s is too large", name)
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxCRSFile+1))
		if err != nil {
			return nil, err
		}
		out[rel] = b
	}
	if _, ok := out["crs-setup.conf"]; !ok {
		return nil, errors.New("the archive has no crs-setup.conf.example")
	}
	n := 0
	for k := range out {
		if strings.HasPrefix(k, "rules/") && strings.HasSuffix(k, ".conf") {
			n++
		}
	}
	if n < 10 {
		return nil, errors.New("the archive has too few rule files")
	}
	return out, nil
}

// InstallCRS installs the Core Rule Set (if missing) and turns it on in
// detection only.
func InstallCRS(ctx context.Context, e *host.Env, src CRSSource, retentionDays int, log io.Writer) (map[string]any, error) {
	installMu.Lock()
	defer installMu.Unlock()
	allowInstallerPaths(e)
	for _, line := range txn.Recover(ctx, e, TxKind, Hooks(e, log), log) {
		fmt.Fprintln(log, line)
	}
	if c := FindCRS(e); c.Available {
		fmt.Fprintf(log, "The OWASP Core Rule Set %s is already installed (%s); nothing was changed.\n", orNone(c.Version), c.Rules)
		return map[string]any{"changed": false, "version": c.Version}, nil
	}
	if err := e.PrecheckNginx(ctx); err != nil {
		return nil, err
	}
	tx, err := txn.Begin(e, TxKind, log)
	if err != nil {
		return nil, err
	}
	meta := InstallMeta{OS: e.Platform.Info().ID + " " + e.Platform.Info().VersionID}
	if err := tx.Snapshot(snapshotMax, uniq([]string{"/etc/modsecurity", Dir(e), statePath(e)})...); err != nil {
		tx.Rollback(ctx, Hooks(e, log), err)
		return nil, fmt.Errorf("the backup before installing failed; nothing was changed: %v", err)
	}
	fail := func(cause error) (map[string]any, error) {
		if rerr := tx.Rollback(ctx, Hooks(e, log), cause); rerr != nil {
			return nil, fmt.Errorf("%v — and the rollback was incomplete: %v (transaction %s)", cause, rerr, tx.ID)
		}
		return nil, fmt.Errorf("%v — everything was rolled back (transaction %s)", cause, tx.ID)
	}
	if name, ok := e.Platform.PackageName("modsecurity-crs"); ok && pkgs.Available(e) {
		meta.Method = "crs (package)"
		tx.SetMeta(meta)
		before := pkgs.InstalledNative(ctx, e, []string{name})
		err := pkgs.InstallNative(ctx, e, log, []string{name})
		for p := range pkgs.InstalledNative(ctx, e, []string{name}) {
			if _, had := before[p]; !had {
				tx.Package(p)
			}
		}
		if err != nil {
			return fail(fmt.Errorf("the package %s could not be installed: %v", name, err))
		}
		meta.Sources = []SourceInfo{{Name: name, Origin: "package"}}
	} else {
		meta.Method = "crs (release)"
		tx.SetMeta(meta)
		archive, err := download(ctx, src.Client, src.URL)
		if err != nil {
			return fail(err)
		}
		if h := sha256.Sum256(archive); hex.EncodeToString(h[:]) != src.SHA256 {
			return fail(fmt.Errorf("the Core Rule Set download does not match its checksum; nothing was installed from it"))
		}
		tx.Logf("Downloaded %s (SHA-256 %s verified).", src.URL, src.SHA256)
		files, err := CRSFiles(archive)
		if err != nil {
			return fail(fmt.Errorf("the Core Rule Set archive is not usable: %v", err))
		}
		base := filepath.Join(CRSRoot, CRSVersion)
		if err := tx.MkdirAll(filepath.Join(base, "rules"), 0o755, agentfs.RootOwner); err != nil {
			return fail(err)
		}
		names := make([]string, 0, len(files))
		for n := range files {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if err := tx.WriteFile(filepath.Join(base, n), files[n], 0o644, agentfs.RootOwner); err != nil {
				return fail(err)
			}
		}
		meta.Sources = []SourceInfo{{Name: "coreruleset", Version: CRSVersion, Origin: src.URL, SHA256: src.SHA256}}
	}
	crs := FindCRS(e)
	if !crs.Available {
		return fail(errors.New("the Core Rule Set is still not found after the installation"))
	}
	// Turn it on, in detection only until an administrator allows blocking.
	// The state is read and written under the same lock as the other
	// ModSecurity actions, so a website's mode changed meanwhile is kept.
	mu.Lock()
	defer mu.Unlock()
	st := Load(e)
	st.Config.CRS.Enabled = true
	st.Config.DetectionOnly = true
	if st.Config.Enabled {
		if err := apply(ctx, e, st, nil); err != nil {
			return fail(fmt.Errorf("the rules do not load: %v", err))
		}
	} else {
		b, _ := json.MarshalIndent(st, "", "  ")
		if err := tx.WriteFile(statePath(e), b, 0o600, agentfs.RootOwner); err != nil {
			return fail(err)
		}
	}
	meta.Module = crs.Rules
	tx.SetMeta(meta)
	tx.Logf("OWASP Core Rule Set %s installed (%s), on in detection only.", orNone(crs.Version), crs.Rules)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if retentionDays <= 0 {
		retentionDays = 30
	}
	eff := effective(e)
	txn.Prune(e, TxKind, time.Duration(retentionDays)*24*time.Hour, func(t *txn.Tx) bool { return t.ID == eff })
	return map[string]any{"changed": true, "transaction": tx.ID, "version": crs.Version, "method": meta.Method, "detection_only": true}, nil
}

func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s could not be downloaded: %v", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s could not be downloaded (HTTP %d)", url, res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxDownload+1))
	if err != nil || len(b) > maxDownload {
		return nil, fmt.Errorf("%s could not be downloaded", url)
	}
	return b, nil
}
