package security

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func sshString(b []byte) []byte {
	out := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(out, uint32(len(b)))
	copy(out[4:], b)
	return out
}

func mpint(n *big.Int) []byte {
	b := n.Bytes()
	if len(b) > 0 && b[0]&0x80 != 0 {
		b = append([]byte{0}, b...)
	}
	return sshString(b)
}

func keyLine(typ string, parts ...[]byte) string {
	blob := sshString([]byte(typ))
	for _, p := range parts {
		blob = append(blob, p...)
	}
	return typ + " " + base64.StdEncoding.EncodeToString(blob)
}

func ed25519Line(t *testing.T, comment string) string {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	return keyLine("ssh-ed25519", sshString(pub)) + " " + comment
}

func rsaLine(t *testing.T, bits int) string {
	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}
	return keyLine("ssh-rsa", mpint(big.NewInt(int64(k.E))), mpint(k.N)) + " rsa@laptop"
}

func TestParsePublicKey(t *testing.T) {
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ecLine := keyLine("ecdsa-sha2-nistp256", sshString([]byte("nistp256")), sshString(elliptic.Marshal(elliptic.P256(), ec.X, ec.Y)))
	for _, good := range []string{ed25519Line(t, "admin@laptop"), rsaLine(t, 2048), ecLine, "  " + ed25519Line(t, "") + "  "} {
		k, err := ParsePublicKey(good)
		if err != nil {
			t.Errorf("%s: %v", good[:30], err)
			continue
		}
		if !strings.HasPrefix(k.Fingerprint, "SHA256:") || len(k.Fingerprint) != 50 {
			t.Errorf("fingerprint %q", k.Fingerprint)
		}
	}
	ed := ed25519Line(t, "x")
	f := strings.Fields(ed)
	for name, bad := range map[string]string{
		"rsa 1024":       rsaLine(t, 1024),
		"options":        `command="/bin/sh",from="*" ` + ed,
		"two lines":      ed + "\n" + ed,
		"dsa":            "ssh-dss AAAAB3NzaC1kc3MAAACBAP== x",
		"type mismatch":  "ssh-rsa " + f[1],
		"not base64":     "ssh-ed25519 !!!notbase64",
		"private key":    "-----BEGIN OPENSSH PRIVATE KEY-----",
		"empty":          "",
		"truncated":      "ssh-ed25519 " + base64.StdEncoding.EncodeToString(sshString([]byte("ssh-ed25519"))),
		"comment escape": "",
	} {
		if bad == "" && name != "empty" {
			continue
		}
		if _, err := ParsePublicKey(bad); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Control characters never reach the file.
	k, err := ParsePublicKey(f[0] + " " + f[1] + " ok\x1b[31mred")
	if err != nil || strings.ContainsRune(k.Comment, 0x1b) {
		t.Errorf("comment %q %v", k.Comment, err)
	}
}

func adminHost(t *testing.T) (*Service, string, *testhost.Host) {
	h, s := setup(t, "")
	base := h.Base
	os.WriteFile(filepath.Join(base, "etc/passwd"), []byte("root:x:0:0:root:/root:/bin/bash\nops:x:1000:1000::/home/ops:/bin/bash\nshop:x:1600:1600::/home/nimvor/shop.test:/usr/sbin/nologin\n"), 0o644)
	os.WriteFile(filepath.Join(base, "etc/group"), []byte("root:x:0:\nsudo:x:27:ops\nshop:x:1600:\n"), 0o644)
	os.MkdirAll(filepath.Join(base, "root"), 0o700)
	os.MkdirAll(filepath.Join(base, "home/ops"), 0o750)
	return s, base, h
}

func TestAdminKeys(t *testing.T) {
	s, base, _ := adminHost(t)
	env := s.env
	ctx := context.Background()
	admins := Admins(env)
	if len(admins) != 2 || admins[0].User != "root" || admins[1].User != "ops" || len(admins[1].Keys) != 0 {
		t.Fatalf("admins: %+v", admins)
	}
	key := ed25519Line(t, "ops@laptop")
	k, err := AddKey(ctx, env, "ops", key)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(base, "home/ops/.ssh/authorized_keys")
	b, _ := os.ReadFile(file)
	if string(b) != key+"\n" {
		t.Fatalf("authorized_keys:\n%q", b)
	}
	if st, _ := os.Stat(file); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	if st, _ := os.Stat(filepath.Dir(file)); st.Mode().Perm() != 0o700 {
		t.Errorf(".ssh mode %v", st.Mode())
	}
	if _, err := AddKey(ctx, env, "ops", key); err == nil {
		t.Error("the same key added twice")
	}
	// Website accounts and unknown users are not administrators.
	for _, u := range []string{"shop", "nobody", "../root"} {
		if _, err := AddKey(ctx, env, u, ed25519Line(t, "")); err == nil {
			t.Errorf("key added for %s", u)
		}
	}
	// Lines added by hand are kept and listed.
	os.WriteFile(file, append(b, []byte("# my note\n"+`from="10.0.0.1" `+ed25519Line(t, "restricted")+"\ngarbage line\n")...), 0o600)
	a := Admins(env)[1]
	if len(a.Keys) != 2 || !a.Keys[1].Options || a.Other != 1 {
		t.Fatalf("listed: %+v", a)
	}
	if err := RemoveKey(ctx, env, "ops", k.Fingerprint); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(file)
	if strings.Contains(string(b), "ops@laptop") || !strings.Contains(string(b), "# my note") || !strings.Contains(string(b), "garbage line") || !strings.Contains(string(b), "restricted") {
		t.Fatalf("after removal:\n%s", b)
	}
}

func TestLastKeyKeptWhilePasswordsOff(t *testing.T) {
	s, _, h := adminHost(t)
	env := s.env
	ctx := context.Background()
	k, err := AddKey(ctx, env, "root", ed25519Line(t, "root@laptop"))
	if err != nil {
		t.Fatal(err)
	}
	h.Runner.Outputs["sshd -T"] = "port 22\npermitrootlogin prohibit-password\npasswordauthentication no\n"
	err = RemoveKey(ctx, env, "root", k.Fingerprint)
	var ae *actions.Error
	if !errors.As(err, &ae) || !strings.Contains(err.Error(), "lock everyone out") {
		t.Fatalf("last key removed: %v", err)
	}
	if _, err := AddKey(ctx, env, "ops", ed25519Line(t, "ops")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveKey(ctx, env, "root", k.Fingerprint); err != nil {
		t.Fatalf("with another key: %v", err)
	}
}

// A ~/.ssh that is a link (to another account's folder or /etc) is never
// written through.
func TestKeysNotWrittenThroughLinks(t *testing.T) {
	s, base, _ := adminHost(t)
	os.MkdirAll(filepath.Join(base, "etc/elsewhere"), 0o755)
	os.Symlink("../../etc/elsewhere", filepath.Join(base, "home/ops/.ssh"))
	if _, err := AddKey(context.Background(), s.env, "ops", ed25519Line(t, "")); err == nil {
		t.Fatal("written through a linked ~/.ssh")
	}
	if _, err := os.Stat(filepath.Join(base, "etc/elsewhere/authorized_keys")); err == nil {
		t.Fatal("authorized_keys created outside the home")
	}
}
