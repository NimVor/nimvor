#!/usr/bin/env bash
# Diagnostics (CI, as root, after install.sh --enable-writes): runs the
# agent's "command as a site account" exec inside a clone of the installed
# whacp-agent unit, then again with each sandbox property relaxed, to find
# which one refuses it.
set -uo pipefail
cd "$(dirname "$0")/probe"
[[ -f go.mod ]] || go mod init probe >/dev/null 2>&1
CGO_ENABLED=0 go build -o /usr/local/bin/whacp-probe . || exit 1
id whacpprobe >/dev/null 2>&1 || useradd -m -d /home/whacp/probe.test -s /usr/sbin/nologin -K UID_MIN=2000 whacpprobe
echo hello > /home/whacp/probe.test/index.html; chown -R whacpprobe: /home/whacp/probe.test

echo "--- running agent"
pid=$(systemctl show -p MainPID --value whacp-agent)
grep -E '^(Cap|NoNewPrivs|Seccomp)' "/proc/$pid/status"

sed -e 's#^ExecStart=.*#ExecStart=/usr/local/bin/whacp-probe whacpprobe#' -e '/^ExecStartPre=/d' -e 's/^Type=.*/Type=oneshot/' -e '/^Restart/d' \
    /etc/systemd/system/whacp-agent.service > /etc/systemd/system/whacp-probe.service
rm -rf /etc/systemd/system/whacp-probe.service.d
cp -r /etc/systemd/system/whacp-agent.service.d /etc/systemd/system/whacp-probe.service.d
run() {
    systemctl daemon-reload
    systemctl start whacp-probe.service >/dev/null 2>&1
    journalctl -u whacp-probe.service --no-pager -o cat -n 20 --since "-5s" | grep -E 'RESULT|^Cap|NoNewPrivs|Seccomp' | tr '\n' ' '
    echo
}
echo "--- exact clone"; run
for p in NoNewPrivileges=no RestrictNamespaces=no LockPersonality=no SystemCallArchitectures= RestrictAddressFamilies= SystemCallErrorNumber= PrivateTmp=no ProtectClock=no ProtectHostname=no ProtectKernelLogs=no ProtectControlGroups=no RestrictRealtime=no UMask=0022 Group= TasksMax=infinity MemoryMax=infinity Nice=0 RuntimeDirectory= 'CapabilityBoundingSet=~'; do
    printf '[Service]\n%s\n' "$p" > /etc/systemd/system/whacp-probe.service.d/99-probe.conf
    printf -- '--- with %s: ' "$p"; run
done
rm -rf /etc/systemd/system/whacp-probe.service /etc/systemd/system/whacp-probe.service.d; systemctl daemon-reload
userdel -r whacpprobe >/dev/null 2>&1 || true
