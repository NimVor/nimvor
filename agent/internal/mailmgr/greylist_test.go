package mailmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/greylist"
)

func TestWithGreylisting(t *testing.T) {
	on := withGreylisting("", true)
	if on != "permit_mynetworks, permit_sasl_authenticated, reject_unauth_destination, "+greylistClause {
		t.Fatalf("on: %s", on)
	}
	if withGreylisting(on, true) != on {
		t.Fatal("added twice")
	}
	if got := withGreylisting(on, false); got != "permit_mynetworks, permit_sasl_authenticated, reject_unauth_destination" {
		t.Fatalf("off: %q", got)
	}
	custom := "permit_mynetworks, reject_unauth_destination, check_policy_service { unix:private/whacp-greylist, default_action=DUNNO }, reject_rbl_client zen.example"
	if got := withGreylisting(custom, false); got != "permit_mynetworks, reject_unauth_destination, reject_rbl_client zen.example" {
		t.Fatalf("custom off: %q", got)
	}
}

type providerDNS struct{}

func (providerDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	switch name {
	case "_spf.google.com":
		return []string{"v=spf1 include:_netblocks.google.com ~all"}, nil
	case "_netblocks.google.com":
		return []string{"v=spf1 ip4:209.85.128.0/17 ip6:2a00:1450:4000::/36 ~all"}, nil
	}
	return nil, &net.DNSError{Name: name, IsNotFound: true}
}
func (providerDNS) LookupNetIP(_ context.Context, _, h string) ([]netip.Addr, error) {
	return nil, &net.DNSError{Name: h, IsNotFound: true}
}
func (providerDNS) LookupMX(_ context.Context, h string) ([]*net.MX, error) {
	return nil, &net.DNSError{Name: h, IsNotFound: true}
}

func TestGreylistActions(t *testing.T) {
	h, r := setup(t)
	if _, err := call(t, r, "mail.greylist_set", map[string]any{"enabled": true, "deferral": 5, "resend": 240, "expire": 4320}); code(err) != "conflict" {
		t.Fatalf("before email setup: %v", err)
	}
	h.Env.Cfg.SimulateCommands = true
	if _, err := call(t, r, "mail.configure", map[string]any{"hostname": "mail.example.com", "confirm_edit_package_config": true}); err != nil {
		t.Fatal(err)
	}
	h.Env.Cfg.SimulateCommands = false
	oldR, oldC := greylistResolver, greylistCheck
	greylistResolver = providerDNS{}
	checked := ""
	greylistCheck = func(_ context.Context, socket string) error { checked = socket; return nil }
	defer func() { greylistResolver, greylistCheck = oldR, oldC }()
	postconf := map[string]string{"smtpd_recipient_restrictions": "", "queue_directory": "/var/spool/postfix"}
	failCheck := false
	var systemctl []string
	h.Respond = func(id string, args []string) (string, error, bool) {
		switch {
		case id == "postconf" && len(args) == 2 && args[0] == "-h":
			return postconf[args[1]] + "\n", nil, true
		case id == "postconf" && len(args) == 2 && args[0] == "-e":
			k, v, _ := strings.Cut(args[1], "=")
			postconf[k] = v
			return "", nil, true
		case id == "postfix" && len(args) == 1 && args[0] == "check" && failCheck:
			return "postfix: fatal: bad", fmt.Errorf("exit status 1"), true
		case id == "systemctl" && args[0] == "is-active":
			return "inactive\n", fmt.Errorf("exit status 3"), true
		case id == "systemctl":
			systemctl = append(systemctl, strings.Join(args, " "))
		}
		return "", nil, false
	}
	get, err := call(t, r, "mail.greylist_get", nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := get["config"].(map[string]any)
	if cfg["enabled"] != false || cfg["deferral"] != float64(5) || cfg["spf_bypass"] != true || cfg["providers"].(map[string]any)["google"].(map[string]any)["trust"] != true {
		t.Fatalf("defaults: %v", cfg)
	}
	for _, bad := range []map[string]any{
		{"enabled": true, "deferral": 0, "resend": 240, "expire": 4320},
		{"enabled": true, "deferral": 5, "resend": 240, "expire": 4320, "trusted": []map[string]string{{"range": "1.2.3.4; rm -rf /"}}},
		{"enabled": true, "deferral": 5, "resend": 240, "expire": 4320, "providers": map[string]any{"evil": map[string]bool{"trust": true}}},
	} {
		if _, err := call(t, r, "mail.greylist_set", bad); code(err) != "invalid_params" {
			t.Errorf("%v: %v", bad, err)
		}
	}
	res, err := call(t, r, "mail.greylist_set", map[string]any{
		"enabled": true, "deferral": 3, "resend": 120, "expire": 2880, "spf_bypass": false, "auto_trust_new": true,
		"trusted":   []map[string]string{{"range": "192.0.2.0/24", "comment": "office"}},
		"providers": map[string]any{"microsoft": map[string]bool{"trust": false, "auto_update": false}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(postconf["smtpd_recipient_restrictions"], greylistClause) || checked != "/var/spool/postfix/private/whacp-greylist" {
		t.Fatalf("postfix: %q, self-test %q", postconf["smtpd_recipient_restrictions"], checked)
	}
	unit := h.Read("etc/systemd/system/whacp-greylist.service")
	for _, want := range []string{"DynamicUser=yes", "CapabilityBoundingSet=\n", "LoadCredential=config:" + h.Env.M.MailDir + "/greylist.json", " greylist-policy\n", "NoNewPrivileges=yes"} {
		if !strings.Contains(unit, want) {
			t.Errorf("service unit lacks %q:\n%s", want, unit)
		}
	}
	if sock := h.Read("etc/systemd/system/whacp-greylist.socket"); !strings.Contains(sock, "ListenStream=/var/spool/postfix/private/whacp-greylist\nSocketUser=postfix") || !strings.Contains(sock, "SocketMode=0600") {
		t.Fatalf("socket unit:\n%s", sock)
	}
	saved, err := loadGL(h.Read(strings.TrimPrefix(h.Env.M.MailDir, "/") + "/greylist.json"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Deferral != 3 || saved.Trusted[0].Comment != "office" || saved.Providers["microsoft"].Trust || !saved.Providers["google"].Trust {
		t.Fatalf("saved: %+v", saved)
	}
	if g := saved.Providers["google"]; len(g.Ranges) != 2 || g.Ranges[0] != "209.85.128.0/17" {
		t.Fatalf("google ranges looked up when trusted: %+v", g)
	}
	_ = res
	if !strings.Contains(strings.Join(systemctl, "\n"), "restart -- whacp-greylist.service") && !strings.Contains(strings.Join(systemctl, "\n"), "restart whacp-greylist.service") {
		t.Fatalf("service not (re)started: %v", systemctl)
	}

	// Postfix refuses: restriction and files come back.
	failCheck = true
	before := postconf["smtpd_recipient_restrictions"]
	postconf["smtpd_recipient_restrictions"] = "permit_mynetworks, reject_unauth_destination"
	if _, err := call(t, r, "mail.greylist_set", map[string]any{"enabled": true, "deferral": 9, "resend": 120, "expire": 2880}); err == nil {
		t.Fatal("accepted although postfix check failed")
	}
	if postconf["smtpd_recipient_restrictions"] != "permit_mynetworks, reject_unauth_destination" {
		t.Fatalf("restriction not restored: %q", postconf["smtpd_recipient_restrictions"])
	}
	if c, _ := loadGL(h.Read(strings.TrimPrefix(h.Env.M.MailDir, "/") + "/greylist.json")); c.Deferral != 3 {
		t.Fatalf("config not rolled back: %d", c.Deferral)
	}
	failCheck = false
	postconf["smtpd_recipient_restrictions"] = before

	// Off: the check leaves Postfix.
	if _, err := call(t, r, "mail.greylist_set", map[string]any{"enabled": false, "deferral": 3, "resend": 120, "expire": 2880}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(postconf["smtpd_recipient_restrictions"], "whacp-greylist") {
		t.Fatalf("still in postfix: %q", postconf["smtpd_recipient_restrictions"])
	}
	// Reports need the running service.
	if out, err := call(t, r, "mail.greylist_report", map[string]any{"search": "x"}); err != nil || out["running"] != false {
		t.Fatalf("report: %v %v", out, err)
	}
	if _, err := call(t, r, "mail.greylist_cleanup", map[string]any{"op": "rm"}); code(err) != "invalid_params" {
		t.Fatalf("bad op: %v", err)
	}
}

func loadGL(s string) (*greylist.Config, error) {
	var c greylist.Config
	err := json.Unmarshal([]byte(s), &c)
	return &c, err
}
