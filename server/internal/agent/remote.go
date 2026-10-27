package agent

// The client for a remote agent: HTTPS with TLS 1.3 whose certificate is
// self-signed, so trust comes from a pin of its public key recorded when the
// server was added (RemoteTransport in the PHP panel). The key is checked in
// the handshake, before the request — and the token — is sent.

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"regexp"
	"time"
)

var (
	remoteURLRe   = regexp.MustCompile(`^https://([A-Za-z0-9.-]+|\[[0-9a-fA-F:]+\])(:[0-9]{1,5})?$`)
	remotePinRe   = regexp.MustCompile(`^sha256//[A-Za-z0-9+/]{43}=$`)
	errPinNoMatch = errors.New("the remote agent's key does not match the pin")
)

// ValidRemoteURL reports whether u is an agent address (https://host[:port]).
func ValidRemoteURL(u string) bool { return remoteURLRe.MatchString(u) }

// ValidPin reports whether p is a public-key pin (sha256//base64).
func ValidPin(p string) bool { return remotePinRe.MatchString(p) }

// PinOf is the pin of a certificate's public key.
func PinOf(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return "sha256//" + base64.StdEncoding.EncodeToString(sum[:])
}

// NewRemote returns a client for the agent at baseURL, which must present
// exactly the pinned key. An invalid address or pin gives a client whose
// every call fails with agent_unavailable.
func NewRemote(baseURL, pin, token string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	c := &Client{remote: true, base: baseURL, staticToken: token, timeout: timeout}
	if !ValidRemoteURL(baseURL) || !ValidPin(pin) {
		c.http = &http.Client{Transport: failingTransport{}}
		return c
	}
	tr := &http.Transport{
		DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			// Self-signed: the chain is not verified, the pinned key is.
			InsecureSkipVerify: true,
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				if len(raw) == 0 {
					return errPinNoMatch
				}
				cert, err := x509.ParseCertificate(raw[0])
				if err != nil || subtle.ConstantTimeCompare([]byte(PinOf(cert)), []byte(pin)) != 1 {
					return errPinNoMatch
				}
				return nil
			},
		},
	}
	c.http = &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return c
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("invalid remote agent configuration")
}

// remoteFailure classifies a transport failure like RemoteTransport does.
func remoteFailure(err error) *Error {
	switch {
	case errors.Is(err, errPinNoMatch):
		return &Error{Code: "agent_pin_mismatch", Message: "The remote server presented a different certificate.",
			Details: "Its TLS key does not match the pin recorded for it. Nothing was sent. If the agent was reinstalled, verify the new pin (whacp-agent -pin on that server) and update it.", Status: 502}
	case errors.Is(err, context.DeadlineExceeded):
		return &Error{Code: "agent_timeout", Message: "The remote agent did not respond in time.",
			Details: "Check that the agent listens on this address and the firewall allows the panel.", Status: 504}
	}
	return &Error{Code: "agent_unavailable", Message: "The remote agent is not reachable.",
		Details: "Check that the agent listens on this address, the firewall allows the panel, and allowed_clients includes this server.", Status: 502}
}
