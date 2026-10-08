package security

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAllowPortsOneGuardedChange(t *testing.T) {
	old := ConfirmWindow
	ConfirmWindow = 100 * time.Millisecond
	defer func() { ConfirmWindow = old }()
	h, s := setup(t, "ufw allow 22/tcp\nufw allow 80/tcp\n")
	p, err := s.AllowPorts(context.Background(), []PortSpec{{Port: "80", Proto: "tcp"}, {Port: "443", Proto: "tcp"}, {Port: "53", Proto: "udp"}}, "Web server")
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || p.Summary != "firewall: allow 443/tcp, 53/udp" {
		t.Fatalf("pending: %+v", p)
	}
	calls := strings.Join(h.Runner.Calls, "\n")
	if strings.Contains(calls, "port 80 comment") || !strings.Contains(calls, "ufw allow proto tcp from any to any port 443 comment whacp: Web server") || !strings.Contains(calls, "ufw allow proto udp from any to any port 53") {
		t.Fatalf("calls:\n%s", calls)
	}
	waitRollback(t, s)
	if !h.Runner.Called("ufw --force delete allow proto tcp from any to any port 443") || !h.Runner.Called("ufw --force delete allow proto udp from any to any port 53") {
		t.Fatalf("not undone together: %v", h.Runner.Calls)
	}
	// Already open: nothing to do, no pending change.
	if p, err := s.AllowPorts(context.Background(), []PortSpec{{Port: "22", Proto: "tcp"}}, "SSH"); err != nil || p != nil {
		t.Fatalf("%+v %v", p, err)
	}
	for _, bad := range [][]PortSpec{nil, {{Port: "0", Proto: "tcp"}}, {{Port: "80", Proto: "icmp"}}, {{Port: "80;reboot", Proto: "tcp"}}} {
		if _, err := s.AllowPorts(context.Background(), bad, "x"); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

// Rocky Linux 9: firewalld on with only ssh, cockpit, dhcpv6-client.
func TestAllowPortsFirewalld(t *testing.T) {
	old := ConfirmWindow
	ConfirmWindow = time.Hour
	defer func() { ConfirmWindow = old }()
	h, s := setup(t, "")
	h.Runner.Unavailable = map[string]bool{"ufw": true}
	h.Runner.Outputs["firewall-cmd --state"] = "running"
	h.Runner.Outputs["firewall-cmd --permanent --list-services"] = "ssh cockpit dhcpv6-client"
	h.Runner.Outputs["firewall-cmd --permanent --list-ports"] = ""
	h.Runner.Outputs["firewall-cmd --permanent --list-rich-rules"] = ""
	p, err := s.AllowPorts(context.Background(), []PortSpec{{Port: "80", Proto: "tcp"}, {Port: "443", Proto: "tcp"}}, "Web server")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"firewall-cmd --add-port=80/tcp", "firewall-cmd --permanent --add-port=80/tcp", "firewall-cmd --add-port=443/tcp", "firewall-cmd --permanent --add-port=443/tcp"} {
		if !h.Runner.Called(want) {
			t.Errorf("missing %q: %v", want, h.Runner.Calls)
		}
	}
	if err := s.guard.Undo(p.ID); err != nil {
		t.Fatal(err)
	}
	if !h.Runner.Called("firewall-cmd --permanent --remove-port=443/tcp") || !h.Runner.Called("firewall-cmd --permanent --remove-port=80/tcp") {
		t.Errorf("not undone: %v", h.Runner.Calls)
	}
}
