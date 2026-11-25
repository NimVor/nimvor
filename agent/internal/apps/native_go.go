package apps

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/workers"
)

// Cloudreve and Gopeed: single Go programs, installed like Gitea (a worker service behind the website's reverse
// proxy). Each is verified against a published SHA-256 before it is written, runs as the website's account and
// listens only on the website's loopback port.

func init() {
	nativeApps["cloudreve"] = nativeApp{manifest: cloudreveManifest(), install: installCloudreve}
	nativeApps["gopeed"] = nativeApp{manifest: gopeedManifest(), install: installGopeed}
}

// githubDownloads is where GitHub release files live; a variable so tests can serve them locally.
var githubDownloads = "https://github.com/"

// maxProgram bounds the program extracted from an archive.
const maxProgram = 400 << 20

// goArch is the release name of this server's CPU ("amd64" or "arm64"), or "".
func goArch() string { return giteaArch() }

// latestRelease is the newest stable release of a GitHub project: its tag, the version without a leading "v", and
// its assets by name.
func latestRelease(ctx context.Context, env *host.Env, src Sources, repo, name string) (tag, version string, assets map[string]struct{ URL, Digest string }, err error) {
	rels, err := githubReleases(ctx, src.Client, AppDef{Name: name, GitHub: repo}, "/releases/latest")
	if err != nil {
		return "", "", nil, err
	}
	if len(rels) == 0 || rels[0].Draft || rels[0].Prerelease || rels[0].TagName == "" {
		return "", "", nil, fmt.Errorf("no stable %s release was found", name)
	}
	tag = rels[0].TagName
	version = strings.TrimPrefix(tag, "v")
	if !versionTag.MatchString(version) {
		return "", "", nil, fmt.Errorf("the latest %s release (%q) is not a plain version number", name, tag)
	}
	assets = map[string]struct{ URL, Digest string }{}
	for _, a := range rels[0].Assets {
		if strings.HasPrefix(a.URL, githubDownloads+repo+"/releases/download/") {
			assets[a.Name] = struct{ URL, Digest string }{a.URL, a.Digest}
		}
	}
	return tag, version, assets, nil
}

// checksumLine finds "<sha256>  <name>" for name in a checksums file.
func checksumLine(text, name string) (string, error) {
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && strings.TrimPrefix(f[1], "*") == name && hex64.MatchString(strings.ToLower(f[0])) {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("the checksum file does not list %s", name)
}

// programFromArchive copies the one regular file called member (by base name) out of a .tar.gz or .zip into a
// temporary file in the agent's download folder and returns its path. Nothing else is extracted.
func programFromArchive(env *host.Env, format, archive, member string) (string, error) {
	dir := filepath.Join(env.Cfg.StateDir, "downloads")
	if _, err := env.Writer.MkdirAll(dir, 0o700, agentfs.RootOwner); err != nil {
		return "", err
	}
	real, err := env.Root.Path(dir)
	if err != nil {
		return "", err
	}
	out, err := os.CreateTemp(real, "program-*.bin")
	if err != nil {
		return "", err
	}
	defer out.Close()
	copyCapped := func(r io.Reader) error {
		n, err := io.Copy(out, io.LimitReader(r, maxProgram+1))
		if err != nil {
			return err
		}
		if n > maxProgram {
			return fmt.Errorf("the program in the archive is larger than %d MB", maxProgram>>20)
		}
		return nil
	}
	found := false
	switch format {
	case "targz":
		f, err := os.Open(archive)
		if err != nil {
			os.Remove(out.Name())
			return "", err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			os.Remove(out.Name())
			return "", fmt.Errorf("the download is not a valid archive")
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				os.Remove(out.Name())
				return "", fmt.Errorf("the archive could not be read: %v", err)
			}
			if h.Typeflag == tar.TypeReg && path.Base(h.Name) == member {
				if err := copyCapped(tr); err != nil {
					os.Remove(out.Name())
					return "", err
				}
				found = true
				break
			}
		}
	case "zip":
		zr, err := zip.OpenReader(archive)
		if err != nil {
			os.Remove(out.Name())
			return "", fmt.Errorf("the download is not a valid archive")
		}
		defer zr.Close()
		for _, zf := range zr.File {
			if !zf.FileInfo().IsDir() && zf.Mode().IsRegular() && path.Base(zf.Name) == member {
				rc, err := zf.Open()
				if err != nil {
					os.Remove(out.Name())
					return "", err
				}
				err = copyCapped(rc)
				rc.Close()
				if err != nil {
					os.Remove(out.Name())
					return "", err
				}
				found = true
				break
			}
		}
	}
	if !found {
		os.Remove(out.Name())
		return "", fmt.Errorf("the archive does not contain %s", member)
	}
	return out.Name(), nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// writeProgram streams the program file into <target>/bin/<name> as the website account.
func writeProgram(ctx context.Context, env *host.Env, cred *execx.Credential, target, dirs, name, file string, log io.Writer) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = asUser(ctx, env, cred, target, f, log, "umask 027 && mkdir -p bin "+dirs+" && cat > bin/"+name+".new && chmod 750 bin/"+name+".new && mv -f bin/"+name+".new bin/"+name)
	return err
}

// ---------------------------------------------------------------------------
// Cloudreve: file management and sharing (GPL-3.0).

func cloudreveManifest() Manifest {
	return Manifest{
		Schema: ManifestSchema, ID: "cloudreve", Name: "Cloudreve", Kind: KindApplication, Category: "files",
		Description: "Self-hosted file manager and cloud storage with sharing (a single program, SQLite).",
		Status:      StatusTesting,
		Runtime:     RuntimeSpec{Type: "binary"},
		Database:    DatabaseSpec{Type: "sqlite"},
		Strategy:    "binary-service",
		Source: SourceSpec{
			Kind: "github-release", GitHub: "cloudreve/Cloudreve", AssetRe: `^cloudreve_[0-9.]+_linux_(amd64|arm64)\.tar\.gz$`,
			Integrity: "checksums-file",
		},
		Service: &ServiceSpec{Type: "worker", Proxy: true},
		Health:  HealthSpec{Type: "http", Path: "/"},
		Backup:  "files", Upgrade: "binary-replace", Uninstall: "files",
		License: LicenseSpec{SPDX: "GPL-3.0", URL: "https://github.com/cloudreve/Cloudreve/blob/master/LICENSE"},
		Compat:  Compat{Arch: []string{"amd64", "arm64"}, MinDiskMB: 500, MinRAMMB: 256},
		// Proven by a real installation (.github/workflows/app-native.yml) before it leaves testing.
		Verified: "realhost:TestRealHostCloudreveInstall",
	}
}

// cloudreveConfig is conf.ini: the same sections Cloudreve writes on its first start, with a loopback-only listener
// and random secrets of its own.
func cloudreveConfig(port int, sessionSecret, hashidSalt string) string {
	return fmt.Sprintf("[System]\nDebug = false\nMode = master\nListen = 127.0.0.1:%d\nSessionSecret = %s\nHashIDSalt = %s\n", port, sessionSecret, hashidSalt)
}

func installCloudreve(ctx context.Context, env *host.Env, src Sources, r Request, cred *execx.Credential, log io.Writer) (map[string]any, error) {
	root, err := env.Root.Path(r.Root)
	if err != nil {
		return nil, fmt.Errorf("the website folder could not be resolved")
	}
	target := filepath.Join(root, r.Path)
	arch := goArch()
	if arch == "" {
		return nil, fmt.Errorf("Cloudreve is not available for this server's CPU (%s)", runtime.GOARCH)
	}
	if err := serviceFree(env, r.ServiceSlug); err != nil {
		return nil, err
	}

	fmt.Fprintln(log, "Looking up the newest stable Cloudreve release…")
	tag, version, assets, err := latestRelease(ctx, env, src, "cloudreve/Cloudreve", "Cloudreve")
	if err != nil {
		return nil, err
	}
	name := "cloudreve_" + version + "_linux_" + arch + ".tar.gz"
	asset, ok := assets[name]
	sums, hasSums := assets["checksums.txt"]
	if !ok || !hasSums {
		return nil, fmt.Errorf("the Cloudreve %s release has no %s with a checksums.txt; nothing was installed", tag, name)
	}
	fmt.Fprintf(log, "Cloudreve %s.\n", version)

	text, err := fetchText(ctx, src.Client, sums.URL)
	if err != nil {
		return nil, fmt.Errorf("the Cloudreve checksums could not be read: %v", err)
	}
	want, err := checksumLine(text, name)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(log, "Downloading Cloudreve…")
	file, got, err := downloadSum(ctx, env, src.Client, asset.URL, "sha256", log)
	if err != nil {
		return nil, err
	}
	defer os.Remove(file)
	if got != want {
		return nil, fmt.Errorf("the Cloudreve download does not match its published checksum; nothing was installed")
	}
	fmt.Fprintln(log, "Checksum verified against the release's checksums.txt.")
	prog, err := programFromArchive(env, "targz", file, "cloudreve")
	if err != nil {
		return nil, err
	}
	defer os.Remove(prog)

	secret, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	salt, err := randomHex(32)
	if err != nil {
		return nil, err
	}

	existed := env.Root.Exists(target)
	created := []string{"bin", "data"}
	fail := func(err error) (map[string]any, error) {
		cleanup(context.Background(), env, cred, target, created, log)
		if !existed {
			if _, rerr := asUser(context.Background(), env, cred, root, nil, nil, "rmdir -- "+shQuote(r.Path)); rerr != nil {
				fmt.Fprintf(log, "Cleanup: the folder %s could not be removed: %v.\n", r.Path, rerr)
			}
		}
		return nil, err
	}
	if _, err := asUser(ctx, env, cred, root, nil, log, "umask 027 && mkdir -p -- "+shQuote(r.Path)); err != nil {
		return nil, fmt.Errorf("the folder %s could not be created", r.Path)
	}
	if err := writeProgram(ctx, env, cred, target, "data", "cloudreve", prog, log); err != nil {
		return fail(fmt.Errorf("the Cloudreve files could not be written"))
	}
	if _, err := asUser(ctx, env, cred, target, strings.NewReader(cloudreveConfig(r.Port, secret, salt)), log, "umask 077 && cat > data/conf.ini"); err != nil {
		return fail(fmt.Errorf("the Cloudreve configuration could not be written"))
	}
	fmt.Fprintln(log, "Wrote the program and its configuration.")

	spec := workers.Spec{
		Slug: r.ServiceSlug, Name: "Cloudreve", Domain: r.Domain, User: r.User, Root: r.Root, Directory: r.Path,
		// -w keeps its data folder in the website folder (not beside the program), -c names the file above.
		Command: shQuote(filepath.Join(target, "bin", "cloudreve")) + " -w -c " + shQuote(filepath.Join(target, "data", "conf.ini")),
		Env:     map[string]string{"HOME": target, "USER": r.User},
		Restart: "always", Procs: 1, Enabled: true,
	}
	if _, err := workers.Apply(ctx, env, spec); err != nil {
		return fail(fmt.Errorf("the Cloudreve service could not be created: %v", err))
	}
	fmt.Fprintln(log, "Created and started the Cloudreve service.")
	if err := healthCheck(ctx, r.Port, "/", 120*time.Second); err != nil {
		_, _ = workers.Remove(context.Background(), env, r.ServiceSlug)
		return fail(fmt.Errorf("Cloudreve started but is not healthy: %v. Its service was removed again", err))
	}
	fmt.Fprintln(log, "Cloudreve answers on its port. The first account registered on it becomes its administrator: register it right away.")
	return map[string]any{
		"app": "cloudreve", "version": version, "path": r.Path, "strategy": "binary-service", "port": r.Port,
		"staged": true, "finish_url": strings.TrimRight(r.URL, "/") + "/",
		"service": serviceResult(spec),
	}, nil
}

// ---------------------------------------------------------------------------
// Gopeed: a download manager with a web interface (GPL-3.0).

func gopeedManifest() Manifest {
	return Manifest{
		Schema: ManifestSchema, ID: "gopeed", Name: "Gopeed", Kind: KindApplication, Category: "files",
		Description: "A download manager (HTTP, BitTorrent, magnet links) with a web interface.",
		Status:      StatusTesting,
		Runtime:     RuntimeSpec{Type: "binary"},
		Database:    DatabaseSpec{Type: "none"},
		Strategy:    "binary-service",
		Source: SourceSpec{
			Kind: "github-release", GitHub: "GopeedLab/gopeed", AssetRe: `^gopeed-web-v[0-9.]+-linux-(amd64|arm64)\.zip$`,
			// Gopeed publishes no checksum file: GitHub's own SHA-256 of each release file (the "digest" of the
			// release asset) is the check, and the install refuses when GitHub reports none.
			Integrity: "release-digest",
		},
		Service: &ServiceSpec{Type: "worker", Proxy: true},
		Health:  HealthSpec{Type: "http", Path: "/"},
		Backup:  "files", Upgrade: "binary-replace", Uninstall: "files",
		License:  LicenseSpec{SPDX: "GPL-3.0", URL: "https://github.com/GopeedLab/gopeed/blob/main/LICENSE"},
		Compat:   Compat{Arch: []string{"amd64", "arm64"}, MinDiskMB: 300, MinRAMMB: 128},
		Verified: "realhost:TestRealHostGopeedInstall",
	}
}

// gopeedConfig is config.json: loopback only, one login, storage inside the website folder. Gopeed shows no login
// at all when no password is set, so one is always written.
func gopeedConfig(r Request, target string) ([]byte, error) {
	return json.MarshalIndent(map[string]any{
		"address": "127.0.0.1", "port": r.Port, "username": r.AdminUser, "password": r.AdminPassword,
		"storageDir": filepath.Join(target, "storage"),
	}, "", "  ")
}

func installGopeed(ctx context.Context, env *host.Env, src Sources, r Request, cred *execx.Credential, log io.Writer) (map[string]any, error) {
	root, err := env.Root.Path(r.Root)
	if err != nil {
		return nil, fmt.Errorf("the website folder could not be resolved")
	}
	target := filepath.Join(root, r.Path)
	arch := goArch()
	if arch == "" {
		return nil, fmt.Errorf("Gopeed is not available for this server's CPU (%s)", runtime.GOARCH)
	}
	if err := serviceFree(env, r.ServiceSlug); err != nil {
		return nil, err
	}

	fmt.Fprintln(log, "Looking up the newest stable Gopeed release…")
	tag, version, assets, err := latestRelease(ctx, env, src, "GopeedLab/gopeed", "Gopeed")
	if err != nil {
		return nil, err
	}
	name := "gopeed-web-v" + version + "-linux-" + arch + ".zip"
	asset, ok := assets[name]
	if !ok {
		return nil, fmt.Errorf("the Gopeed %s release has no %s; nothing was installed", tag, name)
	}
	want := strings.ToLower(strings.TrimPrefix(asset.Digest, "sha256:"))
	if !strings.HasPrefix(asset.Digest, "sha256:") || !hex64.MatchString(want) {
		return nil, fmt.Errorf("GitHub reports no SHA-256 for %s, so it cannot be verified; nothing was installed", name)
	}
	fmt.Fprintf(log, "Gopeed %s.\n", version)
	fmt.Fprintln(log, "Downloading Gopeed…")
	file, got, err := downloadSum(ctx, env, src.Client, asset.URL, "sha256", log)
	if err != nil {
		return nil, err
	}
	defer os.Remove(file)
	if got != want {
		return nil, fmt.Errorf("the Gopeed download does not match the SHA-256 GitHub reports; nothing was installed")
	}
	fmt.Fprintln(log, "Checksum verified against the SHA-256 GitHub reports for the release file.")
	prog, err := programFromArchive(env, "zip", file, "gopeed")
	if err != nil {
		return nil, err
	}
	defer os.Remove(prog)
	cfg, err := gopeedConfig(r, target)
	if err != nil {
		return nil, err
	}

	existed := env.Root.Exists(target)
	created := []string{"bin", "storage", "config.json"}
	fail := func(err error) (map[string]any, error) {
		cleanup(context.Background(), env, cred, target, created, log)
		if !existed {
			if _, rerr := asUser(context.Background(), env, cred, root, nil, nil, "rmdir -- "+shQuote(r.Path)); rerr != nil {
				fmt.Fprintf(log, "Cleanup: the folder %s could not be removed: %v.\n", r.Path, rerr)
			}
		}
		return nil, err
	}
	if _, err := asUser(ctx, env, cred, root, nil, log, "umask 027 && mkdir -p -- "+shQuote(r.Path)); err != nil {
		return nil, fmt.Errorf("the folder %s could not be created", r.Path)
	}
	if err := writeProgram(ctx, env, cred, target, "storage", "gopeed", prog, log); err != nil {
		return fail(fmt.Errorf("the Gopeed files could not be written"))
	}
	if _, err := asUser(ctx, env, cred, target, strings.NewReader(string(cfg)), log, "umask 077 && cat > config.json"); err != nil {
		return fail(fmt.Errorf("the Gopeed configuration could not be written"))
	}
	fmt.Fprintln(log, "Wrote the program and its configuration.")

	spec := workers.Spec{
		Slug: r.ServiceSlug, Name: "Gopeed", Domain: r.Domain, User: r.User, Root: r.Root, Directory: r.Path,
		Command: shQuote(filepath.Join(target, "bin", "gopeed")) + " -c " + shQuote(filepath.Join(target, "config.json")),
		Env:     map[string]string{"HOME": target, "USER": r.User},
		Restart: "always", Procs: 1, Enabled: true,
	}
	if _, err := workers.Apply(ctx, env, spec); err != nil {
		return fail(fmt.Errorf("the Gopeed service could not be created: %v", err))
	}
	fmt.Fprintln(log, "Created and started the Gopeed service.")
	if err := healthCheck(ctx, r.Port, "/", 90*time.Second); err != nil {
		_, _ = workers.Remove(context.Background(), env, r.ServiceSlug)
		return fail(fmt.Errorf("Gopeed started but is not healthy: %v. Its service was removed again", err))
	}
	fmt.Fprintf(log, "Gopeed answers on its port. Sign in as %s with the password shown once by the panel.\n", r.AdminUser)
	return map[string]any{
		"app": "gopeed", "version": version, "path": r.Path, "strategy": "binary-service", "port": r.Port,
		"admin_user": r.AdminUser, "staged": true, "finish_url": strings.TrimRight(r.URL, "/") + "/",
		"service": serviceResult(spec),
	}, nil
}
