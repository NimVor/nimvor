#!/usr/bin/env bash
# The short `nimvor` command runs the panel's tool as the panel account with
# the panel's configuration, maps the two aliases and refuses without an
# installation. Uses stand-ins for runuser and php: nothing is installed.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/prefix/panel/bin" "$T/bin"
: > "$T/prefix/panel/bin/nimvor"
printf '#!/bin/sh\nexit 0\n' > "$T/bin/php"
# runuser -u ACCOUNT -- env NIMVOR_CONFIG=... CMD...: record and stop.
printf '#!/bin/sh\necho "$@" > "%s/out"\n' "$T" > "$T/bin/runuser"
chmod +x "$T/bin/php" "$T/bin/runuser"
[[ $(id -u) -eq 0 ]] || { echo "run as root (the wrapper hands over with runuser)"; exit 0; }

run() { PATH="$T/bin:$PATH" NIMVOR_PREFIX="$T/prefix" NIMVOR_CONF=/etc/nimvor/panel.php NIMVOR_PANEL_UNIT=/nonexistent bash "$ROOT/installer/config/nimvor-cli" "$@"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

run user:password admin
grep -q -- '-u nimvor -- env NIMVOR_CONFIG=/etc/nimvor/panel.php .*/prefix/panel/bin/nimvor user:password admin' "$T/out" || fail "password command: $(cat "$T/out")"
run reset-password admin;  grep -q 'nimvor user:password admin' "$T/out" || fail "alias reset-password"
run reset-2fa admin;       grep -q 'nimvor user:2fa-reset admin' "$T/out" || fail "alias reset-2fa"
run;                       grep -q 'nimvor help$' "$T/out" || fail "no argument shows help"
rm -rf "$T/prefix/panel"
if run doctor 2>/dev/null; then fail "ran without an installation"; fi
echo "cli wrapper: ok"
