package dnsmgr

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// A driver is one authoritative nameserver NimVor can run the zones with.
// All of them serve the same zone files (RFC 1035 format, written by the
// agent into the zone folder); a driver only knows how to list those files
// in its own configuration, check them, load them and, where it can, sign
// them (DNSSEC).
type driver interface {
	id() string
	label() string
	// packages are NimVor package names (mapped per distribution).
	packages() []string
	installed(env *host.Env) bool
	unit(env *host.Env) string
	// account is the system account the daemon runs as; its group may read
	// the zone files ("" = root only).
	account(env *host.Env) string
	// confFile is the file NimVor edits (an include line, a drop-in that is
	// wholly NimVor's, or the whole configuration: see wholeFile).
	confFile(env *host.Env) string
	// mainConf is the configuration file the daemon's checker reads.
	mainConf(env *host.Env) string
	listName() string
	// renderList renders the list of zones the daemon serves.
	renderList(env *host.Env, p paths, zones []zoneInfo, signed map[string]bool) string
	// wire returns the new content of confFile (cur = its present content)
	// and whether cur already wires NimVor's zones in.
	wire(env *host.Env, p paths, cur string) (content string, wired bool)
	// wholeFile: NimVor replaces confFile (the original is kept next to it
	// as <name>.pre-nimvor).
	wholeFile() bool
	// prepare runs once the files are written, before the daemon is
	// (re)started (e.g. PowerDNS creates its DNSSEC database).
	prepare(ctx context.Context, env *host.Env, p paths) error
	checkConf(ctx context.Context, env *host.Env, p paths) error
	checkZone(ctx context.Context, env *host.Env, p paths, zone, file string) error
	running(ctx context.Context, env *host.Env) bool
	// activate makes the daemon load what changed: the zone's new content
	// (found: the zone existed before) and/or the zone list (listChanged:
	// a zone was added or removed, or its secondaries changed).
	activate(ctx context.Context, env *host.Env, zone string, found, listChanged bool) error
	// afterApply sets per-zone things a zone file cannot carry (PowerDNS
	// keeps the secondaries of a zone as metadata).
	afterApply(ctx context.Context, env *host.Env, p paths, zone string, secondaries []string) error
	// dnssec returns the signing operations, or nil when the daemon cannot
	// sign (NSD serves zones signed elsewhere).
	dnssec() dnssecOps
	advantages() []string
	disadvantages() []string
	notes() []string
}

// dnssecOps are the signing operations of a driver.
type dnssecOps interface {
	// supported reports whether this installation can sign (version, tools).
	supported(ctx context.Context, env *host.Env) (bool, string)
	// signedDirs are folders the daemon must write to when zones are signed.
	signedDirs(p paths) []string
	// enable starts signing a zone (the zone list already carries it).
	enable(ctx context.Context, env *host.Env, p paths, zone string) error
	// disable stops signing a zone (the zone list no longer carries it).
	disable(ctx context.Context, env *host.Env, p paths, zone string) error
	// ds returns the delegation-signer records to publish at the registrar.
	ds(ctx context.Context, env *host.Env, p paths, zone string) ([]DS, error)
}

// DS is one delegation-signer record (what the registrar needs to chain the
// zone to its parent).
type DS struct {
	KeyTag     int    `json:"key_tag"`
	Algorithm  int    `json:"algorithm"`
	DigestType int    `json:"digest_type"`
	Digest     string `json:"digest"`
}

func (d DS) String() string {
	return fmt.Sprintf("%d %d %d %s", d.KeyTag, d.Algorithm, d.DigestType, d.Digest)
}

var dsRe = regexp.MustCompile(`(?m)^\S+\s+(?:\d+\s+)?(?:IN\s+)?DS\s+(\d+)\s+(\d+)\s+(\d+)\s+([0-9A-Fa-f]+)`)

// parseDS reads "name [ttl] [IN] DS tag alg digesttype digest" lines (BIND's,
// Knot's and PowerDNS' output look alike) and drops duplicates.
func parseDS(out string) []DS {
	var res []DS
	seen := map[string]bool{}
	for _, m := range dsRe.FindAllStringSubmatch(out, -1) {
		tag, _ := strconv.Atoi(m[1])
		alg, _ := strconv.Atoi(m[2])
		dt, _ := strconv.Atoi(m[3])
		d := DS{KeyTag: tag, Algorithm: alg, DigestType: dt, Digest: strings.ToLower(m[4])}
		if !seen[d.String()] {
			seen[d.String()] = true
			res = append(res, d)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].KeyTag != res[j].KeyTag {
			return res[i].KeyTag < res[j].KeyTag
		}
		return res[i].DigestType < res[j].DigestType
	})
	return res
}

// drivers lists the choices in the order the panel shows them.
func drivers() []driver {
	return []driver{pdnsDriver{}, nsdDriver{}, knotDriver{}, bindDriver{}}
}

func driverByID(id string) driver {
	for _, d := range drivers() {
		if d.id() == id {
			return d
		}
	}
	return nil
}

// stateFile names (in the zone folder) what NimVor remembers about DNS.
const (
	backendFile = ".backend"
	signedFile  = "dnssec-zones"
)

// active is the nameserver NimVor currently runs the zones with (BIND until
// the administrator chooses another).
func active(env *host.Env) driver {
	if b, err := env.Root.ReadFile(env.M.DNSZoneDir + "/" + backendFile); err == nil {
		if d := driverByID(strings.TrimSpace(string(b))); d != nil {
			return d
		}
	}
	return bindDriver{}
}

type paths struct {
	dir, list, conf string
	d               driver
}

func dnsPaths(env *host.Env) paths { return pathsFor(env, active(env)) }

func pathsFor(env *host.Env, d driver) paths {
	return paths{dir: env.M.DNSZoneDir, list: env.M.DNSZoneDir + "/" + d.listName(), conf: d.confFile(env), d: d}
}

func (p paths) zoneFile(zone string) string { return p.dir + "/db." + zone }
func (p paths) keysDir() string             { return p.dir + "/keys" }

func signedZones(env *host.Env, p paths) map[string]bool {
	out := map[string]bool{}
	b, err := env.Root.ReadFile(p.dir + "/" + signedFile)
	if err != nil {
		return out
	}
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out[l] = true
		}
	}
	return out
}

func renderSigned(signed map[string]bool) string {
	names := make([]string, 0, len(signed))
	for z := range signed {
		names = append(names, z)
	}
	sort.Strings(names)
	return "# Zones NimVor signs (DNSSEC). Managed by NimVor.\n" + strings.Join(names, "\n") + "\n"
}

// zoneOwner is who owns the zone files: root, group of the daemon's account.
func zoneOwner(env *host.Env, d driver) agentfs.Owner {
	if acct := d.account(env); acct != "" {
		if u, err := env.LookupUser(acct); err == nil {
			return agentfs.Owner{UID: 0, GID: u.GID}
		}
	}
	return agentfs.RootOwner
}

// The marked block NimVor keeps in a package file it only partly owns.
const (
	blockBegin = "# BEGIN NimVor DNS (managed by NimVor, do not edit)"
	blockEnd   = "# END NimVor DNS"
)

func withBlock(cur, block string) (string, bool) {
	body := blockBegin + "\n" + strings.TrimRight(block, "\n") + "\n" + blockEnd + "\n"
	if i := strings.Index(cur, blockBegin); i >= 0 {
		if j := strings.Index(cur[i:], blockEnd); j >= 0 {
			end := i + j + len(blockEnd)
			end += len(cur[end:]) - len(strings.TrimLeft(cur[end:], "\n"))
			next := cur[:i] + body + cur[end:]
			return next, cur[i:i+j+len(blockEnd)]+"\n" == body
		}
	}
	return strings.TrimRight(cur, "\n") + "\n\n" + body, false
}

func hasBlock(cur string) bool {
	return strings.Contains(cur, blockBegin) && strings.Contains(cur, blockEnd)
}

// hasIPv6 reports whether this machine has IPv6 at all: a nameserver told to
// listen on [::] fails to start where the kernel has none.
func hasIPv6(env *host.Env) bool { return env.Root.Exists("/proc/net/if_inet6") }

// interfaceAddrs is replaced in tests.
var interfaceAddrs = net.InterfaceAddrs

// stubHeld reports whether systemd-resolved's stub listener holds 127.0.0.53:53: a nameserver that binds
// every address on port 53 then fails to start ("address already in use"), where BIND binds one address at a
// time and carries on.
func stubHeld(ctx context.Context, env *host.Env) bool {
	return env.Root.Exists("/run/systemd/resolve/stub-resolv.conf") && env.UnitActive(ctx, "systemd-resolved.service")
}

// listenAddresses returns the addresses a nameserver should bind to instead of all of them, or nil when it can
// bind all: every address of this machine except the stub's, link-local ones and other loopback addresses.
func listenAddresses(env *host.Env) []string {
	if !stubHeld(context.Background(), env) {
		return nil
	}
	seen := map[string]bool{"127.0.0.1": true}
	out := []string{"127.0.0.1"}
	v6 := hasIPv6(env)
	addrs, _ := interfaceAddrs()
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := n.IP
		switch {
		case ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified():
			continue
		case ip.To4() == nil && !v6:
			continue
		}
		if k := ip.String(); !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
