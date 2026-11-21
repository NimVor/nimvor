// Package probemgr checks that the websites on this server answer: one real
// HTTP request each, sent to this server itself (the website's own address, or
// loopback) with the website's name in the Host header. DNS and CDNs are not in
// the way, so the answer is the server's, and a redirect or a login page is an
// answer like any other.
package probemgr

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

const (
	// maxSites bounds one request.
	maxSites = 500
	// workers is how many websites are asked at once.
	workers = 8
	// defaultTimeout and maxTimeout bound one website's request.
	defaultTimeout = 10 * time.Second
	maxTimeout     = 30 * time.Second
	// maxBody is read (and thrown away) so the connection ends cleanly; the time
	// reported is the time to the first byte of the answer.
	maxBody = 64 << 10
)

// Site is one website to ask.
type Site struct {
	Domain string `json:"domain"`
	IPv4   string `json:"ipv4"`
	HTTPS  bool   `json:"https"`
}

// Result is what one website answered.
type Result struct {
	Domain string `json:"domain"`
	// Status is the HTTP status code, 0 when nothing answered.
	Status int `json:"status"`
	// Millis is the time to the first byte of the answer; 0 when nothing answered.
	Millis int64 `json:"ms"`
	// Error says why nothing answered: timeout, refused, tls or other.
	Error string `json:"error,omitempty"`
	// Scheme is the one used (https or http).
	Scheme string `json:"scheme"`
}

// interfaceAddrs lists this server's addresses (replaced in tests).
var interfaceAddrs = net.InterfaceAddrs

// thisServer reports whether ip is loopback or one of this server's own
// addresses: a website is only ever asked on the server itself.
func thisServer(ip net.IP) bool {
	if ip.To4() == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	addrs, err := interfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// portFor returns the port to connect to (overridden in tests).
var portFor = func(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// Probe asks one website.
func Probe(ctx context.Context, s Site, timeout time.Duration) Result {
	scheme := "http"
	if s.HTTPS {
		scheme = "https"
	}
	res := Result{Domain: s.Domain, Scheme: scheme}
	addr := "127.0.0.1"
	if s.IPv4 != "" {
		addr = s.IPv4
	}
	ip := net.ParseIP(addr)
	if ip == nil || !thisServer(ip) {
		res.Error = "other"
		return res
	}
	dialer := &net.Dialer{Timeout: timeout}
	tr := &http.Transport{
		// Always this server, whatever the website's name resolves to.
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", net.JoinHostPort(addr, portFor(scheme)))
		},
		// The certificate is not what is being checked here (the SSL widget does that), and it is
		// issued for the website's name, not for the address used.
		TLSClientConfig:       &tls.Config{ServerName: s.Domain, InsecureSkipVerify: true}, //nolint:gosec
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		DisableKeepAlives:     true,
		Proxy:                 nil,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport: tr,
		Timeout:   timeout,
		// A redirect is an answer; it is not followed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, scheme+"://"+s.Domain+"/", nil)
	if err != nil {
		res.Error = "other"
		return res
	}
	req.Header.Set("User-Agent", "NimVor-health-check")
	req.Header.Set("Accept", "*/*")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		res.Error = classify(err)
		return res
	}
	res.Millis = time.Since(start).Milliseconds()
	res.Status = resp.StatusCode
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	resp.Body.Close()
	return res
}

// classify names why a request got no answer.
func classify(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, syscall.EAFNOSUPPORT), errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EACCES):
		return "unsupported"
	}
	var cert *tls.CertificateVerificationError
	var rec tls.RecordHeaderError
	if errors.As(err, &cert) || errors.As(err, &rec) || strings.Contains(err.Error(), "tls:") {
		return "tls"
	}
	return "other"
}

// ProbeAll asks every website, a few at a time.
func ProbeAll(ctx context.Context, sites []Site, timeout time.Duration) []Result {
	out := make([]Result, len(sites))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i := range sites {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = Probe(ctx, sites[i], timeout)
		}(i)
	}
	wg.Wait()
	return out
}

// Register adds the website check action.
func Register(r *actions.Registry, _ *host.Env) {
	r.Register(actions.Action{Name: "sites.probe", Timeout: 5 * time.Minute, Description: "Ask each website for its home page on this server (its own address or loopback, with its name in the Host header) and report the status code and time to first byte, or why nothing answered. Redirects are not followed. Read-only.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Sites          []Site `json:"sites"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if len(in.Sites) > maxSites {
			return nil, actions.InvalidParams(fmt.Sprintf("sites: at most %d at a time", maxSites))
		}
		timeout := defaultTimeout
		if in.TimeoutSeconds > 0 {
			timeout = time.Duration(in.TimeoutSeconds) * time.Second
			if timeout > maxTimeout {
				timeout = maxTimeout
			}
		}
		for i := range in.Sites {
			if err := validate.Domain("domain", in.Sites[i].Domain); err != nil {
				return nil, host.Invalid(err)
			}
			if in.Sites[i].IPv4 != "" {
				if ip := net.ParseIP(in.Sites[i].IPv4); ip == nil || ip.To4() == nil {
					return nil, actions.InvalidParams("ipv4: not an IPv4 address")
				}
			}
		}
		results := ProbeAll(ctx, in.Sites, timeout)
		// When the agent may not open network connections at all (read-only mode), no website is "down":
		// the check is unsupported here.
		supported := true
		if len(results) > 0 {
			supported = false
			for _, r := range results {
				if r.Error != "unsupported" {
					supported = true
					break
				}
			}
		}
		return map[string]any{"supported": supported, "results": results}, nil
	}})
}
