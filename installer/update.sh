#!/usr/bin/env bash
#
# Update NimVor from its source checkout in one step:
#
#   sudo ./installer/update.sh [install.sh options]
#
# 1. Gives the checkout back to its owner where root left files in it (for
#    example after "sudo git pull"), so updating works again.
# 2. Pulls the latest version as that owner (fast-forward only: local
#    changes are never overwritten; it stops and names them instead).
# 3. Runs install.sh --reinstall --yes from the new version: dependencies,
#    agent build, panel files and its dependencies, database migrations,
#    SELinux policy, services. The configuration, token, database, write
#    mode and the panel's ports (public HTTPS included) are kept, so no
#    options are needed.
#
# Release archives (no .git) are updated with the new release's
# installer/upgrade.sh instead.

set -Eeuo pipefail

# Everything runs from main, which bash reads completely before running:
# git pull may replace this file while it runs.
main() {
    local src owner before after
    src="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
    log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
    ok()   { printf '\033[1;32m  ✓\033[0m %s\n' "$*"; }
    die()  { printf '\033[1;31mError:\033[0m %s\n' "$*" >&2; exit 1; }

    [[ $EUID -eq 0 ]] || die "run as root: sudo $0"
    [[ -d $src/.git ]] || die "$src is not a git checkout; for a release archive, download the new release and run its installer/upgrade.sh"
    [[ -d /opt/whacp && -d /etc/whacp ]] || die "NimVor is not installed here; run installer/install.sh"
    command -v git >/dev/null || die "git is not installed"

    owner=$(stat -c %U "$src")
    as_owner() {
        if [[ $owner == root ]]; then "$@"; else runuser -u "$owner" -- "$@"; fi
    }

    log "Updating the NimVor checkout in $src (owner: $owner)"
    if [[ $owner != root && -n $(find "$src" ! -user "$owner" -print -quit 2>/dev/null) ]]; then
        chown -R "$owner": "$src"
        ok "gave files left by root back to $owner"
    fi
    local changes
    changes=$(as_owner git -C "$src" status --porcelain --untracked-files=no) || die "git cannot read $src as $owner (see above)"
    if [[ -n $changes ]]; then
        as_owner git -C "$src" status --short --untracked-files=no >&2
        die "the checkout has local changes (above); commit or undo them (git -C $src stash), then run this again"
    fi
    before=$(as_owner git -C "$src" rev-parse --short HEAD)
    as_owner git -C "$src" pull --ff-only --quiet || die "git pull failed (see above); nothing was changed"
    after=$(as_owner git -C "$src" rev-parse --short HEAD)
    if [[ $before == "$after" ]]; then
        ok "already at the latest commit ($after); reinstalling to repair anything missing"
    else
        ok "updated $before → $after"
        as_owner git -C "$src" log --oneline --no-decorate "$before..$after" | head -20 | sed 's/^/    /'
    fi

    exec "$src/installer/install.sh" --reinstall --yes "$@"
}

main "$@"
