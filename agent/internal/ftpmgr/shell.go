package ftpmgr

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/changeset"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Shell access for website accounts, on top of their SSH login (the SFTP
// settings: keys and password, root-owned key files). Three modes:
//
//   - disabled (default): SFTP only, chrooted, no shell (nologin);
//   - jailed: the login shell is JailShell, which runs every session and
//     command (SFTP too, through ForceCommand) in a bubblewrap sandbox:
//     /usr and a short list of /etc read-only, the account's own website
//     folder writable, private /tmp, /proc, IPC and hostname, no other
//     accounts' files, no_new_privs and no capabilities;
//   - normal: /bin/bash on the host, as the account (administrators only
//     choose it; the panel says what it exposes).
//
// A mode other than disabled takes effect only while SSH/SFTP login is on
// for the account, so a login shell never exists without the Match block
// that pins its key file to root-owned SFTPKeysDir.
const (
	ShellDisabled = "disabled"
	ShellJailed   = "jailed"
	ShellNormal   = "normal"

	JailShell   = "/usr/local/sbin/nimvor-jailsh"
	NormalShell = "/bin/bash"
	NoLogin     = "/usr/sbin/nologin"

	// Where AppArmor restricts unprivileged user namespaces (Ubuntu 23.10
	// and later), the sandbox runs from this copy of bubblewrap, which only
	// members of JailGroup can execute and which the installer's AppArmor
	// profile (nimvor-bwrap) allows to create user namespaces. Other
	// accounts keep the restriction.
	JailBwrap   = "/usr/local/lib/nimvor/bwrap"
	JailGroup   = "nimvor-jail"
	jailProfile = "nimvor-bwrap"
	systemBwrap = "/usr/bin/bwrap"
)

// shellMode is the mode in effect for an account.
func shellMode(st sftpState, user string) string {
	if _, on := st.Users[user]; !on {
		return ShellDisabled
	}
	if m := st.Shells[user]; m == ShellJailed || m == ShellNormal {
		return m
	}
	return ShellDisabled
}

func loginShell(mode string) string {
	switch mode {
	case ShellJailed:
		return JailShell
	case ShellNormal:
		return NormalShell
	}
	return NoLogin
}

// usernsRestricted reports whether AppArmor keeps unprivileged processes
// from creating user namespaces.
func usernsRestricted(env *host.Env) bool {
	b, err := env.Root.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns")
	return err == nil && strings.TrimSpace(string(b)) == "1"
}

func profileLoaded(env *host.Env) bool {
	b, err := env.Root.ReadFile("/sys/kernel/security/apparmor/profiles")
	return err == nil && bytes.Contains(b, []byte(jailProfile+" "))
}

// jailProblem says why jailed shells cannot work here, or "".
func jailProblem(env *host.Env) string {
	if !env.Root.Exists(systemBwrap) {
		return "Bubblewrap is not installed. Install it under Packages (bubblewrap), then try again."
	}
	if usernsRestricted(env) && !profileLoaded(env) {
		return "AppArmor restricts user namespaces on this server and the nimvor-bwrap profile is not loaded. Run the installer again (install.sh --reinstall), which installs it."
	}
	return ""
}

func groupGID(env *host.Env, name string) (int, bool) {
	b, err := env.Root.ReadFile("/etc/group")
	if err != nil {
		return 0, false
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) >= 3 && f[0] == name {
			gid, err := strconv.Atoi(f[2])
			return gid, err == nil
		}
	}
	return 0, false
}

// prepareJail adds the jail's files to the changeset: the login shell and,
// where user namespaces are restricted, the group-only bubblewrap copy
// (refreshed from the system's, so package updates reach it).
func prepareJail(ctx context.Context, env *host.Env, set *changeset.Set) error {
	if p := jailProblem(env); p != "" {
		return actions.Conflict(p)
	}
	set.Write(JailShell, []byte(JailScript(env.Cfg.SitesRoot)), 0o755, agentfs.RootOwner)
	if _, ok := groupGID(env, JailGroup); !ok {
		if out, err := env.Runner.Run(ctx, "groupadd", "--system", "--", JailGroup); err != nil {
			return actions.Failed("The "+JailGroup+" group could not be created.", host.Output(out))
		}
	}
	if usernsRestricted(env) {
		gid, ok := groupGID(env, JailGroup)
		if !ok {
			return actions.Failed("The "+JailGroup+" group was not found after creating it.", "")
		}
		bin, err := env.Root.ReadFile(systemBwrap)
		if err != nil {
			return actions.Failed("Bubblewrap could not be read.", err.Error())
		}
		set.Mkdir("/usr/local/lib/nimvor", 0o755, agentfs.RootOwner)
		set.Write(JailBwrap, bin, 0o750, agentfs.Owner{UID: 0, GID: gid})
	}
	return nil
}

// shq quotes s for sh.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// JailScript is the login shell of jailed accounts.
func JailScript(sitesRoot string) string {
	return strings.NewReplacer("@SITES_ROOT@", shq(sitesRoot), "@SELF@", shq(JailShell), "@COPY@", shq(JailBwrap), "@BWRAP@", shq(systemBwrap)).Replace(jailScript)
}

// Every path the sandbox shows from /etc (read-only, when present). No
// account data, service configuration or secrets.
var jailEtc = []string{
	"alternatives", "ld.so.cache", "ld.so.conf", "ld.so.conf.d", "localtime", "timezone",
	"resolv.conf", "hosts", "host.conf", "nsswitch.conf", "gai.conf", "services", "protocols",
	"ssl", "ca-certificates", "ca-certificates.conf", "pki", "crypto-policies",
	"mime.types", "profile", "profile.d", "bash.bashrc", "bashrc", "inputrc", "terminfo",
	"php", "opt/remi", "mysql/my.cnf", "mysql/conf.d", "mysql/mariadb.cnf", "mysql/mariadb.conf.d",
	"my.cnf", "my.cnf.d", "gitconfig", "ssh/ssh_config", "ssh/ssh_config.d", "wgetrc",
}

func init() {
	var b strings.Builder
	for _, p := range jailEtc {
		fmt.Fprintf(&b, " \\\n    --ro-bind-try /etc/%s /etc/%s", p, p)
	}
	jailScript = strings.Replace(jailScript, "@ETC@", b.String(), 1)
}

var jailScript = `#!/bin/sh
# Managed by NimVor: the login shell of website accounts with a jailed shell.
# Every session and command (SFTP too) runs in a bubblewrap sandbox: /usr
# and a few files from /etc read-only, only the account's own website
# folder writable, private /tmp, processes, IPC and hostname, no other
# accounts, no privileges (no_new_privs, no capabilities). Do not edit:
# changes are overwritten.
set -u
PATH=/usr/bin:/bin
export PATH
umask 027
SITES_ROOT=@SITES_ROOT@
SELF=@SELF@

cmd=
mode=login
if [ "$#" -ge 2 ] && [ "$1" = "-c" ]; then
    mode=command
    cmd=$2
    # sshd's ForceCommand runs "<shell> -c <this script>"; the client's
    # request is in SSH_ORIGINAL_COMMAND (none: an interactive login).
    if [ "$cmd" = "$SELF" ]; then
        cmd=${SSH_ORIGINAL_COMMAND:-}
        [ -n "$cmd" ] || mode=login
    fi
elif [ "$#" -ne 0 ]; then
    echo "nimvor-jailsh: unsupported arguments" >&2
    exit 1
fi

uid=$(id -u) && gid=$(id -g) && user=$(id -un) && group=$(id -gn) || exit 1
if [ "$uid" -lt 1000 ]; then
    echo "nimvor-jailsh: not for system accounts" >&2
    exit 1
fi
home=$(getent passwd "$user" | cut -d: -f6)
name=${home#"$SITES_ROOT"/}
case $name in
    "$home"|''|.|..|*/*|*[!A-Za-z0-9._-]*)
        echo "nimvor-jailsh: the account has no website folder" >&2
        exit 1 ;;
esac
[ -d "$home" ] || { echo "nimvor-jailsh: $home does not exist" >&2; exit 1; }

# SFTP runs the server inside the sandbox, whatever sshd's Subsystem is.
case $cmd in
    internal-sftp|internal-sftp\ *|*/sftp-server|*/sftp-server\ *)
        mode=sftp
        sftp=
        for s in /usr/lib/openssh/sftp-server /usr/libexec/openssh/sftp-server /usr/lib/ssh/sftp-server; do
            if [ -x "$s" ]; then sftp=$s; break; fi
        done
        [ -n "$sftp" ] || { echo "nimvor-jailsh: no sftp-server" >&2; exit 1; } ;;
esac

bwrap=@COPY@
[ -x "$bwrap" ] || bwrap=@BWRAP@
term=${TERM:-dumb}
case $term in *[!A-Za-z0-9._+-]*|'') term=dumb ;; esac

set -- --unshare-all --share-net --die-with-parent \
    --ro-bind /usr /usr \
    --symlink usr/bin /bin --symlink usr/sbin /sbin --symlink usr/lib /lib --symlink usr/lib64 /lib64 \
    --proc /proc --dev /dev --tmpfs /tmp --tmpfs /var/tmp --tmpfs /run \
    --ro-bind-try /run/mysqld /run/mysqld \
    --ro-bind-try /var/lib/mysql/mysql.sock /var/lib/mysql/mysql.sock@ETC@ \
    --ro-bind-data 3 /etc/passwd --ro-bind-data 4 /etc/group \
    --bind "$home" "$home" --chdir "$home" \
    --clearenv --setenv HOME "$home" --setenv USER "$user" --setenv LOGNAME "$user" \
    --setenv SHELL /bin/bash --setenv PATH /usr/local/bin:/usr/bin:/bin \
    --setenv TERM "$term" --setenv LANG C.UTF-8
if "$bwrap" --help 2>&1 | grep -q -e --disable-userns; then
    set -- "$@" --unshare-user --disable-userns --assert-userns-disabled
fi
# Without a terminal, a new session keeps the sandbox off the caller's.
[ -t 0 ] || set -- "$@" --new-session

case $mode in
    login) set -- "$@" /bin/bash -l ;;
    command) set -- "$@" /bin/bash -c "$cmd" ;;
    sftp) set -- "$@" "$sftp" -u 0027 -d "$home" ;;
esac
exec "$bwrap" "$@" 3<<PASSWD 4<<GROUP
root:x:0:0:root:/root:/usr/sbin/nologin
$user:x:$uid:$gid::$home:/bin/bash
nobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin
PASSWD
root:x:0:
$group:x:$gid:
nogroup:x:65534:
GROUP
`

// ShellFor is the shell mode in effect for an account (ShellDisabled,
// ShellJailed or ShellNormal), for the web terminal, and the login shell
// program of that mode.
func ShellFor(env *host.Env, user string) (mode, program string) {
	mode = shellMode(loadState(env), user)
	return mode, loginShell(mode)
}

// JailReady says why a jailed shell cannot start here ("" when it can).
func JailReady(env *host.Env) string {
	if p := jailProblem(env); p != "" {
		return p
	}
	if !env.Root.Exists(JailShell) {
		return "The jailed shell is not installed yet; set the account's shell access again (Security → Shell access)."
	}
	return ""
}

// CredentialsChanged, when set (by the web terminal), is called with an
// account whose password was set or locked, whose shell access or SSH
// login changed, or that was suspended: its open sessions end.
var CredentialsChanged func(user string)

func credentialsChanged(user string) {
	if CredentialsChanged != nil && user != "" {
		CredentialsChanged(user)
	}
}
