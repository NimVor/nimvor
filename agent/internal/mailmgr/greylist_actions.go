package mailmgr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/greylist"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/spf"
)

// Greylisting: NimVor's own policy service (see package greylist) runs as
// an unprivileged, socket-activated unit; Postfix asks it at RCPT time.
// Should the service fail, Postfix accepts the mail (default_action=DUNNO)
// rather than deferring everything.

const (
	GreylistService = "nimvor-greylist.service"
	GreylistSocket  = "nimvor-greylist.socket"
	greylistClause  = "check_policy_service { unix:private/nimvor-greylist, default_action=DUNNO }"
)

// Replaced in tests.
var (
	greylistControl                 = "/run/nimvor-greylist/control"
	greylistCheck                   = greylistSelfTest
	greylistResolver   spf.Resolver = net.DefaultResolver
	greylistExecutable              = func() string {
		if p, err := os.Executable(); err == nil {
			if r, err := filepath.EvalSymlinks(p); err == nil {
				return r
			}
			return p
		}
		return "/usr/local/bin/nimvor-agent"
	}
)

// socketctl runs systemctl for NimVor's greylisting socket (a fixed name;
// Env.Systemctl accepts service units only).
func (e env) socketctl(ctx context.Context, verb string) error {
	out, err := e.Runner.Run(ctx, "systemctl", verb, "--", GreylistSocket)
	if err != nil {
		return fmt.Errorf("systemctl %s %s failed: %s", verb, GreylistSocket, host.Output(out))
	}
	return nil
}

func (e env) greylistPath() string { return e.M.MailDir + "/greylist.json" }

func (e env) loadGreylist() (*greylist.Config, error) {
	c := &greylist.Config{}
	b, err := e.Root.ReadFile(e.greylistPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, c); err != nil {
			return nil, err
		}
	} else {
		c.AutoTrustNew = true
		c.SPFBypass = true
	}
	c.Normalize()
	return c, nil
}

func (e env) queueDir(ctx context.Context) string {
	if d := e.postconfValue(ctx, "queue_directory"); strings.HasPrefix(d, "/") && !strings.ContainsAny(d, " \n") {
		return d
	}
	return "/var/spool/postfix"
}

func renderGreylistSocket(queueDir string) string {
	return "# Managed by NimVor (greylisting). Do not edit: changes are overwritten.\n[Unit]\nDescription=NimVor greylisting policy socket (Postfix)\n\n" +
		"[Socket]\nListenStream=" + queueDir + "/private/nimvor-greylist\nSocketUser=postfix\nSocketGroup=postfix\nSocketMode=0600\n\n" +
		"[Install]\nWantedBy=sockets.target\n"
}

// renderGreylistService: the service runs as a dynamic, unprivileged user
// with no capabilities; its configuration arrives as a systemd credential
// (read by systemd as root, so the file stays 0600 root).
func renderGreylistService(bin, configPath string) string {
	return "# Managed by NimVor (greylisting). Do not edit: changes are overwritten.\n[Unit]\nDescription=NimVor greylisting policy service (Postfix)\nRequires=" + GreylistSocket + "\nAfter=network.target\n\n" +
		"[Service]\nType=simple\nExecStart=" + bin + " greylist-policy\n" +
		"LoadCredential=config:" + configPath + "\n" +
		"DynamicUser=yes\nStateDirectory=nimvor-greylist\nStateDirectoryMode=0700\nRuntimeDirectory=nimvor-greylist\nRuntimeDirectoryMode=0700\n" +
		"Restart=on-failure\nRestartSec=2\nTimeoutStopSec=20\n" +
		"NoNewPrivileges=yes\nCapabilityBoundingSet=\nAmbientCapabilities=\n" +
		"ProtectSystem=strict\nProtectHome=yes\nPrivateTmp=yes\nPrivateDevices=yes\nPrivateUsers=yes\n" +
		"ProtectKernelTunables=yes\nProtectKernelModules=yes\nProtectKernelLogs=yes\nProtectControlGroups=yes\nProtectClock=yes\nProtectHostname=yes\n" +
		"ProtectProc=invisible\nProcSubset=pid\n" +
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6\nRestrictNamespaces=yes\nRestrictRealtime=yes\nRestrictSUIDSGID=yes\n" +
		"LockPersonality=yes\nMemoryDenyWriteExecute=yes\nSystemCallArchitectures=native\nSystemCallFilter=@system-service\nSystemCallFilter=~@privileged @resources\nSystemCallErrorNumber=EPERM\n" +
		"UMask=0077\nMemoryMax=256M\nTasksMax=128\n\n" +
		"[Install]\nWantedBy=multi-user.target\n"
}

// withGreylisting adds or removes the policy check in a restriction list.
func withGreylisting(current string, on bool) string {
	cur := strings.TrimSpace(strings.ReplaceAll(current, "\n", " "))
	idx := strings.Index(cur, "check_policy_service { unix:private/nimvor-greylist")
	if idx >= 0 {
		end := strings.Index(cur[idx:], "}")
		rest := ""
		if end >= 0 {
			rest = cur[idx+end+1:]
		}
		cur = strings.TrimRight(strings.TrimSpace(cur[:idx]), ", ") + rest
		cur = strings.Trim(strings.TrimSpace(cur), ", ")
	}
	if !on {
		return cur
	}
	if cur == "" {
		cur = "permit_mynetworks, permit_sasl_authenticated, reject_unauth_destination"
	}
	return cur + ", " + greylistClause
}

// updateProviderRanges reads the SPF ranges of trusted providers that are
// updated automatically (or have none yet); only changed returns true.
func updateProviderRanges(ctx context.Context, c *greylist.Config, all bool, now time.Time) (changed bool) {
	for _, p := range greylist.Providers {
		st := c.Providers[p.ID]
		if st == nil || !st.Trust || (!st.AutoUpdate && len(st.Ranges) > 0 && !all) {
			continue
		}
		if !all && now.Unix()-st.Checked < 20*3600 {
			continue
		}
		st.Checked = now.Unix()
		var ranges []string
		var errs []string
		for _, d := range p.SPF {
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			got, err := spf.Ranges(cctx, greylistResolver, d)
			cancel()
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			for _, r := range got {
				ranges = append(ranges, r.String())
			}
		}
		sort.Strings(ranges)
		ranges = compactStrings(ranges)
		if len(ranges) > 5000 {
			ranges = ranges[:5000]
		}
		changed = true
		if len(ranges) == 0 {
			// Keep the ranges known so far.
			st.Error = strings.Join(errs, "; ")
			if st.Error == "" {
				st.Error = "no ranges found"
			}
			continue
		}
		st.Ranges, st.Updated, st.Error = ranges, now.Unix(), strings.Join(errs, "; ")
	}
	return changed
}

func compactStrings(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// selfTest sends Postfix's kind of request to the policy socket.
func greylistSelfTest(ctx context.Context, socket string) error {
	var d net.Dialer
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var c net.Conn
	var err error
	for i := 0; i < 20; i++ {
		if c, err = d.DialContext(ctx, "unix", socket); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte("request=smtpd_access_policy\nprotocol_state=DATA\nclient_address=192.0.2.1\n\n")); err != nil {
		return err
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return err
	}
	if strings.TrimSpace(line) != "action=DUNNO" {
		return fmt.Errorf("unexpected answer %q", line)
	}
	return nil
}

func registerGreylist(r *actions.Registry, e env) {
	status := func(ctx context.Context, c *greylist.Config) map[string]any {
		cur := e.postconfValue(ctx, "smtpd_recipient_restrictions")
		return map[string]any{
			"config":     c,
			"providers":  greylist.Providers,
			"service":    map[string]bool{"active": e.UnitActive(ctx, GreylistService), "socket": e.UnitActive(ctx, GreylistSocket), "postfix": strings.Contains(cur, "unix:private/nimvor-greylist")},
			"configured": e.configured(),
		}
	}
	r.Register(actions.Action{Name: "mail.greylist_get", Description: "Greylisting configuration, trusted hosts, mail providers and service state.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		c, err := e.loadGreylist()
		if err != nil {
			return nil, actions.Failed("The greylisting configuration could not be read.", err.Error())
		}
		return status(ctx, c), nil
	}})

	// apply writes the configuration and units and brings the service and
	// Postfix in line with it; on failure the previous state comes back.
	apply := func(ctx context.Context, c *greylist.Config) error {
		b, _ := json.MarshalIndent(c, "", "  ")
		set := e.Changes("mail-greylist")
		set.Write(e.greylistPath(), b, 0o600, agentfs.RootOwner)
		set.Write(filepath.Join(e.M.UnitDir, GreylistSocket), []byte(renderGreylistSocket(e.queueDir(ctx))), 0o644, agentfs.RootOwner)
		set.Write(filepath.Join(e.M.UnitDir, GreylistService), []byte(renderGreylistService(greylistExecutable(), e.greylistPath())), 0o644, agentfs.RootOwner)
		applied, err := set.Commit(nil)
		if err != nil {
			return host.FailWithValidation("The greylisting configuration was not written", err)
		}
		prevRestr := e.postconfValue(ctx, "smtpd_recipient_restrictions")
		wasActive := e.UnitActive(ctx, GreylistService)
		fail := func(msg string, cause error) error {
			e.run(ctx, "postconf", "-e", "smtpd_recipient_restrictions="+prevRestr)
			e.reloadPostfix(ctx)
			applied.Rollback()
			e.DaemonReload(ctx)
			if wasActive {
				e.Systemctl(ctx, "restart", GreylistService)
			}
			detail := cause.Error()
			if j := e.JournalTail(ctx, GreylistService, 8); j != "" {
				detail += "\n" + j
			}
			return actions.Failed(msg+" The previous settings were restored.", detail)
		}
		if err := e.DaemonReload(ctx); err != nil {
			return fail("systemd could not load the greylisting service.", err)
		}
		postfixRunning := e.run(ctx, "postfix", "status") == nil
		if c.Enabled {
			e.socketctl(ctx, "enable")
			e.Systemctl(ctx, "enable", GreylistService)
			if err := e.socketctl(ctx, "start"); err != nil {
				return fail("The greylisting socket did not start.", err)
			}
			if err := e.Systemctl(ctx, "restart", GreylistService); err != nil {
				return fail("The greylisting service did not start.", err)
			}
			if !e.Cfg.SimulateCommands {
				if err := greylistCheck(ctx, filepath.Join(e.queueDir(ctx), "private", "nimvor-greylist")); err != nil {
					return fail("The greylisting service does not answer.", err)
				}
			}
			if err := e.run(ctx, "postconf", "-e", "smtpd_recipient_restrictions="+withGreylisting(prevRestr, true)); err != nil {
				return fail("Postfix was not configured.", err)
			}
			if err := e.run(ctx, "postfix", "check"); err != nil {
				return fail("Postfix refused the greylisting check.", err)
			}
			if postfixRunning {
				if err := e.reloadPostfix(ctx); err != nil {
					return fail("Postfix did not reload.", err)
				}
			}
			return nil
		}
		// Off: Postfix stops asking first, then the service stops.
		if strings.Contains(prevRestr, "unix:private/nimvor-greylist") {
			if err := e.run(ctx, "postconf", "-e", "smtpd_recipient_restrictions="+withGreylisting(prevRestr, false)); err != nil {
				return fail("Postfix was not configured.", err)
			}
			if postfixRunning {
				if err := e.reloadPostfix(ctx); err != nil {
					return fail("Postfix did not reload.", err)
				}
			}
		}
		e.Systemctl(ctx, "disable", GreylistService)
		e.socketctl(ctx, "disable")
		e.Systemctl(ctx, "stop", GreylistService)
		e.socketctl(ctx, "stop")
		return nil
	}

	r.Register(actions.Action{Name: "mail.greylist_set", Mutating: true, Description: "Change greylisting: on/off, deferral, resend acceptance and record expiration (minutes), SPF bypass, trusted hosts, trusted mail providers.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Enabled      bool                   `json:"enabled"`
			Deferral     int                    `json:"deferral"`
			Resend       int                    `json:"resend"`
			Expire       int                    `json:"expire"`
			SPFBypass    bool                   `json:"spf_bypass"`
			AutoTrustNew bool                   `json:"auto_trust_new"`
			Trusted      []greylist.TrustedHost `json:"trusted"`
			Providers    map[string]struct {
				Trust      bool `json:"trust"`
				AutoUpdate bool `json:"auto_update"`
			} `json:"providers"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if !e.configured() {
			return nil, actions.Conflict("Set up email first.")
		}
		mu.Lock()
		defer mu.Unlock()
		c, err := e.loadGreylist()
		if err != nil {
			return nil, actions.Failed("The greylisting configuration could not be read.", err.Error())
		}
		c.Enabled, c.Deferral, c.Resend, c.Expire, c.SPFBypass, c.AutoTrustNew = in.Enabled, in.Deferral, in.Resend, in.Expire, in.SPFBypass, in.AutoTrustNew
		c.Trusted = in.Trusted
		if c.Trusted == nil {
			c.Trusted = []greylist.TrustedHost{}
		}
		for id, p := range in.Providers {
			st := c.Providers[id]
			if st == nil {
				return nil, actions.InvalidParams("unknown mail provider " + id)
			}
			st.Trust, st.AutoUpdate = p.Trust, p.AutoUpdate
		}
		if err := c.Validate(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		// Providers trusted now but never looked up get their ranges.
		for _, p := range greylist.Providers {
			if st := c.Providers[p.ID]; st.Trust && len(st.Ranges) == 0 && !e.Cfg.SimulateCommands {
				updateProviderRanges(ctx, &greylist.Config{Providers: map[string]*greylist.ProviderState{p.ID: st}}, true, e.Now())
			}
		}
		if err := apply(ctx, c); err != nil {
			return nil, err
		}
		return status(ctx, c), nil
	}})

	r.Register(actions.Action{Name: "mail.greylist_update_providers", Mutating: true, Description: "Refresh the address ranges of trusted mail providers from their SPF records (all trusted ones with all=true, else those updated automatically and older than 20 hours).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			All bool `json:"all"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		c, err := e.loadGreylist()
		if err != nil {
			return nil, actions.Failed("The greylisting configuration could not be read.", err.Error())
		}
		// Automatic updates only while greylisting is on.
		if e.Cfg.SimulateCommands || (!c.Enabled && !in.All) || !updateProviderRanges(ctx, c, in.All, e.Now()) {
			return status(ctx, c), nil
		}
		b, _ := json.MarshalIndent(c, "", "  ")
		set := e.Changes("mail-greylist")
		set.Write(e.greylistPath(), b, 0o600, agentfs.RootOwner)
		if _, err := set.Commit(nil); err != nil {
			return nil, host.FailWithValidation("The provider ranges were not saved", err)
		}
		if c.Enabled && e.UnitActive(ctx, GreylistService) {
			// The service reads its configuration when it starts; the
			// socket keeps Postfix's requests waiting meanwhile.
			if err := e.Systemctl(ctx, "restart", GreylistService); err != nil {
				return nil, actions.Failed("The greylisting service did not restart.", err.Error())
			}
		}
		return status(ctx, c), nil
	}})

	r.Register(actions.Action{Name: "mail.greylist_report", Description: "Greylisting triplets (sender IP, from, to) with counts and times; search, status filter, sort and pages.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var q greylist.ReportQuery
		if err := host.Decode(raw, &q); err != nil {
			return nil, err
		}
		if len(q.Search) > 200 {
			return nil, actions.InvalidParams("search: up to 200 characters")
		}
		empty := map[string]any{"running": false, "rows": []any{}, "total": 0, "counts": map[string]int{}}
		if e.Cfg.SimulateCommands || !e.UnitActive(ctx, GreylistService) {
			return empty, nil
		}
		var out map[string]any
		if err := greylist.Control(ctx, greylistControl, greylist.ControlRequest{Op: "report", Query: q}, &out); err != nil {
			return nil, actions.Failed("The greylisting service did not answer.", err.Error())
		}
		out["running"] = true
		return out, nil
	}})

	r.Register(actions.Action{Name: "mail.greylist_cleanup", Mutating: true, Description: "Remove greylisting triplets: expired ones (purge), selected ones (delete) or all (delete_all).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Op       string             `json:"op"`
			Triplets []greylist.Triplet `json:"triplets"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if in.Op != "purge" && in.Op != "delete" && in.Op != "delete_all" {
			return nil, actions.InvalidParams("op: purge, delete or delete_all")
		}
		if in.Op == "delete" && (len(in.Triplets) == 0 || len(in.Triplets) > 1000) {
			return nil, actions.InvalidParams("select 1 to 1000 records")
		}
		if e.Cfg.SimulateCommands {
			return map[string]any{"removed": 0}, nil
		}
		if !e.UnitActive(ctx, GreylistService) {
			return nil, actions.Conflict("The greylisting service is not running.")
		}
		var out map[string]any
		if err := greylist.Control(ctx, greylistControl, greylist.ControlRequest{Op: in.Op, Triplets: in.Triplets}, &out); err != nil {
			return nil, actions.Failed("The greylisting service did not answer.", err.Error())
		}
		return out, nil
	}})
}
