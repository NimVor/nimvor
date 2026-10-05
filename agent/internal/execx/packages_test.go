package execx

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPackageTransaction(t *testing.T) {
	for _, c := range []struct {
		id   string
		args string
		want bool
	}{
		{"apt-get", "update -q", true},
		{"apt-get", "install -y -q --no-install-recommends -- redis-server", true},
		{"apt-get", "remove -y -q -- redis-server", true},
		{"apt-get", "-q source --download-only nginx=1.24", true}, // downloads as _apt
		{"apt-get", "-s remove -- redis-server", false},
		{"apt-get", "-s -o Debug::NoLocking=1 install -- x", false},
		{"dpkg", "--configure -a", true},
		{"dpkg", "--print-architecture", false},
		{"dnf", "install -y -q -- dovecot", true},
		{"dnf", "-q install -y -- dovecot", true},
		{"dnf", "makecache -q", true},
		{"dnf", "module enable -y php:remi-8.3", true},
		{"dnf", "install --assumeno -- redis", false},
		{"dnf", "-q --cacheonly list php83-php-fpm", false},
		{"dnf", "-q list php83-php-fpm", false},
		{"dnf", "repoquery --qf %{name} -- redis", false},
		{"dnf", "check-update", false},
		{"rpm", "--rebuilddb", true},
		{"rpm", "-e -- redis", true},
		{"rpm", "-e --test -- redis", false},
		{"rpm", "-q --qf %{VERSION} -- redis", false},
		{"rpm", "-qf --qf %{NAME} -- /usr/bin/redis-server", false},
		{"rpm", "-qa", false},
		{"rpm", "-K /tmp/x.src.rpm", false},
		{"rpm", "-ivh /tmp/x.rpm", true},
		{"apt-cache", "policy -- redis", false},
		{"systemctl", "restart redis", false},
	} {
		if got := PackageTransaction(c.id, strings.Fields(c.args)); got != c.want {
			t.Errorf("%s %s: %v, want %v", c.id, c.args, got, c.want)
		}
	}
}

// A package transaction unit has none of the agent's confinement (see
// packages.go), no bind mounts and is named for the waiting logic.
func TestPackageTransientArgs(t *testing.T) {
	a := strings.Join(TransientArgs(PackageUnitPrefix+"x.service", "/bin/sh", "/usr/bin/apt-get", "/", []string{"HOME=/root", "DEBIAN_FRONTEND=noninteractive"},
		Credential{Home: "/root", Package: true}, 0, "", "/run/whacp-run/o", "/run/whacp-run/e", []string{"/a:/b"}, []string{"update", "-q"}), "\n")
	for _, want := range []string{"--unit=" + PackageUnitPrefix + "x.service", "--uid=0", "--gid=0", "--description=WhaCP package transaction", "--setenv=DEBIAN_FRONTEND=noninteractive", "/usr/bin/apt-get\nupdate\n-q"} {
		if !strings.Contains(a, want) {
			t.Errorf("lacks %q:\n%s", want, a)
		}
	}
	for _, not := range []string{"NoNewPrivileges", "CapabilityBoundingSet", "ProtectSystem", "BindReadOnlyPaths", "RuntimeMaxSec", "PrivateTmp"} {
		if strings.Contains(a, not) {
			t.Errorf("has %q:\n%s", not, a)
		}
	}
}

// Package transactions never overlap, whoever starts them.
func TestPackageTransactionsAreSerialised(t *testing.T) {
	var running, maxRunning int32
	var wg sync.WaitGroup
	var out strings.Builder
	var mu sync.Mutex
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := acquirePackageSlot(context.Background(), writerFunc(func(p []byte) { mu.Lock(); out.Write(p); mu.Unlock() }))
			if err != nil {
				t.Error(err)
				return
			}
			n := atomic.AddInt32(&running, 1)
			for {
				m := atomic.LoadInt32(&maxRunning)
				if n <= m || atomic.CompareAndSwapInt32(&maxRunning, m, n) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			atomic.AddInt32(&running, -1)
			release()
		}()
	}
	wg.Wait()
	if maxRunning != 1 {
		t.Errorf("%d transactions ran at once", maxRunning)
	}
	if !strings.Contains(out.String(), "Waiting for another package operation") {
		t.Errorf("no progress message: %q", out.String())
	}
	// A caller that gives up stops waiting.
	release, _ := acquirePackageSlot(context.Background(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := acquirePackageSlot(ctx, nil); err == nil {
		t.Error("acquired a busy slot")
	}
	release()
}

type writerFunc func([]byte)

func (f writerFunc) Write(p []byte) (int, error) { f(p); return len(p), nil }

// RunWith itself takes the slot: a second transaction waits for the first.
func TestRunWithWaitsForTheSlot(t *testing.T) {
	r := &SystemRunner{paths: map[string]string{"apt-get": "/bin/true"}}
	release, _ := acquirePackageSlot(context.Background(), nil)
	done := make(chan error, 1)
	var log strings.Builder
	var mu sync.Mutex
	go func() {
		_, err := r.RunWith(context.Background(), "apt-get", Opts{Output: writerFunc(func(p []byte) { mu.Lock(); log.Write(p); mu.Unlock() })}, "update")
		done <- err
	}()
	select {
	case <-done:
		t.Fatal("ran while another transaction held the slot")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(log.String(), "Waiting for another package operation") {
		t.Errorf("log: %q", log.String())
	}
	// Queries do not wait.
	release, _ = acquirePackageSlot(context.Background(), nil)
	defer release()
	r.paths["apt-cache"] = "/bin/true"
	if _, err := r.RunWith(context.Background(), "apt-cache", Opts{Timeout: time.Second}, "policy"); err != nil {
		t.Fatal(err)
	}
}
