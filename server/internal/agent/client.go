// Package agent is the panel's client for the WhaCP system agent. It speaks the
// existing wire protocol unchanged: HTTP over a Unix-domain socket, a bearer
// token read from a file, POST /v1/actions/<name> with a JSON body, and the
// {success,data,error{code,message,details},request_id} envelope. The panel
// never runs privileged commands itself; every host change is an agent action,
// and the caller must authorize the operation before invoking the client.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

var actionName = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// Error is a structured agent failure, mirroring the PHP AgentException fields.
type Error struct {
	Code    string
	Message string
	Details string
	Status  int
}

func (e *Error) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("agent %s: %s (%s)", e.Code, e.Message, e.Details)
	}
	return fmt.Sprintf("agent %s: %s", e.Code, e.Message)
}

// Client calls named actions on the agent over its Unix socket.
type Client struct {
	socketPath string
	tokenFile  string
	http       *http.Client
	timeout    time.Duration
}

// New returns a client dialling socketPath and authenticating with the token in
// tokenFile (read fresh on each call so rotation is picked up).
func New(socketPath, tokenFile string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		DisableKeepAlives: false,
	}
	return &Client{socketPath: socketPath, tokenFile: tokenFile, http: &http.Client{Transport: tr}, timeout: timeout}
}

func (c *Client) token() (string, error) {
	b, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return "", fmt.Errorf("agent token: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

type envelope struct {
	Success   bool            `json:"success"`
	Data      json.RawMessage `json:"data"`
	RequestID string          `json:"request_id"`
	Error     *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details string `json:"details"`
	} `json:"error"`
}

// Health calls GET /v1/health and returns the decoded data.
func (c *Client) Health(ctx context.Context) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, "/v1/health", nil, 5*time.Second)
}

// Call executes an action and returns its raw data payload. Unmarshal it into a
// typed struct at the call site.
func (c *Client) Call(ctx context.Context, action string, params any, timeout ...time.Duration) (json.RawMessage, error) {
	if !actionName.MatchString(action) {
		return nil, fmt.Errorf("invalid agent action name %q", action)
	}
	var body []byte
	if params == nil {
		body = []byte("{}")
	} else {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		body = b
	}
	to := c.timeout
	if len(timeout) > 0 && timeout[0] > 0 {
		to = timeout[0]
	}
	return c.do(ctx, http.MethodPost, "/v1/actions/"+action, body, to)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, timeout time.Duration) (json.RawMessage, error) {
	tok, err := c.token()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	// The host part is ignored because the transport always dials the socket.
	req, err := http.NewRequestWithContext(ctx, method, "http://agent"+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Code: "agent_unreachable", Message: "The WhaCP agent could not be reached.", Details: err.Error(), Status: 502}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, &Error{Code: "agent_bad_response", Message: "The WhaCP agent returned an unexpected response.", Status: resp.StatusCode}
	}
	if env.Success {
		return env.Data, nil
	}
	code, msg, det := "agent_error", "The agent reported an error.", ""
	if env.Error != nil {
		code, msg, det = env.Error.Code, env.Error.Message, env.Error.Details
	}
	status := resp.StatusCode
	if status < 400 {
		status = 502
	}
	return nil, &Error{Code: code, Message: msg, Details: det, Status: status}
}
