package sitemetrics

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// fakePool answers like PHP-FPM's master does for the status page: FastCGI STDOUT records holding the
// headers, a blank line and the json, then END_REQUEST. It records the parameters it was sent.
func fakePool(t *testing.T, sock string, status func() map[string]any, got chan<- map[string]string) {
	t.Helper()
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				params := map[string]string{}
				var raw []byte
				for {
					h := make([]byte, 8)
					if _, err := io.ReadFull(c, h); err != nil {
						return
					}
					n := int(binary.BigEndian.Uint16(h[4:6]))
					body := make([]byte, n+int(h[6]))
					if _, err := io.ReadFull(c, body); err != nil {
						return
					}
					if h[1] == fcgiParams {
						raw = append(raw, body[:n]...)
					}
					if h[1] == fcgiStdin && n == 0 {
						break
					}
				}
				for len(raw) > 2 {
					kl, vl := int(raw[0]), int(raw[1])
					params[string(raw[2:2+kl])] = string(raw[2+kl : 2+kl+vl])
					raw = raw[2+kl+vl:]
				}
				if got != nil {
					got <- params
				}
				js, _ := json.Marshal(status())
				resp := append([]byte("Expires: Thu, 01 Jan 1970 00:00:00 GMT\r\nContent-Type: application/json\r\n\r\n"), js...)
				out := fcgiRecord(fcgiStdout, resp)
				out = append(out, fcgiRecord(fcgiStdout, nil)...)
				out = append(out, fcgiRecord(fcgiEnd, make([]byte, 8))...)
				c.Write(out)
			}(c)
		}
	}()
}

func status(start, accepted, slow, reached, queue, active, idle int) map[string]any {
	return map[string]any{"pool": "nimvor-a.test", "process manager": "ondemand", "start time": start, "start since": 100, "accepted conn": accepted,
		"listen queue": queue, "max listen queue": 4, "listen queue len": 128, "idle processes": idle, "active processes": active, "total processes": active + idle,
		"max active processes": 5, "max children reached": reached, "slow requests": slow}
}

func TestQueryStatusSpeaksFastCGI(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "p.sock")
	got := make(chan map[string]string, 1)
	fakePool(t, sock, func() map[string]any { return status(1000, 50, 1, 0, 0, 2, 1) }, got)
	body, err := QueryStatus(sock, "/nimvor-status", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	p := <-got
	if p["SCRIPT_NAME"] != "/nimvor-status" || p["SCRIPT_FILENAME"] != "/nimvor-status" || p["REQUEST_URI"] != "/nimvor-status" || p["QUERY_STRING"] != "json" || p["REQUEST_METHOD"] != "GET" {
		t.Errorf("params sent: %v", p)
	}
	st, err := ParseStatus(body)
	if err != nil || st.Active != 2 || st.Idle != 1 || st.Total != 3 || st.Accepted != 50 || st.MaxActive != 5 {
		t.Fatalf("status %+v %v", st, err)
	}
	if _, err := QueryStatus(filepath.Join(t.TempDir(), "none.sock"), "/x", 200*time.Millisecond); err == nil {
		t.Error("a missing socket is an error")
	}
	if _, err := ParseStatus([]byte("File not found.")); err == nil {
		t.Error("a pool without a status page answers 'File not found.': not a status page")
	}
}

func TestCollectAddsTheStatusPageOnlyWhenSwitchedOn(t *testing.T) {
	h := testhost.New(t)
	procDir := t.TempDir()
	h.Env.Cfg.ProcDir = procDir
	h.Env.Cfg.SitesRoot = "/var/www"
	sockDir := filepath.Join(h.Base, h.Env.M.PHPSocketDir)
	os.MkdirAll(sockDir, 0o755)
	cur := status(1000, 100, 0, 0, 0, 1, 2)
	fakePool(t, filepath.Join(sockDir, "nimvor-a.test.sock"), func() map[string]any { return cur }, nil)
	fakePool(t, filepath.Join(sockDir, "nimvor-idle.test.sock"), func() map[string]any { return status(2000, 5, 0, 0, 0, 0, 0) }, nil)
	proc(t, procDir, 100, "a.test", 100, 5000, 0, 0, 1000)

	c := New()
	c.uidName = func(*host.Env, uint32) string { return "u" }
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	off := c.Collect(h.Env, []string{"a.test", "idle.test"}, t0)
	if off.Sites[0].PHP == nil || off.Sites[0].PHP.FPM != nil {
		t.Fatalf("switched off: no status page is asked for: %+v", off.Sites[0].PHP)
	}
	if off.Sites[1].PHP != nil {
		t.Fatal("an idle on-demand pool shows nothing while the status page is off")
	}

	os.MkdirAll(filepath.Join(h.Base, h.Env.Cfg.StateDir), 0o755)
	os.WriteFile(filepath.Join(h.Base, h.Env.Cfg.StateDir, "fpm-status.json"), []byte(`{"enabled":true}`), 0o644)
	first := c.Collect(h.Env, []string{"a.test", "idle.test"}, t0.Add(60*time.Second))
	a := first.Sites[0].PHP
	if a.FPM == nil || a.FPM.Active != 1 || a.FPM.Idle != 2 || a.FPM.Total != 3 || a.FPM.MaxActive != 5 || a.Workers != 3 {
		t.Fatalf("a.test: %+v %+v", a, a.FPM)
	}
	if a.FPM.Accepted != nil || a.FPM.MaxChildrenReached != nil {
		t.Error("the first reading has no deltas")
	}
	idle := first.Sites[1].PHP
	if idle == nil || idle.FPM == nil || idle.Workers != 0 || idle.FPM.Total != 0 {
		t.Fatalf("an on-demand pool with no worker is a measurement of zero: %+v", idle)
	}

	// 60 s later: 40 requests, 2 slow, the pool hit pm.max_children 3 times, 2 requests are queued.
	cur = status(1000, 140, 2, 3, 2, 4, 0)
	second := c.Collect(h.Env, []string{"a.test"}, t0.Add(120*time.Second)).Sites[0].PHP.FPM
	if second.Accepted == nil || *second.Accepted != 40 || *second.SlowRequests != 2 || *second.MaxChildrenReached != 3 || second.Queue != 2 || second.Active != 4 {
		t.Fatalf("second: %+v", second)
	}
	// PHP-FPM restarted: its counters started again, so no delta is made up.
	cur = status(5000, 10, 0, 0, 0, 1, 0)
	third := c.Collect(h.Env, []string{"a.test"}, t0.Add(180*time.Second)).Sites[0].PHP.FPM
	if third.Accepted != nil || third.MaxChildrenReached != nil || third.Active != 1 {
		t.Fatalf("after a restart: %+v", third)
	}
}
