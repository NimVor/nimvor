package server

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCertCreatedOnceAndPinStable(t *testing.T) {
	dir := t.TempDir()
	c1, err := LoadOrCreateCert(filepath.Join(dir, "a.crt"), filepath.Join(dir, "a.key"))
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := LoadOrCreateCert(filepath.Join(dir, "a.crt"), filepath.Join(dir, "a.key"))
	p1, _ := Pin(c1)
	p2, _ := Pin(c2)
	if p1 != p2 || !strings.HasPrefix(p1, "sha256//") {
		t.Fatalf("pins %s %s", p1, p2)
	}
}

func TestClientPolicy(t *testing.T) {
	nets, err := ParseCIDRs([]string{"203.0.113.4", "10.0.0.0/8", "2001:db8::1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCIDRs([]string{"not-an-ip"}); err == nil {
		t.Error("invalid address accepted")
	}
	now := time.Now()
	p := &ClientPolicy{Allowed: nets, MaxFailures: 3, Window: time.Minute, Now: func() time.Time { return now }}
	for _, a := range []string{"203.0.113.4:5555", "10.1.2.3:1", "[2001:db8::1]:80"} {
		if !p.Allows(a) {
			t.Errorf("%s should be allowed", a)
		}
	}
	for _, a := range []string{"203.0.113.5:1", "[2001:db8::2]:80", "garbage"} {
		if p.Allows(a) {
			t.Errorf("%s should be refused", a)
		}
	}
	for i := 0; i < 3; i++ {
		p.Failed("10.1.2.3:1")
	}
	if p.Allows("10.1.2.3:9") || !p.Allows("10.9.9.9:1") {
		t.Error("lockout should apply to the failing address only")
	}
	now = now.Add(2 * time.Minute)
	if !p.Allows("10.1.2.3:9") {
		t.Error("lockout should expire")
	}
}

func TestTLSListenerEndToEnd(t *testing.T) {
	dir := t.TempDir()
	cert, err := LoadOrCreateCert(filepath.Join(dir, "a.crt"), filepath.Join(dir, "a.key"))
	if err != nil {
		t.Fatal(err)
	}
	pin, _ := Pin(cert)
	ln, err := ListenTLS("127.0.0.1:0", cert)
	if err != nil {
		t.Fatal(err)
	}
	nets, _ := ParseCIDRs([]string{"127.0.0.1"})
	policy := &ClientPolicy{Allowed: nets, MaxFailures: 3, Window: time.Minute}
	srv := &http.Server{Handler: policy.Guard(newServer().Handler())}
	go srv.Serve(ln)
	defer srv.Close()
	url := "https://" + ln.Addr().String() + "/v1/health"

	// A client that checks the pin instead of a CA.
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			c, _ := x509.ParseCertificate(raw[0])
			sum := sha256.Sum256(c.RawSubjectPublicKeyInfo)
			if "sha256//"+base64.StdEncoding.EncodeToString(sum[:]) != pin {
				t.Fatal("pin mismatch")
			}
			return nil
		},
	}}}
	get := func(tok string) int {
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if got := get(token); got != 200 {
		t.Fatalf("good token: %d", got)
	}
	for i := 0; i < 3; i++ {
		if got := get("wrong"); got != 401 {
			t.Fatalf("bad token: %d", got)
		}
	}
	if got := get(token); got != 403 {
		t.Fatalf("address should be locked out after repeated failures, got %d", got)
	}

	// TLS 1.2 is refused.
	old := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12}}}
	if _, err := old.Get(url); err == nil {
		t.Error("TLS 1.2 accepted")
	}

	// curl (as used by the PHP panel) accepts the pin format and rejects others.
	if _, err := exec.LookPath("curl"); err == nil {
		ok := exec.Command("curl", "-sk", "--pinnedpubkey", pin, "-o", "/dev/null", "-w", "%{http_code}", url)
		out, err := ok.Output()
		if err != nil || (string(out) != "401" && string(out) != "403") {
			t.Errorf("curl with the right pin: %s %v", out, err)
		}
		bad := exec.Command("curl", "-sk", "--pinnedpubkey", "sha256//AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", url)
		if err := bad.Run(); err == nil {
			t.Error("curl accepted a wrong pin")
		}
	}
}
