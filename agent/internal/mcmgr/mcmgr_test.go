package mcmgr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

const debianConf = `# memcached default config file
-d
logfile /var/log/memcached.log
-m 64
-p 11211
-u memcache
-l 127.0.0.1
-l ::1
-P /var/run/memcached/memcached.pid
`

const rhelConf = `PORT="11211"
USER="memcached"
MAXCONN="1024"
CACHESIZE="64"
OPTIONS="-l 127.0.0.1,::1 -U 0"
`

func TestParseAndRenderDebian(t *testing.T) {
	s := Parse(debianConf, "/etc/memcached.conf")
	if s.MemoryMB != 64 || s.Port != 11211 || strings.Join(s.Listen, ",") != "127.0.0.1,::1" || s.MaxConns != 1024 {
		t.Fatalf("parse = %+v", s)
	}
	s.MemoryMB, s.MaxConns, s.Listen = 256, 2048, []string{"127.0.0.1"}
	out := Render(debianConf, "/etc/memcached.conf", s)
	for _, want := range []string{"-m 256", "-c 2048", "-l 127.0.0.1\n-P", "logfile /var/log/memcached.log", "# memcached default"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "-l ::1") || strings.Contains(out, "-m 64") {
		t.Errorf("old values kept:\n%s", out)
	}
	if got := Parse(out, "/etc/memcached.conf"); got.MemoryMB != 256 || got.MaxConns != 2048 {
		t.Errorf("round trip = %+v", got)
	}
}

func TestParseAndRenderRHEL(t *testing.T) {
	s := Parse(rhelConf, "/etc/sysconfig/memcached")
	if s.MemoryMB != 64 || s.MaxConns != 1024 || strings.Join(s.Listen, ",") != "127.0.0.1,::1" {
		t.Fatalf("parse = %+v", s)
	}
	s.MemoryMB, s.Listen = 128, []string{"127.0.0.1"}
	out := Render(rhelConf, "/etc/sysconfig/memcached", s)
	if !strings.Contains(out, `CACHESIZE="128"`) || !strings.Contains(out, `OPTIONS="-l 127.0.0.1 -U 0"`) || !strings.Contains(out, `USER="memcached"`) {
		t.Errorf("render:\n%s", out)
	}
}

func TestValidateOnlyLoopback(t *testing.T) {
	ok := Settings{MemoryMB: 64, MaxConns: 1024, Listen: []string{"127.0.0.1", "::1"}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Settings{
		{MemoryMB: 64, MaxConns: 1024, Listen: []string{"0.0.0.0"}},
		{MemoryMB: 64, MaxConns: 1024, Listen: []string{"203.0.113.5"}},
		{MemoryMB: 8, MaxConns: 1024, Listen: []string{"127.0.0.1"}},
		{MemoryMB: 64, MaxConns: 1024},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestConfigureRollsBackWhenMemcachedDoesNotAnswer(t *testing.T) {
	h := testhost.New(t)
	// Port 1: nothing answers there, so the check after the restart fails.
	orig := strings.Replace(debianConf, "-p 11211", "-p 1", 1)
	os.WriteFile(filepath.Join(h.Base, "etc/memcached.conf"), []byte(orig), 0o644)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("memcached.configure")
	if _, err := a.Handler(context.Background(), json.RawMessage(`{"memory_mb":128,"max_conns":1024,"listen":["127.0.0.1"]}`)); err == nil {
		t.Fatal("edit without confirmation accepted")
	}
	_, err := a.Handler(context.Background(), json.RawMessage(`{"memory_mb":128,"max_conns":1024,"listen":["127.0.0.1"],"confirm_edit_package_config":true}`))
	if err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("err = %v", err)
	}
	if got := h.Read("etc/memcached.conf"); got != orig {
		t.Errorf("file not restored:\n%s", got)
	}
	if _, err := a.Handler(context.Background(), json.RawMessage(`{"memory_mb":128,"max_conns":1024,"listen":["0.0.0.0"],"confirm_edit_package_config":true}`)); err == nil {
		t.Error("public listen address accepted")
	}
}
