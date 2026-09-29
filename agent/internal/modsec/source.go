package modsec

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/capability"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/pkgs"
)

// buildPackages are what compiling needs (this system's names): the base
// set, libmodsecurity's development package, and what building
// libmodsecurity itself needs when that package does not exist.
func buildPackages(e *host.Env) (base []string, libPkg string, libBuild []string) {
	if e.Platform.PackageManager() == "dnf" {
		return []string{"gcc", "make", "pcre2-devel", "zlib-devel", "openssl-devel", "pkgconf-pkg-config", "dnf-plugins-core", "cpio"},
			"libmodsecurity-devel",
			[]string{"gcc-c++", "libxml2-devel", "yajl-devel", "libcurl-devel", "lmdb-devel", "libmaxminddb-devel"}
	}
	return []string{"gcc", "make", "libc6-dev", "libpcre2-dev", "zlib1g-dev", "libssl-dev", "pkgconf"},
		"libmodsecurity-dev",
		[]string{"g++", "libxml2-dev", "libyajl-dev", "libcurl4-openssl-dev", "liblmdb-dev", "libmaxminddb-dev"}
}

// installRecorded installs packages and records the ones that are new.
func (in *installer) installRecorded(ctx context.Context, names []string) error {
	before := pkgs.InstalledNative(ctx, in.e, names)
	var want []string
	for _, n := range names {
		if _, ok := before[n]; !ok {
			want = append(want, n)
		}
	}
	if len(want) == 0 {
		return nil
	}
	in.logf("Installing %s…", strings.Join(want, " "))
	err := pkgs.InstallNative(ctx, in.e, in.log, want)
	after := pkgs.InstalledNative(ctx, in.e, want)
	for _, n := range want {
		if _, ok := after[n]; ok {
			if rerr := in.tx.Package(n); rerr != nil {
				return rerr
			}
		}
	}
	return err
}

// buildAccount makes sure the unprivileged build account exists.
func (in *installer) buildAccount(ctx context.Context) (*execx.Credential, error) {
	if _, err := in.e.LookupUser(BuildAccount); err != nil {
		if err := in.tx.Account(BuildAccount); err != nil {
			return nil, err
		}
		if out, err := in.e.Runner.Run(ctx, "useradd", "--create-home", "--home-dir", BuildHome, "--shell", "/usr/sbin/nologin", "--comment", "WhaCP build account", "--user-group", BuildAccount); err != nil {
			return nil, fmt.Errorf("the build account could not be created: %s", host.Output(out))
		}
		in.createdAccount = true
	}
	cred, _, err := in.e.Credential(BuildAccount)
	if err != nil {
		return nil, fmt.Errorf("the build account %s: %v", BuildAccount, err)
	}
	return cred, nil
}

func (in *installer) cleanupBuild(ctx context.Context) {
	if in.build == "" {
		return
	}
	if in.createdAccount {
		if _, err := in.e.LookupUser(BuildAccount); err == nil {
			in.e.Runner.Run(ctx, "userdel", "--remove", BuildAccount)
		}
	}
	if real, err := in.e.Root.Path(in.build); err == nil {
		os.RemoveAll(real)
	}
	in.build = ""
}

// fetch downloads url into the build folder, checking its SHA-256.
func (in *installer) fetch(ctx context.Context, url, sum, name string, owner agentfs.Owner) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := in.src.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s could not be downloaded: %v", name, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s could not be downloaded (HTTP %d from %s)", name, res.StatusCode, url)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxDownload+1))
	if err != nil || len(b) > maxDownload {
		return "", fmt.Errorf("%s could not be downloaded", name)
	}
	h := sha256.Sum256(b)
	if got := hex.EncodeToString(h[:]); got != sum {
		return "", fmt.Errorf("%s does not match its checksum (expected %s, got %s); nothing was built from it", name, sum, got)
	}
	p := filepath.Join(in.build, name)
	if _, err := in.e.Writer.WriteFile(p, b, 0o644, owner); err != nil {
		return "", err
	}
	in.logf("Downloaded %s (%d bytes, SHA-256 %s verified).", url, len(b), sum)
	return p, nil
}

// as runs a build command as the build account in dir.
func (in *installer) as(ctx context.Context, cred *execx.Credential, dir string, env []string, id string, args ...string) error {
	in.logf("$ %s %s", id, strings.Join(args, " "))
	out, err := in.e.Runner.RunWith(ctx, id, execx.Opts{As: cred, Dir: dir, Env: env, Output: in.log, Timeout: -1}, args...)
	if err != nil {
		return fmt.Errorf("%s %s failed: %v %s", id, firstArg(args), err, tail(string(out), 1500))
	}
	return nil
}

func firstArg(a []string) string {
	if len(a) == 0 {
		return ""
	}
	return a[0]
}

func tail(s string, n int) string {
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// extract unpacks a tarball in the build folder and returns the folder
// holding file (e.g. "configure").
func (in *installer) extract(ctx context.Context, cred *execx.Credential, tarball, pattern string) (string, error) {
	if err := in.as(ctx, cred, in.realBuild(), nil, "tar", "-xf", in.real(tarball), "-C", in.realBuild()); err != nil {
		return "", err
	}
	matches, _ := filepath.Glob(filepath.Join(in.realBuild(), pattern))
	if len(matches) != 1 {
		return "", fmt.Errorf("the source archive %s has an unexpected layout", filepath.Base(tarball))
	}
	return filepath.Dir(matches[0]), nil
}

func (in *installer) real(p string) string {
	r, _ := in.e.Root.Path(p)
	return r
}

func (in *installer) realBuild() string { return in.real(in.build) }

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// fromSource builds the connector for the installed Nginx.
func (in *installer) fromSource(ctx context.Context) error {
	if !in.n.Compat {
		return actions.Conflict("This Nginx was built without --with-compat, so a module cannot be built for it separately. Install a connector built together with Nginx, or an Nginx package built with --with-compat.")
	}
	base, libPkg, libBuild := buildPackages(in.e)
	if err := in.installRecorded(ctx, base); err != nil {
		return fmt.Errorf("the build dependencies could not be installed: %v", err)
	}
	libFromSource := false
	if err := in.installRecorded(ctx, []string{libPkg}); err != nil {
		in.logf("libmodsecurity is not available as a package (%v); it is built from source.", err)
		libFromSource = true
		if err := in.installRecorded(ctx, libBuild); err != nil {
			return fmt.Errorf("the dependencies of libmodsecurity could not be installed: %v", err)
		}
	}
	cred, err := in.buildAccount(ctx)
	if err != nil {
		return err
	}
	owner := agentfs.Owner{UID: int(cred.UID), GID: int(cred.GID)}
	in.build = filepath.Join(BuildHome, in.tx.ID)
	if _, err := in.e.Writer.MkdirAll(in.build, 0o700, owner); err != nil {
		return err
	}

	// Sources: the connector (and libmodsecurity), then Nginx's own.
	conTar, err := in.fetch(ctx, in.src.Connector, in.src.ConnectorSHA256, "ModSecurity-nginx-v"+ConnectorVersion+".tar.gz", owner)
	if err != nil {
		return err
	}
	in.meta.Sources = append(in.meta.Sources, SourceInfo{Name: "ModSecurity-nginx", Version: ConnectorVersion, Origin: in.src.Connector, SHA256: in.src.ConnectorSHA256})
	conDir, err := in.extract(ctx, cred, conTar, "ModSecurity-nginx-*/config")
	if err != nil {
		return err
	}
	nginxDir, err := in.nginxSource(ctx, cred, owner)
	if err != nil {
		return err
	}

	short := in.tx.ID[len(in.tx.ID)-8:]
	var env []string
	ldOpt := ""
	libDir := ""
	stageLib := ""
	if libFromSource {
		msTar, err := in.fetch(ctx, in.src.ModSecurity, in.src.ModSecuritySHA256, "modsecurity-v"+ModSecurityVersion+".tar.gz", owner)
		if err != nil {
			return err
		}
		in.meta.Sources = append(in.meta.Sources, SourceInfo{Name: "libmodsecurity", Version: ModSecurityVersion, Origin: in.src.ModSecurity, SHA256: in.src.ModSecuritySHA256})
		msDir, err := in.extract(ctx, cred, msTar, "modsecurity-v*/configure")
		if err != nil {
			return err
		}
		libDir = filepath.Join(LibRoot, ModSecurityVersion+"-"+short)
		stage := filepath.Join(in.realBuild(), "stage")
		if err := in.as(ctx, cred, msDir, nil, "sh", "./configure", "--prefix="+libDir, "--with-pcre2", "--without-lua", "--disable-doxygen-doc", "--disable-examples"); err != nil {
			return err
		}
		if err := in.as(ctx, cred, msDir, nil, "make", parallelJobs()); err != nil {
			return err
		}
		if err := in.as(ctx, cred, msDir, nil, "make", "install", "DESTDIR="+stage); err != nil {
			return err
		}
		stageLib = filepath.Join(stage, strings.TrimPrefix(libDir, "/"))
		env = []string{"MODSECURITY_INC=" + filepath.Join(stageLib, "include"), "MODSECURITY_LIB=" + filepath.Join(stageLib, "lib")}
		ldOpt = "--with-ld-opt=-Wl,-rpath," + filepath.Join(libDir, "lib")
	} else {
		in.meta.Sources = append(in.meta.Sources, SourceInfo{Name: libPkg, Origin: "package"})
	}

	args := []string{"./configure", "--with-compat", "--add-dynamic-module=" + conDir}
	if ldOpt != "" {
		args = append(args, ldOpt)
	}
	if err := in.as(ctx, cred, nginxDir, env, "sh", args...); err != nil {
		return err
	}
	if err := in.as(ctx, cred, nginxDir, env, "make", parallelJobs(), "modules"); err != nil {
		return err
	}
	built := filepath.Join(nginxDir, "objs", "ngx_http_modsecurity_module.so")
	needed, _, err := capability.ReadNeeded(built)
	if err != nil {
		return fmt.Errorf("the build produced no usable module: %v", err)
	}
	if !contains(needed, "libmodsecurity.so.3") {
		return fmt.Errorf("the built module is not linked to libmodsecurity.so.3 (%s)", strings.Join(needed, ", "))
	}
	in.logf("Built %s (needs %s).", filepath.Base(built), strings.Join(needed, ", "))

	// Install: new, versioned files only.
	if libFromSource {
		src, err := filepath.EvalSymlinks(filepath.Join(stageLib, "lib", "libmodsecurity.so.3"))
		if err != nil {
			return fmt.Errorf("the built libmodsecurity is missing: %v", err)
		}
		if err := in.tx.MkdirAll(filepath.Join(libDir, "lib"), 0o755, agentfs.RootOwner); err != nil {
			return err
		}
		if err := in.tx.InstallFile(filepath.Join(libDir, "lib", "libmodsecurity.so.3"), src, 0o644, agentfs.RootOwner); err != nil {
			return err
		}
		in.meta.LibDir = libDir
	}
	module := filepath.Join(ModuleDir, "ngx_http_modsecurity_module-"+in.n.Version+"-"+short+".so")
	if err := in.tx.MkdirAll(ModuleDir, 0o755, agentfs.RootOwner); err != nil {
		return err
	}
	if err := in.tx.InstallFile(module, built, 0o644, agentfs.RootOwner); err != nil {
		return err
	}
	return in.enable(ctx, module)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func parallelJobs() string {
	n := runtime.NumCPU()
	if n > 8 {
		n = 8
	}
	return fmt.Sprintf("-j%d", n)
}

// nginxSource gets the source of exactly the installed Nginx, verified:
// the distribution's source package (checked by the package manager's
// signatures) or, for an Nginx no package installed, nginx.org's release
// checked against the SHA-256 the administrator gave.
func (in *installer) nginxSource(ctx context.Context, cred *execx.Credential, owner agentfs.Owner) (string, error) {
	pkg := in.meta.NginxBinary
	switch {
	case pkg != "" && in.e.Platform.PackageManager() != "dnf":
		return in.aptSource(ctx, cred, owner, pkg)
	case pkg != "":
		return in.dnfSource(ctx, cred, owner, pkg)
	}
	sum := strings.ToLower(strings.TrimSpace(in.opt.NginxSourceSHA256))
	if !sha256Re.MatchString(sum) {
		return "", actions.Conflict("This Nginx " + in.n.Version + " was not installed from a package, so its source cannot be verified automatically. Give the SHA-256 of nginx-" + in.n.Version + ".tar.gz from nginx.org to build the connector for it.")
	}
	url := fmt.Sprintf(in.src.NginxOrg, in.n.Version)
	tarball, err := in.fetch(ctx, url, sum, "nginx-"+in.n.Version+".tar.gz", owner)
	if err != nil {
		return "", err
	}
	in.meta.Sources = append(in.meta.Sources, SourceInfo{Name: "nginx", Version: in.n.Version, Origin: url, SHA256: sum})
	return in.extract(ctx, cred, tarball, "nginx-*/configure")
}

// DebSrc turns the system's apt sources into deb-src ones (one-line and
// deb822 formats), so the source package comes from the same, signed
// repositories.
func DebSrc(list string, deb822 bool) string {
	var out strings.Builder
	if !deb822 {
		for _, l := range strings.Split(list, "\n") {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "deb ") {
				out.WriteString("deb-src " + strings.TrimPrefix(t, "deb ") + "\n")
			}
		}
		return out.String()
	}
	for _, stanza := range regexp.MustCompile(`\n\s*\n`).Split(list, -1) {
		var lines []string
		hasDeb := false
		enabled := true
		sc := bufio.NewScanner(strings.NewReader(stanza))
		for sc.Scan() {
			l := sc.Text()
			if strings.HasPrefix(strings.TrimSpace(l), "#") {
				continue
			}
			k, v, _ := strings.Cut(l, ":")
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "types":
				hasDeb = contains(strings.Fields(v), "deb")
				l = "Types: deb-src"
			case "enabled":
				enabled = strings.TrimSpace(strings.ToLower(v)) != "no"
			}
			lines = append(lines, l)
		}
		if hasDeb && enabled {
			out.WriteString(strings.Join(lines, "\n") + "\n\n")
		}
	}
	return out.String()
}

func (in *installer) aptSource(ctx context.Context, cred *execx.Credential, owner agentfs.Owner, pkg string) (string, error) {
	out, err := in.e.Runner.Run(ctx, "dpkg-query", "-W", `-f=${source:Package}\t${source:Version}`, "--", pkg)
	f := strings.Split(strings.TrimSpace(string(out)), "\t")
	if err != nil || len(f) != 2 || f[0] == "" || f[1] == "" {
		return "", fmt.Errorf("the source package of %s is unknown: %s", pkg, host.Output(out))
	}
	srcPkg, srcVer := f[0], f[1]
	apt := filepath.Join(in.build, "apt")
	for _, d := range []string{apt, apt + "/parts", apt + "/lists/partial", apt + "/dl"} {
		if _, err := in.e.Writer.MkdirAll(d, 0o755, agentfs.RootOwner); err != nil {
			return "", err
		}
	}
	var list strings.Builder
	if b, err := in.e.Root.ReadFile("/etc/apt/sources.list"); err == nil {
		list.WriteString(DebSrc(string(b), false))
	}
	parts, _ := in.e.Root.Glob("/etc/apt/sources.list.d/*")
	for i, p := range parts {
		b, err := in.e.Root.ReadFile(p)
		if err != nil {
			continue
		}
		switch {
		case strings.HasSuffix(p, ".list"):
			list.WriteString(DebSrc(string(b), false))
		case strings.HasSuffix(p, ".sources"):
			if s := DebSrc(string(b), true); s != "" {
				if _, err := in.e.Writer.WriteFile(filepath.Join(apt, "parts", fmt.Sprintf("%02d.sources", i)), []byte(s), 0o644, agentfs.RootOwner); err != nil {
					return "", err
				}
			}
		}
	}
	if _, err := in.e.Writer.WriteFile(filepath.Join(apt, "sources.list"), []byte(list.String()), 0o644, agentfs.RootOwner); err != nil {
		return "", err
	}
	opts := []string{"-o", "Dir::Etc::SourceList=" + in.real(filepath.Join(apt, "sources.list")), "-o", "Dir::Etc::SourceParts=" + in.real(filepath.Join(apt, "parts")),
		"-o", "Dir::State::Lists=" + in.real(filepath.Join(apt, "lists")), "-o", "Dir::Cache::pkgcache=", "-o", "Dir::Cache::srcpkgcache=", "-o", "Debug::NoLocking=1", "-o", "APT::Sandbox::User=root"}
	in.logf("Downloading the source package %s %s (signature-checked by apt)…", srcPkg, srcVer)
	// One unreachable or unsigned repository fails "update" as a whole;
	// the indexes that did download are signature-checked and usable
	// (the others are simply not used).
	if _, err := in.e.Runner.RunWith(ctx, "apt-get", execx.Opts{Output: in.log, Timeout: -1}, append(opts, "-q", "update")...); err != nil {
		in.logf("Some repositories' source indexes could not be read (see above); the others are used.")
	}
	if out, err := in.e.Runner.RunWith(ctx, "apt-get", execx.Opts{Dir: in.real(filepath.Join(apt, "dl")), Output: in.log, Timeout: -1}, append(opts, "-q", "source", "--download-only", srcPkg+"="+srcVer)...); err != nil {
		return "", fmt.Errorf("the source of %s %s could not be downloaded (its repository may not publish sources): %s", srcPkg, srcVer, host.Output(out))
	}
	origs, _ := filepath.Glob(filepath.Join(in.real(filepath.Join(apt, "dl")), "*.orig.tar.*"))
	if len(origs) != 1 {
		return "", fmt.Errorf("the source package %s %s has no single upstream archive", srcPkg, srcVer)
	}
	// The build account reads it.
	os.Chmod(filepath.Dir(origs[0]), 0o755)
	os.Chmod(origs[0], 0o644)
	in.meta.Sources = append(in.meta.Sources, SourceInfo{Name: "nginx", Version: srcVer, Origin: "source package " + srcPkg + " (apt)"})
	rel, _ := filepath.Rel(in.realBuild(), origs[0])
	return in.extract(ctx, cred, filepath.Join(in.build, rel), "nginx-*/configure")
}

func (in *installer) dnfSource(ctx context.Context, cred *execx.Credential, owner agentfs.Owner, pkg string) (string, error) {
	out, err := in.e.Runner.Run(ctx, "rpm", "-q", "--qf", `%{VERSION}-%{RELEASE}`, "--", pkg)
	if err != nil {
		return "", fmt.Errorf("the version of %s is unknown: %s", pkg, host.Output(out))
	}
	ver := strings.TrimSpace(string(out))
	dl := filepath.Join(in.build, "srpm")
	if _, err := in.e.Writer.MkdirAll(dl, 0o755, owner); err != nil {
		return "", err
	}
	in.logf("Downloading the source package %s-%s…", pkg, ver)
	if out, err := in.e.Runner.RunWith(ctx, "dnf", execx.Opts{Output: in.log, Timeout: -1}, "download", "--source", "--enablerepo=*-source", "--destdir", in.real(dl), pkg+"-"+ver); err != nil {
		return "", fmt.Errorf("the source package of %s-%s could not be downloaded: %s", pkg, ver, host.Output(out))
	}
	srpms, _ := filepath.Glob(filepath.Join(in.real(dl), "*.src.rpm"))
	if len(srpms) != 1 {
		return "", fmt.Errorf("no single source package for %s-%s", pkg, ver)
	}
	if out, err := in.e.Runner.Run(ctx, "rpm", "-K", srpms[0]); err != nil || !strings.Contains(string(out), "OK") || strings.Contains(string(out), "NOT OK") {
		return "", fmt.Errorf("the source package's signature could not be verified: %s", host.Output(out))
	}
	os.Chown(srpms[0], owner.UID, owner.GID)
	if err := in.as(ctx, cred, in.real(dl), nil, "sh", "-c", `rpm2cpio "$1" | cpio -idm --quiet 'nginx-*.tar.gz'`, "sh", srpms[0]); err != nil {
		return "", err
	}
	tars, _ := filepath.Glob(filepath.Join(in.real(dl), "nginx-*.tar.gz"))
	if len(tars) != 1 {
		return "", fmt.Errorf("the source package of %s holds no single nginx archive", pkg)
	}
	in.meta.Sources = append(in.meta.Sources, SourceInfo{Name: "nginx", Version: ver, Origin: "source package " + pkg + " (dnf, signature-checked)"})
	rel, _ := filepath.Rel(in.realBuild(), tars[0])
	return in.extract(ctx, cred, filepath.Join(in.build, rel), "nginx-*/configure")
}
