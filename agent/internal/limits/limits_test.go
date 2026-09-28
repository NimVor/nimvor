package limits

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func TestDirectivesAndCronDeny(t *testing.T) {
	d := Directives(Limits{CPUPercent: 200, MemoryMB: 2048, TasksMax: 256, IOReadMBps: 50, IOWriteMBps: 20}, "/home/whacp")
	want := []string{"CPUQuota=200%", "MemoryHigh=1843M", "MemoryMax=2048M", "TasksMax=256", "IOReadBandwidthMax=/home/whacp 50M", "IOWriteBandwidthMax=/home/whacp 20M"}
	if strings.Join(d, "|") != strings.Join(want, "|") {
		t.Fatalf("%v", d)
	}
	if len(Directives(Limits{}, "/x")) != 0 {
		t.Fatal("empty limits produced directives")
	}
	for _, bad := range []Limits{{CPUPercent: 5}, {MemoryMB: 10}, {TasksMax: 3}, {CPUPercent: -1}} {
		if bad.Validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	cur := "daemon\nbin\n"
	next := RenderCronDeny(cur, []string{"zed", "amy"})
	if next != "daemon\nbin\n"+CronDenyStart+"\namy\nzed\n"+CronDenyEnd+"\n" {
		t.Fatalf("deny:\n%s", next)
	}
	if RenderCronDeny(next, nil) != cur {
		t.Fatalf("block not removed:\n%s", RenderCronDeny(next, nil))
	}
	if RenderCronDeny(CronDenyStart+"\nx\n"+CronDenyEnd+"\n", nil) != "" {
		t.Fatal("only WhaCP's block: file becomes empty")
	}
	if !ValidSlice("whacp-acct12.slice") || ValidSlice("whacp-acct12.slice/../x") || ValidSlice("user.slice") {
		t.Fatal("ValidSlice")
	}
}

func TestSetAndClear(t *testing.T) {
	h := testhost.New(t)
	os.MkdirAll(filepath.Join(h.Base, "sys/fs/cgroup"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "sys/fs/cgroup/cgroup.controllers"), []byte("cpu memory pids io\n"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "etc/cron.deny"), []byte("daemon\n"), 0o644)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	call := func(p string) (map[string]any, error) {
		a, _ := r.Lookup("limits.set")
		res, err := a.Handler(context.Background(), json.RawMessage(p))
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(res)
		var m map[string]any
		json.Unmarshal(b, &m)
		return m, nil
	}
	if _, err := call(`{"account":"acct7","users":["example"],"cpu_percent":150,"memory_mb":1024,"tasks_max":200}`); err != nil {
		t.Fatal(err)
	}
	unit, _ := os.ReadFile(filepath.Join(h.Base, h.Env.M.UnitDir, "whacp-acct7.slice"))
	if !strings.Contains(string(unit), "CPUQuota=150%") || !strings.Contains(string(unit), "MemoryMax=1024M") {
		t.Fatalf("slice:\n%s", unit)
	}
	drop, _ := os.ReadFile(filepath.Join(h.Base, h.Env.M.UnitDir, "user-"+itoa(h.UID)+".slice.d/50-whacp-limits.conf"))
	if !strings.Contains(string(drop), "TasksMax=200") {
		t.Fatalf("drop-in:\n%s", drop)
	}
	if !h.Runner.Called("systemctl set-property --runtime -- whacp-acct7.slice CPUQuota=150% MemoryHigh=921M MemoryMax=1024M TasksMax=200") {
		t.Fatalf("calls: %v", h.Runner.Calls)
	}
	if deny, _ := os.ReadFile(filepath.Join(h.Base, "etc/cron.deny")); !strings.Contains(string(deny), "daemon\n"+CronDenyStart+"\nexample\n") {
		t.Fatalf("cron.deny:\n%s", deny)
	}
	if got := SliceFor(h.Env, "example"); got != "whacp-acct7.slice" {
		t.Fatalf("SliceFor = %q", got)
	}
	if cred, _, _ := h.Env.Credential("example"); cred.Slice != "whacp-acct7.slice" {
		t.Fatalf("credential slice = %q", cred.Slice)
	}
	for _, bad := range []string{`{"account":"../x","users":[]}`, `{"account":"acct7","users":["root"],"cpu_percent":100}`, `{"account":"acct7","users":["x;y"]}`, `{"account":"acct7","users":[],"memory_mb":5}`} {
		if _, err := call(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	// Clearing removes the slice, the drop-in and the cron.deny block.
	if _, err := call(`{"account":"acct7","users":["example"]}`); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"whacp-acct7.slice", "user-" + itoa(h.UID) + ".slice.d/50-whacp-limits.conf"} {
		if _, err := os.Stat(filepath.Join(h.Base, h.Env.M.UnitDir, f)); !os.IsNotExist(err) {
			t.Errorf("%s left behind", f)
		}
	}
	if deny, _ := os.ReadFile(filepath.Join(h.Base, "etc/cron.deny")); string(deny) != "daemon\n" {
		t.Fatalf("cron.deny:\n%s", deny)
	}
	if SliceFor(h.Env, "example") != "" {
		t.Fatal("still assigned")
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
