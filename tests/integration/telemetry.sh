#!/usr/bin/env bash
# installer/telemetry.sh: telemetry is opt-in. The script sends nothing at all (a stub curl would record it),
# asks nobody who cannot be asked, and only notes events after a yes.
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
export CURL_LOG="$t/curl.log" PATH="$t/bin:$PATH"
unset CI GITHUB_ACTIONS NIMVOR_TESTING NIMVOR_PREFIX NIMVOR_CONF_DIR NIMVOR_ALLOW_LOCAL_SOURCE
ok() { printf '  ok  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*" >&2; exit 1; }
tel="$here/installer/telemetry.sh"
conf="$t/conf"

# No answer, no event: nothing is written.
"$tel" event "$conf" install "" 1.9.0 true
[[ ! -e $conf/release-events.jsonl ]] || bad "an event was noted without a yes"
ok "no event is noted before a yes"

# Nobody is asked when there is no terminal or --yes was used; the answer is no.
"$tel" consent "$conf" ask 1 </dev/null
[[ ! -e $conf/telemetry-optin ]] || bad "--yes answered yes"
"$tel" consent "$conf" ask 0 </dev/null
[[ ! -e $conf/telemetry-optin ]] || bad "no terminal answered yes"
CI=true "$tel" consent "$conf" ask 0
[[ ! -e $conf/telemetry-optin ]] || bad "CI answered yes"
ok "scripts, --yes and CI are never asked and never opted in"

# Explicit choices.
"$tel" consent "$conf" yes
[[ "$(cat "$conf/telemetry-optin")" == "yes" ]] || bad "yes was not recorded"
"$tel" consent "$conf" ask 1 </dev/null
[[ -e $conf/telemetry-optin ]] || bad "an earlier yes was forgotten by a later silent run"
ok "an explicit yes is recorded and survives a later run without a question"

# Events: only well-formed, only after a yes, last 20 kept, no host data.
"$tel" event "$conf" upgrade 1.8.1 1.9.0 true
line="$(tail -n 1 "$conf/release-events.jsonl")"
grep -Eq '^\{"event_id":"[0-9a-f-]{36}","kind":"upgrade","from_version":"1\.8\.1","to_version":"1\.9\.0","success":true,"occurred_at":"[0-9TZ:-]+"\}$' <<<"$line" || bad "event line: $line"
grep -q "$(hostname)" <<<"$line" && bad "the host name is in the event" || true
"$tel" event "$conf" upgrade 1.8.1 'x; rm -rf /' true
"$tel" event "$conf" bogus 1.8.1 1.9.0 true
"$tel" event "$conf" upgrade 1.8.1 1.9.0 maybe
[[ $(wc -l <"$conf/release-events.jsonl") == 1 ]] || bad "a bad event was noted"
for i in $(seq 1 30); do "$tel" event "$conf" upgrade 1.8.1 1.9.0 true; done
[[ $(wc -l <"$conf/release-events.jsonl") == 20 ]] || bad "the file is not capped at 20 lines"
ok "events are validated, capped and contain only versions"

"$tel" consent "$conf" no
[[ ! -e $conf/telemetry-optin ]] || bad "no did not remove the opt-in"
ok "an explicit no removes the opt-in"

# It never sends anything and never fails.
[[ ! -e $CURL_LOG ]] || bad "something was sent: $(cat "$CURL_LOG")"
grep -q 'curl' "$tel" && bad "telemetry.sh mentions curl" || true
"$tel" consent "$t/missing" yes; "$tel" event "$t/missing" install "" 1.9.0 true; "$tel" bogus "$conf"
ok "nothing is sent, and a missing folder or an unknown command is harmless"

# The installers use it: an option each way, no hidden report.
grep -q -- '--telemetry' "$here/installer/install.sh" && grep -q -- '--no-telemetry' "$here/installer/install.sh" || bad "the installer has no option"
grep -q 'api/v1/installs' "$here/installer/install.sh" "$here/installer/upgrade.sh" "$tel" && bad "the old report address is still there" || true
ok "the installers have the option and no longer report anything"
echo "telemetry: all checks passed"
