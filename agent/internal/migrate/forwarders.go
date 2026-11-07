package migrate

import (
	"regexp"
	"strings"
)

// Forwarders (aliases) are read the same way from every panel that keeps them: a source address and the
// plain addresses it delivers to. Anything else (a pipe, a bounce, a local user name) is left out.

var destSplit = regexp.MustCompile(`[\s,;]+|\\n`)

// splitDests splits a destination list written with commas, spaces, semicolons or newlines.
func splitDests(s string) []string {
	var out []string
	for _, p := range destSplit.Split(strings.TrimSpace(s), -1) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// makeForwarder validates one forwarder. source is local@domain, or @domain / *@domain for a catch-all;
// destinations equal to the source are dropped (a mailbox that "forwards to itself" is not a forwarder).
func makeForwarder(source string, dests []string) (Forwarder, bool) {
	source = strings.ToLower(strings.TrimSpace(source))
	if strings.HasPrefix(source, "@") {
		source = "*" + source
	}
	local, dom, ok := strings.Cut(source, "@")
	if !ok || !valiasLocal.MatchString(local) || !domainName.MatchString(dom) {
		return Forwarder{}, false
	}
	seen := map[string]bool{source: true}
	var clean []string
	for _, d := range dests {
		d = strings.ToLower(strings.TrimSpace(d))
		if !valiasAddr.MatchString(d) {
			return Forwarder{}, false // one destination that is not a plain address: skip the whole rule
		}
		if !seen[d] {
			seen[d] = true
			clean = append(clean, d)
		}
	}
	if len(clean) == 0 || len(clean) > 50 {
		return Forwarder{}, false
	}
	return Forwarder{Source: source, Destinations: clean}, true
}

// attachForwarders puts each forwarder into the account that owns its domain, or into an account "mail"
// for the server's mail when no account owns it.
func attachForwarders(accts []*Account, fwds []Forwarder) []*Account {
	owner := map[string]*Account{}
	var mail *Account
	for _, a := range accts {
		if a.ID == "mail" {
			mail = a
		}
		for _, d := range a.Domains {
			owner[d.Name] = a
		}
		for _, m := range a.Email {
			if _, dom, ok := strings.Cut(m.Address, "@"); ok && owner[dom] == nil {
				owner[dom] = a
			}
		}
	}
	for _, f := range fwds {
		_, dom, _ := strings.Cut(f.Source, "@")
		a := owner[dom]
		if a == nil {
			if mail == nil {
				mail = newAccount("mail", "/")
				mail.Name = "mail (addresses of the server)"
				accts = append(accts, mail)
			}
			a = mail
			owner[dom] = mail
		}
		a.Forwarders = append(a.Forwarders, f)
	}
	return accts
}
