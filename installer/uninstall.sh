#!/usr/bin/env bash
#
# Removes NimVor's own files and services. It never touches the websites,
# databases or services NimVor was observing.
#
#   sudo ./installer/uninstall.sh            remove program files and services, keep data
#   sudo ./installer/uninstall.sh --purge    also remove /etc/whacp, /var/lib/whacp, /var/log/whacp and the whacp user
#
# Websites, databases, certificates and accounts created through NimVor keep
# running; so do backups (/var/backups/whacp), the agent state with the
# backup key (/var/lib/whacp-agent) and the launchers of background workers
# (/var/lib/whacp-workers), which are only listed, never deleted.

set -Eeuo pipefail

PURGE=0
[[ ${1:-} == --purge ]] && PURGE=1
[[ $EUID -eq 0 ]] || { echo "run as root" >&2; exit 1; }

read -r -p "Remove NimVor from this server$([[ $PURGE -eq 1 ]] && echo ' including its data and configuration')? [y/N] " reply
[[ $reply =~ ^[Yy]$ ]] || { echo "aborted"; exit 1; }

systemctl disable --now whacp-scheduler.timer 2>/dev/null || true
rm -f /etc/systemd/system/whacp-scheduler.timer
for svc in whacp-scheduler whacp-panel whacp-agent; do
    systemctl disable --now "$svc.service" 2>/dev/null || true
    rm -f "/etc/systemd/system/$svc.service"
done
# Greylisting runs this package's binary: Postfix stops asking it first.
if [[ -e /etc/systemd/system/whacp-greylist.service ]]; then
    if command -v postconf >/dev/null; then
        restr=$(postconf -h smtpd_recipient_restrictions 2>/dev/null || true)
        if [[ $restr == *unix:private/whacp-greylist* ]]; then
            restr=$(sed -E 's/,? *check_policy_service \{ unix:private\/whacp-greylist[^}]*\}//' <<<"$restr")
            postconf -e "smtpd_recipient_restrictions=$restr" && postfix reload 2>/dev/null || true
        fi
    fi
    systemctl disable --now whacp-greylist.service whacp-greylist.socket 2>/dev/null || true
    rm -f /etc/systemd/system/whacp-greylist.service /etc/systemd/system/whacp-greylist.socket
fi
# NimVor's SELinux module (systemd sockets in Postfix's private folder).
if command -v semodule >/dev/null && semodule -l 2>/dev/null | grep -q '^whacp_postfix_sockets\b'; then
    semodule -r whacp_postfix_sockets || true
fi
# NimVor's nftables tables (firewall protections, SMTP restrictions,
# outgoing limits of website accounts, brute force blocks): unloaded with
# their units, so nothing NimVor added keeps dropping traffic.
for svc in whacp-flood whacp-smtp-restrict whacp-egress; do
    systemctl disable --now "$svc.service" 2>/dev/null || true
    rm -f "/etc/systemd/system/$svc.service"
done
if command -v nft >/dev/null; then
    for t in whacp_flood whacp_smtp whacp_egress whacp_hulk; do
        nft delete table inet "$t" 2>/dev/null || true
    done
fi
# Drop-ins (write mode, remote access) must go too: a later read-only
# install would otherwise inherit the lifted sandbox.
rm -rf /etc/systemd/system/whacp-agent.service.d
systemctl daemon-reload
# The panel's HTTPS front end (install.sh --public-https). Its firewall rule
# is left for you to remove (ufw delete allow PORT/tcp, or firewall-cmd).
panel_site=0
for f in /etc/nginx/sites-enabled/whacp-panel.conf /etc/nginx/sites-available/whacp-panel.conf \
         /etc/nginx/conf.d/whacp-panel.conf /etc/nginx/whacp-available/whacp-panel.conf; do
    [[ -e $f || -L $f ]] && { rm -f "$f"; panel_site=1; }
done
rm -f /etc/ssl/certs/whacp-panel.crt /etc/ssl/private/whacp-panel.key /etc/pki/tls/certs/whacp-panel.crt /etc/pki/tls/private/whacp-panel.key
if [[ $panel_site -eq 1 ]] && command -v nginx >/dev/null && nginx -t >/dev/null 2>&1; then
    systemctl reload nginx 2>/dev/null || true
    echo "Removed the panel's HTTPS site from Nginx."
fi
rm -f /usr/local/bin/whacp-agent
# FFmpeg limited to the whacp-ffmpeg group: back to the package's own modes.
if getent group whacp-ffmpeg >/dev/null; then
    for f in /usr/bin/ffmpeg /usr/bin/ffprobe; do
        if command -v dpkg-statoverride >/dev/null; then dpkg-statoverride --remove "$f" 2>/dev/null || true; fi
        if [[ -f $f && ! -L $f ]]; then chown root:root "$f"; chmod 0755 "$f"; fi
    done
    groupdel whacp-ffmpeg 2>/dev/null || true
fi
# Jailed shells: accounts still using the jail's login shell get nologin.
if [[ -e /usr/local/sbin/whacp-jailsh ]]; then
    for u in $(awk -F: '$7 == "/usr/local/sbin/whacp-jailsh" {print $1}' /etc/passwd); do usermod --shell /usr/sbin/nologin -- "$u" || true; done
fi
rm -f /usr/local/sbin/whacp-jailsh /usr/local/lib/whacp/bwrap /usr/local/lib/whacp/wp-cli.phar
if [[ -e /etc/apparmor.d/whacp-bwrap ]]; then
    apparmor_parser -R /etc/apparmor.d/whacp-bwrap 2>/dev/null || true
    rm -f /etc/apparmor.d/whacp-bwrap
fi
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
for d in /var/backups/whacp /var/lib/whacp-agent; do
    [[ -d $d ]] && echo "Kept $d (backups / backup key and change history). Remove it yourself when no longer needed."
done
[[ -d /var/lib/whacp-workers ]] && echo "Kept /var/lib/whacp-workers (the background workers of websites keep running and start from it)."
exit 0
