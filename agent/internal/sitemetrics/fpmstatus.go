package sitemetrics

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"time"
)

// FPM is a pool's own account of itself, from PHP-FPM's status page (switched
// on by an administrator: sites.fpm_status_set). Instants are as of the call;
// the *Delta fields are what happened since the previous call, and are nil
// when they cannot be told (first call, or PHP-FPM restarted in between).
type FPM struct {
	Active    int   `json:"active"`
	Idle      int   `json:"idle"`
	Total     int   `json:"total"`
	Queue     int   `json:"queue"`      // requests waiting for a worker right now
	MaxQueue  int   `json:"max_queue"`  // the most that waited since PHP-FPM started
	MaxActive int   `json:"max_active"` // the most workers busy at once since PHP-FPM started
	StartTime int64 `json:"start_time"`
	// Deltas since the previous call.
	MaxChildrenReached *int64 `json:"max_children_reached,omitempty"` // times the pool wanted more workers than pm.max_children allows
	SlowRequests       *int64 `json:"slow_requests,omitempty"`
	Accepted           *int64 `json:"accepted,omitempty"` // requests the pool accepted
}

type fpmCounters struct {
	start                 int64
	accepted, slow, reach int64
}

// statusJSON is PHP-FPM's status page in its json format.
type statusJSON struct {
	StartTime int64 `json:"start time"`
	Accepted  int64 `json:"accepted conn"`
	Queue     int   `json:"listen queue"`
	MaxQueue  int   `json:"max listen queue"`
	Idle      int   `json:"idle processes"`
	Active    int   `json:"active processes"`
	Total     int   `json:"total processes"`
	MaxActive int   `json:"max active processes"`
	Reached   int64 `json:"max children reached"`
	Slow      int64 `json:"slow requests"`
}

// ParseStatus reads the body of the status page.
func ParseStatus(body []byte) (statusJSON, error) {
	var s statusJSON
	if err := json.Unmarshal(body, &s); err != nil {
		return s, err
	}
	if s.Total == 0 && s.Idle == 0 && s.Active == 0 && s.StartTime == 0 {
		return s, errors.New("not a PHP-FPM status page")
	}
	return s, nil
}

const (
	fcgiBegin  = 1
	fcgiParams = 4
	fcgiStdin  = 5
	fcgiStdout = 6
	fcgiEnd    = 3
)

func fcgiRecord(typ byte, body []byte) []byte {
	h := make([]byte, 8, 8+len(body))
	h[0], h[1] = 1, typ
	binary.BigEndian.PutUint16(h[2:], 1) // request id
	binary.BigEndian.PutUint16(h[4:], uint16(len(body)))
	return append(h, body...)
}

func fcgiPair(k, v string) []byte {
	return append(append([]byte{byte(len(k)), byte(len(v))}, k...), v...)
}

// QueryStatus asks a pool's FastCGI socket for its status page (json). It is
// answered by PHP-FPM's master process for the pool: no PHP file is run.
func QueryStatus(sock, path string, timeout time.Duration) ([]byte, error) {
	c, err := net.DialTimeout("unix", sock, timeout)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(timeout))
	var params []byte
	for _, kv := range [][2]string{{"SCRIPT_NAME", path}, {"SCRIPT_FILENAME", path}, {"REQUEST_URI", path}, {"REQUEST_METHOD", "GET"}, {"QUERY_STRING", "json"}} {
		params = append(params, fcgiPair(kv[0], kv[1])...)
	}
	req := fcgiRecord(fcgiBegin, []byte{0, 1, 0, 0, 0, 0, 0, 0}) // responder, no keep-alive
	req = append(req, fcgiRecord(fcgiParams, params)...)
	req = append(req, fcgiRecord(fcgiParams, nil)...)
	req = append(req, fcgiRecord(fcgiStdin, nil)...)
	if _, err := c.Write(req); err != nil {
		return nil, err
	}
	out, err := io.ReadAll(io.LimitReader(c, 1<<20))
	if err != nil && len(out) == 0 {
		return nil, err
	}
	var body bytes.Buffer
	for len(out) >= 8 {
		n := int(binary.BigEndian.Uint16(out[4:6]))
		pad := int(out[6])
		if len(out) < 8+n+pad {
			break
		}
		if out[1] == fcgiStdout {
			body.Write(out[8 : 8+n])
		}
		if out[1] == fcgiEnd {
			break
		}
		out = out[8+n+pad:]
	}
	s := body.String()
	i := strings.Index(s, "\r\n\r\n")
	if i < 0 {
		return nil, errors.New("no response from the pool")
	}
	return []byte(s[i+4:]), nil
}

// fpmOf turns a status page into a sample, with deltas against prev.
func fpmOf(s statusJSON, prev *fpmCounters) (*FPM, fpmCounters) {
	f := &FPM{Active: s.Active, Idle: s.Idle, Total: s.Total, Queue: s.Queue, MaxQueue: s.MaxQueue, MaxActive: s.MaxActive, StartTime: s.StartTime}
	cur := fpmCounters{start: s.StartTime, accepted: s.Accepted, slow: s.Slow, reach: s.Reached}
	if prev != nil && prev.start == s.StartTime && s.Accepted >= prev.accepted && s.Slow >= prev.slow && s.Reached >= prev.reach {
		a, sl, r := s.Accepted-prev.accepted, s.Slow-prev.slow, s.Reached-prev.reach
		f.Accepted, f.SlowRequests, f.MaxChildrenReached = &a, &sl, &r
	}
	return f, cur
}
