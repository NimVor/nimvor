// Package users lists local system accounts from /etc/passwd (read-only).
// /etc/shadow is never read.
package users

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
)

// User is one account.
type User struct {
	Name     string `json:"name"`
	UID      int    `json:"uid"`
	GID      int    `json:"gid"`
	Comment  string `json:"comment"`
	Home     string `json:"home"`
	Shell    string `json:"shell"`
	CanLogin bool   `json:"can_login"`
	System   bool   `json:"system"`
}

var noLoginShells = map[string]bool{
	"/usr/sbin/nologin": true, "/sbin/nologin": true, "/bin/false": true, "/usr/bin/false": true, "": true,
}

// Discover reads passwd and returns all accounts.
func Discover(root filesystem.Root, passwdPath string) ([]User, error) {
	data, err := root.ReadFile(passwdPath)
	if err != nil {
		return nil, err
	}
	return Parse(data), nil
}

// Parse parses passwd content.
func Parse(data []byte) []User {
	out := []User{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.Split(line, ":")
		if len(f) != 7 {
			continue
		}
		uid, err1 := strconv.Atoi(f[2])
		gid, err2 := strconv.Atoi(f[3])
		if err1 != nil || err2 != nil {
			continue
		}
		u := User{Name: f[0], UID: uid, GID: gid, Comment: f[4], Home: f[5], Shell: f[6]}
		u.CanLogin = !noLoginShells[u.Shell]
		u.System = uid < 1000 || uid >= 60000
		out = append(out, u)
	}
	return out
}
