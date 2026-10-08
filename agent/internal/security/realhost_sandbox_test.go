//go:build realhost

// Real-host sandbox-escape probes (security items 90–91), the part that the
// kernel's discretionary access control enforces for every unprivileged
// website account regardless of systemd. Run as root on a disposable host:
//
//	go test -tags realhost ./internal/security -run RealHostSandboxEscape -v
//
// It makes two throwaway accounts (whacp-esc-a and whacp-esc-b), then runs a
// probe script as the real whacp-esc-a account that attempts the escape and
// discovery surfaces from the GA brief — shared /tmp and /var/tmp, another
// account's files, /etc/shadow, /proc of a root process, /sys and /dev
// writes, SUID/SGID discovery and use, and privilege gain via sudo/su — and
// asserts every one is denied, and that no SUID-root binary hands the
// account a root shell.
//
// The systemd parts of the sandbox (NoNewPrivileges, capability bounding,
// cgroup limits, ProtectSystem) are covered by the workers, terminal, cron
// and FPM real-host tests; this complements them with the DAC layer.
package security

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealHostSandboxEscape(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("runuser"); err != nil {
		t.Skip("no runuser")
	}
	for _, u := range []string{"whacp-esc-a", "whacp-esc-b"} {
		exec.Command("useradd", "-M", "-U", "-s", "/usr/sbin/nologin", u).Run()
		name := u
		t.Cleanup(func() { exec.Command("userdel", name).Run() })
	}

	// Account B's private file (0600, owned by B).
	bHome, err := os.MkdirTemp("/home", "whacp-esc-b-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(bHome) })
	os.Chmod(bHome, 0o711)
	bSecret := filepath.Join(bHome, "secret.txt")
	os.WriteFile(bSecret, []byte("SECRET-OF-B"), 0o600)
	exec.Command("chown", "-R", "whacp-esc-b:whacp-esc-b", bHome).Run()

	// A root-only file in shared /tmp and /var/tmp.
	tmpSecret := filepath.Join("/tmp", "whacp-esc-root-secret")
	varTmpSecret := filepath.Join("/var/tmp", "whacp-esc-root-secret")
	for _, p := range []string{tmpSecret, varTmpSecret} {
		os.WriteFile(p, []byte("SECRET-ROOT-TMP"), 0o600) // owned by root, 0600
		pp := p
		t.Cleanup(func() { os.Remove(pp) })
	}

	// The probe runs as whacp-esc-a. Each attempt prints
	// "OP <name> <ALLOWED|DENIED>"; ALLOWED means the escape worked (bad).
	probe := `
emit() { echo "OP $1 $2"; }
try_read() { if out=$(cat "$1" 2>/dev/null) && [ -n "$out" ]; then emit "$2" ALLOWED; else emit "$2" DENIED; fi; }

echo "WHOAMI $(id -un) $(id -u)"
try_read "` + bSecret + `" read_b_secret
try_read "` + tmpSecret + `" read_tmp_root
try_read "` + varTmpSecret + `" read_var_tmp_root
try_read /etc/shadow read_shadow
try_read /proc/1/environ read_pid1_environ

# Writing into protected pseudo-filesystems. Each attempt runs in a
# subshell so that a redirection failure (which aborts a dash script) only
# ends the subshell, not the whole probe.
if ( exec 2>/dev/null; echo 0 > /proc/sys/kernel/randomize_va_space ); then emit write_proc_sys ALLOWED; else emit write_proc_sys DENIED; fi
if ( exec 2>/dev/null; echo 1 > /sys/kernel/profiling ); then emit write_sys ALLOWED; else emit write_sys DENIED; fi
if ( exec 2>/dev/null; : > /dev/sda ); then emit write_dev_sda ALLOWED; else emit write_dev_sda DENIED; fi

# Privilege gain.
if sudo -n id -u 2>/dev/null | grep -qx 0; then emit sudo ALLOWED; else emit sudo DENIED; fi
if su -c 'id -u' root </dev/null 2>/dev/null | grep -qx 0; then emit su ALLOWED; else emit su DENIED; fi

# Mounting / namespaces (need privilege the account lacks).
if unshare -rm true 2>/dev/null; then emit unshare_mount ALLOWED; else emit unshare_mount DENIED; fi
if mount -t tmpfs none /mnt 2>/dev/null; then emit mount ALLOWED; umount /mnt 2>/dev/null; else emit mount DENIED; fi

# SUID/SGID discovery: find setuid-root binaries and try the ones an
# attacker would, to read a root-only file. None must succeed.
leaked=no
for b in $(find /usr/bin /bin /usr/sbin /sbin -xdev -perm -4000 -user root -type f 2>/dev/null); do
  base=$(basename "$b")
  case "$base" in
    # Known GTFOBins-style file readers, if present as SUID-root.
    cat|head|tail|more|less|cp|dd) if "$b" /etc/shadow 2>/dev/null | grep -q '^root:'; then leaked=yes; fi;;
  esac
done
emit suid_read_shadow $([ "$leaked" = yes ] && echo ALLOWED || echo DENIED)
`
	out, _ := exec.Command("runuser", "-u", "whacp-esc-a", "--", "sh", "-c", probe).CombinedOutput()
	s := string(out)
	t.Logf("sandbox-escape probe (as whacp-esc-a):\n%s", s)

	if !strings.Contains(s, "WHOAMI whacp-esc-a") {
		t.Fatalf("probe did not run as whacp-esc-a:\n%s", s)
	}
	if strings.Contains(s, "SECRET-OF-B") || strings.Contains(s, "SECRET-ROOT-TMP") || strings.Contains(s, "root:x:0:0") {
		t.Errorf("the probe leaked protected content:\n%s", s)
	}
	results := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[0] == "OP" {
			results[f[1]] = f[2]
		}
	}
	for _, op := range []string{
		"read_b_secret", "read_tmp_root", "read_var_tmp_root", "read_shadow", "read_pid1_environ",
		"write_proc_sys", "write_sys", "write_dev_sda", "sudo", "su", "mount", "suid_read_shadow",
	} {
		got, ok := results[op]
		if !ok {
			t.Errorf("%s: no result", op)
			continue
		}
		if got != "DENIED" {
			t.Errorf("%s = %s, must be DENIED (sandbox escape)", op, got)
		}
	}
	// unshare_mount: on some kernels an unprivileged user namespace is
	// allowed; that alone is not an escape (it maps to the same uid with no
	// new capabilities on the host). Record it without failing.
	if results["unshare_mount"] == "ALLOWED" {
		t.Logf("note: unprivileged user namespaces are enabled on this host (not an escape by itself)")
	}
}
