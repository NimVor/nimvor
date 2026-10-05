package sysmgr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func wait(t *testing.T, h *testhost.Host, res any) (jobs.Job, string) {
	t.Helper()
	id := res.(map[string]any)["job_id"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	j, err := h.Env.Jobs.Wait(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	log, _, _ := h.Env.Jobs.Log(id, 0)
	return j, log
}

// Installed means working: Redis is started and answers PING; a service
// that does not start is reported with the cause from its log.
func TestComponentHealth(t *testing.T) {
	h := testhost.New(t)
	active := map[string]bool{}
	portTaken := false
	h.Respond = func(id string, a []string) (string, error, bool) {
		cmd := strings.Join(a, " ")
		switch {
		case id == "systemctl" && strings.HasPrefix(cmd, "is-active"):
			if active[a[len(a)-1]] {
				return "active\n", nil, true
			}
			return "inactive\n", errors.New("exit status 3"), true
		case id == "systemctl" && strings.HasPrefix(cmd, "start"):
			if !portTaken {
				active[a[len(a)-1]] = true
			}
			return "", nil, true
		case id == "journalctl":
			return "redis-server[812]: Could not create server TCP listening socket *:6379: bind: Address already in use\n", nil, true
		case id == "redis-cli":
			return "PONG\n", nil, true
		}
		return "", nil, false
	}
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	res, err := call(t, r, "packages.install", `{"name":"redis-server"}`)
	if err != nil {
		t.Fatal(err)
	}
	j, log := wait(t, h, res)
	if j.Status != jobs.Succeeded || !h.Runner.Called("systemctl start -- redis-server.service") || !h.Runner.Called("redis-cli ping") || !strings.Contains(log, "✓ redis-server: answers PING") {
		t.Fatalf("%+v\n%s", j, log)
	}

	active = map[string]bool{}
	portTaken = true
	res, _ = call(t, r, "packages.install", `{"name":"redis-server"}`)
	j, log = wait(t, h, res)
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "PORT_CONFLICT") || !strings.Contains(log, "Address already in use") {
		t.Fatalf("%+v\n%s", j, log)
	}
}
