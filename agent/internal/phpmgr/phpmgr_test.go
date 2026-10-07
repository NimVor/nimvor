package phpmgr

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func TestParseAvailable(t *testing.T) {
	got := ParseAvailable("php8.3-fpm - server-side\nphp8.10-fpm - x\nphp7.4-fpm - old\nphp8.3-fpm - dup\n")
	if !reflect.DeepEqual(got, []string{"7.4", "8.3", "8.10"}) {
		t.Fatalf("got %v", got)
	}
}

func TestInstallRunsAptAsJob(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("php.install")
	res, err := a.Handler(context.Background(), json.RawMessage(`{"version":"8.4","extensions":["curl"]}`))
	if err != nil {
		t.Fatal(err)
	}
	id := res.(map[string]any)["job_id"].(string)
	j, _ := h.Env.Jobs.Wait(context.Background(), id)
	if j.Status != "succeeded" || !h.Runner.Called("apt-get install -y -q --no-install-recommends -o Dpkg::Options::=--force-confold -o DPkg::Lock::Timeout=300 -- php8.4-fpm php8.4-cli php8.4-curl") {
		t.Fatalf("job %+v calls %v", j, h.Runner.Calls)
	}
	if !h.Runner.Called("systemctl enable -- php8.4-fpm.service") || !h.Runner.Called("systemctl start -- php8.4-fpm.service") {
		t.Fatalf("PHP-FPM not enabled and started: %v", h.Runner.Calls)
	}
	if _, err := a.Handler(context.Background(), json.RawMessage(`{"version":"8.4","extensions":["curl;rm"]}`)); err == nil {
		t.Fatal("bad extension accepted")
	}
}

func TestDisableRefusedWhileInUse(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("php.set_enabled")
	_, err := a.Handler(context.Background(), json.RawMessage(`{"version":"8.3","enabled":false}`))
	if err == nil || !strings.Contains(err.Error(), "example.com") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseAvailableRemi(t *testing.T) {
	out := "Available Packages\nphp74-php-fpm.x86_64  7.4.33-22.el9.remi  remi-safe\nphp83-php-fpm.x86_64  8.3.26-1.el9.remi  remi-safe\nphp84-php-fpm.x86_64 8.4.13-1.el9.remi remi-safe\n"
	if got := strings.Join(ParseAvailableRemi(out), ","); got != "7.4,8.3,8.4" {
		t.Fatalf("got %s", got)
	}
}
