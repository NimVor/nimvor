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
# A checkout made while the project's default branch had another name keeps tracking that branch. When the
# branch is gone from the remote, follow the remote's current default branch instead of failing.
switch_if_branch_gone() {
    local branch def
    branch=$(as_owner git -C "$src" symbolic-ref -q --short HEAD) || return 0
    if as_owner git -C "$src" ls-remote --exit-code --heads origin "$branch" >/dev/null 2>&1; then
        return 0
    fi
    def=$(as_owner git -C "$src" ls-remote --symref origin HEAD 2>/dev/null | awk '/^ref:/ { sub("refs/heads/", "", $2); print $2; exit }')
    if [[ -z $def || $def == "$branch" ]]; then
        return 0
    fi
    log "The branch \"$branch\" no longer exists upstream; switching to \"$def\""
    as_owner git -C "$src" fetch --quiet origin "$def" || die "could not fetch $def (see above); nothing was changed"
    as_owner git -C "$src" checkout --quiet -B "$def" "origin/$def" || die "could not switch to $def (see above); nothing was changed"
    as_owner git -C "$src" branch --quiet --set-upstream-to="origin/$def" "$def" || true
}

main() {
    local src owner before after
    src="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
    log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
    ok()   { printf '\033[1;32m  ✓\033[0m %s\n' "$*"; }
    die()  { printf '\033[1;31mError:\033[0m %s\n' "$*" >&2; exit 1; }

    [[ $EUID -eq 0 ]] || die "run as root: sudo $0"
    [[ -d $src/.git ]] || die "$src is not a git checkout; for a release archive, download the new release and run its installer/upgrade.sh"
    [[ -d /opt/nimvor && -d /etc/nimvor ]] || die "NimVor is not installed here; run installer/install.sh"
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
    as_owner git -C "$src" symbolic-ref -q HEAD >/dev/null || die "this checkout is pinned to a release (detached HEAD), so there is no branch to pull. To change release, run the install command again: curl -fsSL https://nimvor.com/install | sudo bash -s -- [--version X]"
    switch_if_branch_gone
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
