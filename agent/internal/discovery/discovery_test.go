package discovery

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

func fixtureService(t *testing.T) Service {
	t.Helper()
	abs, _ := filepath.Abs("../../tests/fixtures/ubuntu-host")
	root, err := filesystem.NewRoot(abs)
	if err != nil {
		t.Fatal(err)
	}
	return Service{
		Root:     root,
		Platform: platform.Detect(root),
		Runner:   &execx.FakeRunner{Outputs: map[string]string{}},
		Now:      func() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) },
	}
}

func TestWebsitesFixture(t *testing.T) {
	res := fixtureService(t).Websites(context.Background())
	sites := map[string]Website{}
	for _, w := range res.Websites {
		sites[w.Domain] = w
	}
	if len(res.Websites) != 5 {
		names := []string{}
		for _, w := range res.Websites {
			names = append(names, w.Domain)
		}
		t.Fatalf("websites = %v", names)
	}
	if res.CatchAll != 1 {
		t.Errorf("catch-all = %d", res.CatchAll)
	}

	ex := sites["example.com"]
	if !ex.Enabled || !ex.HTTPS || !ex.HTTPSRedirect || ex.Kind != "php" {
		t.Errorf("example.com flags: %+v", ex)
	}
	if len(ex.Aliases) != 1 || ex.Aliases[0] != "www.example.com" {
		t.Errorf("aliases = %v", ex.Aliases)
	}
	if ex.DocumentRoot != "/var/www/example.com/public" || !ex.DocRootExists {
		t.Errorf("docroot = %q exists=%v", ex.DocumentRoot, ex.DocRootExists)
	}
	if ex.PHP == nil || !ex.PHP.Resolved || ex.PHP.Pool != "example" || ex.PHP.Version != "8.3" || ex.PHP.User != "example" {
		t.Errorf("php = %+v", ex.PHP)
	}
	if ex.SSL == nil || !ex.SSL.CoversDomain || ex.SSL.Status != "valid" {
		t.Errorf("ssl = %+v", ex.SSL)
	}

	shop := sites["shop.example.com"]
	if shop.PHP == nil || shop.PHP.Version != "8.4" || shop.PHP.Pool != "shop" || shop.HTTPS {
		t.Errorf("shop = %+v php=%+v", shop, shop.PHP)
	}
	api := sites["api.example.com"]
	if api.Kind != "proxy" || api.SSL == nil || !api.SSL.CoversDomain {
		t.Errorf("api = %+v", api)
	}
	old := sites["old.example.com"]
	if old.Enabled || old.DocRootExists {
		t.Errorf("old = %+v", old)
	}
	if ex.ID == "" || ex.ID == old.ID {
		t.Error("website IDs must be stable and distinct")
	}
}

func TestInventoryHasNoSecrets(t *testing.T) {
	inv := fixtureService(t).Full(context.Background())
	b, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"super-secret-redis-password", "S3cretPass", "abc123def", "must-not-leak"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("inventory leaks %q", secret)
		}
	}
	if !inv.OS.Supported || inv.OS.VersionID != "24.04" {
		t.Errorf("os = %+v", inv.OS)
	}
	if len(inv.Users) == 0 || len(inv.PHP.Versions) != 2 || len(inv.Databases.Schemas) != 2 {
		t.Errorf("inventory incomplete")
	}
}

func TestServiceUnitsIncludesPHPFPM(t *testing.T) {
	units := fixtureService(t).ServiceUnits()
	found := 0
	for _, u := range units {
		if strings.HasPrefix(u, "php8.") {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("units = %v", units)
	}
}
