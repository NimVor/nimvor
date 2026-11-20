package shotmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
)

// Google Chrome comes from Google's own signed repository (the distributions
// ship Chromium as a snap or not at all, and a snap cannot run in a
// service's sandbox). It is added only when an administrator asks for
// website pictures: the key is fetched over TLS from Google and trusted for
// this repository only.
const (
	keyURL      = "https://dl.google.com/linux/linux_signing_key.pub"
	aptKeyring  = "/etc/apt/keyrings/nimvor-google-chrome.asc"
	aptSources  = "/etc/apt/sources.list.d/nimvor-google-chrome.sources"
	dnfRepoFile = "/etc/yum.repos.d/nimvor-google-chrome.repo"
	pkgName     = "google-chrome-stable"
	maxKey      = 1 << 20
)

func aptSource() string {
	return "# Added by NimVor (website pictures): Google Chrome from Google.\n" +
		"Types: deb\nURIs: https://dl.google.com/linux/chrome/deb/\nSuites: stable\nComponents: main\nArchitectures: amd64\n" +
		"Signed-By: " + aptKeyring + "\n"
}

func dnfRepo() string {
	return "# Added by NimVor (website pictures): Google Chrome from Google.\n[nimvor-google-chrome]\nname=Google Chrome (added by NimVor)\n" +
		"baseurl=https://dl.google.com/linux/chrome/rpm/stable/x86_64\nenabled=1\ngpgcheck=1\ngpgkey=" + keyURL + "\n"
}

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
	if err != nil || len(b) > maxKey || !strings.Contains(string(b), "BEGIN PGP PUBLIC KEY BLOCK") {
		return nil, fmt.Errorf("%s did not return a PGP public key", keyURL)
	}
	return b, nil
}

func install(ctx context.Context, env *host.Env, log io.Writer) error {
	if runtime.GOARCH != "amd64" {
		return actions.Conflict("Google Chrome is only offered for 64-bit Intel/AMD servers; website pictures need a browser this server cannot get.")
	}
	dnf := env.Platform.PackageManager() == "dnf"
	if dnf {
		fmt.Fprintf(log, "Adding Google's Chrome repository; DNF imports the signing key from %s on first use.\n", keyURL)
		if _, err := env.Writer.WriteFile(dnfRepoFile, []byte(dnfRepo()), 0o644, agentfs.RootOwner); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(log, "Fetching Google's signing key over TLS from %s (trusted for this repository only).\n", keyURL)
		key, err := fetchKey(ctx)
		if err != nil {
			return actions.Failed("Google's signing key could not be fetched.", err.Error())
		}
		if _, err := env.Writer.MkdirAll("/etc/apt/keyrings", 0o755, agentfs.RootOwner); err != nil {
			return err
		}
		if _, err := env.Writer.WriteFile(aptKeyring, key, 0o644, agentfs.RootOwner); err != nil {
			return err
		}
		if _, err := env.Writer.WriteFile(aptSources, []byte(aptSource()), 0o644, agentfs.RootOwner); err != nil {
			env.Writer.Remove(aptKeyring)
			return err
		}
	}
	undo := func() {
		env.Writer.Remove(dnfRepoFile)
		env.Writer.Remove(aptSources)
		env.Writer.Remove(aptKeyring)
		pkgs.Refresh(ctx, env, io.Discard)
	}
	if err := pkgs.Refresh(ctx, env, log); err != nil {
		undo()
		return fmt.Errorf("the package manager refused the repository, so it was removed again: %w", err)
	}
	fmt.Fprintln(log, "Installing Google Chrome (about 300 MB)…")
	if err := pkgs.Install(ctx, env, log, []string{pkgName}); err != nil {
		return err
	}
	fmt.Fprintln(log, "  ✓ Website pictures are ready")
	return nil
}

func registerInstall(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "screenshot.install", Mutating: true, Description: "Install a browser for website pictures: add Google's signed Chrome repository (key fetched over TLS, trusted for it only) and install google-chrome-stable (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		if err := host.Decode(p, &struct{}{}); err != nil {
			return nil, err
		}
		if Installed(env) {
			return nil, actions.Conflict("A browser for website pictures is installed already.")
		}
		j := env.Jobs.Start("screenshot.install", "server:screenshot", func(ctx context.Context, log io.Writer) (any, error) {
			return nil, install(ctx, env, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
}
