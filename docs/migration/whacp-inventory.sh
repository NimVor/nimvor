#!/usr/bin/env bash
# Read-only inventory of a server that still runs NimVor under its former
# name (WhaCP). It changes nothing: it lists what the migration has to deal
# with (docs/migration/WHACP-TO-NIMVOR.md). Run as root and keep the output.
#
#   sudo bash whacp-inventory.sh | tee /root/whacp-inventory.txt
set -uo pipefail
[[ $EUID -eq 0 ]] || { echo "run as root" >&2; exit 1; }
sec() { printf '\n== %s\n' "$*"; }
lsx() { for p in "$@"; do compgen -G "$p" >/dev/null && ls -ld $p; done 2>/dev/null; }

sec "Version and OS"
cat /opt/whacp/panel/VERSION 2>/dev/null || echo "no /opt/whacp/panel/VERSION"
. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME"
sec "Services"
systemctl list-unit-files 'whacp*' --no-legend 2>/dev/null
systemctl list-units 'whacp*' --no-legend --all 2>/dev/null
sec "Users and groups"
getent passwd whacp; getent group | grep -i whacp
sec "Program, configuration and data folders"
lsx /opt/whacp /etc/whacp /etc/whacp-* /var/lib/whacp /var/lib/whacp-* /var/log/whacp /var/log/whacp-* /var/backups/whacp /run/whacp /usr/local/lib/whacp /usr/local/bin/whacp-* /usr/local/sbin/whacp-*
sec "Source checkout(s)"
cat /etc/whacp/source.json 2>/dev/null; echo
for d in /root/WhaCP /home/*/WhaCP /usr/local/src/*; do [[ -d $d/.git ]] && { echo "$d: $(git -C "$d" remote get-url origin 2>/dev/null) @ $(git -C "$d" rev-parse --short HEAD 2>/dev/null)"; }; done
sec "Host files the agent wrote (names containing whacp)"
find /etc /usr/local /var/lib /var/www /home -xdev \( -name '*whacp*' -o -name '.whacp*' \) -not -path '/home/*/.cache/*' 2>/dev/null | head -300
sec "Files that mention whacp inside"
grep -rIl -i whacp /etc /usr/local/sbin /usr/local/bin 2>/dev/null | head -100
sec "Cron markers"
grep -l "whacp-run\|whacp" /var/spool/cron/crontabs/* /etc/cron.d/* /etc/cron.deny 2>/dev/null
sec "Panel database summary"
DB=$(ls /var/lib/whacp/*.sqlite 2>/dev/null | head -1)
if [[ -n $DB ]] && command -v sqlite3 >/dev/null; then
    for t in users websites databases mail_domains mailboxes dns_zones ftp_accounts backups; do
        printf '%-14s %s\n' "$t" "$(sqlite3 "$DB" "SELECT COUNT(*) FROM $t" 2>/dev/null || echo -)"
    done
else
    echo "(sqlite3 not installed or no database found: $DB)"
fi
sec "Websites served"
ls /etc/nginx/sites-enabled /etc/nginx/conf.d 2>/dev/null
sec "nftables tables"
nft list tables 2>/dev/null | grep -i whacp
sec "Done"
