package mailmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/mailrate"
)

func TestWithMailRate(t *testing.T) {
	if got := withMailRate("", true); got != mailRateClause {
		t.Fatalf("empty: %q", got)
	}
	base := "permit_mynetworks, reject_unauth_destination, " + greylistClause
	on := withMailRate(base, true)
	if on != mailRateClause+", "+base || withMailRate(on, true) != on {
		t.Fatalf("on: %q", on)
	}
	if got := withMailRate(on, false); got != base {
		t.Fatalf("off: %q", got)
	}
	mid := "permit_mynetworks, " + mailRateClause + ", reject_unauth_destination"
	if got := withMailRate(mid, false); got != "permit_mynetworks, reject_unauth_destination" {
		t.Fatalf("middle: %q", got)
	}
}

func TestMailRateActions(t *testing.T) {
	h, r := setup(t)
	if _, err := call(t, r, "mail.rate_set", map[string]any{"enabled": true, "per_hour": 100}); code(err) != "conflict" {
		t.Fatalf("before email setup: %v", err)
	}
	h.Env.Cfg.SimulateCommands = true
	if _, err := call(t, r, "mail.configure", map[string]any{"hostname": "mail.example.com", "confirm_edit_package_config": true}); err != nil {
		t.Fatal(err)
	}
	h.Env.Cfg.SimulateCommands = false
	// Two website accounts and a system one.
	f, _ := os.OpenFile(filepath.Join(h.Base, "etc/passwd"), os.O_APPEND|os.O_WRONLY, 0o644)
	fmt.Fprintf(f, "shop:x:1003:1003::%s/shop.test:/usr/sbin/nologin\nblog:x:1004:1004::%s/blog.test:/usr/sbin/nologin\nadmin:x:1000:1000::/home/admin:/bin/bash\n", h.Env.Cfg.SitesRoot, h.Env.Cfg.SitesRoot)
	f.Close()

	oldCheck := mailRateCheck
	checked := ""
	mailRateCheck = func(_ context.Context, socket string) error { checked = socket; return nil }
	defer func() { mailRateCheck = oldCheck }()
	postconf := map[string]string{"smtpd_recipient_restrictions": "permit_mynetworks, reject_unauth_destination", "authorized_submit_users": submitAll, "queue_directory": "/var/spool/postfix"}
	failCheck := false
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
		}
		return "", nil, false
	}
	for _, bad := range []map[string]any{
		{"enabled": true, "per_hour": -1},
		{"enabled": true, "per_hour": 100, "accounts": map[string]int{"admin": 5}},
		{"enabled": true, "per_hour": 100, "accounts": map[string]int{"root": 5}},
		{"enabled": true, "per_hour": 100, "accounts": map[string]int{"shop": 2000000}},
	} {
		if _, err := call(t, r, "mail.rate_set", bad); code(err) != "invalid_params" {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if _, err := call(t, r, "mail.rate_set", map[string]any{"enabled": true, "per_hour": 200, "accounts": map[string]int{"shop": 50}}); err != nil {
		t.Fatal(err)
	}
	dir := h.Env.M.MailDir
	if !strings.HasPrefix(postconf["smtpd_recipient_restrictions"], mailRateClause+", permit_mynetworks") || checked != "/var/spool/postfix/private/nimvor-mailrate" {
		t.Fatalf("postfix: %q, self-test %q", postconf["smtpd_recipient_restrictions"], checked)
	}
	if postconf["authorized_submit_users"] != "!nobody, !texthash:"+dir+"/nosubmit, static:anyone" {
		t.Fatalf("submit users: %q", postconf["authorized_submit_users"])
	}
	if got := h.Read(strings.TrimPrefix(dir, "/") + "/nosubmit"); !strings.Contains(got, "\nblog website\nshop website\n") || strings.Contains(got, "admin") {
		t.Fatalf("nosubmit:\n%s", got)
	}
	var saved mailrate.Config
	json.Unmarshal([]byte(h.Read(strings.TrimPrefix(dir, "/")+"/mailrate.json")), &saved)
	if saved.PerHour != 200 || saved.Accounts["1003"] != 50 || saved.Names["1004"] != "blog" {
		t.Fatalf("saved: %+v", saved)
	}
	unit := h.Read("etc/systemd/system/nimvor-mailrate.service")
	for _, want := range []string{"DynamicUser=yes", "CapabilityBoundingSet=\n", " mailrate-policy\n", "NoNewPrivileges=yes", "RestrictAddressFamilies=AF_UNIX\n"} {
		if !strings.Contains(unit, want) {
			t.Errorf("service unit lacks %q", want)
		}
	}
	if strings.Contains(unit, "PrivateUsers") || strings.Contains(unit, "PrivateNetwork") {
		t.Error("the service could not see the real socket owners")
	}
	get, err := call(t, r, "mail.rate_get", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c := get["config"].(map[string]any); c["accounts"].(map[string]any)["shop"] != float64(50) {
		t.Fatalf("get: %v", c)
	}

	// Postfix refuses: everything comes back.
	failCheck = true
	prev := map[string]string{"smtpd_recipient_restrictions": "permit_mynetworks", "authorized_submit_users": submitAll}
	for k, v := range prev {
		postconf[k] = v
	}
	if _, err := call(t, r, "mail.rate_set", map[string]any{"enabled": true, "per_hour": 10}); err == nil {
		t.Fatal("accepted what Postfix refused")
	}
	for k, v := range prev {
		if postconf[k] != v {
			t.Errorf("%s not restored: %q", k, postconf[k])
		}
	}
	json.Unmarshal([]byte(h.Read(strings.TrimPrefix(dir, "/")+"/mailrate.json")), &saved)
	if saved.PerHour != 200 {
		t.Errorf("configuration not restored: %+v", saved)
	}
	failCheck = false

	// Off: Postfix stops asking and accounts may use sendmail again.
	postconf["smtpd_recipient_restrictions"] = mailRateClause + ", permit_mynetworks"
	if _, err := call(t, r, "mail.rate_set", map[string]any{"enabled": false, "per_hour": 200}); err != nil {
		t.Fatal(err)
	}
	if postconf["smtpd_recipient_restrictions"] != "permit_mynetworks" || postconf["authorized_submit_users"] != submitAll {
		t.Fatalf("off: %v", postconf)
	}
}
