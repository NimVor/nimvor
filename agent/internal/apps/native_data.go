package apps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/workers"
)

// ClickHouse (a column database for analytics, with its web query page): a single program from a GitHub release,
// installed like Gopeed: verified against the SHA-256 GitHub reports for the release file, run as a worker service
// under the website's account and published through the website's reverse proxy. binaryApp describes what differs
// between such programs (release file, configuration, start command), so another one is a short entry.

func init() {
	nativeApps["clickhouse"] = nativeApp{manifest: clickhouseManifest(), install: func(ctx context.Context, env *host.Env, src Sources, r Request, cred *execx.Credential, log io.Writer) (map[string]any, error) {
		return installBinary(ctx, env, src, r, cred, log, clickhouseApp)
	}}
}

// appFile is a file written into the install folder (private to the website's account).
type appFile struct {
	Path string // relative to the install folder
	Data string
}

// binaryApp describes a single-program application.
type binaryApp struct {
	ID, Name, Repo string
	TagRe          *regexp.Regexp
	// Asset is the release file's name for a CPU ("amd64" or "arm64"), as a pattern; the only match is used.
	Asset func(arch string) *regexp.Regexp
	// Archive is "" when the release file is the program itself, else "targz" or "zip" holding Member.
	Archive, Member string
	Dirs            []string // folders created next to bin/ (and removed again on failure)
	Leftovers       []string // files the program makes itself, also removed on failure
	Health          string
	HealthWithin    time.Duration
	Ready           string // what the job log says when it answers
	Memory          int    // MB limit for the service (0 = none)
	// Prepare returns the files, environment and command line of the service (program is its absolute path).
	Prepare func(r Request, target, program string) (files []appFile, env map[string]string, command string, err error)
}

// latestReleaseMatching is the newest stable release of a GitHub project whose tag matches tagRe, with its files.
func latestReleaseMatching(ctx context.Context, src Sources, repo, name string, tagRe *regexp.Regexp) (tag string, assets map[string]struct{ URL, Digest string }, err error) {
	rels, err := githubReleases(ctx, src.Client, AppDef{Name: name, GitHub: repo}, "/releases/latest")
	if err != nil {
		return "", nil, err
	}
	if len(rels) == 0 || rels[0].Draft || rels[0].Prerelease || rels[0].TagName == "" {
		return "", nil, fmt.Errorf("no stable %s release was found", name)
	}
	tag = rels[0].TagName
	if !tagRe.MatchString(tag) {
		return "", nil, fmt.Errorf("the latest %s release (%q) is not a plain version number", name, tag)
	}
	assets = map[string]struct{ URL, Digest string }{}
	prefix := strings.ToLower(githubDownloads + repo + "/releases/download/")
	for _, a := range rels[0].Assets {
		if strings.HasPrefix(strings.ToLower(a.URL), prefix) {
			assets[a.Name] = struct{ URL, Digest string }{a.URL, a.Digest}
		}
	}
	return tag, assets, nil
}

func installBinary(ctx context.Context, env *host.Env, src Sources, r Request, cred *execx.Credential, log io.Writer, b binaryApp) (map[string]any, error) {
	root, err := env.Root.Path(r.Root)
	if err != nil {
		return nil, fmt.Errorf("the website folder could not be resolved")
	}
	target := filepath.Join(root, r.Path)
	arch := goArch()
	if arch == "" {
		return nil, fmt.Errorf("%s is not available for this server's CPU (%s)", b.Name, runtime.GOARCH)
	}
	if err := serviceFree(env, r.ServiceSlug); err != nil {
		return nil, err
	}

	fmt.Fprintf(log, "Looking up the newest stable %s release…\n", b.Name)
	tag, assets, err := latestReleaseMatching(ctx, src, b.Repo, b.Name, b.TagRe)
	if err != nil {
		return nil, err
	}
	re := b.Asset(arch)
	var name string
	for n := range assets {
		if re.MatchString(n) {
			if name != "" {
				return nil, fmt.Errorf("the %s %s release has more than one file matching %s (%s, %s); nothing was installed", b.Name, tag, re, name, n)
			}
			name = n
		}
	}
	if name == "" {
		return nil, fmt.Errorf("the %s %s release has no file matching %s (files seen: %s); nothing was installed", b.Name, tag, re, assetNames(assets))
	}
	asset := assets[name]
	want := strings.ToLower(strings.TrimPrefix(asset.Digest, "sha256:"))
	if !strings.HasPrefix(asset.Digest, "sha256:") || !hex64.MatchString(want) {
		return nil, fmt.Errorf("GitHub reports no SHA-256 for %s, so it cannot be verified; nothing was installed", name)
	}
	version := strings.TrimPrefix(tag, "v")
	fmt.Fprintf(log, "%s %s.\n", b.Name, version)
	fmt.Fprintf(log, "Downloading %s…\n", b.Name)
	file, got, err := downloadSum(ctx, env, src.Client, asset.URL, "sha256", log)
	if err != nil {
		return nil, err
	}
	defer os.Remove(file)
	if got != want {
		return nil, fmt.Errorf("the %s download does not match the SHA-256 GitHub reports; nothing was installed", b.Name)
	}
	fmt.Fprintln(log, "Checksum verified against the SHA-256 GitHub reports for the release file.")
	prog := file
	if b.Archive != "" {
		prog, err = programFromArchive(env, b.Archive, file, b.Member)
		if err != nil {
			return nil, err
		}
		defer os.Remove(prog)
	}

	programPath := filepath.Join(target, "bin", b.ID)
	files, svcEnv, command, err := b.Prepare(r, target, programPath)
	if err != nil {
		return nil, err
	}

	existed := env.Root.Exists(target)
	created := append(append([]string{"bin"}, b.Dirs...), b.Leftovers...)
	for _, f := range files {
		created = append(created, strings.SplitN(f.Path, "/", 2)[0])
	}
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
	if err := writeProgram(ctx, env, cred, target, strings.Join(b.Dirs, " "), b.ID, prog, log); err != nil {
		return fail(fmt.Errorf("the %s files could not be written", b.Name))
	}
	for _, f := range files {
		script := "umask 077 && mkdir -p -- " + shQuote(filepath.Dir(f.Path)) + " && cat > " + shQuote(f.Path)
		if _, err := asUser(ctx, env, cred, target, strings.NewReader(f.Data), log, script); err != nil {
			return fail(fmt.Errorf("the %s configuration could not be written", b.Name))
		}
	}
	fmt.Fprintln(log, "Wrote the program and its configuration.")

	spec := workers.Spec{
		Slug: r.ServiceSlug, Name: b.Name, Domain: r.Domain, User: r.User, Root: r.Root, Directory: r.Path,
		Command: command, Env: svcEnv, Restart: "always", Procs: 1, MemoryMB: b.Memory, Enabled: true,
	}
	if _, err := workers.Apply(ctx, env, spec); err != nil {
		return fail(fmt.Errorf("the %s service could not be created: %v", b.Name, err))
	}
	fmt.Fprintf(log, "Created and started the %s service.\n", b.Name)
	if err := healthCheck(ctx, r.Port, b.Health, b.HealthWithin); err != nil {
		_, _ = workers.Remove(context.Background(), env, r.ServiceSlug)
		return fail(fmt.Errorf("%s started but is not healthy: %v. Its service was removed again", b.Name, err))
	}
	fmt.Fprintf(log, "%s answers on its port. %s\n", b.Name, b.Ready)
	return map[string]any{
		"app": b.ID, "version": version, "path": r.Path, "strategy": "binary-service", "port": r.Port,
		"admin_user": r.AdminUser, "staged": true, "finish_url": strings.TrimRight(r.URL, "/") + "/",
		"service": serviceResult(spec),
	}, nil
}

// ---------------------------------------------------------------------------
// ClickHouse (Apache-2.0): the server program with only its HTTP interface, on the loopback port.

func clickhouseManifest() Manifest {
	return Manifest{
		Schema: ManifestSchema, ID: "clickhouse", Name: "ClickHouse", Kind: KindApplication, Category: "developer-tools",
		Description: "A column-oriented database for analytics, with its web query page (the HTTP interface, behind the website).",
		Status:      StatusTesting,
		Runtime:     RuntimeSpec{Type: "binary"},
		Database:    DatabaseSpec{Type: "none"},
		Strategy:    "binary-service",
		Source: SourceSpec{
			Kind: "github-release", GitHub: "ClickHouse/ClickHouse", AssetRe: `^clickhouse-common-static-[0-9.]+-(amd64|arm64)\.tgz$`,
			Integrity: "release-digest",
		},
		Service: &ServiceSpec{Type: "worker", Proxy: true},
		Health:  HealthSpec{Type: "http", Path: "/ping"},
		Backup:  "files", Upgrade: "binary-replace", Uninstall: "files",
		License:  LicenseSpec{SPDX: "Apache-2.0", URL: "https://github.com/ClickHouse/ClickHouse/blob/master/LICENSE"},
		Compat:   Compat{Arch: []string{"amd64", "arm64"}, MinDiskMB: 3000, MinRAMMB: 2048},
		Verified: "realhost:TestRealHostClickHouseInstall",
	}
}

var clickhouseApp = binaryApp{
	ID: "clickhouse", Name: "ClickHouse", Repo: "ClickHouse/ClickHouse",
	TagRe:   regexp.MustCompile(`^v[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,4}\.[0-9]{1,4}-(lts|stable)$`),
	Archive: "targz", Member: "clickhouse",
	Asset: func(arch string) *regexp.Regexp {
		return regexp.MustCompile(`^clickhouse-common-static-[0-9.]+-` + arch + `\.tgz$`)
	},
	Dirs: []string{"data", "logs"}, Health: "/ping", HealthWithin: 180 * time.Second,
	Ready:  "Open the website and use the Play page (/play) to run queries.",
	Memory: 0,
	Prepare: func(r Request, target, program string) ([]appFile, map[string]string, string, error) {
		return []appFile{
				{Path: "config.xml", Data: clickhouseConfig(r.Port, target)},
				{Path: "users.xml", Data: clickhouseUsers(r.AdminUser, r.AdminPassword)},
			}, map[string]string{"HOME": target, "USER": r.User},
			shQuote(program) + " server --config-file=" + shQuote(filepath.Join(target, "config.xml")), nil
	},
}

// clickhouseConfig is config.xml: the HTTP interface on the loopback port only (no native, MySQL or PostgreSQL
// listeners, no interserver port), everything stored in the website's folder, and a memory limit.
func clickhouseConfig(port int, target string) string {
	data := filepath.Join(target, "data")
	return fmt.Sprintf(`<clickhouse>
    <logger>
        <level>warning</level>
        <log>%[3]s/clickhouse-server.log</log>
        <errorlog>%[3]s/clickhouse-server.err.log</errorlog>
        <size>50M</size>
        <count>3</count>
    </logger>
    <listen_host>127.0.0.1</listen_host>
    <http_port>%[1]d</http_port>
    <path>%[2]s/</path>
    <tmp_path>%[2]s/tmp/</tmp_path>
    <user_files_path>%[2]s/user_files/</user_files_path>
    <format_schema_path>%[2]s/format_schemas/</format_schema_path>
    <users_config>%[4]s</users_config>
    <default_profile>default</default_profile>
    <default_database>default</default_database>
    <max_server_memory_usage_to_ram_ratio>0.5</max_server_memory_usage_to_ram_ratio>
    <mark_cache_size>268435456</mark_cache_size>
    <send_crash_reports><enabled>false</enabled></send_crash_reports>
</clickhouse>
`, port, data, filepath.Join(target, "logs"), filepath.Join(target, "users.xml"))
}

// clickhouseUsers is users.xml: one user, with the password stored only as its SHA-256, allowed from this machine
// (the website's proxy) only.
func clickhouseUsers(user, password string) string {
	sum := sha256.Sum256([]byte(password))
	return fmt.Sprintf(`<clickhouse>
    <profiles><default/></profiles>
    <quotas><default/></quotas>
    <users>
        <%[1]s>
            <password_sha256_hex>%[2]s</password_sha256_hex>
            <networks><ip>127.0.0.1</ip><ip>::1</ip></networks>
            <profile>default</profile>
            <quota>default</quota>
            <access_management>1</access_management>
        </%[1]s>
    </users>
</clickhouse>
`, user, hex.EncodeToString(sum[:]))
}
