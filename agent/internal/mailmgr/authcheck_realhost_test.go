//go:build realhost

package mailmgr

import (
	"context"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/spf"
)

// The checker and the SPF evaluator against real, published DNS.
func TestRealHostAuthCheckNetwork(t *testing.T) {
	if os.Getenv("WHACP_NETWORK") != "1" {
		t.Skip("needs WHACP_NETWORK=1 (public DNS)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ranges, err := spf.Ranges(ctx, net.DefaultResolver, "_spf.google.com")
	if err != nil || len(ranges) < 5 {
		t.Fatalf("Google's ranges: %v %v", ranges, err)
	}
	// A Google sending address passes gmail.com's SPF; a documentation
	// address does not.
	var google netip.Addr
	for _, p := range ranges {
		if p.Addr().Is4() {
			google = p.Addr().Next()
			break
		}
	}
	if res, err := spf.Check(ctx, net.DefaultResolver, google, "someone@gmail.com", ""); res != spf.Pass {
		t.Fatalf("%s for gmail.com: %s %v", google, res, err)
	}
	if res, _ := spf.Check(ctx, net.DefaultResolver, netip.MustParseAddr("192.0.2.1"), "someone@gmail.com", ""); res == spf.Pass {
		t.Fatal("192.0.2.1 passes gmail.com's SPF")
	}
	got := statuses(CheckAuth(ctx, net.DefaultResolver, AuthCheckInput{Domain: "gmail.com", Hostname: "whacp-check.invalid", IPs: []string{"192.0.2.1"}}))
	t.Logf("gmail.com as seen from 192.0.2.1: %v", got)
	if got["host"] != CheckMissing || got["mx"] != CheckFail || (got["spf"] != CheckWarn && got["spf"] != CheckFail) || (got["dmarc"] != CheckPass && got["dmarc"] != CheckWarn) || got["rdns"] == CheckPass {
		t.Fatalf("unexpected results: %v", got)
	}
}
