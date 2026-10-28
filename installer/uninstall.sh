#!/usr/bin/env bash
#
# Removes NimVor's own files and services. It never touches the websites,
# databases or services NimVor was observing.
#
#   sudo ./installer/uninstall.sh            remove program files and services, keep data
#   sudo ./installer/uninstall.sh --purge    also remove /etc/nimvor, /var/lib/nimvor, /var/log/nimvor and the nimvor user
#
# Websites, databases, certificates and accounts created through NimVor keep
# running; so do backups (/var/backups/nimvor), the agent state with the
# backup key (/var/lib/nimvor-agent) and the launchers of background workers
# (/var/lib/nimvor-workers), which are only listed, never deleted.

set -Eeuo pipefail

PURGE=0
[[ ${1:-} == --purge ]] && PURGE=1
[[ $EUID -eq 0 ]] || { echo "run as root" >&2; exit 1; }

read -r -p "Remove NimVor from this server$([[ $PURGE -eq 1 ]] && echo ' including its data and configuration')? [y/N] " reply
[[ $reply =~ ^[Yy]$ ]] || { echo "aborted"; exit 1; }

systemctl disable --now nimvor-scheduler.timer 2>/dev/null || true
rm -f /etc/systemd/system/nimvor-scheduler.timer
for svc in nimvor-scheduler nimvor-panel nimvor-agent; do
    systemctl disable --now "$svc.service" 2>/dev/null || true
    rm -f "/etc/systemd/system/$svc.service"
done
# Greylisting runs this package's binary: Postfix stops asking it first.
if [[ -e /etc/systemd/system/nimvor-greylist.service ]]; then
    if command -v postconf >/dev/null; then
        restr=$(postconf -h smtpd_recipient_restrictions 2>/dev/null || true)
        if [[ $restr == *unix:private/nimvor-greylist* ]]; then
            restr=$(sed -E 's/,? *check_policy_service \{ unix:private\/nimvor-greylist[^}]*\}//' <<<"$restr")
            postconf -e "smtpd_recipient_restrictions=$restr" && postfix reload 2>/dev/null || true
        fi
    fi
    systemctl disable --now nimvor-greylist.service nimvor-greylist.socket 2>/dev/null || true
    rm -f /etc/systemd/system/nimvor-greylist.service /etc/systemd/system/nimvor-greylist.socket
fi
# NimVor's SELinux module (systemd sockets in Postfix's private folder).
if command -v semodule >/dev/null && semodule -l 2>/dev/null | grep -q '^nimvor_postfix_sockets\b'; then
    semodule -r nimvor_postfix_sockets || true
fi
# NimVor's nftables tables (firewall protections, SMTP restrictions,
# outgoing limits of website accounts, brute force blocks): unloaded with
# their units, so nothing NimVor added keeps dropping traffic.
for svc in nimvor-flood nimvor-smtp-restrict nimvor-egress; do
    systemctl disable --now "$svc.service" 2>/dev/null || true
    rm -f "/etc/systemd/system/$svc.service"
done
if command -v nft >/dev/null; then
    for t in nimvor_flood nimvor_smtp nimvor_egress nimvor_hulk; do
        nft delete table inet "$t" 2>/dev/null || true
    done
fi
# Drop-ins (write mode, remote access) must go too: a later read-only
# install would otherwise inherit the lifted sandbox.
rm -rf /etc/systemd/system/nimvor-agent.service.d
systemctl daemon-reload
# The panel's HTTPS front end (install.sh --public-https). Its firewall rule
# is left for you to remove (ufw delete allow PORT/tcp, or firewall-cmd).
panel_site=0
for f in /etc/nginx/sites-enabled/nimvor-panel.conf /etc/nginx/sites-available/nimvor-panel.conf \
         /etc/nginx/conf.d/nimvor-panel.conf /etc/nginx/nimvor-available/nimvor-panel.conf; do
    [[ -e $f || -L $f ]] && { rm -f "$f"; panel_site=1; }
done
rm -f /etc/ssl/certs/nimvor-panel.crt /etc/ssl/private/nimvor-panel.key /etc/pki/tls/certs/nimvor-panel.crt /etc/pki/tls/private/nimvor-panel.key
if [[ $panel_site -eq 1 ]] && command -v nginx >/dev/null && nginx -t >/dev/null 2>&1; then
    systemctl reload nginx 2>/dev/null || true
    echo "Removed the panel's HTTPS site from Nginx."
fi
rm -f /usr/local/bin/nimvor-agent /usr/local/bin/nimvor
# FFmpeg limited to the nimvor-ffmpeg group: back to the package's own modes.
if getent group nimvor-ffmpeg >/dev/null; then
    for f in /usr/bin/ffmpeg /usr/bin/ffprobe; do
        if command -v dpkg-statoverride >/dev/null; then dpkg-statoverride --remove "$f" 2>/dev/null || true; fi
        if [[ -f $f && ! -L $f ]]; then chown root:root "$f"; chmod 0755 "$f"; fi
    done
    groupdel nimvor-ffmpeg 2>/dev/null || true
fi
# Jailed shells: accounts still using the jail's login shell get nologin.
if [[ -e /usr/local/sbin/nimvor-jailsh ]]; then
    for u in $(awk -F: '$7 == "/usr/local/sbin/nimvor-jailsh" {print $1}' /etc/passwd); do usermod --shell /usr/sbin/nologin -- "$u" || true; done
fi
rm -f /usr/local/sbin/nimvor-jailsh /usr/local/lib/nimvor/bwrap /usr/local/lib/nimvor/wp-cli.phar
if [[ -e /etc/apparmor.d/nimvor-bwrap ]]; then
    apparmor_parser -R /etc/apparmor.d/nimvor-bwrap 2>/dev/null || true
    rm -f /etc/apparmor.d/nimvor-bwrap
fi
rm -rf /opt/nimvor
echo "Program files and services removed."

if [[ $PURGE -eq 1 ]]; then
    rm -rf /etc/nimvor /var/lib/nimvor /var/log/nimvor
    if id nimvor >/dev/null 2>&1; then userdel nimvor; fi
    if getent group nimvor >/dev/null; then groupdel nimvor 2>/dev/null || true; fi
    echo "Configuration, data and the nimvor user removed."
else
    echo "Kept /etc/nimvor, /var/lib/nimvor and /var/log/nimvor (use --purge to remove)."
fi
for d in /var/backups/nimvor /var/lib/nimvor-agent; do
    [[ -d $d ]] && echo "Kept $d (backups / backup key and change history). Remove it yourself when no longer needed."
done
[[ -d /var/lib/nimvor-workers ]] && echo "Kept /var/lib/nimvor-workers (the background workers of websites keep running and start from it)."
exit 0
