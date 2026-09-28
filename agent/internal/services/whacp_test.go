package services

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func TestWhacpServices(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	var mu sync.Mutex
	var calls []string
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id != "systemctl" {
			return "", nil, false
		}
		mu.Lock()
		calls = append(calls, strings.Join(args, " "))
		mu.Unlock()
		switch args[0] {
		case "show":
			return "Id=whacp-agent.service\nLoadState=loaded\nActiveState=active\nSubState=running\n\nId=whacp-panel.service\nLoadState=loaded\nActiveState=active\nSubState=running\n\nId=whacp-ftpd.service\nLoadState=not-found\nActiveState=inactive\n", nil, true
		case "is-active":
			return "active\n", nil, true
		}
		return "", nil, true
	}
	call := func(name string, params any) (map[string]any, error) {
		a, err := r.Lookup(name)
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(params)
		res, err := a.Handler(context.Background(), b)
		if err != nil {
			return nil, err
		}
		out, _ := json.Marshal(res)
		var m map[string]any
		json.Unmarshal(out, &m)
		return m, nil
	}
	st, err := call("whacp.services", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(st["services"].([]any)); n != 2 {
		t.Fatalf("services (units not found are left out): %v", st)
	}
	restartDelay = 10 * time.Millisecond
	res, err := call("whacp.restart", map[string]string{"key": "agent"})
	if err != nil || res["scheduled"] != true {
		t.Fatalf("agent: %v %v", res, err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := call("whacp.restart", map[string]string{"key": "scheduler"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	joined := strings.Join(calls, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "restart --no-block -- whacp-agent.service") || !strings.Contains(joined, "restart -- whacp-scheduler.timer") {
		t.Fatalf("calls:\n%s", joined)
	}
	if _, err := call("whacp.restart", map[string]string{"key": "nginx"}); err == nil {
		t.Fatal("unknown key accepted")
	}
	if _, err := call("services.control", map[string]string{"unit": "whacp-panel.service", "op": "stop"}); err == nil {
		t.Fatal("the panel could be stopped")
	}
}
