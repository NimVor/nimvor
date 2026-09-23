package systemd

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
)

const showOut = `Id=nginx.service
Description=A high performance web server
LoadState=loaded
ActiveState=active
SubState=running
UnitFileState=enabled
MainPID=812
MemoryCurrent=10485760
ActiveEnterTimestamp=Tue 2026-09-22 10:11:12 UTC

Id=mysql.service
Description=mysql.service
LoadState=not-found
ActiveState=inactive
SubState=dead
UnitFileState=
MainPID=0
MemoryCurrent=[not set]
ActiveEnterTimestamp=n/a
`

func TestStatus(t *testing.T) {
	r := &execx.FakeRunner{Outputs: map[string]string{
		"systemctl show --no-pager --property=" + showProps + " -- nginx.service mysql.service": showOut,
	}}
	svcs, err := Client{Runner: r}.Status(context.Background(), []string{"nginx.service", "mysql.service", "evil; rm -rf /"})
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 2 {
		t.Fatalf("services = %+v", svcs)
	}
	n := svcs[0]
	if !n.Running || n.MainPID != 812 || n.MemoryBytes == nil || *n.MemoryBytes != 10485760 || n.ActiveSince == nil {
		t.Errorf("nginx = %+v", n)
	}
	if svcs[1].LoadState != "not-found" || svcs[1].MemoryBytes != nil {
		t.Errorf("mysql = %+v", svcs[1])
	}
}

func TestValidUnit(t *testing.T) {
	for _, u := range []string{"nginx.service", "php8.3-fpm.service", "getty@tty1.service"} {
		if !ValidUnit(u) {
			t.Errorf("%q should be valid", u)
		}
	}
	for _, u := range []string{"nginx", "a b.service", "--all.service", "-x.service", "../x.service", "$(x).service"} {
		if ValidUnit(u) {
			t.Errorf("%q should be invalid", u)
		}
	}
}

func TestDiscoverUnits(t *testing.T) {
	abs, _ := filepath.Abs("../../tests/fixtures/ubuntu-host")
	root, _ := filesystem.NewRoot(abs)
	units := DiscoverUnits(root, []string{"/lib/systemd/system"}, []string{"php*-fpm.service"})
	if len(units) != 2 || units[0] != "php8.3-fpm.service" {
		t.Fatalf("units = %v", units)
	}
}
