#!/usr/bin/env bash
#
# Removes WhaCP's own files and services. It never touches the websites,
# databases or services WhaCP was observing.
#
#   sudo ./installer/uninstall.sh            remove program files and services, keep data
#   sudo ./installer/uninstall.sh --purge    also remove /etc/whacp, /var/lib/whacp, /var/log/whacp and the whacp user

set -Eeuo pipefail

PURGE=0
[[ ${1:-} == --purge ]] && PURGE=1
[[ $EUID -eq 0 ]] || { echo "run as root" >&2; exit 1; }

read -r -p "Remove WhaCP from this server$([[ $PURGE -eq 1 ]] && echo ' including its data and configuration')? [y/N] " reply
[[ $reply =~ ^[Yy]$ ]] || { echo "aborted"; exit 1; }

systemctl disable --now whacp-scheduler.timer 2>/dev/null || true
rm -f /etc/systemd/system/whacp-scheduler.timer
for svc in whacp-scheduler whacp-panel whacp-agent; do
    systemctl disable --now "$svc.service" 2>/dev/null || true
    rm -f "/etc/systemd/system/$svc.service"
done
systemctl daemon-reload
rm -f /usr/local/bin/whacp-agent
rm -rf /opt/whacp
echo "Program files and services removed."

if [[ $PURGE -eq 1 ]]; then
    rm -rf /etc/whacp /var/lib/whacp /var/log/whacp
    if id whacp >/dev/null 2>&1; then userdel whacp; fi
    if getent group whacp >/dev/null; then groupdel whacp 2>/dev/null || true; fi
    echo "Configuration, data and the whacp user removed."
else
    echo "Kept /etc/whacp, /var/lib/whacp and /var/log/whacp (use --purge to remove)."
fi
