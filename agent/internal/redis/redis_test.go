package redis

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

func TestDiscoverNeverExposesPassword(t *testing.T) {
	abs, _ := filepath.Abs("../../tests/fixtures/ubuntu-host")
	root, _ := filesystem.NewRoot(abs)
	runner := &execx.FakeRunner{Outputs: map[string]string{"redis-server --version": "Redis server v=7.0.15 sha=00000000:0 malloc=jemalloc-5.3.0 bits=64 build=1"}}
	res := Discover(context.Background(), root, platform.Detect(root).Paths(), runner)
	if !res.Installed || res.Version != "7.0.15" || res.Port != "6379" || res.MaxMemory != "256mb" || !res.PasswordSet {
		t.Fatalf("unexpected result %+v", res)
	}
	b, _ := json.Marshal(res)
	if strings.Contains(string(b), "super-secret") {
		t.Fatal("redis password leaked into result")
	}
}
