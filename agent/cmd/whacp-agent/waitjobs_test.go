package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
)

// fakeAgent answers jobs.list on a Unix socket: the job runs for the first
// `running` calls, then it has finished.
func fakeAgent(t *testing.T, running int32) (socket string, calls *atomic.Int32) {
	t.Helper()
	socket = filepath.Join(t.TempDir(), "agent.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	calls = &atomic.Int32{}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/actions/jobs.list" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "Authentication failed."})
			return
		}
		status := jobs.Succeeded
		if calls.Add(1) <= running {
			status = jobs.Running
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []jobs.Job{
			{ID: "aaaaaaaaaaaaaaaa", Kind: "backup.restore", Target: "site:shop.test", Status: status},
			{ID: "bbbbbbbbbbbbbbbb", Kind: "backup.create", Target: "site:blog.test", Status: jobs.Failed},
		}})
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return socket, calls
}

func TestWaitJobsUntilIdle(t *testing.T) {
	socket, calls := fakeAgent(t, 2)
	var out bytes.Buffer
	if rc := waitJobs(context.Background(), socket, "tok", time.Minute, 10*time.Millisecond, &out); rc != waitIdle {
		t.Fatalf("rc = %d: %s", rc, out.String())
	}
	if calls.Load() != 3 || !strings.Contains(out.String(), "waiting for 1 job(s): backup.restore site:shop.test (running") {
		t.Fatalf("calls %d, output %q", calls.Load(), out.String())
	}
}

func TestWaitJobsTimesOut(t *testing.T) {
	socket, _ := fakeAgent(t, 1000)
	var out bytes.Buffer
	if rc := waitJobs(context.Background(), socket, "tok", 50*time.Millisecond, 10*time.Millisecond, &out); rc != waitTimeout {
		t.Fatalf("rc = %d: %s", rc, out.String())
	}
}

func TestWaitJobsWithoutAgentOrWithBadToken(t *testing.T) {
	var out bytes.Buffer
	if rc := waitJobs(context.Background(), filepath.Join(t.TempDir(), "none.sock"), "tok", time.Second, time.Millisecond, &out); rc != waitIdle {
		t.Fatalf("no agent: rc = %d", rc)
	}
	// A refusal is an error, never "no jobs".
	socket, _ := fakeAgent(t, 0)
	if rc := waitJobs(context.Background(), socket, "wrong", time.Second, time.Millisecond, &out); rc != waitError {
		t.Fatalf("bad token: rc = %d", rc)
	}
}
