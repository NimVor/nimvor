package mailmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/safewalk"
	"github.com/NimVor/nimvor/agent/internal/selinux"
)

// Email administration tools: the mail troubleshooter (how an address is
// routed) and the repair of mailbox ownership and permissions.

// TraceStep is one step of an address's route.
type TraceStep struct {
	Depth   int    `json:"depth"`
	Address string `json:"address"`
	Result  string `json:"result"` // mailbox, forward, catchall, remote, backup, external, rejected, loop
	Detail  string `json:"detail"`
}

type mxLookup func(ctx context.Context, domain string) ([]*net.MX, error)

// Trace follows an address through NimVor's mail configuration.
func Trace(ctx context.Context, d Data, addr, relayhost string, mx mxLookup) []TraceStep {
	var steps []TraceStep
	seen := map[string]bool{}
	mxText := func(dom string) string {
		if relayhost != "" {
			return "through the smart host " + relayhost
		}
		recs, err := mx(ctx, dom)
		if err != nil || len(recs) == 0 {
			return "to " + dom + " itself (no MX record found)"
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Pref < recs[j].Pref })
		var hosts []string
		for _, r := range recs {
			hosts = append(hosts, fmt.Sprintf("%s (%d)", strings.TrimSuffix(r.Host, "."), r.Pref))
		}
		return "to its mail exchanger " + strings.Join(hosts, ", ")
	}
	var walk func(addr string, depth int)
	walk = func(addr string, depth int) {
		addr = strings.ToLower(strings.TrimSpace(addr))
		if depth > 10 {
			steps = append(steps, TraceStep{depth, addr, "loop", "Forwarded more than 10 times; stopped here."})
			return
		}
		if seen[addr] {
			steps = append(steps, TraceStep{depth, addr, "loop", "Already on this route (the forwarders form a loop); Postfix delivers each address once."})
			return
		}
		seen[addr] = true
		local, dom, ok := strings.Cut(addr, "@")
		if !ok || local == "" || dom == "" {
			steps = append(steps, TraceStep{depth, addr, "rejected", "Not an email address."})
			return
		}
		if _, hosted := d.Domains[dom]; !hosted {
			steps = append(steps, TraceStep{depth, addr, "external", dom + " is not hosted here; mail is sent " + mxText(dom) + "."})
			return
		}
		switch d.route(dom) {
		case RouteRemote:
			steps = append(steps, TraceStep{depth, addr, "remote", dom + " is set to remote routing: mail is not delivered here but sent " + mxText(dom) + "."})
			return
		case RouteBackup:
			steps = append(steps, TraceStep{depth, addr, "backup", "This server is a backup mail exchanger for " + dom + ": mail is queued and passed on " + mxText(dom) + "."})
			return
		}
		mb, isBox := d.Mailboxes[addr]
		dests, isAlias := d.Aliases[addr]
		if isBox {
			detail := "Delivered to the mailbox " + addr
			if mb.QuotaMB > 0 {
				detail += fmt.Sprintf(" (quota %d MB)", mb.QuotaMB)
			}
			detail += "."
			var notes []string
			if n := countEnabled(mb.Filters); n > 0 {
				notes = append(notes, fmt.Sprintf("%d email filter(s) run first", n))
			}
			if mb.Responder != nil && mb.Responder.Enabled {
				notes = append(notes, "the autoresponder replies")
			}
			if d.Spam.Enabled && !containsString(d.Spam.NoFilterDomains, dom) {
				notes = append(notes, "the spam filter scans it")
			}
			if containsString(d.Suspended, dom) {
				notes = append(notes, "the account is suspended: mail is delivered, but the mailbox cannot sign in")
			}
			if len(notes) > 0 {
				detail += " Also: " + strings.Join(notes, "; ") + "."
			}
			steps = append(steps, TraceStep{depth, addr, "mailbox", detail})
		}
		if isAlias {
			steps = append(steps, TraceStep{depth, addr, "forward", "Forwarded to " + strings.Join(dests, ", ") + "."})
			for _, x := range dests {
				walk(x, depth+1)
			}
		}
		if isBox || isAlias {
			return
		}
		if dests, ok := d.Aliases["@"+dom]; ok {
			steps = append(steps, TraceStep{depth, addr, "catchall", "No mailbox or forwarder " + addr + "; the catch-all of " + dom + " forwards it to " + strings.Join(dests, ", ") + "."})
			for _, x := range dests {
				walk(x, depth+1)
			}
			return
		}
		steps = append(steps, TraceStep{depth, addr, "rejected", "There is no mailbox or forwarder " + addr + ": the sending server gets “550 5.1.1 User unknown”."})
	}
	walk(addr, 0)
	return steps
}

func countEnabled(fs []Filter) int {
	n := 0
	for _, f := range fs {
		if f.Enabled {
			n++
		}
	}
	return n
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// RepairStats counts what a repair changed.
type RepairStats struct {
	Dirs    int      `json:"dirs"`
	Files   int      `json:"files"`
	Fixed   int      `json:"fixed"`
	Skipped []string `json:"skipped,omitempty"` // links, special files, extra hard links (not touched)
}

// repairTree gives a mail folder tree to uid:gid and removes access for
// others (Dovecot's own modes), through safewalk: links are never followed.
func repairTree(parent int, name string, uid, gid int, st *RepairStats) error {
	res, err := safewalk.Walk(parent, name, func(e *safewalk.Entry) error {
		if e.Dir {
			st.Dirs++
		} else {
			// A second name of a file someone else owns (a hard link made
			// into the mailbox) is not taken over.
			if e.Stat.Nlink > 1 && int(e.Stat.Uid) != uid {
				st.Skipped = appendCapped(st.Skipped, e.Rel)
				return nil
			}
			st.Files++
		}
		changed := false
		if int(e.Stat.Uid) != uid || int(e.Stat.Gid) != gid {
			if err := syscall.Fchown(e.FD, uid, gid); err != nil {
				return err
			}
			changed = true
		}
		mode := e.Stat.Mode & 0o7777
		want := (mode &^ 0o7007) | 0o600
		if e.Dir {
			want |= 0o700
		}
		if want != mode {
			if err := syscall.Fchmod(e.FD, want); err != nil {
				return err
			}
			changed = true
		}
		if changed {
			st.Fixed++
		}
		return nil
	})
	for _, s := range res.Skipped {
		st.Skipped = appendCapped(st.Skipped, s)
	}
	return err
}

func appendCapped(list []string, s string) []string {
	if len(list) < 50 {
		return append(list, s)
	}
	return list
}

func registerTools(r *actions.Registry, e env) {
	r.Register(actions.Action{Name: "mail.trace", Description: "Mail troubleshooter: how mail to an address is routed (mailbox, forwarders, catch-all, routing, external MX or smart host).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Address string `json:"address"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		addr := strings.ToLower(strings.TrimSpace(in.Address))
		if len(addr) > 254 || strings.ContainsAny(addr, " \t\r\n\x00<>,;") || strings.Count(addr, "@") != 1 {
			return nil, actions.InvalidParams("enter one email address")
		}
		if _, dom, _ := strings.Cut(addr, "@"); !domainRe.MatchString(dom) {
			return nil, actions.InvalidParams("enter one email address")
		}
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		relay := ""
		if e.configured() {
			relay = e.postconfValue(ctx, "relayhost")
		}
		lookup := func(ctx context.Context, dom string) ([]*net.MX, error) {
			if e.Cfg.SimulateCommands {
				return nil, fmt.Errorf("simulated")
			}
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			return net.DefaultResolver.LookupMX(ctx, dom)
		}
		return map[string]any{"address": addr, "steps": Trace(ctx, d, addr, relay, lookup)}, nil
	}})

	r.Register(actions.Action{Name: "mail.repair_permissions", Mutating: true, Description: "Give mailbox folders back to the vmail user and remove access for others (one domain, or all); symbolic links are never followed.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
		if in.Domain != "" && !domainRe.MatchString(in.Domain) {
			return nil, actions.InvalidParams("domain: a domain name")
		}
		if !e.configured() {
			return nil, actions.Conflict("Set up email first.")
		}
		vmail, err := e.LookupUser("vmail")
		if err != nil {
			return nil, actions.Failed("The vmail user does not exist.", err.Error())
		}
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		var doms []string
		for dom := range d.Domains {
			if in.Domain == "" || dom == in.Domain {
				doms = append(doms, dom)
			}
		}
		if len(doms) == 0 {
			return nil, actions.NotFound("That domain has no email here.")
		}
		sort.Strings(doms)
		mu.Lock()
		defer mu.Unlock()
		st := &RepairStats{}
		if e.Cfg.SimulateCommands {
			return st, nil
		}
		root, err := e.Root.Path(e.M.VmailDir)
		if err != nil {
			return nil, actions.Failed("The mailbox folder could not be found.", err.Error())
		}
		rfd, err := syscall.Open(root, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
		if err != nil {
			return nil, actions.Failed("The mailbox folder could not be opened.", err.Error())
		}
		defer syscall.Close(rfd)
		for _, dom := range doms {
			if err := repairTree(rfd, dom, vmail.UID, vmail.GID, st); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue // no mail received yet
				}
				return nil, actions.Failed("The permissions could not be repaired.", err.Error())
			}
		}
		// SELinux: the mailbox label, for files moved in from elsewhere.
		if err := selinux.Label(ctx, e.Env, filepath.Clean(e.M.VmailDir), "mail_spool_t"); err != nil {
			e.Log.Warn("selinux: mailbox label", "err", err)
		}
		return st, nil
	}})
}
