package database

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

func TestDiscoverFixture(t *testing.T) {
	abs, _ := filepath.Abs("../../tests/fixtures/ubuntu-host")
	root, _ := filesystem.NewRoot(abs)
	runner := &execx.FakeRunner{Outputs: map[string]string{
		"mariadbd --version": "/usr/sbin/mariadbd  Ver 10.11.8-MariaDB-0ubuntu0.24.04.1 for debian-linux-gnu on x86_64 (Ubuntu 24.04)\n",
	}}
	res := Discover(context.Background(), root, platform.Detect(root).Paths(), runner)
	if !res.Installed || res.Engine != "mariadb" || res.Version != "10.11.8" {
		t.Fatalf("engine=%q version=%q", res.Engine, res.Version)
	}
	if res.Bind != "127.0.0.1" || res.Port != 3306 || res.Socket != "/run/mysqld/mysqld.sock" {
		t.Errorf("config = %+v", res)
	}
	if len(res.Schemas) != 2 {
		t.Fatalf("schemas = %+v", res.Schemas)
	}
	if res.Schemas[0].Name != "blog-db" || res.Schemas[1].Name != "shop_db" {
		t.Errorf("names = %q, %q", res.Schemas[0].Name, res.Schemas[1].Name)
	}
	if res.Schemas[1].SizeBytes != 6144 || res.Schemas[1].Tables != 2 {
		t.Errorf("shop_db = %+v", res.Schemas[1])
	}
}

func TestParseVersionMySQL(t *testing.T) {
	v, e := parseVersion("/usr/sbin/mysqld  Ver 8.0.39-0ubuntu0.24.04.2 for Linux on x86_64 ((Ubuntu))", "mysql")
	if v != "8.0.39" || e != "mysql" {
		t.Errorf("got %q %q", v, e)
	}
}

func TestDecodeName(t *testing.T) {
	if got := DecodeName("my@002ddb@0024x"); got != "my-db$x" {
		t.Errorf("DecodeName = %q", got)
	}
}
