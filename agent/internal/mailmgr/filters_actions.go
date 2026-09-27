package mailmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// sieveDropIn turns on personal Sieve scripts (~/.dovecot.sieve in the
// mailbox folder) for Dovecot 2.3 and 2.4.
func (e env) sieveDropIn() string {
	return filepath.Join(filepath.Dir(e.M.DovecotDropIn), "99-whacp-sieve.conf")
}

func renderSieveDropIn(v24 bool) string {
	head := "# Managed by WhaCP (email filters and autoresponders). Do not edit: changes are overwritten.\n"
	if v24 {
		return head + "sieve_script personal {\n  type = personal\n  path = ~/sieve\n  active_path = ~/.dovecot.sieve\n}\n"
	}
	return head + "plugin {\n  sieve = file:~/sieve;active=~/.dovecot.sieve\n}\n"
}

// mailboxHome is where Dovecot keeps a mailbox (its Sieve script too).
func (e env) mailboxHome(addr string) string {
	local, dom, _ := strings.Cut(addr, "@")
	return filepath.Join(e.M.VmailDir, dom, local)
}

func registerFilters(r *actions.Registry, e env) {
	r.Register(actions.Action{Name: "mail.filters_get", Description: "Email filters and autoresponder of a mailbox.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Address string `json:"address"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		local, dom, err := SplitAddress(in.Address)
		if err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		m, ok := d.Mailboxes[local+"@"+dom]
		if !ok {
			return nil, actions.NotFound("No mailbox " + local + "@" + dom + ".")
		}
		filters := m.Filters
		if filters == nil {
			filters = []Filter{}
		}
		resp := Responder{Days: 1}
		if m.Responder != nil {
			resp = *m.Responder
		}
		return map[string]any{"address": local + "@" + dom, "filters": filters, "responder": resp}, nil
	}})

	r.Register(actions.Action{Name: "mail.filters_save", Mutating: true, Description: "Save a mailbox's email filters and autoresponder (rendered to its personal Sieve script, compiled with sievec; rolled back on failure).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Address   string     `json:"address"`
			Filters   []Filter   `json:"filters"`
			Responder *Responder `json:"responder"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		local, dom, err := SplitAddress(in.Address)
		if err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		addr := local + "@" + dom
		if err := ValidateFilters(in.Filters, in.Responder); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		if !e.installed()["sieve"] {
			return nil, actions.Conflict("Dovecot's Sieve plugin is not installed (dovecot-sieve / dovecot-pigeonhole).")
		}
		mu.Lock()
		defer mu.Unlock()
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		m, ok := d.Mailboxes[addr]
		if !ok {
			return nil, actions.NotFound("No mailbox " + addr + ".")
		}
		vmail, err := e.LookupUser("vmail")
		if err != nil {
			return nil, actions.Conflict("Set up email first (no vmail account).")
		}
		owner := agentfs.Owner{UID: vmail.UID, GID: vmail.GID}
		m.Filters, m.Responder = in.Filters, in.Responder
		d.Mailboxes[addr] = m
		script := RenderSieve(addr, m.Filters, m.Responder)
		home := e.mailboxHome(addr)
		path := filepath.Join(home, ".dovecot.sieve")

		set := e.Changes("mail-filters")
		e.writeData(set, d)
		dropIn := renderSieveDropIn(d.Dovecot24)
		dropInChanged := false
		if cur, err := e.Root.ReadFile(e.sieveDropIn()); err != nil || string(cur) != dropIn {
			set.Write(e.sieveDropIn(), []byte(dropIn), 0o644, agentfs.RootOwner)
			dropInChanged = true
		}
		if script != "" {
			set.Mkdir(filepath.Dir(home), 0o700, owner)
			set.Mkdir(home, 0o700, owner)
			set.Write(path, []byte(script), 0o600, owner)
		} else if e.Root.Exists(path) {
			set.Remove(path)
			if e.Root.Exists(path[:len(path)-len(".sieve")] + ".svbin") {
				set.Remove(path[:len(path)-len(".sieve")] + ".svbin")
			}
		}
		applied, err := set.Commit(func() error {
			if script != "" {
				if err := e.run(ctx, "sievec", path); err != nil {
					return err
				}
			}
			if dropInChanged {
				if err := e.run(ctx, "doveconf", "-n"); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, host.FailWithValidation("The filters were not saved", err)
		}
		if script != "" {
			// Dovecot runs as vmail and recompiles the script when it changes.
			svbin := strings.TrimSuffix(path, ".sieve") + ".svbin"
			if real, err := e.Root.Path(svbin); err == nil {
				if err := chownNoFollow(real, vmail.UID, vmail.GID); err != nil {
					e.Log.Warn("sieve binary owner", "path", svbin, "error", err)
				}
			}
		}
		if dropInChanged && e.UnitActive(ctx, "dovecot.service") {
			if err := e.Systemctl(ctx, "reload", "dovecot.service"); err != nil {
				applied.Rollback()
				e.Systemctl(ctx, "reload", "dovecot.service")
				return nil, actions.Failed("Dovecot did not reload; the previous settings were restored.", err.Error())
			}
		}
		return map[string]any{"address": addr, "filters": len(m.Filters), "responder": m.Responder != nil && m.Responder.Enabled, "active": script != ""}, nil
	}})

	r.Register(actions.Action{Name: "mail.routing_set", Mutating: true, Description: "Route a mail domain: local (mailboxes here), backup (queue its mail for the primary MX) or remote (another server receives it; mail for it from this server goes there).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Domain  string `json:"domain"`
			Routing string `json:"routing"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		dom := strings.ToLower(strings.TrimSpace(in.Domain))
		if !ValidDomain(dom) {
			return nil, actions.InvalidParams("domain is not a valid domain name")
		}
		if in.Routing != RouteLocal && in.Routing != RouteRemote && in.Routing != RouteBackup {
			return nil, actions.InvalidParams("routing must be local, backup or remote")
		}
		mu.Lock()
		defer mu.Unlock()
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		if _, ok := d.Domains[dom]; !ok {
			return nil, actions.Conflict("Email is not hosted for " + dom + ".")
		}
		if d.route(dom) == in.Routing {
			return map[string]any{"domain": dom, "routing": in.Routing, "changed": false}, nil
		}
		if d.Routing == nil {
			d.Routing = map[string]string{}
		}
		delete(d.Routing, dom)
		if in.Routing != RouteLocal {
			d.Routing[dom] = in.Routing
		}
		// Older setups have no backup-MX map yet (the file is written with
		// the data, before Postfix is pointed at it).
		want := "texthash:" + e.M.MailDir + "/relay_domains"
		if err := e.saveData(ctx, d, nil); err != nil {
			return nil, err
		}
		if out, err := e.Runner.Run(ctx, "postconf", "-h", "relay_domains"); err == nil && strings.TrimSpace(string(out)) != want {
			if err := e.run(ctx, "postconf", "-e", "relay_domains="+want); err != nil {
				return nil, actions.Failed("Postfix's relay_domains could not be set.", err.Error())
			}
			if e.run(ctx, "postfix", "status") == nil {
				if err := e.reloadPostfix(ctx); err != nil {
					return nil, actions.Failed("Postfix did not reload.", err.Error())
				}
			}
		}
		return map[string]any{"domain": dom, "routing": in.Routing, "changed": true}, nil
	}})
}

// chownNoFollow changes the owner of a file without following a link.
func chownNoFollow(path string, uid, gid int) error {
	if err := lchown(path, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	return nil
}

var lchown = os.Lchown
