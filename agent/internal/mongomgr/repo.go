package mongomgr

// MongoDB is not in the base repositories of any supported distribution, so
// installing it means adding MongoDB Inc.'s own signed repository first. This
// is the administrator's choice (a button with a confirmation on the MongoDB
// page); NimVor never adds it on its own.
//
// Unlike the PHP repositories, whose signing key is embedded in the agent
// build (phpmgr), MongoDB's key is not redistributed here: it is fetched at
// enable time from MongoDB's official HTTPS key server over the agent's
// CA-verified connection, and the repository is trusted only for its own
// packages (APT Signed-By; DNF per-repo gpgkey), never added to the system's
// global keyring.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
)

const (
	// Version is the MongoDB major version NimVor's repository tracks.
	Version = "8.0"
	// Package is the metapackage that installs the server (mongod), the
	// shell (mongosh) and the tools; the same name on APT and DNF.
	Package = "mongodb-org"

	keyURL      = "https://pgp.mongodb.com/server-" + Version + ".asc"
	aptKeyring  = "/etc/apt/keyrings/nimvor-mongodb-" + Version + ".asc"
	aptSources  = "/etc/apt/sources.list.d/nimvor-mongodb-" + Version + ".sources"
	aptURIUbunt = "https://repo.mongodb.org/apt/ubuntu"
	aptURIDeb   = "https://repo.mongodb.org/apt/debian"
	dnfRepoFile = "/etc/yum.repos.d/nimvor-mongodb-" + Version + ".repo"
	dnfBaseURL  = "https://repo.mongodb.org/yum/redhat/$releasever/mongodb-org/" + Version + "/x86_64/"

	maxKey = 1 << 20 // a public key is a few KB; cap the download well above that
)

// Repository describes the MongoDB repository for this system.
type Repository struct {
	Kind      string `json:"kind"` // "apt-ubuntu", "apt-debian", "dnf" or "" (none)
	Name      string `json:"name"`
	URL       string `json:"url"`
	Version   string `json:"version"`
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Note      string `json:"note,omitempty"`
}

// RepositoryOf describes the MongoDB repository for this system.
func RepositoryOf(env *host.Env) Repository {
	r := Repository{Version: Version}
	if env.Platform.PackageManager() == "dnf" {
		r.Kind, r.Name, r.URL, r.Supported = "dnf", "MongoDB "+Version+" (yum)", "https://repo.mongodb.org/yum/redhat/", true
		r.Enabled = repoConfigured(env)
		return r
	}
	info := env.Platform.Info()
	switch info.ID {
	case "ubuntu":
		r.Kind, r.URL = "apt-ubuntu", aptURIUbunt
	case "debian":
		r.Kind, r.URL = "apt-debian", aptURIDeb
	default:
		r.Note = "MongoDB's repository is only offered on Ubuntu, Debian and the RHEL family."
		return r
	}
	r.Name = "MongoDB " + Version + " (apt)"
	if !codenameOK(info.Codename) {
		r.Note = "The distribution release name could not be read from /etc/os-release."
		return r
	}
	r.Supported = true
	r.Enabled = repoConfigured(env)
	return r
}

// codenameOK reports whether the release codename is a plain word safe to put
// into a source file.
func codenameOK(s string) bool {
	if s == "" || len(s) > 20 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z') {
			return false
		}
	}
	return true
}

// repoConfigured reports whether a MongoDB repository is already present
// (added by NimVor, by the installer or by hand).
func repoConfigured(env *host.Env) bool {
	if env.Platform.PackageManager() == "dnf" {
		return fileMentions(env, "/etc/yum.repos.d", "repo.mongodb.org", ".repo")
	}
	if env.Root.Exists(aptSources) {
		return true
	}
	return fileMentions(env, "/etc/apt/sources.list.d", "repo.mongodb.org", ".list", ".sources")
}

// fileMentions reports whether any file in dir with one of the suffixes, or
// the single file dir itself, contains needle on a line that is not commented
// out.
func fileMentions(env *host.Env, dir, needle string, suffixes ...string) bool {
	var files []string
	if entries, err := env.Root.ReadDir(dir); err == nil {
		for _, e := range entries {
			for _, suf := range suffixes {
				if strings.HasSuffix(e.Name(), suf) {
					files = append(files, path.Join(dir, e.Name()))
					break
				}
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
			if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(strings.ToLower(l), "enabled: no") {
				continue
			}
			if strings.Contains(l, needle) {
				return true
			}
		}
	}
	return false
}

// aptSource is the deb822 source for the MongoDB repository.
func aptSource(kind, codename string) string {
	uri, comp := aptURIUbunt, "multiverse"
	if kind == "apt-debian" {
		uri, comp = aptURIDeb, "main"
	}
	return "# Added by NimVor (MongoDB page): MongoDB " + Version + " from MongoDB Inc.\n" +
		"Types: deb\n" +
		"URIs: " + uri + "\n" +
		"Suites: " + codename + "/mongodb-org/" + Version + "\n" +
		"Components: " + comp + "\n" +
		"Architectures: amd64 arm64\n" +
		"Signed-By: " + aptKeyring + "\n"
}

// dnfRepo is the .repo file for the MongoDB repository. DNF fetches and
// imports the signing key from MongoDB's HTTPS key server on first use.
func dnfRepo() string {
	return "# Added by NimVor (MongoDB page): MongoDB " + Version + " from MongoDB Inc.\n" +
		"[nimvor-mongodb-org-" + Version + "]\n" +
		"name=MongoDB " + Version + " (added by NimVor)\n" +
		"baseurl=" + dnfBaseURL + "\n" +
		"gpgcheck=1\n" +
		"enabled=1\n" +
		"gpgkey=" + keyURL + "\n"
}

// fetchKey downloads MongoDB's armored signing key over the agent's
// CA-verified HTTPS connection and checks that it is a public key block.
func fetchKey(ctx context.Context) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, keyURL, nil)
	req.Header.Set("User-Agent", "NimVor")
	res, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not fetch %s: %w", keyURL, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("could not fetch %s: HTTP %d", keyURL, res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxKey+1))
	if err != nil || len(b) > maxKey {
		return nil, fmt.Errorf("could not read the signing key from %s", keyURL)
	}
	if !strings.Contains(string(b), "BEGIN PGP PUBLIC KEY BLOCK") {
		return nil, fmt.Errorf("%s did not return a PGP public key", keyURL)
	}
	return b, nil
}

// enable adds the MongoDB repository and refreshes the package index. On
// failure the files it wrote are removed again, so a failed attempt leaves the
// package manager as it was.
func enable(ctx context.Context, env *host.Env, log io.Writer) error {
	repo := RepositoryOf(env)
	if !repo.Supported {
		return actions.Conflict("MongoDB's repository is not supported on this system. " + repo.Note)
	}
	if repo.Enabled {
		return actions.Conflict("MongoDB's repository is already enabled.")
	}
	if repo.Kind == "dnf" {
		fmt.Fprintf(log, "Adding MongoDB %s (%s); DNF imports the signing key from %s on first use.\n", Version, dnfBaseURL, keyURL)
		if _, err := env.Writer.WriteFile(dnfRepoFile, []byte(dnfRepo()), 0o644, agentfs.RootOwner); err != nil {
			return fmt.Errorf("write %s: %w", dnfRepoFile, err)
		}
		if err := pkgs.Refresh(ctx, env, log); err != nil {
			env.Writer.Remove(dnfRepoFile)
			pkgs.Refresh(ctx, env, io.Discard)
			return fmt.Errorf("DNF refused the repository, so it was removed again: %w", err)
		}
		fmt.Fprintln(log, "  ✓ MongoDB's repository is enabled: install it from the MongoDB page")
		return nil
	}
	codename := env.Platform.Info().Codename
	fmt.Fprintf(log, "Adding MongoDB %s for %s %s.\n", Version, env.Platform.Info().ID, codename)
	fmt.Fprintf(log, "Fetching the signing key over TLS from %s (MongoDB Inc.'s official key server; trusted for this repository only).\n", keyURL)
	key, err := fetchKey(ctx)
	if err != nil {
		return actions.Failed("The MongoDB signing key could not be fetched.", err.Error())
	}
	if _, err := env.Writer.MkdirAll(path.Dir(aptKeyring), 0o755, agentfs.RootOwner); err != nil {
		return fmt.Errorf("create %s: %w", path.Dir(aptKeyring), err)
	}
	if _, err := env.Writer.WriteFile(aptKeyring, key, 0o644, agentfs.RootOwner); err != nil {
		return fmt.Errorf("write %s: %w", aptKeyring, err)
	}
	if _, err := env.Writer.WriteFile(aptSources, []byte(aptSource(repo.Kind, codename)), 0o644, agentfs.RootOwner); err != nil {
		env.Writer.Remove(aptKeyring)
		return fmt.Errorf("write %s: %w", aptSources, err)
	}
	if err := pkgs.Refresh(ctx, env, log); err != nil {
		env.Writer.Remove(aptSources)
		env.Writer.Remove(aptKeyring)
		pkgs.Refresh(ctx, env, io.Discard)
		return fmt.Errorf("APT refused the repository, so it was removed again: %w", err)
	}
	fmt.Fprintln(log, "  ✓ MongoDB's repository is enabled: install it from the MongoDB page")
	return nil
}

// registerRepo adds the repository and install actions.
func registerRepo(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "mongo.repository", Description: "MongoDB's third-party repository for this system, and whether it is enabled.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return RepositoryOf(env), nil
	}})

	r.Register(actions.Action{Name: "mongo.repository.enable", Mutating: true, Description: "Add MongoDB Inc.'s signed repository (APT: key fetched over TLS, trusted for this repository only; DNF: per-repo gpgkey) and refresh the package index.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		if err := host.Decode(p, &struct{}{}); err != nil {
			return nil, err
		}
		repo := RepositoryOf(env)
		if !repo.Supported {
			return nil, actions.Conflict("MongoDB's repository is not supported on this system. " + repo.Note)
		}
		if repo.Enabled {
			return nil, actions.Conflict("MongoDB's repository is already enabled.")
		}
		j := env.Jobs.Start("mongo.repository.enable", "server:mongo", func(ctx context.Context, log io.Writer) (any, error) {
			return nil, enable(ctx, env, log)
		})
		return map[string]any{"job_id": j.ID, "repository": repo.Name}, nil
	}})

	r.Register(actions.Action{Name: "mongo.install", Mutating: true, Description: "Install MongoDB (mongodb-org) from its repository, then enable and start the mongod service.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		if err := host.Decode(p, &struct{}{}); err != nil {
			return nil, err
		}
		if installed(env) {
			return nil, actions.Conflict("MongoDB is already installed.")
		}
		if !RepositoryOf(env).Enabled {
			return nil, actions.Conflict("MongoDB's repository is not enabled yet. Enable it first, then install.")
		}
		j := env.Jobs.Start("mongo.install", "server:mongo", func(ctx context.Context, log io.Writer) (any, error) {
			if err := pkgs.Install(ctx, env, log, []string{Package}); err != nil {
				return nil, err
			}
			fmt.Fprintln(log, "Enabling and starting the mongod service…")
			if err := env.Systemctl(ctx, "enable", "mongod.service"); err != nil {
				return nil, fmt.Errorf("mongod could not be enabled: %w", err)
			}
			if err := env.Systemctl(ctx, "start", "mongod.service"); err != nil {
				return nil, fmt.Errorf("mongod could not be started: %w", err)
			}
			fmt.Fprintln(log, "  ✓ MongoDB is installed and running")
			return nil, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
}
