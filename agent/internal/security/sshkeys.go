package security

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/changeset"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/selinux"
)

// SSH keys of the server's administrators: root and the members of the
// sudo, admin and wheel groups. The panel adds and removes public keys in
// their ~/.ssh/authorized_keys, so that password logins can be turned off
// without locking anyone out.
//
// Only plain public keys are accepted: no options (command=, from=,
// environment=…), no DSA, RSA of 2048 bits and more. Lines WhaCP did not
// add are listed and kept as they are.

// AdminGroups are the groups whose members administer the server.
var AdminGroups = []string{"sudo", "admin", "wheel"}

var keyTypes = map[string]bool{
	"ssh-ed25519": true, "ssh-rsa": true,
	"ecdsa-sha2-nistp256": true, "ecdsa-sha2-nistp384": true, "ecdsa-sha2-nistp521": true,
	"sk-ssh-ed25519@openssh.com": true, "sk-ecdsa-sha2-nistp256@openssh.com": true,
}

// PublicKey is one key of an authorized_keys file.
type PublicKey struct {
	Type        string `json:"type"`
	Bits        int    `json:"bits,omitempty"`
	Comment     string `json:"comment"`
	Fingerprint string `json:"fingerprint"`
	// Options: the line restricts the key (command=, from=…), added by
	// hand; listed, never written by WhaCP.
	Options bool   `json:"options,omitempty"`
	blob    string // base64
}

// wire reads the SSH wire format (RFC 4251 strings).
type wire []byte

func (w *wire) next() ([]byte, error) {
	if len(*w) < 4 {
		return nil, errors.New("truncated key")
	}
	n := binary.BigEndian.Uint32(*w)
	if uint64(n) > uint64(len(*w)-4) {
		return nil, errors.New("truncated key")
	}
	v := (*w)[4 : 4+n]
	*w = (*w)[4+n:]
	return v, nil
}

// decodeKey checks the key blob against its type.
func decodeKey(typ, b64 string) (bits int, err error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) == 0 {
		return 0, errors.New("the key data is not valid base64")
	}
	w := wire(raw)
	t, err := w.next()
	if err != nil {
		return 0, err
	}
	if string(t) != typ {
		return 0, fmt.Errorf("the key data is a %s key, not %s", t, typ)
	}
	switch {
	case typ == "ssh-rsa":
		e, err1 := w.next()
		n, err2 := w.next()
		if err1 != nil || err2 != nil || len(e) == 0 {
			return 0, errors.New("truncated RSA key")
		}
		bits = new(big.Int).SetBytes(n).BitLen()
		if bits < 2048 {
			return bits, fmt.Errorf("RSA keys need at least 2048 bits (this one has %d); an Ed25519 key is recommended", bits)
		}
	case strings.HasPrefix(typ, "ecdsa-sha2-"), strings.HasPrefix(typ, "sk-ecdsa-sha2-"):
		curve, err := w.next()
		if err != nil || !strings.Contains(typ, string(curve)) {
			return 0, errors.New("the ECDSA curve does not match the key type")
		}
		if _, err := w.next(); err != nil {
			return 0, err
		}
		bits, _ = strconv.Atoi(strings.TrimPrefix(string(curve), "nistp"))
	case typ == "ssh-ed25519", typ == "sk-ssh-ed25519@openssh.com":
		k, err := w.next()
		if err != nil || len(k) != 32 {
			return 0, errors.New("an Ed25519 key has 32 bytes")
		}
		bits = 256
	}
	return bits, nil
}

func fingerprint(b64 string) string {
	raw, _ := base64.StdEncoding.DecodeString(b64)
	sum := sha256.Sum256(raw)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func cleanComment(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	c := strings.TrimSpace(b.String())
	if utf8.RuneCountInString(c) > 100 {
		c = string([]rune(c)[:100])
	}
	return c
}

// ParsePublicKey checks a public key as pasted into the panel: one line,
// "type base64 [comment]", without options.
func ParsePublicKey(line string) (PublicKey, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return PublicKey{}, errors.New("paste a public key (the contents of a .pub file)")
	}
	if len(line) > 16<<10 || strings.ContainsAny(line, "\r\n\x00") {
		return PublicKey{}, errors.New("one key per line, on a single line")
	}
	if strings.Contains(line, "PRIVATE KEY") {
		return PublicKey{}, errors.New("this is a private key; paste the public key (the .pub file) and keep the private key secret")
	}
	f := strings.Fields(line)
	if len(f) < 2 || !keyTypes[f[0]] {
		if f[0] == "ssh-dss" {
			return PublicKey{}, errors.New("DSA keys are not accepted (OpenSSH turned them off); create an Ed25519 key: ssh-keygen -t ed25519")
		}
		return PublicKey{}, errors.New("not a public key WhaCP accepts: it must start with ssh-ed25519, ssh-rsa, ecdsa-sha2-… or sk-…, without options")
	}
	bits, err := decodeKey(f[0], f[1])
	if err != nil {
		return PublicKey{}, err
	}
	return PublicKey{Type: f[0], Bits: bits, Comment: cleanComment(strings.Join(f[2:], " ")), Fingerprint: fingerprint(f[1]), blob: f[1]}, nil
}

// parseAuthorizedLine reads a line of authorized_keys (options allowed).
func parseAuthorizedLine(line string) (PublicKey, bool) {
	f := strings.Fields(line)
	for i := 0; i+1 < len(f); i++ {
		if keyTypes[f[i]] || f[i] == "ssh-dss" {
			bits, _ := decodeKey(f[i], f[i+1])
			return PublicKey{Type: f[i], Bits: bits, Comment: cleanComment(strings.Join(f[i+2:], " ")), Fingerprint: fingerprint(f[i+1]), Options: i > 0, blob: f[i+1]}, true
		}
	}
	return PublicKey{}, false
}

// AdminAccount is an administrator and the keys it can log in with.
type AdminAccount struct {
	User string      `json:"user"`
	Home string      `json:"home"`
	Keys []PublicKey `json:"keys"`
	// Unrecognised lines of its authorized_keys (kept as they are).
	Other int `json:"other_lines,omitempty"`
	uid   int
	gid   int
}

// Admins lists root and the administrators with their keys.
func Admins(env *host.Env) []AdminAccount {
	type pw struct {
		home     string
		uid, gid int
	}
	accts := map[string]pw{}
	if b, err := env.Root.ReadFile("/etc/passwd"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Split(l, ":")
			if len(f) >= 7 {
				uid, e1 := strconv.Atoi(f[2])
				gid, e2 := strconv.Atoi(f[3])
				if e1 == nil && e2 == nil {
					accts[f[0]] = pw{f[5], uid, gid}
				}
			}
		}
	}
	names := map[string]bool{"root": true}
	if b, err := env.Root.ReadFile("/etc/group"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Split(l, ":")
			if len(f) == 4 && f[3] != "" {
				for _, g := range AdminGroups {
					if f[0] == g {
						for _, u := range strings.Split(f[3], ",") {
							names[strings.TrimSpace(u)] = true
						}
					}
				}
			}
		}
	}
	var out []AdminAccount
	for n := range names {
		p, ok := accts[n]
		if !ok || p.home == "" || !filepath.IsAbs(p.home) {
			continue
		}
		a := AdminAccount{User: n, Home: p.home, Keys: []PublicKey{}, uid: p.uid, gid: p.gid}
		if b, err := env.Root.ReadFile(filepath.Join(p.home, ".ssh", "authorized_keys")); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if l = strings.TrimSpace(l); l == "" || strings.HasPrefix(l, "#") {
					continue
				}
				if k, ok := parseAuthorizedLine(l); ok {
					a.Keys = append(a.Keys, k)
				} else {
					a.Other++
				}
			}
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].User == "root") != (out[j].User == "root") {
			return out[i].User == "root"
		}
		return out[i].User < out[j].User
	})
	return out
}

func findAdmin(env *host.Env, user string) (AdminAccount, error) {
	for _, a := range Admins(env) {
		if a.User == user {
			return a, nil
		}
	}
	return AdminAccount{}, actions.InvalidParams("user: " + user + " is not root or a member of " + strings.Join(AdminGroups, "/"))
}

// keysWriter may write only this account's ~/.ssh (no links inside).
func keysWriter(env *host.Env, a AdminAccount) (*changeset.Set, string, error) {
	dir := filepath.Join(a.Home, ".ssh")
	real, err := env.Root.Path(dir)
	if err != nil {
		return nil, "", err
	}
	if info, err := os.Lstat(real); err == nil && !info.IsDir() {
		return nil, "", fmt.Errorf("%s is not a folder (a link?); not changed", dir)
	}
	w := agentfs.Writer{Root: env.Root, Policy: agentfs.Policy{Patterns: []string{dir, dir + "/**", env.Cfg.StateDir + "/**"}}, Owners: env.Writer.Owners}
	set := changeset.New(w, filepath.Join(env.Cfg.StateDir, "config-backups"), "ssh-keys-"+a.User)
	owner := agentfs.Owner{UID: a.uid, GID: a.gid}
	if !env.Root.Exists(dir) {
		set.Mkdir(dir, 0o700, owner)
	}
	return set, filepath.Join(dir, "authorized_keys"), nil
}

// AddKey adds a public key to an administrator's authorized_keys.
func AddKey(ctx context.Context, env *host.Env, user, key string) (PublicKey, error) {
	a, err := findAdmin(env, user)
	if err != nil {
		return PublicKey{}, err
	}
	k, err := ParsePublicKey(key)
	if err != nil {
		return PublicKey{}, actions.InvalidParams("key: " + err.Error())
	}
	for _, have := range a.Keys {
		if have.Fingerprint == k.Fingerprint {
			return PublicKey{}, actions.Conflict("This key is already allowed for " + user + ".")
		}
	}
	set, file, err := keysWriter(env, a)
	if err != nil {
		return PublicKey{}, actions.Failed("The key was not added.", err.Error())
	}
	cur, _ := env.Root.ReadFile(file)
	next := string(cur)
	if next != "" && !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	line := k.Type + " " + k.blob
	if k.Comment != "" {
		line += " " + k.Comment
	}
	next += line + "\n"
	set.Write(file, []byte(next), 0o600, agentfs.Owner{UID: a.uid, GID: a.gid})
	if _, err := set.Apply(); err != nil {
		return PublicKey{}, actions.Failed("The key was not added.", err.Error())
	}
	// sshd reads keys only with their SELinux label (ssh_home_t).
	selinux.Restore(ctx, env, filepath.Dir(file))
	return k, nil
}

// RemoveKey removes the key with this fingerprint from an administrator's
// authorized_keys. The last administrator key is kept while password
// logins are off: removing it would lock everyone out.
func RemoveKey(ctx context.Context, env *host.Env, user, fp string) error {
	a, err := findAdmin(env, user)
	if err != nil {
		return err
	}
	found := false
	for _, k := range a.Keys {
		found = found || k.Fingerprint == fp
	}
	if !found {
		return actions.NotFound("no key " + fp + " for " + user)
	}
	if cfg, err := SSHEffective(ctx, env); err == nil && cfg["passwordauthentication"] == "no" {
		total := 0
		for _, ad := range Admins(env) {
			if ad.User == "root" && cfg["permitrootlogin"] == "no" {
				continue
			}
			total += len(ad.Keys)
		}
		if total <= 1 {
			return actions.Conflict("This is the last administrator key and password logins are off: removing it would lock everyone out. Add another key or turn password logins on first.")
		}
	}
	set, file, err := keysWriter(env, a)
	if err != nil {
		return actions.Failed("The key was not removed.", err.Error())
	}
	cur, err := env.Root.ReadFile(file)
	if err != nil {
		return actions.Failed("The key was not removed.", err.Error())
	}
	var keep []string
	for _, l := range strings.Split(strings.TrimRight(string(cur), "\n"), "\n") {
		if k, ok := parseAuthorizedLine(strings.TrimSpace(l)); ok && k.Fingerprint == fp {
			continue
		}
		keep = append(keep, l)
	}
	body := strings.Join(keep, "\n")
	if body != "" {
		body += "\n"
	}
	set.Write(file, []byte(body), 0o600, agentfs.Owner{UID: a.uid, GID: a.gid})
	if _, err := set.Apply(); err != nil {
		return actions.Failed("The key was not removed.", err.Error())
	}
	return nil
}
