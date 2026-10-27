package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	maxRequestBody  = 1 << 20
	maxResponseBody = 4 << 20
)

// Request is what the panel forwards for a signed-in user.
type Request struct {
	Method string
	Path   string // the path after /plugins/<name>
	Query  string
	Body   []byte
	Type   string // request Content-Type
	UserID string
	User   string
	Role   string
	CSRF   string
	Server string
}

// Response is a plugin's answer.
type Response struct {
	Status   int
	Type     string
	Title    string
	Location string
	Body     []byte
}

// ErrUnavailable means the plugin's socket could not be used.
var ErrUnavailable = errors.New("the plugin is not running or did not answer")

func client(socket string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Forward sends a request to the plugin and returns its answer. The identity
// headers are set by the panel only; a plugin must trust them only because
// its socket is reachable by the panel alone.
func Forward(ctx context.Context, p *Plugin, in Request) (*Response, error) {
	if len(in.Body) > maxRequestBody {
		return nil, errors.New("the request is too large")
	}
	url := "http://plugin" + in.Path
	if in.Query != "" {
		url += "?" + in.Query
	}
	req, err := http.NewRequestWithContext(ctx, in.Method, url, bytes.NewReader(in.Body))
	if err != nil {
		return nil, err
	}
	if in.Type != "" {
		req.Header.Set("Content-Type", in.Type)
	}
	req.Header.Set("X-WhaCP-User-ID", in.UserID)
	req.Header.Set("X-WhaCP-User", in.User)
	req.Header.Set("X-WhaCP-Role", in.Role)
	req.Header.Set("X-WhaCP-CSRF", in.CSRF)
	req.Header.Set("X-WhaCP-Server", in.Server)
	res, err := client(p.Manifest.Socket, 30*time.Second).Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBody+1))
	if err != nil || len(body) > maxResponseBody {
		return nil, ErrUnavailable
	}
	ct := res.Header.Get("Content-Type")
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	return &Response{Status: res.StatusCode, Type: strings.TrimSpace(strings.ToLower(ct)),
		Title: res.Header.Get("X-WhaCP-Title"), Location: res.Header.Get("Location"), Body: body}, nil
}

// SafeRedirect reports whether a plugin may send the user to location: a
// path on this panel only.
func SafeRedirect(location string) bool {
	return strings.HasPrefix(location, "/") && !strings.HasPrefix(location, "//") && !strings.ContainsAny(location, "\r\n\\")
}

// Emit delivers a panel event to every active plugin subscribed to it
// (POST /_events/<event> with the payload as JSON). Failures are returned
// per plugin and never affect the panel.
func (m *Manager) Emit(ctx context.Context, event string, payload any) map[string]error {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	failed := map[string]error{}
	for _, p := range m.Active(ctx) {
		subscribed := false
		for _, e := range p.Manifest.Events {
			subscribed = subscribed || e == event
		}
		if !subscribed {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://plugin/_events/"+event, bytes.NewReader(b))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := client(p.Manifest.Socket, 5*time.Second).Do(req)
		if err != nil {
			failed[p.Name] = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
		res.Body.Close()
		if res.StatusCode >= 400 {
			failed[p.Name] = errors.New(res.Status)
		}
	}
	return failed
}
