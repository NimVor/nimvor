#!/usr/bin/env bash
# NimVor bootstrap: the script served at https://nimvor.com/install.
#
#   curl -fsSL https://nimvor.com/install | sudo bash
#   curl -fsSL https://nimvor.com/install | sudo bash -s -- --yes --enable-writes
#
# It installs git and curl when missing, fetches the NimVor source from GitHub
# (https://github.com/NimVor/nimvor) into /usr/local/src/nimvor and runs
# installer/install.sh from it, passing every argument it does not know
# itself through. Run it again to update: the checkout is fast-forwarded and
# the installer re-runs. Nothing else is downloaded by this script.
#
# What it trusts: the HTTPS connection to nimvor.com (this script) and to
# GitHub (the source). Pin what you install with --commit SHA (the install
# stops unless the fetched source is exactly that commit) or review the source
# before running it: --fetch-only fetches and stops.
set -euo pipefail
umask 022
export GIT_TERMINAL_PROMPT=0

REPO=${NIMVOR_REPO:-https://github.com/NimVor/nimvor.git}
DIR=${NIMVOR_DIR:-/usr/local/src/nimvor}
VERSION=${NIMVOR_VERSION:-}
BRANCH=${NIMVOR_BRANCH:-}
COMMIT=
FETCH_ONLY=0
PASS=()

say()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mWarning:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mError:\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
    cat <<'TXT'
Usage: curl -fsSL https://nimvor.com/install | sudo bash -s -- [bootstrap options] [installer options]

  --version X      install release vX (default: the newest stable release tag)
  --branch NAME    install a branch instead (development; follows its newest commit)
  --commit SHA     require the fetched source to be exactly this commit
  --source URL     fetch from another https:// or ssh git URL (a mirror)
  --dir PATH       keep the source here (default /usr/local/src/nimvor)
  --fetch-only     fetch the source and stop
  -h, --help       this text

Anything else is passed to installer/install.sh (for example --yes --enable-writes).
TXT
}

while (($#)); do
    case $1 in
        --version) VERSION=${2:?--version needs a value}; shift 2 ;;
        --branch)  BRANCH=${2:?--branch needs a value}; shift 2 ;;
        --commit)  COMMIT=${2:?--commit needs a value}; shift 2 ;;
        --source)  REPO=${2:?--source needs a URL}; shift 2 ;;
        --dir)     DIR=${2:?--dir needs a path}; shift 2 ;;
        --fetch-only) FETCH_ONLY=1; shift ;;
        -h|--help) usage; exit 0 ;;
        --) shift; PASS+=("$@"); break ;;
        *) PASS+=("$1"); shift ;;
    esac
done

[[ $(uname -s) == Linux ]] || die "NimVor installs on Linux servers only."
[[ $EUID -eq 0 ]] || die "run this as root: curl -fsSL https://nimvor.com/install | sudo bash"
[[ -z $VERSION || -z $BRANCH ]] || die "use either --version or --branch, not both."
[[ -z $VERSION || $VERSION =~ ^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || die "--version: use a version like 1.0.0"
[[ -z $BRANCH || ( $BRANCH =~ ^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$ && $BRANCH != *..* ) ]] || die "--branch: not a valid branch name."
[[ -z $COMMIT || $COMMIT =~ ^[0-9a-f]{7,40}$ ]] || die "--commit: use a hexadecimal commit id (at least 7 characters)."
[[ $DIR == /* && $DIR != / ]] || die "--dir: use an absolute path."
if [[ ${NIMVOR_ALLOW_LOCAL_SOURCE:-} == 1 ]]; then
    [[ $REPO =~ ^(https://|ssh://|git@|/) ]] || die "--source: use an https:// or ssh git URL."
else
    [[ $REPO =~ ^(https://|ssh://git@|git@)[A-Za-z0-9] ]] || die "--source: use an https:// or ssh git URL."
fi

install_tools() {
    local need=()
    command -v git  >/dev/null || need+=(git)
    command -v curl >/dev/null || need+=(curl)
    [[ -e /etc/ssl/certs/ca-certificates.crt || -e /etc/pki/tls/certs/ca-bundle.crt ]] || need+=(ca-certificates)
    ((${#need[@]})) || return 0
    say "Installing ${need[*]}"
    if command -v apt-get >/dev/null; then
        DEBIAN_FRONTEND=noninteractive apt-get update -qq
        DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${need[@]}"
    elif command -v dnf >/dev/null; then
        dnf install -y -q "${need[@]}"
    else
        die "install ${need[*]} first (no apt-get or dnf found)."
    fi
}
install_tools

# The source is run as root, so the folder must be root's alone.
check_dir_safe() {
    local d=$1 uid mode
    uid=$(stat -c %u "$d") || die "cannot read $d"
    mode=$(stat -c %a "$d")
    [[ $uid -eq 0 ]] || die "$d is not owned by root; refusing to run code from it. Remove it or use --dir."
    (( (8#$mode & 8#022) == 0 )) || die "$d can be written by other accounts (mode $mode); refusing to run code from it."
    [[ ! -L $d ]] || die "$d is a symbolic link; refusing to use it."
}

# What to fetch: a release tag (the newest stable one by default) or a branch.
kind= ref=
if [[ -n $BRANCH ]]; then
    kind=branch ref=$BRANCH
    warn "installing the branch '$BRANCH': it follows the newest commit and is not a release."
elif [[ -n $VERSION ]]; then
    kind=tag ref=v${VERSION#v}
else
    tags=$(git ls-remote --tags --refs "$REPO" 'v[0-9]*') || die "could not list the releases of $REPO (network or address problem)."
    tags=$(awk '{sub("refs/tags/","",$2); print $2}' <<<"$tags")
    ref=$(grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' <<<"$tags" | sort -V | tail -n1 || true)
    if [[ -z $ref ]]; then
        ref=$(sort -V <<<"$tags" | tail -n1)
        [[ -n $ref ]] && warn "no stable release yet: installing the pre-release $ref."
    fi
    if [[ -n $ref ]]; then
        kind=tag
    else
        warn "no release has been published yet: installing the main branch (a development version)."
        kind=branch ref=main
    fi
fi

fetch() {
    if [[ $kind == tag ]]; then
        git -C "$DIR" fetch --quiet --depth 50 --no-recurse-submodules origin "+refs/tags/$ref:refs/tags/$ref" || return 1
        git -C "$DIR" -c advice.detachedHead=false checkout --quiet --detach "refs/tags/$ref^{commit}"
    else
        git -C "$DIR" fetch --quiet --depth 50 --no-recurse-submodules origin "+refs/heads/$ref:refs/remotes/origin/$ref" || return 1
        # On a branch that tracks origin, so installer/update.sh can pull it.
        git -C "$DIR" checkout --quiet -B "$ref" "refs/remotes/origin/$ref"
        git -C "$DIR" branch --quiet --set-upstream-to="origin/$ref" "$ref"
    fi
}

if [[ -d $DIR/.git ]]; then
    check_dir_safe "$DIR"; check_dir_safe "$DIR/.git"
    say "Updating the source in $DIR ($ref)"
    git -C "$DIR" diff --quiet && git -C "$DIR" diff --cached --quiet || die "$DIR has local changes; commit or remove them, or use --dir."
    git -C "$DIR" remote set-url origin "$REPO"
    fetch || die "could not fetch $ref from $REPO."
elif [[ -e $DIR ]] && [[ -n $(ls -A "$DIR" 2>/dev/null) ]]; then
    die "$DIR exists and is not a NimVor checkout; use --dir."
else
    say "Fetching NimVor ($ref) from $REPO"
    mkdir -p "$DIR" && chmod 755 "$DIR"
    check_dir_safe "$DIR"
    git -C "$DIR" init --quiet
    git -C "$DIR" remote add origin "$REPO"
    fetch || { rm -rf "$DIR"; die "could not fetch $ref from $REPO."; }
fi

head=$(git -C "$DIR" rev-parse HEAD)
if [[ -n $COMMIT && $head != "$COMMIT"* ]]; then
    die "the source is at ${head:0:12}, not the commit you asked for ($COMMIT). Nothing was installed."
fi
say "Source: $ref at ${head:0:12}"
[[ -f $DIR/installer/install.sh ]] || die "$DIR/installer/install.sh is missing: this is not a NimVor source tree."
chmod 755 "$DIR/installer/install.sh" 2>/dev/null || true

if ((FETCH_ONLY)); then
    say "Fetched. Review it, then run: sudo $DIR/installer/install.sh ${PASS[*]:-}"
    exit 0
fi
say "Running the installer"
cd "$DIR"
exec ./installer/install.sh ${PASS[@]+"${PASS[@]}"}
