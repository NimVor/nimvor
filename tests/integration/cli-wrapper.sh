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
[[ $(id -u) -eq 0 ]] || exec sudo -E "$0" "$@"

run() { PATH="$T/bin:$PATH" NIMVOR_PREFIX="$T/prefix" NIMVOR_CONF=/etc/nimvor/panel.php NIMVOR_PANEL_UNIT=/nonexistent bash "$ROOT/installer/config/nimvor-cli" "$@"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

run user:password admin
grep -q -- '-u nimvor -- env NIMVOR_CONFIG=/etc/nimvor/panel.php .*/prefix/panel/bin/nimvor user:password admin' "$T/out" || fail "password command: $(cat "$T/out")"
run reset-password admin;  grep -q 'nimvor user:password admin' "$T/out" || fail "alias reset-password"
run reset-2fa admin;       grep -q 'nimvor user:2fa-reset admin' "$T/out" || fail "alias reset-2fa"
run;                       grep -q 'nimvor help$' "$T/out" || fail "no argument shows help"
rm -rf "$T/prefix/panel"
if run doctor 2>/dev/null; then fail "ran without an installation"; fi

# ---- nimvor update: any older version to the newest release ------------------------
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t GIT_CONFIG_GLOBAL=/dev/null
mkdir -p "$T/up" && cd "$T/up"
git init -q --bare remote.git
git clone -q remote.git work 2>/dev/null
cd work
mkdir -p installer
printf '#!/bin/sh\necho "$@" > "%s/install-args"; cat "$(dirname "$0")/../VERSION" > "%s/install-version"\n' "$T" "$T" > installer/install.sh
chmod +x installer/install.sh
echo 1.8.1 > VERSION; git add -A; git commit -qm one;  git tag v1.8.1
echo 1.9.0 > VERSION; git add -A; git commit -qm two;  git tag v1.9.0
git push -q origin HEAD:refs/heads/main --tags 2>/dev/null
cd "$T/up"
# An installation pinned to 1.8.1 (detached, no local main), like a server installed from a release.
git clone -q remote.git src 2>/dev/null
git -C src -c advice.detachedHead=false checkout -q v1.8.1
git -C src branch -D main >/dev/null 2>&1 || true
mkdir -p "$T/prefix/panel/bin"; : > "$T/prefix/panel/bin/nimvor"; echo 1.8.1 > "$T/prefix/panel/VERSION"
upd() { NIMVOR_SRC="$T/up/src" run update "$@"; }

rm -f "$T/install-args"
out=$(upd --check) || fail "update --check failed: $out"
grep -q 'newer release is available' <<<"$out" || fail "--check does not announce the newer release: $out"
[[ ! -e $T/install-args ]] || fail "--check installed something"
[[ $(git -C src describe --tags) == v1.8.1 ]] || fail "--check moved the checkout"

upd >/dev/null || fail "update from 1.8.1 failed"
[[ $(cat "$T/install-args") == "--reinstall --yes" ]] || fail "installer arguments: $(cat "$T/install-args")"
[[ $(cat "$T/install-version") == 1.9.0 ]] || fail "installer did not run from the newest release: $(cat "$T/install-version")"

echo 1.9.0 > "$T/prefix/panel/VERSION"; rm -f "$T/install-args"
out=$(upd) || fail "update when current failed"
grep -q 'up to date' <<<"$out" || fail "current installation: $out"
[[ ! -e $T/install-args ]] || fail "re-installed although up to date"

echo 1.9.5 > "$T/prefix/panel/VERSION"
out=$(upd) || fail "update of a development build failed"
grep -q 'development build' <<<"$out" || fail "development build: $out"
[[ ! -e $T/install-args ]] || fail "downgraded a development build"
if upd --version 1.8.1 >/dev/null 2>&1; then fail "downgrade without --allow-downgrade"; fi

echo 1.8.1 > "$T/prefix/panel/VERSION"; echo dirty >> "$T/up/src/VERSION"
if upd >/dev/null 2>&1; then fail "updated over local changes"; fi
git -C "$T/up/src" checkout -q -- VERSION

upd --branch main >/dev/null || fail "following a branch failed"
[[ $(git -C "$T/up/src" rev-parse --abbrev-ref HEAD) == main ]] || fail "branch not checked out"
echo "cli wrapper: ok"
