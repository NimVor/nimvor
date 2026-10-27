package mongomgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
)

func TestRepositoryOfUbuntu(t *testing.T) {
	h, _ := setup(t)
	repo := RepositoryOf(h.Env)
	if repo.Kind != "apt-ubuntu" || !repo.Supported || repo.Enabled {
		t.Fatalf("ubuntu repo: %+v", repo)
	}
	if repo.Version != Version {
		t.Fatalf("version: %q", repo.Version)
	}
}

func TestAptSourceContent(t *testing.T) {
	src := aptSource("apt-ubuntu", "noble")
	for _, want := range []string{"URIs: " + aptURIUbunt, "Suites: noble/mongodb-org/" + Version, "Components: multiverse", "Signed-By: " + aptKeyring} {
		if !strings.Contains(src, want) {
			t.Fatalf("apt source missing %q:\n%s", want, src)
		}
	}
	deb := aptSource("apt-debian", "bookworm")
	if !strings.Contains(deb, "URIs: "+aptURIDeb) || !strings.Contains(deb, "Components: main") {
		t.Fatalf("debian source:\n%s", deb)
	}
}

func TestDnfRepoContent(t *testing.T) {
	repo := dnfRepo()
	for _, want := range []string{"baseurl=" + dnfBaseURL, "gpgcheck=1", "gpgkey=" + keyURL} {
		if !strings.Contains(repo, want) {
			t.Fatalf("dnf repo missing %q:\n%s", want, repo)
		}
	}
}

func TestRepoConfiguredDetectsExistingSource(t *testing.T) {
	h, _ := setup(t)
	if repoConfigured(h.Env) {
		t.Fatal("should be unconfigured on a fresh host")
	}
	f := filepath.Join(h.Base, "etc/apt/sources.list.d/mongodb-org-8.0.list")
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte("deb https://repo.mongodb.org/apt/ubuntu noble/mongodb-org/8.0 multiverse\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !repoConfigured(h.Env) {
		t.Fatal("an existing MongoDB source should be detected")
	}
}

// The agent's writer only permits WhaCP-managed paths; the repository files
// must be among them or enabling the repository fails on a real host.
func TestRepositoryPathsAreWritable(t *testing.T) {
	h, _ := setup(t)
	for _, d := range []string{"etc/apt/keyrings", "etc/apt/sources.list.d", "etc/yum.repos.d"} {
		if err := os.MkdirAll(filepath.Join(h.Base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{aptKeyring, aptSources, dnfRepoFile} {
		if _, err := h.Env.Writer.WriteFile(p, []byte("x"), 0o644, agentfs.RootOwner); err != nil {
			t.Fatalf("writer refused %s: %v", p, err)
		}
	}
}

func TestInstallNeedsRepositoryEnabled(t *testing.T) {
	h, r := setup(t)
	h.Runner.Unavailable["mongosh"] = true // not installed
	_, err := call(t, r, "mongo.install", `{}`)
	if err == nil || !strings.Contains(err.Error(), "repository is not enabled") {
		t.Fatalf("expected a repository-not-enabled conflict, got %v", err)
	}
}
