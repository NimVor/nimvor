#!/usr/bin/env bash
#
# The one anonymous report NimVor's installer sends, so the website can say how
# many servers run NimVor (https://nimvor.com/resources/stats).
#
#   installer/telemetry.sh install|upgrade [CONF_DIR]
#
# It sends a random identifier (made here, kept in CONF_DIR/install-id, not derived
# from anything on the server), the NimVor version, the operating system's name and
# version and the processor type. Nothing else: no host name, address, user name or
# setting. It never fails the installer and waits at most five seconds.
#
# Turn it off with --no-telemetry (install.sh), NIMVOR_TELEMETRY=0, or by creating
# CONF_DIR/no-telemetry. It is also off in continuous integration and when the
# installer's locations are overridden (the test suites do that).

set -u

event="${1:-install}"
conf="${2:-/etc/nimvor}"
src="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
url="https://nimvor.com/api/v1/installs"

[[ $event == install || $event == upgrade ]] || exit 0
[[ ${NIMVOR_TELEMETRY:-1} == 0 ]] && exit 0
[[ -e $conf/no-telemetry ]] && exit 0
[[ -n ${CI:-}${GITHUB_ACTIONS:-} ]] && exit 0
[[ -n ${NIMVOR_PREFIX:-}${NIMVOR_CONF_DIR:-}${NIMVOR_ALLOW_LOCAL_SOURCE:-} ]] && exit 0
command -v curl >/dev/null 2>&1 || exit 0
[[ -d $conf ]] || exit 0

idfile="$conf/install-id"
if [[ ! -s $idfile ]]; then
    id="$(head -c 16 /dev/urandom 2>/dev/null | od -An -tx1 | tr -d ' \n')"
    [[ $id =~ ^[a-f0-9]{32}$ ]] || exit 0
    printf '%s\n' "$id" >"$idfile" 2>/dev/null || exit 0
    chmod 0644 "$idfile" 2>/dev/null || true
fi
id="$(tr -d ' \n' <"$idfile")"
[[ $id =~ ^[a-f0-9]{32}$ ]] || exit 0

version="$(tr -d ' \n' <"$src/VERSION" 2>/dev/null)"
[[ $version =~ ^[0-9]{1,3}(\.[0-9]{1,3}){1,2}(-[0-9A-Za-z.]{1,12})?$ ]] || exit 0

os=other
os_version=0
osr="${NIMVOR_TELEMETRY_OS_RELEASE:-/etc/os-release}"
if [[ -r $osr ]]; then
    ID=""; VERSION_ID=""
    # shellcheck disable=SC1090
    . "$osr"
    case "$ID" in
        ubuntu|debian) os="$ID"; os_version="${VERSION_ID%.*}.${VERSION_ID#*.}" ;;
        almalinux|rocky|rhel) os="$ID"; os_version="${VERSION_ID%%.*}" ;;
    esac
    [[ $os_version =~ ^[0-9]{1,2}(\.[0-9]{1,2})?$ ]] || { os=other; os_version=0; }
fi
case "$(uname -m 2>/dev/null)" in
    x86_64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) arch=other ;;
esac

json="{\"id\":\"$id\",\"event\":\"$event\",\"version\":\"$version\",\"os\":\"$os\",\"os_version\":\"$os_version\",\"arch\":\"$arch\"}"
printf '  Sending one anonymous %s report (NimVor %s, %s %s, %s; no address or name). Turn it off with --no-telemetry or NIMVOR_TELEMETRY=0.\n' "$event" "$version" "$os" "$os_version" "$arch"
curl -fsS --max-time 5 --connect-timeout 3 --proto '=https' -H 'Content-Type: application/json' -d "$json" "$url" >/dev/null 2>&1 || true
exit 0
