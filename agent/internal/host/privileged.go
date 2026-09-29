package host

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Accounts with administrator rights must never run website code,
// commands or sessions: a website's PHP (under a PHP-FPM master that does
// not set no_new_privs) could use their sudo or group rights to become
// root. The agent refuses them itself, whatever the panel asks.

var privilegedGroups = map[string]bool{
	"root": true, "sudo": true, "wheel": true, "admin": true, "adm": true, "shadow": true,
	"disk": true, "docker": true, "lxd": true, "lxc": true, "systemd-journal": true,
	"libvirt": true, "kvm": true, "kmem": true,
}

// PrivilegedGroups returns the privileged groups user belongs to (by
// supplementary membership or primary group), by name; extra adds groups
// that sudoers grants rights to.
func (e *Env) PrivilegedGroups(user string, primaryGID int, extra ...string) []string {
	b, err := e.Root.ReadFile("/etc/group")
	if err != nil {
		return nil
	}
	want := map[string]bool{}
	for _, g := range extra {
		want[g] = true
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 4 || !(privilegedGroups[f[0]] || want[f[0]]) {
			continue
		}
		member := f[2] == fmt.Sprint(primaryGID)
		for _, m := range strings.Split(f[3], ",") {
			member = member || m == user
		}
		if member {
			out = append(out, f[0])
		}
	}
	sort.Strings(out)
	return out
}

var sudoWord = regexp.MustCompile(`%?[A-Za-z0-9_.-]+`)

// sudoers reads /etc/sudoers and /etc/sudoers.d: the names and %groups
// its rules mention (Defaults lines aside).
func (e *Env) sudoers() (names, groups map[string]bool) {
	names, groups = map[string]bool{}, map[string]bool{}
	files := []string{"/etc/sudoers"}
	more, _ := e.Root.Glob("/etc/sudoers.d/*")
	for _, f := range more {
		// sudo skips names with a dot or ending in ~.
		if b := filepath.Base(f); !strings.Contains(b, ".") && !strings.HasSuffix(b, "~") {
			files = append(files, f)
		}
	}
	for _, f := range files {
		b, err := e.Root.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "Defaults") || strings.HasPrefix(line, "@include") || line == "" {
				continue
			}
			// Only who a rule is for: "users hosts = (runas) commands"
			// (the first field) or a User_Alias's members.
			var who string
			switch {
			case strings.HasPrefix(line, "User_Alias"):
				_, who, _ = strings.Cut(line, "=")
			case strings.HasPrefix(line, "Host_Alias"), strings.HasPrefix(line, "Cmnd_Alias"), strings.HasPrefix(line, "Cmd_Alias"), strings.HasPrefix(line, "Runas_Alias"):
				continue
			case strings.Contains(line, "="):
				before, _, _ := strings.Cut(line, "=")
				if f := strings.Fields(before); len(f) > 0 {
					who = f[0]
				}
			}
			for _, w := range sudoWord.FindAllString(who, -1) {
				if strings.HasPrefix(w, "%") {
					groups[w[1:]] = true
				} else {
					names[w] = true
				}
			}
		}
	}
	return names, groups
}

// PrivilegedAccount says why an account has administrator rights ("" when
// it has none): privileged groups, or a sudoers rule naming it or a
// group it belongs to.
func (e *Env) PrivilegedAccount(user string, primaryGID int) string {
	names, groups := e.sudoers()
	var extra []string
	for g := range groups {
		extra = append(extra, g)
	}
	var why []string
	if gs := e.PrivilegedGroups(user, primaryGID, extra...); len(gs) > 0 {
		why = append(why, "member of "+strings.Join(gs, ", "))
	}
	if names[user] {
		why = append(why, "named in sudoers")
	}
	return strings.Join(why, "; ")
}
