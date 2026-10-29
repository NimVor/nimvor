package phpmgr

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// The ionCube Loader is closed-source and not in any distribution's or
// Remi's repositories: ionCube Ltd publishes one archive per CPU type with a
// loader for every PHP version it supports. NimVor therefore installs it
// itself, from ionCube's own HTTPS host only, and never trusts the download
// more than it has to:
//
//   - only the one file for the requested PHP version is taken out of the
//     archive (by exact name; nothing else is extracted, so no path in the
//     archive is ever used), it must be an ELF shared object, and it is kept
//     root-owned and not writable by anyone else;
//   - PHP is asked to load it before anything is switched on, and the
//     ionCube banner must show up; otherwise nothing is kept;
//   - PHP-FPM must accept the result, or every file is put back.
//
// Which PHP versions are supported is whatever the archive contains, so a
// new PHP version works as soon as ionCube ships a loader for it, and a
// version it does not cover gets a plain explanation instead of a failure.

// IonCube is the catalog entry for the ionCube Loader. It is not part of
// Extensions: it has no package, and the bulk PHP Manager only handles
// package-based extensions.
var IonCube = Extension{"ioncube", "ionCube Loader (runs ionCube-encoded PHP files)", "", "", []string{"ioncube"}}

const (
	ionCubeDir     = "/usr/local/lib/nimvor/ioncube"
	ionCubeMaxSize = 24 << 20  // one loader
	ionCubeMaxDown = 120 << 20 // the whole archive
)

var ionCubeFile = regexp.MustCompile(`^ioncube/ioncube_loader_lin_([5-9]\.[0-9]{1,2})\.so$`)

// IonCubeSource says where the loaders are downloaded from (tests point it
// elsewhere).
type IonCubeSource struct {
	// URL is the archive for a CPU type ("x86-64" or "aarch64").
	URL    func(cpu string) string
	Client *http.Client
}

// DefaultIonCubeSource is ionCube's own download host.
var DefaultIonCubeSource = IonCubeSource{
	URL: func(cpu string) string {
		return "https://downloads.ioncube.com/loader_downloads/ioncube_loaders_lin_" + cpu + ".tar.gz"
	},
	Client: &http.Client{Timeout: 5 * time.Minute},
}

var (
	ionCubeSource = DefaultIonCubeSource
	ionCubeMu     sync.Mutex
)

func ionCubeCPU() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "x86-64", nil
	case "arm64":
		return "aarch64", nil
	}
	return "", fmt.Errorf("ionCube does not publish a loader for the %s processor", runtime.GOARCH)
}

func ionCubeLoaderPath(v string) string {
	return ionCubeDir + "/" + v + "/ioncube_loader_lin_" + v + ".so"
}

// ionCubeIni is the file that loads the loader: ionCube must be the first
// Zend extension (priority 00), so it is named like Debian's and Remi's own.
func ionCubeIni(env *host.Env, v string) string {
	if rhel(env) {
		return IniDir(env, v) + "/00-ioncube.ini"
	}
	return "/etc/php/" + v + "/mods-available/ioncube.ini"
}

func ionCubeIniContent(v string) []byte {
	return []byte("; Written by NimVor. ionCube must be the first Zend extension.\n; priority=00\nzend_extension = " + ionCubeLoaderPath(v) + "\n")
}

// fetchIonCubeLoader downloads ionCube's archive and returns the loader for
// PHP v, or says which versions the archive does have.
func fetchIonCubeLoader(ctx context.Context, src IonCubeSource, v string) ([]byte, error) {
	cpu, err := ionCubeCPU()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL(cpu), nil)
	if err != nil {
		return nil, err
	}
	res, err := src.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the ionCube Loader could not be downloaded from ionCube's website: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the ionCube Loader could not be downloaded from ionCube's website (HTTP %d)", res.StatusCode)
	}
	zr, err := gzip.NewReader(io.LimitReader(res.Body, ionCubeMaxDown))
	if err != nil {
		return nil, fmt.Errorf("the ionCube download is not a compressed archive")
	}
	tr := tar.NewReader(zr)
	want := "ioncube/ioncube_loader_lin_" + v + ".so"
	var have []string
	var loader []byte
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the ionCube download is damaged: %v", err)
		}
		name := path.Clean(h.Name)
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if m := ionCubeFile.FindStringSubmatch(name); m != nil {
			have = append(have, m[1])
		}
		if name != want {
			continue
		}
		if h.Size <= 0 || h.Size > ionCubeMaxSize {
			return nil, fmt.Errorf("the ionCube loader for PHP %s has an unexpected size", v)
		}
		if loader, err = io.ReadAll(io.LimitReader(tr, ionCubeMaxSize+1)); err != nil || len(loader) > ionCubeMaxSize {
			return nil, fmt.Errorf("the ionCube download is damaged")
		}
	}
	if loader == nil {
		sort.Strings(have)
		if len(have) == 0 {
			return nil, fmt.Errorf("the ionCube download holds no loaders; nothing was installed")
		}
		return nil, fmt.Errorf("ionCube has no loader for PHP %s yet (this download covers PHP %s); nothing was installed", v, strings.Join(have, ", "))
	}
	if len(loader) < 64 || string(loader[:4]) != "\x7fELF" {
		return nil, fmt.Errorf("the ionCube loader for PHP %s is not a program library; nothing was installed", v)
	}
	return loader, nil
}

// ionCubeLoads asks PHP v to load the loader and checks that ionCube's
// banner appears in its version line.
func ionCubeLoads(ctx context.Context, env *host.Env, v string) error {
	out, err := env.Runner.Run(ctx, fmt.Sprintf(env.M.PHPFPMBinary, v), "-d", "zend_extension="+ionCubeLoaderPath(v), "-v")
	if err != nil {
		return fmt.Errorf("PHP %s could not load the ionCube loader: %s", v, host.Why(out, err))
	}
	if !strings.Contains(strings.ToLower(string(out)), "ioncube") {
		return fmt.Errorf("PHP %s started but did not load the ionCube loader", v)
	}
	return nil
}

func installIonCube(ctx context.Context, env *host.Env, log io.Writer, v string) error {
	ionCubeMu.Lock()
	defer ionCubeMu.Unlock()
	fmt.Fprintf(log, "Downloading the ionCube Loader archive…\n")
	loader, err := fetchIonCubeLoader(ctx, ionCubeSource, v)
	if err != nil {
		return err
	}
	fmt.Fprintf(log, "Found the loader for PHP %s (%d KB).\n", v, len(loader)/1024)

	set := env.Changes("php-ioncube-" + v)
	set.Mkdir(ionCubeDir, 0o755, agentfs.RootOwner)
	set.Mkdir(ionCubeDir+"/"+v, 0o755, agentfs.RootOwner)
	set.Write(ionCubeLoaderPath(v), loader, 0o644, agentfs.RootOwner)
	set.Write(ionCubeIni(env, v), ionCubeIniContent(v), 0o644, agentfs.RootOwner)
	if _, err := set.Commit(func() error { return ionCubeLoads(ctx, env, v) }); err != nil {
		return err
	}
	fmt.Fprintln(log, "PHP loads the ionCube Loader.")

	defer env.LockWebConfig()()
	if err := setModules(ctx, env, v, IonCube.Modules, true); err != nil {
		removeIonCubeFiles(ctx, env, v)
		return err
	}
	if err := env.ReloadFPM(ctx, v); err != nil {
		_ = setModules(ctx, env, v, IonCube.Modules, false)
		removeIonCubeFiles(ctx, env, v)
		_ = env.ReloadFPM(ctx, v)
		return fmt.Errorf("PHP-FPM %s did not accept the ionCube Loader, so it was removed again: %v", v, err)
	}
	fmt.Fprintf(log, "PHP-FPM %s reloaded.\n", v)
	return nil
}

func removeIonCubeFiles(ctx context.Context, env *host.Env, v string) {
	set := env.Changes("php-ioncube-remove-" + v)
	set.Remove(ionCubeIni(env, v))
	set.Remove(ionCubeLoaderPath(v))
	if _, err := set.Apply(); err != nil {
		env.Log.Warn("ionCube files not removed", "version", v, "error", err)
	}
}

func removeIonCube(ctx context.Context, env *host.Env, log io.Writer, v string) error {
	ionCubeMu.Lock()
	defer ionCubeMu.Unlock()
	defer env.LockWebConfig()()
	if err := setModules(ctx, env, v, IonCube.Modules, false); err != nil {
		return err
	}
	removeIonCubeFiles(ctx, env, v)
	if err := env.ReloadFPM(ctx, v); err != nil {
		return fmt.Errorf("removed, but PHP-FPM %s did not reload: %v", v, err)
	}
	fmt.Fprintf(log, "PHP-FPM %s reloaded.\n", v)
	return nil
}

func registerIonCube(r *actions.Registry, env *host.Env) {
	job := func(name, desc string, run func(context.Context, *host.Env, io.Writer, string) error, needsAbsent bool) {
		r.Register(actions.Action{Name: name, Mutating: true, Description: desc, Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
			var in extensionReq
			if err := host.Decode(p, &in); err != nil {
				return nil, err
			}
			in.Name = IonCube.Name
			if _, err := in.check(env); err != nil {
				return nil, err
			}
			present := moduleAvailable(env, in.Version, "ioncube")
			if needsAbsent && present {
				return nil, actions.Conflict("The ionCube Loader is already installed for PHP " + in.Version + ".")
			}
			if !needsAbsent && !present {
				return nil, actions.Conflict("The ionCube Loader is not installed for PHP " + in.Version + ".")
			}
			j := env.Jobs.Start(name, "server:packages", func(ctx context.Context, log io.Writer) (any, error) {
				return nil, run(ctx, env, log, in.Version)
			})
			return map[string]any{"job_id": j.ID, "package": "ionCube Loader"}, nil
		}})
	}
	job("php.ioncube_install", "Install the ionCube Loader for a PHP version from ionCube's website (job): only that version's loader is taken from the archive, PHP must load it, and PHP-FPM is reloaded; everything is undone on failure.", installIonCube, true)
	job("php.ioncube_remove", "Remove the ionCube Loader from a PHP version (job); PHP-FPM is reloaded.", removeIonCube, false)
}
