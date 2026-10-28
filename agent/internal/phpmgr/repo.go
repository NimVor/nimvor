package phpmgr

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
)

// More PHP versions than the distribution's one come from a third-party
// repository: Ondřej Surý's PPA on Ubuntu, Remi's repository on the RHEL
// family. Both are signed. Adding one is the administrator's choice (a
// button with a confirmation in the panel); NimVor never adds it on its own.
//
// The PPA's signing key is pinned here (it is not fetched): fingerprint
// 14AA 40EC 0831 7567 56D7  F66C 4F4E A0AA E526 7A6C, "Launchpad PPA for
// Ondřej Surý". The repository is trusted only for its own packages
// (Signed-By), not added to APT's global keyring.

//go:embed ondrej-php.asc
var ondrejKey []byte

const (
	// OndrejFingerprint identifies the pinned key.
	OndrejFingerprint = "14AA40EC0831756756D7F66C4F4EA0AAE5267A6C"
	ondrejKeyring     = "/etc/apt/keyrings/nimvor-ondrej-php.asc"
	ondrejSources     = "/etc/apt/sources.list.d/nimvor-ondrej-php.sources"
	ondrejURI         = "https://ppa.launchpadcontent.net/ondrej/php/ubuntu"
)

var codenameRe = regexp.MustCompile(`^[a-z]{3,20}$`)

// Repository describes the PHP versions repository of this system.
type Repository struct {
	Kind      string `json:"kind"` // "ondrej-ppa", "remi" or "" (none for this system)
	Name      string `json:"name"`
	URL       string `json:"url"`
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Note      string `json:"note,omitempty"`
}

// OndrejSources is the deb822 source of the PPA for an Ubuntu codename.
func OndrejSources(codename string) string {
	return "# Added by NimVor (PHP page): more PHP versions from Ondřej Surý's PPA.\n" +
		"Types: deb\n" +
		"URIs: " + ondrejURI + "\n" +
		"Suites: " + codename + "\n" +
		"Components: main\n" +
		"Signed-By: " + ondrejKeyring + "\n"
}

// ondrejConfigured reports whether a source of the PPA exists (added by
// NimVor, by the installer or by add-apt-repository).
func ondrejConfigured(env *host.Env) bool {
	files := []string{"/etc/apt/sources.list"}
	if entries, err := env.Root.ReadDir("/etc/apt/sources.list.d"); err == nil {
		for _, e := range entries {
			if n := e.Name(); strings.HasSuffix(n, ".list") || strings.HasSuffix(n, ".sources") {
				files = append(files, path.Join("/etc/apt/sources.list.d", n))
			}
		}
	}
	for _, f := range files {
		b, err := env.Root.ReadFile(f)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "#") || strings.HasPrefix(strings.ToLower(l), "enabled: no") {
				continue
			}
			if strings.Contains(l, "/ondrej/php") {
				return true
			}
		}
	}
	return false
}

// RepositoryOf describes the PHP repository for this system.
func RepositoryOf(ctx context.Context, env *host.Env) Repository {
	info := env.Platform.Info()
	if env.Platform.PackageManager() == "dnf" {
		return Repository{Kind: "remi", Name: "Remi's repository", URL: "https://rpms.remirepo.net/", Supported: true,
			Enabled: pkgs.RepoEnabled(ctx, env, "remi-safe")}
	}
	if info.ID != "ubuntu" {
		return Repository{Note: "Only Ubuntu (Ondřej Surý's PPA) and the RHEL family (Remi) are supported."}
	}
	r := Repository{Kind: "ondrej-ppa", Name: "ppa:ondrej/php", URL: "https://launchpad.net/~ondrej/+archive/ubuntu/php", Enabled: ondrejConfigured(env)}
	if !codenameRe.MatchString(info.Codename) {
		r.Note = "The Ubuntu release name could not be read from /etc/os-release."
		return r
	}
	r.Supported = true
	return r
}

func registerRepository(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "php.repository", Description: "The third-party repository with more PHP versions for this system, and whether it is enabled.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		return RepositoryOf(ctx, env), nil
	}})
	r.Register(actions.Action{Name: "php.repository.enable", Mutating: true, Description: "Add the repository with more PHP versions (Ubuntu: Ondřej Surý's PPA, pinned key; RHEL family: Remi) and refresh the package index.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		if err := host.Decode(p, &struct{}{}); err != nil {
			return nil, err
		}
		repo := RepositoryOf(ctx, env)
		if !repo.Supported {
			return nil, actions.Conflict("No PHP repository is supported on this system. " + repo.Note)
		}
		if repo.Enabled {
			return nil, actions.Conflict(repo.Name + " is already enabled.")
		}
		j := env.Jobs.Start("php.repository.enable", "php:repository", func(ctx context.Context, log io.Writer) (any, error) {
			if repo.Kind == "remi" {
				if !pkgs.EnableRemi(ctx, env, log) {
					return nil, fmt.Errorf("Remi's repository could not be enabled (see above)")
				}
				fmt.Fprintln(log, "  ✓ Remi's repository is enabled")
				return nil, nil
			}
			return nil, enableOndrej(ctx, env, log)
		})
		return map[string]any{"job_id": j.ID, "repository": repo.Name}, nil
	}})
}

// enableOndrej writes the pinned key and the source, then refreshes the
// package index; when APT refuses the repository both files are removed
// again, so a failed attempt leaves APT as it was.
func enableOndrej(ctx context.Context, env *host.Env, log io.Writer) error {
	codename := env.Platform.Info().Codename
	if !bytes.Contains(ondrejKey, []byte("BEGIN PGP PUBLIC KEY BLOCK")) {
		return fmt.Errorf("the pinned key of the PPA is missing from this agent build")
	}
	fmt.Fprintf(log, "Adding %s for Ubuntu %s (signing key %s, trusted for this repository only).\n", ondrejURI, codename, OndrejFingerprint)
	if _, err := env.Writer.MkdirAll(path.Dir(ondrejKeyring), 0o755, agentfs.RootOwner); err != nil {
		return fmt.Errorf("create %s: %w", path.Dir(ondrejKeyring), err)
	}
	if _, err := env.Writer.WriteFile(ondrejKeyring, ondrejKey, 0o644, agentfs.RootOwner); err != nil {
		return fmt.Errorf("write %s: %w", ondrejKeyring, err)
	}
	if _, err := env.Writer.WriteFile(ondrejSources, []byte(OndrejSources(codename)), 0o644, agentfs.RootOwner); err != nil {
		env.Writer.Remove(ondrejKeyring)
		return fmt.Errorf("write %s: %w", ondrejSources, err)
	}
	if err := pkgs.Refresh(ctx, env, log); err != nil {
		env.Writer.Remove(ondrejSources)
		env.Writer.Remove(ondrejKeyring)
		if rerr := pkgs.Refresh(ctx, env, io.Discard); rerr != nil {
			fmt.Fprintf(log, "Refreshing the package index without the PPA also failed: %v\n", rerr)
		}
		return fmt.Errorf("APT refused the PPA, so it was removed again: %w", err)
	}
	fmt.Fprintln(log, "  ✓ ppa:ondrej/php is enabled: its PHP versions are now offered on the PHP page")
	return nil
}
