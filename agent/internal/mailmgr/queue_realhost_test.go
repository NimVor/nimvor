//go:build realhost

package mailmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

// The queue actions against a real Postfix: a message to an unresolvable
// domain is deferred; it is listed, its headers read, held, released and
// deleted.
func TestRealHostMailQueue(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("postqueue"); err != nil {
		t.Skip("needs Postfix")
	}
	// Without a journal (containers), Postfix logs to a file for the report.
	if _, err := os.Stat("/run/systemd/journal/socket"); err != nil {
		prev, _ := exec.Command("postconf", "-h", "maillog_file").Output()
		exec.Command("postconf", "-e", "maillog_file=/var/log/postfix.log").Run()
		t.Cleanup(func() { exec.Command("postconf", "-e", "maillog_file="+strings.TrimSpace(string(prev))).Run() })
		postfixStop()
	}
	if exec.Command("postfix", "status").Run() != nil {
		if out, err := postfixStart(); err != nil {
			t.Fatalf("postfix start: %s", out)
		}
		t.Cleanup(func() { postfixStop() })
	}
	// Remote delivery stays deferred while the test runs, whatever DNS
	// answers for the .invalid domain (a definite "not found" bounces it).
	prevDefer, _ := exec.Command("postconf", "-h", "defer_transports").Output()
	exec.Command("postconf", "-e", "defer_transports=smtp").Run()
	exec.Command("postfix", "reload").Run()
	t.Cleanup(func() {
		exec.Command("postconf", "-e", "defer_transports="+strings.TrimSpace(string(prevDefer))).Run()
		exec.Command("postfix", "reload").Run()
	})
	rcpt := fmt.Sprintf("n%d@whacp-queue-test.invalid", time.Now().UnixNano())
	msg := "From: Tester <tester@example.com>\nTo: " + rcpt + "\nSubject: =?UTF-8?B?UXVldWUgdGVzdCDinJM=?=\n\nhello\n"
	cmd := exec.Command("sendmail", "-f", "tester@example.com", rcpt)
	cmd.Stdin = strings.NewReader(msg)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sendmail: %v %s", err, out)
	}
	fsys := filesystem.Host()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(config.Default(), fsys, platform.Detect(fsys), execx.NewSystemRunner(30*time.Second), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)
	call := func(name string, params any) map[string]any {
		a, _ := r.Lookup(name)
		b, _ := json.Marshal(params)
		res, err := a.Handler(context.Background(), b)
		if err != nil {
			t.Fatalf("%s: %+v", name, err)
		}
		out, _ := json.Marshal(res)
		var m map[string]any
		json.Unmarshal(out, &m)
		return m
	}
	var id string
	for i := 0; i < 30 && id == ""; i++ {
		for _, m := range call("mail.queue", nil)["messages"].([]any) {
			m := m.(map[string]any)
			// Wait until pickup gave it its final ID (maildrop IDs change).
			if m["sender"] == "tester@example.com" && m["queue"] == "deferred" && strings.Contains(strings.Join(toStrings(m["recipients"]), ","), rcpt) {
				id = m["id"].(string)
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if id == "" {
		q, _ := exec.Command("postqueue", "-p").CombinedOutput()
		st, _ := exec.Command("postfix", "status").CombinedOutput()
		lg, _ := exec.Command("journalctl", "--no-pager", "-n", "20", "-t", "postfix/smtp", "-t", "postfix/qmgr", "-t", "postfix/pickup", "-t", "postfix/error", "--since", "-2min").CombinedOutput()
		t.Fatalf("the test message did not show up in the queue\n%s\n%s\n%s", st, q, lg)
	}
	t.Cleanup(func() { exec.Command("postsuper", "-d", id).Run() })
	h := call("mail.queue_message", map[string]any{"id": id})["headers"].(map[string]any)
	if h["subject"] != "Queue test ✓" || !strings.Contains(h["from"].(string), "tester@example.com") {
		t.Fatalf("headers: %v", h)
	}
	inQueue := func(queue string) bool {
		for _, m := range call("mail.queue", nil)["messages"].([]any) {
			if m.(map[string]any)["id"] == id && (queue == "" || m.(map[string]any)["queue"] == queue) {
				return true
			}
		}
		return false
	}
	call("mail.queue_action", map[string]any{"action": "hold", "ids": []string{id}})
	if !inQueue("hold") {
		t.Fatal("not held")
	}
	call("mail.queue_action", map[string]any{"action": "release", "ids": []string{id}})
	if inQueue("hold") {
		t.Fatal("not released")
	}
	call("mail.queue_action", map[string]any{"action": "delete", "ids": []string{id}})
	if inQueue("") {
		t.Fatal("not deleted")
	}
	// The deferral shows in the report (from the log).
	var rep map[string]any
	for i := 0; i < 20; i++ {
		rep = call("mail.report", map[string]any{"address": rcpt})
		if len(rep["report"].(map[string]any)["deliveries"].([]any)) > 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	ds := rep["report"].(map[string]any)["deliveries"].([]any)
	if len(ds) == 0 || ds[0].(map[string]any)["status"] != "deferred" || ds[0].(map[string]any)["from"] != "tester@example.com" {
		t.Fatalf("report (%v): %v", rep["source"], rep["report"])
	}
	a, _ := r.Lookup("mail.queue_action")
	if _, err := a.Handler(context.Background(), json.RawMessage(`{"action":"delete","ids":["ABC; rm -rf /"]}`)); err == nil {
		t.Fatal("a bad queue ID was accepted")
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
