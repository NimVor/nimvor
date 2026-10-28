#!/usr/bin/env bash
# Diagnostics (CI, as root, after install.sh --enable-writes): runs the
# agent's "command as a site account" exec inside a clone of the installed
# nimvor-agent unit, then again with each sandbox property relaxed, to find
# which one refuses it.
set -uo pipefail
cd "$(dirname "$0")/probe"
[[ -f go.mod ]] || go mod init probe >/dev/null 2>&1
CGO_ENABLED=0 go build -o /usr/local/bin/nimvor-probe . || exit 1
id nimvorprobe >/dev/null 2>&1 || useradd -m -d /home/nimvor/probe.test -s /usr/sbin/nologin -K UID_MIN=2000 nimvorprobe
echo hello > /home/nimvor/probe.test/index.html; chown -R nimvorprobe: /home/nimvor/probe.test

echo "--- running agent"
pid=$(systemctl show -p MainPID --value nimvor-agent)
grep -E '^(Cap|NoNewPrivs|Seccomp)' "/proc/$pid/status"

sed -e 's#^ExecStart=.*#ExecStart=/usr/local/bin/nimvor-probe nimvorprobe#' -e '/^ExecStartPre=/d' -e 's/^Type=.*/Type=oneshot/' -e '/^Restart/d' \
    /etc/systemd/system/nimvor-agent.service > /etc/systemd/system/nimvor-probe.service
rm -rf /etc/systemd/system/nimvor-probe.service.d
cp -r /etc/systemd/system/nimvor-agent.service.d /etc/systemd/system/nimvor-probe.service.d
run() {
    systemctl daemon-reload
    systemctl start nimvor-probe.service >/dev/null 2>&1
    local inv; inv=$(systemctl show -p InvocationID --value nimvor-probe.service)
    journalctl "_SYSTEMD_INVOCATION_ID=$inv" --no-pager -o cat | grep -E 'RESULT|NoNewPrivs|Seccomp:|CapEff|CapPrm'
}
echo "--- exact clone"; run
for p in NoNewPrivileges=no 'AmbientCapabilities=CAP_SETUID CAP_SETGID' AmbientCapabilities=CAP_SETUID; do
    printf '[Service]\n%s\n' "$p" > /etc/systemd/system/nimvor-probe.service.d/99-probe.conf
    echo "--- with $p"; run
done
rm -rf /etc/systemd/system/nimvor-probe.service /etc/systemd/system/nimvor-probe.service.d; systemctl daemon-reload
userdel -r nimvorprobe >/dev/null 2>&1 || true
