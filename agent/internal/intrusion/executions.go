package intrusion

import (
	"context"
	"encoding/hex"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/logging"
)

// Execution is one program start (or refused start) of a logged account.
type Execution struct {
	Time    string `json:"time"`
	User    string `json:"user"`
	UID     int    `json:"uid"`
	Program string `json:"program"`
	Command string `json:"command"`
	// Blocked: the kernel refused to start it (permission denied), which is
	// what protection does to a program on the blacklist.
	Blocked bool `json:"blocked"`
}

var (
	auditMsgRe  = regexp.MustCompile(`^type=(\w+) msg=audit\((\d+)\.(\d+):(\d+)\): ?(.*)$`)
	auditAttrRe = regexp.MustCompile(`([A-Za-z0-9_]+)=("[^"]*"|\S*)`)
	hexRe       = regexp.MustCompile(`^[0-9A-Fa-f]+$`)
)

type auditGroup struct {
	sec    int64
	attrs  map[string]string
	args   map[int]string
	argc   int
	isExec bool
	// path is the program the call named (the first PATH record): for a
	// refused start the SYSCALL record's exe is the caller, not the program.
	path string
}

func unquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	// Unquoted values of text fields are hex when the text had special
	// characters.
	if len(v) >= 2 && len(v)%2 == 0 && hexRe.MatchString(v) {
		if b, err := hex.DecodeString(v); err == nil && len(b) > 0 {
			return strings.ReplaceAll(string(b), "\x00", " ")
		}
	}
	return v
}

// ParseAudit reads audit log lines and returns the executions NimVor's
// rules recorded, newest first. names maps UIDs to account names (only
// those are kept).
func ParseAudit(data []byte, names map[int]string) []Execution {
	groups := map[string]*auditGroup{}
	var order []string
	for _, line := range strings.Split(string(data), "\n") {
		m := auditMsgRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		typ, rest := m[1], m[5]
		if typ != "SYSCALL" && typ != "EXECVE" && typ != "PATH" {
			continue
		}
		id := m[2] + "." + m[3] + ":" + m[4]
		g := groups[id]
		if g == nil {
			sec, _ := strconv.ParseInt(m[2], 10, 64)
			g = &auditGroup{sec: sec, attrs: map[string]string{}, args: map[int]string{}}
			groups[id] = g
			order = append(order, id)
		}
		if typ == "PATH" {
			attrs := map[string]string{}
			for _, a := range auditAttrRe.FindAllStringSubmatch(rest, -1) {
				attrs[a[1]] = a[2]
			}
			if attrs["item"] == "0" && g.path == "" {
				g.path = unquote(attrs["name"])
			}
			continue
		}
		for _, a := range auditAttrRe.FindAllStringSubmatch(rest, -1) {
			k, v := a[1], a[2]
			if typ == "EXECVE" {
				if k == "argc" {
					g.argc, _ = strconv.Atoi(v)
				} else if len(k) > 1 && k[0] == 'a' {
					if n, err := strconv.Atoi(k[1:]); err == nil {
						g.args[n] = unquote(v)
					}
				}
				continue
			}
			g.attrs[k] = v
			if k == "key" && unquote(v) == Key {
				g.isExec = true
			}
		}
	}
	var out []Execution
	for _, id := range order {
		g := groups[id]
		if !g.isExec {
			continue
		}
		uid, err := strconv.Atoi(g.attrs["uid"])
		if err != nil {
			continue
		}
		name, ok := names[uid]
		if !ok {
			continue
		}
		var parts []string
		for i := 0; i < len(g.args) && i < 40; i++ {
			parts = append(parts, g.args[i])
		}
		cmd := logging.Redact(strings.Join(parts, " "))
		if len(cmd) > 200 {
			cmd = cmd[:200] + "…"
		}
		prog := unquote(g.attrs["exe"])
		if g.attrs["success"] == "no" && g.path != "" {
			prog = g.path
			if cmd == "" {
				cmd = g.path
			}
		}
		if (prog == "" || prog == "(null)") && len(parts) > 0 {
			prog = parts[0]
		}
		out = append(out, Execution{
			Time:    time.Unix(g.sec, 0).UTC().Format(time.RFC3339),
			User:    name,
			UID:     uid,
			Program: prog,
			Command: cmd,
			Blocked: g.attrs["success"] == "no",
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time > out[j].Time })
	return out
}

// maxAuditRead keeps a runaway audit log from being read whole.
const maxAuditRead = 64 << 20

func readExecutions(env *host.Env, names map[int]string) ([]Execution, string) {
	fi, err := env.Root.Stat(AuditLog)
	if err != nil {
		return nil, "The audit log does not exist yet: auditd is not installed or has not recorded anything."
	}
	if fi.Size() > maxAuditRead {
		return nil, "The audit log is too large to read here."
	}
	b, err := env.Root.ReadFile(AuditLog)
	if err != nil {
		return nil, "The audit log could not be read."
	}
	return ParseAudit(b, names), ""
}

// Executions returns the logged executions, newest first, for one account
// (or all when user is empty), at most limit.
func Executions(env *host.Env, user string, limit int) (map[string]any, error) {
	cands, err := candidates(env)
	if err != nil {
		return nil, err
	}
	names := map[int]string{}
	for _, u := range cands {
		names[u.UID] = u.Name
	}
	all, note := readExecutions(env, names)
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var list []Execution
	for _, e := range all {
		if user != "" && e.User != user {
			continue
		}
		list = append(list, e)
		if len(list) >= limit {
			break
		}
	}
	return map[string]any{"executions": nonNil(list), "note": note}, nil
}

func nonNil(l []Execution) []Execution {
	if l == nil {
		return []Execution{}
	}
	return l
}

// Status is everything the panel's page shows.
func Status(ctx context.Context, env *host.Env) map[string]any {
	mu.Lock()
	st := load(env)
	mu.Unlock()
	cands, _ := candidates(env)
	names := map[int]string{}
	for _, u := range cands {
		names[u.UID] = u.Name
	}
	execs, note := readExecutions(env, names)
	today := time.Now().UTC().Format("2006-01-02")
	total := map[string]int{}
	day := map[string]int{}
	blocked := map[string]int{}
	for _, e := range execs {
		total[e.User]++
		if strings.HasPrefix(e.Time, today) {
			day[e.User]++
		}
		if e.Blocked {
			blocked[e.User]++
		}
	}
	accounts := make([]Account, 0, len(cands))
	interceptions := 0
	for _, u := range cands {
		s := st.Accounts[u.Name]
		remark := u.Name + " (nologin)"
		if u.CanLogin {
			remark = u.Name + " (can login)"
		}
		if c := strings.TrimSpace(strings.Split(u.Comment, ",")[0]); c != "" && c != u.Name {
			remark = c + " · " + remark
		}
		interceptions += blocked[u.Name]
		accounts = append(accounts, Account{Name: u.Name, UID: u.UID, Kind: kindOf(env, u), CanLogin: u.CanLogin, Remark: remark,
			Log: s.Log, Protect: s.Protect, Total: total[u.Name], Today: day[u.Name], Blocked: blocked[u.Name]})
	}
	type prog struct {
		Path   string   `json:"path"`
		Exists bool     `json:"exists"`
		Denied []string `json:"denied"`
	}
	list := make([]prog, 0, len(st.Blacklist))
	for _, p := range st.Blacklist {
		list = append(list, prog{Path: p, Exists: env.Root.Exists(p), Denied: nonNilStr(st.Applied[p])})
	}
	var suggested []string
	have := map[string]bool{}
	for _, p := range st.Blacklist {
		have[p] = true
	}
	for _, p := range SuggestedPresent(env) {
		if !have[p] {
			suggested = append(suggested, p)
		}
	}
	ops := make([]Op, 0, len(st.Ops))
	for i := len(st.Ops) - 1; i >= 0; i-- {
		ops = append(ops, st.Ops[i])
	}
	return map[string]any{
		"enabled":       st.Enabled,
		"accounts":      accounts,
		"blacklist":     list,
		"suggested":     nonNilStr(suggested),
		"operations":    ops,
		"interceptions": interceptions,
		"auditd":        map[string]any{"installed": env.Runner.Available("augenrules"), "active": env.UnitActive(ctx, "auditd.service"), "rules": env.Root.Exists(RulesFile), "note": note},
		"acl":           map[string]any{"installed": env.Runner.Available("setfacl")},
	}
}

func nonNilStr(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}
