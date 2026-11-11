package dnsmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/fw"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
	"github.com/NimVor/nimvor/agent/internal/selinux"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// setDirMode changes the mode of an existing folder (BIND signs inside the
// zone folder, so it must be writable by the daemon's group).
func setDirMode(env *host.Env, dir string, mode os.FileMode) {
	if real, err := env.Root.Path(dir); err == nil {
		os.Chmod(real, mode)
	}
}

func zoneName(s string) (string, error) {
	n := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
	if err := validate.Domain("zone", n); err != nil {
		return "", err
	}
	return n, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dnssecInfo(ctx context.Context, env *host.Env, d driver) map[string]any {
	ops := d.dnssec()
	if ops == nil {
		return map[string]any{"supported": false, "reason": d.label() + " does not sign zones; DNSSEC needs the zones to be signed elsewhere."}
	}
	if !d.installed(env) {
		return map[string]any{"supported": true, "reason": ""}
	}
	ok, why := ops.supported(ctx, env)
	return map[string]any{"supported": ok, "reason": why}
}

// Register adds the DNS actions.
func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "dns.status", Description: "Which nameserver NimVor runs the zones with, whether it is installed, set up for NimVor and running, and the zones NimVor serves (with serials).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		d := active(env)
		p := pathsFor(env, d)
		signed := signedZones(env, p)
		return map[string]any{
			"backend": d.id(), "backend_label": d.label(),
			"installed": d.installed(env), "configured": configured(env, p), "running": d.installed(env) && d.running(ctx, env),
			"config_file": p.conf, "zones": listZones(env, p), "signed": sortedKeys(signed),
		}, nil
	}})

	r.Register(actions.Action{Name: "dns.backends", Description: "The nameservers NimVor can run the zones with (PowerDNS, NSD, Knot DNS, BIND): which is in use, which are installed or running, what each offers and whether it can sign zones (DNSSEC). Nothing is changed.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		cur := active(env)
		list := []map[string]any{}
		for _, d := range drivers() {
			p := pathsFor(env, d)
			inst := d.installed(env)
			list = append(list, map[string]any{
				"id": d.id(), "label": d.label(), "active": d.id() == cur.id(), "installed": inst,
				"configured": inst && configured(env, p), "running": inst && d.running(ctx, env),
				"advantages": d.advantages(), "disadvantages": d.disadvantages(), "notes": d.notes(),
				"dnssec": dnssecInfo(ctx, env, d), "replaces_config": d.wholeFile(), "config_file": d.confFile(env),
			})
		}
		zp := pathsFor(env, cur)
		return map[string]any{"backends": list, "active": cur.id(), "zones": len(listZones(env, zp)), "signed": len(signedZones(env, zp))}, nil
	}})

	r.Register(actions.Action{Name: "dns.configure", Mutating: true, Description: "Set up the chosen nameserver for NimVor's zones: the zone list and one change to its configuration (BIND: an include line, requires confirm_edit_package_config), checked before it is kept; the daemon is started or reloaded and port 53 opened when the firewall is active. Restores the previous files when the daemon does not take them.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Confirm bool `json:"confirm_edit_package_config"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		return configureDriver(ctx, env, active(env), in.Confirm)
	}})

	r.Register(actions.Action{Name: "dns.backend_select", Mutating: true, Description: "Switch the nameserver NimVor runs the zones with (PowerDNS, NSD, Knot DNS or BIND), as a job: installs it when needed, writes its configuration for the same zone files, checks it, stops the previous nameserver, starts the new one and verifies every zone is served. Restores the previous nameserver when anything fails. Signed zones get new keys on the new nameserver (the DS record at the registrar must be updated) or become unsigned (NSD).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Backend       string `json:"backend"`
			ConfirmEdit   bool   `json:"confirm_edit_package_config"`
			ConfirmDNSSEC bool   `json:"confirm_dnssec_reset"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		to := driverByID(in.Backend)
		if to == nil {
			return nil, actions.InvalidParams("backend must be one of powerdns, nsd, knot, bind")
		}
		from := active(env)
		if from.id() == to.id() {
			return map[string]any{"backend": to.id(), "changed": false}, nil
		}
		pTo := pathsFor(env, to)
		if signed := signedZones(env, pathsFor(env, from)); len(signed) > 0 && !in.ConfirmDNSSEC {
			msg := fmt.Sprintf("%d zone(s) are signed (DNSSEC). Keys cannot move between nameservers: ", len(signed))
			if to.dnssec() == nil {
				msg += to.label() + " does not sign zones, so they become unsigned; remove their DS record at the registrar first or the domains stop resolving for validating resolvers."
			} else {
				msg += "the zones get new keys on " + to.label() + " and the DS record at the registrar must be replaced with the new one."
			}
			return nil, actions.Conflict(msg + " Confirm to continue.")
		}
		cur, _, _ := readConf(env, pTo)
		if _, wired := to.wire(env, pTo, cur); !wired && !in.ConfirmEdit {
			return nil, actions.Conflict(configureMessage(env, pTo, cur))
		}
		j := env.Jobs.Start("dns.backend_select", "server:dns", func(ctx context.Context, log io.Writer) (any, error) {
			return selectBackend(ctx, env, to, log)
		})
		return map[string]any{"job_id": j.ID, "backend": to.id()}, nil
	}})

	r.Register(actions.Action{Name: "dns.zone_parse", Description: "Read a zone file (BIND format) for import: the supported records, relative to the zone, and the lines skipped with reasons. Nothing is changed.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Zone string `json:"zone"`
			Text string `json:"text"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("zone", strings.TrimSuffix(in.Zone, ".")); err != nil {
			return nil, err
		}
		if len(in.Text) > 1<<20 {
			return nil, actions.InvalidParams("the zone file is larger than 1 MB")
		}
		return ParseZoneFile(in.Text, in.Zone), nil
	}})

	r.Register(actions.Action{Name: "dns.zone_render", Description: "Validate a zone and return its zone file (BIND format) without writing it, e.g. for export.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Zone
			Serial uint32 `json:"serial"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.Zone.Validate(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		return map[string]any{"text": in.Zone.Render(in.Serial)}, nil
	}})

	r.Register(actions.Action{Name: "dns.zone_apply", Mutating: true, Description: "Write a complete zone (SOA, records, optional secondaries) with a new serial, check it with the nameserver's own checker, load it and verify the nameserver serves the new serial; rolls back on failure.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var z Zone
		if err := host.Decode(raw, &z); err != nil {
			return nil, err
		}
		if err := z.Validate(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		mu.Lock()
		defer mu.Unlock()
		d := active(env)
		p := pathsFor(env, d)
		if !configured(env, p) {
			return nil, actions.Conflict("Set up the DNS server first (Server → DNS).")
		}
		file := p.zoneFile(z.Name)
		var old uint32
		if b, err := env.Root.ReadFile(file); err == nil {
			old = FileSerial(string(b))
		}
		serial := NextSerial(old, env.Now())
		content := z.Render(serial)
		content = strings.Replace(content, "\n$ORIGIN", "\n; secondaries "+strings.Join(z.Secondaries, ",")+"\n$ORIGIN", 1)
		zones := listZones(env, p)
		found := false
		for i := range zones {
			if zones[i].Name == z.Name {
				zones[i].Secondaries = z.Secondaries
				found = true
			}
		}
		if !found {
			zones = append(zones, zoneInfo{Name: z.Name, Secondaries: z.Secondaries})
			sort.Slice(zones, func(i, j int) bool { return zones[i].Name < zones[j].Name })
		}
		signed := signedZones(env, p)
		oldList, _ := env.Root.ReadFile(p.list)
		newList := d.renderList(env, p, zones, signed)
		listChanged := string(oldList) != newList
		owner := zoneOwner(env, d)
		set := env.Changes("dns-zone")
		set.Write(file, []byte(content), 0o640, owner)
		set.Write(p.list, []byte(newList), 0o640, owner)
		applied, err := set.Commit(func() error {
			if err := d.checkZone(ctx, env, p, z.Name, file); err != nil {
				return err
			}
			return d.checkConf(ctx, env, p)
		})
		if err != nil {
			return nil, host.FailWithValidation("The zone was not saved", err)
		}
		if !d.running(ctx, env) {
			return map[string]any{"zone": z.Name, "serial": serial, "loaded": false, "warning": d.label() + " is not running; the zone is loaded when it starts."}, nil
		}
		undo := func() {
			applied.Rollback()
			d.activate(ctx, env, z.Name, found, true)
		}
		if err := d.activate(ctx, env, z.Name, found, listChanged); err != nil {
			undo()
			return nil, actions.Failed(d.label()+" did not load the zone; the previous version was restored.", err.Error())
		}
		if err := d.afterApply(ctx, env, p, z.Name, z.Secondaries); err != nil {
			undo()
			return nil, actions.Failed(d.label()+" did not accept the zone's secondaries; the previous version was restored.", err.Error())
		}
		if err := verify(ctx, env, d.label(), z.Name, serial, signed[z.Name]); err != nil {
			undo()
			return nil, actions.Failed(d.label()+" did not serve the new zone; the previous version was restored.", err.Error())
		}
		return map[string]any{"zone": z.Name, "serial": serial, "loaded": true}, nil
	}})

	r.Register(actions.Action{Name: "dns.zone_delete", Mutating: true, Description: "Stop serving a zone and remove its file (confirm = zone name).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Zone    string `json:"zone"`
			Confirm string `json:"confirm"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		z := Zone{Name: in.Zone}
		z.Name = strings.ToLower(strings.TrimSuffix(z.Name, "."))
		if in.Confirm != z.Name {
			return nil, actions.InvalidParams("confirm must repeat the zone name")
		}
		mu.Lock()
		defer mu.Unlock()
		d := active(env)
		p := pathsFor(env, d)
		file := p.zoneFile(z.Name)
		if !env.Root.Exists(file) {
			return nil, actions.NotFound("NimVor does not serve the zone " + z.Name)
		}
		var keep []zoneInfo
		for _, zi := range listZones(env, p) {
			if zi.Name != z.Name {
				keep = append(keep, zi)
			}
		}
		signed := signedZones(env, p)
		wasSigned := signed[z.Name]
		delete(signed, z.Name)
		owner := zoneOwner(env, d)
		set := env.Changes("dns-zone")
		set.Write(p.list, []byte(d.renderList(env, p, keep, signed)), 0o640, owner)
		if wasSigned {
			set.Write(p.dir+"/"+signedFile, []byte(renderSigned(signed)), 0o640, owner)
		}
		set.Remove(file)
		applied, err := set.Commit(func() error { return d.checkConf(ctx, env, p) })
		if err != nil {
			return nil, host.FailWithValidation("The zone was not removed", err)
		}
		if d.running(ctx, env) {
			if err := d.activate(ctx, env, "", false, true); err != nil {
				applied.Rollback()
				d.activate(ctx, env, "", false, true)
				return nil, actions.Failed(d.label()+" did not reload; the zone was restored.", err.Error())
			}
			if wasSigned {
				if ops := d.dnssec(); ops != nil {
					ops.disable(ctx, env, p, z.Name) // PowerDNS keeps the keys in its own database
				}
			}
		}
		return map[string]any{"zone": z.Name, "deleted": true}, nil
	}})

	registerDNSSEC(r, env)
}

func registerDNSSEC(r *actions.Registry, env *host.Env) {
	// guard returns the active driver, its signing operations and paths when zones can be signed.
	guard := func(ctx context.Context) (driver, dnssecOps, paths, error) {
		d := active(env)
		p := pathsFor(env, d)
		ops := d.dnssec()
		if ops == nil {
			return d, nil, p, actions.Conflict(d.label() + " does not sign zones. Choose PowerDNS, Knot DNS or BIND (Server → DNS → Nameserver selection) to use DNSSEC.")
		}
		if !configured(env, p) {
			return d, nil, p, actions.Conflict("Set up the DNS server first (Server → DNS).")
		}
		if ok, why := ops.supported(ctx, env); !ok {
			return d, nil, p, actions.Conflict(why)
		}
		return d, ops, p, nil
	}
	waitDS := func(ctx context.Context, ops dnssecOps, p paths, zone string) ([]DS, error) {
		var last error
		for i := 0; i < 30; i++ {
			ds, err := ops.ds(ctx, env, p, zone)
			if err == nil && len(ds) > 0 {
				return ds, nil
			}
			last = err
			if env.Cfg.SimulateCommands {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
		return nil, last
	}

	r.Register(actions.Action{Name: "dns.dnssec_status", Description: "Whether the nameserver in use can sign zones, which zones are signed and, for one zone, the DS records to publish at its registrar.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Zone string `json:"zone"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		d := active(env)
		p := pathsFor(env, d)
		info := dnssecInfo(ctx, env, d)
		signed := signedZones(env, p)
		out := map[string]any{"backend": d.id(), "backend_label": d.label(), "supported": info["supported"], "reason": info["reason"], "signed": sortedKeys(signed)}
		if in.Zone != "" {
			name, err := zoneName(in.Zone)
			if err != nil {
				return nil, err
			}
			out["zone"] = name
			out["enabled"] = signed[name]
			out["ds"] = []DS{}
			if ops := d.dnssec(); ops != nil && signed[name] && d.running(ctx, env) {
				if ds, err := ops.ds(ctx, env, p, name); err == nil {
					out["ds"] = ds
				} else {
					out["ds_error"] = err.Error()
				}
			}
		}
		return out, nil
	}})

	r.Register(actions.Action{Name: "dns.dnssec_enable", Mutating: true, Description: "Start signing a zone (DNSSEC) with the nameserver in use and return the DS records to publish at the registrar. Restores the previous state when the nameserver does not take it.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Zone string `json:"zone"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		name, err := zoneName(in.Zone)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		d, ops, p, err := guard(ctx)
		if err != nil {
			return nil, err
		}
		if !env.Root.Exists(p.zoneFile(name)) {
			return nil, actions.NotFound("NimVor does not serve the zone " + name)
		}
		signed := signedZones(env, p)
		if signed[name] {
			ds, _ := waitDS(ctx, ops, p, name)
			return map[string]any{"zone": name, "enabled": true, "changed": false, "ds": ds}, nil
		}
		if !d.running(ctx, env) {
			return nil, actions.Conflict(d.label() + " is not running.")
		}
		signed[name] = true
		owner := zoneOwner(env, d)
		if b, ok := ops.(interface {
			before(context.Context, *host.Env, paths) error
		}); ok {
			if err := b.before(ctx, env, p); err != nil {
				env.Log.Warn("dnssec", "err", err)
			}
		}
		set := env.Changes("dns-dnssec")
		for _, dir := range ops.signedDirs(p) {
			set.Mkdir(dir, 0o770, owner)
		}
		set.Write(p.dir+"/"+signedFile, []byte(renderSigned(signed)), 0o640, owner)
		set.Write(p.list, []byte(d.renderList(env, p, listZones(env, p), signed)), 0o640, owner)
		if len(ops.signedDirs(p)) > 0 {
			setDirMode(env, p.dir, 0o770)
		}
		applied, err := set.Commit(func() error { return d.checkConf(ctx, env, p) })
		if err != nil {
			setDirMode(env, p.dir, 0o750)
			return nil, host.FailWithValidation("DNSSEC was not turned on", err)
		}
		undo := func() {
			applied.Rollback()
			d.activate(ctx, env, "", false, true)
			if len(ops.signedDirs(p)) > 0 && len(signedZones(env, p)) == 0 {
				setDirMode(env, p.dir, 0o750)
			}
		}
		for _, pth := range []string{p.dir} {
			selinux.Restore(ctx, env, pth)
		}
		if err := d.activate(ctx, env, "", false, true); err != nil {
			undo()
			return nil, actions.Failed(d.label()+" did not load the signing configuration; the previous state was restored.", err.Error())
		}
		if err := ops.enable(ctx, env, p, name); err != nil {
			undo()
			return nil, actions.Failed("The zone could not be signed; the previous state was restored.", err.Error())
		}
		ds, derr := waitDS(ctx, ops, p, name)
		res := map[string]any{"zone": name, "enabled": true, "changed": true, "ds": ds}
		if derr != nil || len(ds) == 0 {
			res["warning"] = "The zone is signed but its DS record is not available yet; open the zone again in a moment."
		}
		return res, nil
	}})

	r.Register(actions.Action{Name: "dns.dnssec_disable", Mutating: true, Description: "Stop signing a zone. Remove the DS record at the registrar first, or validating resolvers stop resolving the domain.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Zone string `json:"zone"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		name, err := zoneName(in.Zone)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		d, ops, p, err := guard(ctx)
		if err != nil {
			return nil, err
		}
		signed := signedZones(env, p)
		if !signed[name] {
			return map[string]any{"zone": name, "enabled": false, "changed": false}, nil
		}
		delete(signed, name)
		owner := zoneOwner(env, d)
		set := env.Changes("dns-dnssec")
		set.Write(p.dir+"/"+signedFile, []byte(renderSigned(signed)), 0o640, owner)
		set.Write(p.list, []byte(d.renderList(env, p, listZones(env, p), signed)), 0o640, owner)
		applied, err := set.Commit(func() error { return d.checkConf(ctx, env, p) })
		if err != nil {
			return nil, host.FailWithValidation("DNSSEC was not turned off", err)
		}
		if d.running(ctx, env) {
			if err := d.activate(ctx, env, "", false, true); err != nil {
				applied.Rollback()
				d.activate(ctx, env, "", false, true)
				return nil, actions.Failed(d.label()+" did not load the change; the previous state was restored.", err.Error())
			}
			if err := ops.disable(ctx, env, p, name); err != nil {
				env.Log.Warn("dnssec", "err", err)
			}
		}
		if len(signed) == 0 && len(ops.signedDirs(p)) > 0 {
			setDirMode(env, p.dir, 0o750)
		}
		return map[string]any{"zone": name, "enabled": false, "changed": true}, nil
	}})
}

// selectBackend switches the nameserver: the target's files for the same zone
// files, checked; then the previous daemon stops and the new one starts and is
// asked for every zone. Anything failing puts the previous nameserver back.
func selectBackend(ctx context.Context, env *host.Env, to driver, log io.Writer) (any, error) {
	mu.Lock()
	defer mu.Unlock()
	from := active(env)
	if from.id() == to.id() {
		return map[string]any{"backend": to.id(), "changed": false}, nil
	}
	pFrom := pathsFor(env, from)
	fromRunning := configured(env, pFrom) && from.running(ctx, env)
	complete := true
	if c, ok := to.(interface {
		complete(context.Context, *host.Env) bool
	}); ok {
		complete = c.complete(ctx, env)
	}
	if !to.installed(env) || !complete {
		fmt.Fprintf(log, "Installing %s…\n", to.label())
		if err := pkgs.Install(ctx, env, log, to.packages()); err != nil {
			return nil, err
		}
		if !to.installed(env) {
			return nil, fmt.Errorf("%s was installed but its program was not found", to.label())
		}
	}
	pTo := pathsFor(env, to)
	zones := listZones(env, pTo)
	signed := signedZones(env, pTo)
	ops := to.dnssec()
	var dnssecNotes []string
	if len(signed) > 0 {
		switch {
		case ops == nil:
			dnssecNotes = append(dnssecNotes, fmt.Sprintf("%s cannot sign zones: %d zone(s) are served unsigned now.", to.label(), len(signed)))
			signed = map[string]bool{}
		default:
			if ok, why := ops.supported(ctx, env); !ok {
				dnssecNotes = append(dnssecNotes, why+" The zones are served unsigned now.")
				signed = map[string]bool{}
			}
		}
		for _, n := range dnssecNotes {
			fmt.Fprintln(log, n)
		}
	}
	cur, exists, err := readConf(env, pTo)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", pTo.conf, err)
	}
	content, already := to.wire(env, pTo, cur)
	owner := zoneOwner(env, to)
	set := env.Changes("dns-select")
	set.Mkdir(pTo.dir, 0o750, owner)
	// The zone files get the new daemon's group.
	for _, z := range zones {
		if b, err := env.Root.ReadFile(pTo.zoneFile(z.Name)); err == nil {
			set.Write(pTo.zoneFile(z.Name), b, 0o640, owner)
		}
	}
	set.Write(pTo.list, []byte(to.renderList(env, pTo, zones, signed)), 0o640, owner)
	set.Write(pTo.dir+"/"+signedFile, []byte(renderSigned(signed)), 0o640, owner)
	if !already {
		if to.wholeFile() && exists && !env.Root.Exists(pTo.conf+".pre-nimvor") {
			set.Write(pTo.conf+".pre-nimvor", []byte(cur), 0o600, agentfs.RootOwner)
		}
		o := agentfs.Keep
		if !exists {
			o = agentfs.RootOwner
		}
		set.Write(pTo.conf, []byte(content), confMode(env, pTo), o)
	}
	if ops != nil && len(signed) > 0 {
		for _, dir := range ops.signedDirs(pTo) {
			if dir != pTo.dir {
				set.Mkdir(dir, 0o770, owner)
			}
		}
	}
	fmt.Fprintf(log, "Writing %s's configuration for the zones and checking it…\n", to.label())
	applied, err := set.Commit(func() error {
		if err := to.prepare(ctx, env, pTo); err != nil {
			return err
		}
		return to.checkConf(ctx, env, pTo)
	})
	if err != nil {
		return nil, host.FailWithValidation(to.label()+" was not set up", err)
	}
	if ops != nil && len(signed) > 0 && len(ops.signedDirs(pTo)) > 0 {
		setDirMode(env, pTo.dir, 0o770)
	}
	for _, pth := range []string{pTo.conf, pTo.dir} {
		selinux.Restore(ctx, env, pth)
	}
	back := func(why error) (any, error) {
		fmt.Fprintf(log, "%s did not work out: %v\nPutting %s back…\n", to.label(), why, from.label())
		env.Systemctl(ctx, "stop", to.unit(env))
		env.Systemctl(ctx, "disable", to.unit(env))
		applied.Rollback()
		setDirMode(env, pTo.dir, 0o750)
		if fromRunning {
			env.Systemctl(ctx, "enable", from.unit(env))
			env.Systemctl(ctx, "start", from.unit(env))
		}
		return nil, actions.Failed(to.label()+" could not take over; "+from.label()+" is running again with the same zones.", why.Error()+"\n"+env.JournalTail(ctx, to.unit(env), 30))
	}
	if fromRunning || env.UnitActive(ctx, from.unit(env)) {
		fmt.Fprintf(log, "Stopping %s…\n", from.label())
		env.Systemctl(ctx, "stop", from.unit(env))
		env.Systemctl(ctx, "disable", from.unit(env))
	}
	fmt.Fprintf(log, "Starting %s…\n", to.label())
	env.Systemctl(ctx, "enable", to.unit(env))
	if err := env.Systemctl(ctx, "start", to.unit(env)); err != nil {
		return back(err)
	}
	if !env.Cfg.SimulateCommands {
		up := false
		for i := 0; i < 20; i++ {
			if to.running(ctx, env) {
				up = true
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if !up {
			return back(fmt.Errorf("%s did not start", to.label()))
		}
	}
	for i, z := range zones {
		if i >= 10 {
			break
		}
		if err := verify(ctx, env, to.label(), z.Name, z.Serial, true); err != nil {
			return back(fmt.Errorf("%s does not serve %s: %w", to.label(), z.Name, err))
		}
	}
	if _, err := env.Writer.WriteFile(pTo.dir+"/"+backendFile, []byte(to.id()+"\n"), 0o640, agentfs.RootOwner); err != nil {
		return back(err)
	}
	fmt.Fprintf(log, "%s now answers for %d zone(s).\n", to.label(), len(zones))
	var dnssecErrors []string
	if ops != nil {
		for _, z := range sortedKeys(signed) {
			if err := ops.enable(ctx, env, pTo, z); err != nil {
				dnssecErrors = append(dnssecErrors, z+": "+err.Error())
				fmt.Fprintf(log, "Signing %s failed: %v\n", z, err)
			} else {
				fmt.Fprintf(log, "Signing %s with new keys; publish its new DS record at the registrar.\n", z)
			}
		}
	}
	notes := []string{fw.Open(ctx, env, "DNS", "53/tcp", "53/udp")}
	return map[string]any{"backend": to.id(), "changed": true, "zones": len(zones), "signed": sortedKeys(signed), "dnssec_notes": dnssecNotes, "dnssec_errors": dnssecErrors, "notes": notes}, nil
}
