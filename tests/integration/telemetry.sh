#!/usr/bin/env bash
# installer/telemetry.sh: what it sends, and every way it stays silent. A stub
# curl records the request, so nothing leaves the machine.
set -Eeuo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
t="$(mktemp -d)"
trap 'rm -rf "$t"' EXIT
mkdir -p "$t/bin" "$t/conf"
cat >"$t/bin/curl" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$CURL_LOG"
STUB
chmod +x "$t/bin/curl"
cat >"$t/os-release" <<'E2'
ID=almalinux
VERSION_ID="9.6"
E2
export CURL_LOG="$t/curl.log" PATH="$t/bin:$PATH" NIMVOR_TELEMETRY_OS_RELEASE="$t/os-release"
unset CI GITHUB_ACTIONS NIMVOR_TELEMETRY NIMVOR_PREFIX NIMVOR_CONF_DIR NIMVOR_ALLOW_LOCAL_SOURCE
ok() { printf '  ok  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*" >&2; exit 1; }
calls() { [[ -f $CURL_LOG ]] && wc -l <"$CURL_LOG" || echo 0; }

"$here/installer/telemetry.sh" install "$t/conf" >/dev/null
[[ $(calls) == 1 ]] || bad "no report was sent"
line="$(cat "$CURL_LOG")"
grep -q -- '--proto =https' <<<"$line" || bad "not restricted to https"
grep -q 'https://nimvor.com/api/v1/installs' <<<"$line" || bad "wrong address"
id="$(cat "$t/conf/install-id")"
[[ $id =~ ^[a-f0-9]{32}$ ]] || bad "identifier is not 32 hex characters: $id"
grep -q "\"id\":\"$id\"" <<<"$line" || bad "identifier not sent"
grep -q '"event":"install"' <<<"$line" || bad "event not sent"
grep -q "\"version\":\"$(tr -d ' \n' <"$here/VERSION")\"" <<<"$line" || bad "version not sent"
grep -q '"os":"almalinux","os_version":"9"' <<<"$line" || bad "system not reduced to the major version: $line"
grep -Eq '"arch":"(amd64|arm64|other)"' <<<"$line" || bad "arch missing"
grep -q "$(hostname)" <<<"$line" && bad "the host name was sent" || true
ok "one report with the expected fields and nothing else"

"$here/installer/telemetry.sh" upgrade "$t/conf" >/dev/null
[[ "$(cat "$t/conf/install-id")" == "$id" ]] || bad "identifier changed"
grep -q '"event":"upgrade"' "$CURL_LOG" || bad "upgrade event not sent"
ok "the same identifier is reused for an upgrade"

silent() {
    : >"$CURL_LOG"
    "$@" >/dev/null
    [[ $(calls) == 0 ]] || bad "sent a report although: $label"
    ok "silent: $label"
}
label="NIMVOR_TELEMETRY=0"; silent env NIMVOR_TELEMETRY=0 "$here/installer/telemetry.sh" install "$t/conf"
label="CI is set"; silent env CI=true "$here/installer/telemetry.sh" install "$t/conf"
label="test locations are overridden"; silent env NIMVOR_PREFIX=/tmp/x "$here/installer/telemetry.sh" install "$t/conf"
touch "$t/conf/no-telemetry"
label="the no-telemetry file exists"; silent "$here/installer/telemetry.sh" install "$t/conf"
rm "$t/conf/no-telemetry"
label="an unknown event"; silent "$here/installer/telemetry.sh" bogus "$t/conf"
label="no configuration folder"; silent "$here/installer/telemetry.sh" install "$t/missing"

# --no-telemetry in install.sh is a real option (help text lists it).
grep -q -- '--no-telemetry) NO_TELEMETRY=1' "$here/installer/install.sh" || bad "install.sh lacks --no-telemetry"
echo "telemetry: all checks passed"
