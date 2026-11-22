#!/usr/bin/env bash
#
# Telemetry is opt-in. This script sends NOTHING: it only records, in two small local files, what the
# administrator answered and (once they said yes) which install and upgrade events happened, so the panel can
# report them later if and only if telemetry is turned on in Settings -> Telemetry. What is collected and what
# never is: docs/telemetry/README.md.
#
#   installer/telemetry.sh consent CONF_DIR yes|no|ask [ASSUME_YES]
#       yes  writes CONF_DIR/telemetry-optin (the panel turns telemetry on at its next scheduler pass)
#       no   removes it
#       ask  keeps an earlier answer; else asks on the terminal (default no). Without a terminal, or with
#            --yes (ASSUME_YES=1), nobody is asked and the answer is no.
#   installer/telemetry.sh event CONF_DIR install|upgrade FROM_VERSION TO_VERSION true|false
#       appends one line to CONF_DIR/release-events.jsonl, only when the opt-in file exists.
#
# Never fails the installer.

set -u

cmd="${1:-}"
conf="${2:-/etc/nimvor}"
optin="$conf/telemetry-optin"
events="$conf/release-events.jsonl"

[[ -d $conf ]] || exit 0

case "$cmd" in
    consent)
        mode="${3:-ask}"
        assume_yes="${4:-0}"
        case "$mode" in
            yes) printf 'yes\n' >"$optin" 2>/dev/null && chmod 0644 "$optin" 2>/dev/null; exit 0 ;;
            no) rm -f "$optin" 2>/dev/null; exit 0 ;;
        esac
        [[ -e $optin ]] && exit 0                      # an earlier answer stays
        [[ $assume_yes == 1 ]] && exit 0               # scripts are never asked
        [[ -n ${CI:-}${GITHUB_ACTIONS:-}${NIMVOR_TESTING:-} ]] && exit 0
        { exec 3</dev/tty; } 2>/dev/null || exit 0
        cat >&2 <<'TXT'

Help improve NimVor? (optional, default: no)
NimVor can send the project a small anonymous report once a day: its version, the system type (for example
ubuntu 24.04, amd64), the size of the server rounded up, which components are installed, how many websites and
mailboxes exist (counts only) and a few yes/no health answers. Never an address, host name, domain, user,
e-mail or file. You can see exactly what would be sent, turn it off or delete it any time under
Settings -> Telemetry in the panel. Details: docs/telemetry/README.md
TXT
        if read -r -u 3 -p "Turn telemetry on? [y/N] " reply && [[ $reply =~ ^[Yy]$ ]]; then
            printf 'yes\n' >"$optin" 2>/dev/null && chmod 0644 "$optin" 2>/dev/null
        fi
        exec 3<&-
        exit 0
        ;;
    event)
        kind="${3:-}"
        from="${4:-}"
        to="${5:-}"
        ok="${6:-true}"
        [[ -e $optin ]] || exit 0
        [[ $kind == install || $kind == upgrade ]] || exit 0
        ver='^[0-9]{1,3}(\.[0-9]{1,3}){1,3}(-[0-9A-Za-z.]{1,12})?$'
        [[ $to =~ $ver ]] || exit 0
        [[ -z $from || $from =~ $ver ]] || from=""
        [[ $ok == true || $ok == false ]] || exit 0
        id="$(cat /proc/sys/kernel/random/uuid 2>/dev/null)"
        [[ $id =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]] || exit 0
        now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
        printf '{"event_id":"%s","kind":"%s","from_version":"%s","to_version":"%s","success":%s,"occurred_at":"%s"}\n' "$id" "$kind" "$from" "$to" "$ok" "$now" >>"$events" 2>/dev/null || exit 0
        chmod 0644 "$events" 2>/dev/null || true
        tail -n 20 "$events" >"$events.tmp" 2>/dev/null && mv "$events.tmp" "$events" 2>/dev/null
        exit 0
        ;;
esac
exit 0
