package phpmgr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func enableRepo(t *testing.T, h *testhost.Host) (string, string) {
	t.Helper()
	os.MkdirAll(filepath.Join(h.Base, "etc/apt/sources.list.d"), 0o755)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("php.repository.enable")
	res, err := a.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		return "", err.Error()
	}
	j, _ := h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string))
	return j.Status, j.Error
}

func TestEnableOndrejPPAWritesPinnedSource(t *testing.T) {
	h := testhost.New(t)
	if repo := RepositoryOf(context.Background(), h.Env); repo.Kind != "ondrej-ppa" || !repo.Supported || repo.Enabled {
		t.Fatalf("before: %+v", repo)
	}
	if st, msg := enableRepo(t, h); st != "succeeded" {
		t.Fatalf("job %s: %s", st, msg)
	}
	src := h.Read(ondrejSources)
	codename := h.Env.Platform.Info().Codename
	if !strings.Contains(src, "URIs: "+ondrejURI) || !strings.Contains(src, "Suites: "+codename+"\n") || !strings.Contains(src, "Signed-By: "+ondrejKeyring) {
		t.Fatalf("source:\n%s", src)
	}
	if !strings.Contains(h.Read(ondrejKeyring), "BEGIN PGP PUBLIC KEY BLOCK") {
		t.Fatal("pinned key not written")
	}
	if !h.Runner.Called("apt-get update -q -o DPkg::Lock::Timeout=300") {
		t.Fatalf("index not refreshed: %v", h.Runner.Calls)
	}
	if repo := RepositoryOf(context.Background(), h.Env); !repo.Enabled {
		t.Fatalf("after: %+v", repo)
	}
	if _, msg := enableRepo(t, h); !strings.Contains(msg, "already enabled") {
		t.Fatalf("second enable: %q", msg)
	}
}

func TestEnableOndrejPPARolledBackWhenAptRefuses(t *testing.T) {
	h := testhost.New(t)
	h.Fail["apt-get update"] = "E: The repository is not signed."
	h.FailFrom["apt-get update"] = 1
	if st, msg := enableRepo(t, h); st != "failed" || !strings.Contains(msg, "removed again") {
		t.Fatalf("job %s: %s", st, msg)
	}
	if h.Exists(ondrejSources) || h.Exists(ondrejKeyring) {
		t.Fatal("source or key left behind after a refused refresh")
	}
}

func TestOndrejDetectedFromAddAptRepository(t *testing.T) {
	h := testhost.New(t)
	d := filepath.Join(h.Base, "etc/apt/sources.list.d")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "ondrej-ubuntu-php-noble.sources"), []byte("Types: deb\nURIs: https://ppa.launchpadcontent.net/ondrej/php/ubuntu/\nSuites: noble\n"), 0o644)
	if !RepositoryOf(context.Background(), h.Env).Enabled {
		t.Fatal("an existing PPA source is not recognised")
	}
}
