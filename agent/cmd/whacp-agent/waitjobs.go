package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
)

// Exit codes of -wait-jobs.
const (
	waitIdle    = 0 // no job is running (or no agent is running)
	waitError   = 1 // the agent could not be asked
	waitTimeout = 3 // jobs were still running when the time was up
)

// activeJobs asks the agent listening on socket for its queued and running
// jobs. ok is false when no agent is listening there.
func activeJobs(ctx context.Context, socket, token string) (active []jobs.Job, ok bool, err error) {
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}, Timeout: 30 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://agent/v1/actions/jobs.list", strings.NewReader(`{"limit":200}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	var env struct {
		Success bool       `json:"success"`
		Message string     `json:"message"`
		Data    []jobs.Job `json:"data"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&env); err != nil {
		return nil, true, fmt.Errorf("unexpected answer from the agent (HTTP %d)", resp.StatusCode)
	}
	if !env.Success {
		return nil, true, fmt.Errorf("the agent refused: %s", env.Message)
	}
	for _, j := range env.Data {
		if jobs.Active(j.Status) {
			active = append(active, j)
		}
	}
	return active, true, nil
}

// waitJobs waits until the agent has no queued or running job, printing
// what it waits for. Used by upgrade.sh before it stops the services, so an
// upgrade does not interrupt a restore or a deployment half-way.
func waitJobs(ctx context.Context, socket, token string, limit, every time.Duration, out io.Writer) int {
	deadline := time.Now().Add(limit)
	last := ""
	for {
		active, ok, err := activeJobs(ctx, socket, token)
		if err != nil {
			fmt.Fprintln(out, "cannot list the agent's jobs:", err)
			return waitError
		}
		if !ok {
			fmt.Fprintln(out, "the agent is not running: no job to wait for")
			return waitIdle
		}
		if len(active) == 0 {
			return waitIdle
		}
		var names []string
		for _, j := range active {
			names = append(names, fmt.Sprintf("%s %s (%s, %s)", j.Kind, j.Target, j.Status, j.ID))
		}
		if list := strings.Join(names, "; "); list != last {
			fmt.Fprintf(out, "waiting for %d job(s): %s\n", len(active), list)
			last = list
		}
		if !time.Now().Before(deadline) {
			fmt.Fprintf(out, "jobs are still running after %s\n", limit)
			return waitTimeout
		}
		select {
		case <-ctx.Done():
			return waitError
		case <-time.After(every):
		}
	}
}
