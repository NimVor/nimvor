#!/usr/bin/env bash
# NimVor bootstrap: the script served at https://nimvor.com/install.
#
#   curl -fsSL https://nimvor.com/install | sudo bash
#   curl -fsSL https://nimvor.com/install | sudo bash -s -- --yes --enable-writes
#
# It installs git and curl when missing, fetches the NimVor source from
# GitHub (https://github.com/NimVor/nimvor) into /usr/local/src/nimvor and
# runs installer/install.sh from it, passing every argument it does not
# know itself through. Running it again updates the checkout (fast-forward
# only) and re-runs the installer. The panel and agent are the ones in that
# checkout; nothing else is downloaded by this script.
#
# Options (before the installer's own, or after `--`):
#   --version X      install the release tag vX (default: the newest tag,
#                    or the main branch when there are no tags yet)
#   --branch NAME    install a branch instead
#   --commit SHA     require the checkout to be exactly this commit
#   --source URL     fetch from another git URL (a mirror)
#   --dir PATH       keep the source checkout here (default /usr/local/src/nimvor)
#   --fetch-only     fetch the source, print the install command, stop
#   -h, --help       this text
#
# Environment: NIMVOR_REPO, NIMVOR_DIR, NIMVOR_VERSION, NIMVOR_BRANCH
# set the same things.
set -euo pipefail

REPO=${NIMVOR_REPO:-https://github.com/NimVor/nimvor.git}
DIR=${NIMVOR_DIR:-/usr/local/src/nimvor}
VERSION=${NIMVOR_VERSION:-}
BRANCH=${NIMVOR_BRANCH:-}
COMMIT=
FETCH_ONLY=0
PASS=()

say() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31mError:\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
    cat <<'TXT'
Usage: curl -fsSL https://nimvor.com/install | sudo bash -s -- [bootstrap options] [installer options]

  --version X      install release vX (default: the newest tag, else the main branch)
  --branch NAME    install a branch instead
  --commit SHA     require the checkout to be exactly this commit
  --source URL     fetch from another git URL (a mirror)
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
[[ -n $VERSION && -n $BRANCH ]] && die "use either --version or --branch, not both."
[[ -z $VERSION || $VERSION =~ ^v?[0-9]+(\.[0-9]+){1,3}([-.][0-9A-Za-z.]+)?$ ]] || die "--version: use a version like 1.0.0"
[[ -z $BRANCH || $BRANCH =~ ^[A-Za-z0-9._/-]{1,100}$ ]] || die "--branch: not a valid branch name."
[[ -z $COMMIT || $COMMIT =~ ^[0-9a-f]{7,40}$ ]] || die "--commit: use a hexadecimal commit id."
[[ $REPO =~ ^(https://|ssh://|git@|/|file://) ]] || die "--source: use an https:// or ssh git URL."
[[ $DIR == /* ]] || die "--dir: use an absolute path."

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

# What to check out: a branch, a version tag, the newest tag, or main.
ref=
if [[ -n $BRANCH ]]; then
    ref=$BRANCH
elif [[ -n $VERSION ]]; then
    ref=v${VERSION#v}
else
    ref=$(git ls-remote --tags --refs "$REPO" 'v[0-9]*' 2>/dev/null | awk '{sub("refs/tags/","",$2); print $2}' | sort -V | tail -n1 || true)
    [[ -n $ref ]] || ref=main
fi

if [[ -d $DIR/.git ]]; then
    say "Updating the source in $DIR ($ref)"
    git -C "$DIR" diff --quiet && git -C "$DIR" diff --cached --quiet || die "$DIR has local changes; commit or remove them, or use --dir."
    git -C "$DIR" remote set-url origin "$REPO"
    git -C "$DIR" fetch --quiet --tags --depth 50 origin "$ref" || die "could not fetch $ref from $REPO."
    git -C "$DIR" checkout --quiet --detach FETCH_HEAD
elif [[ -e $DIR ]] && [[ -n $(ls -A "$DIR" 2>/dev/null) ]]; then
    die "$DIR exists and is not a NimVor checkout; use --dir."
else
    say "Fetching NimVor ($ref) from $REPO"
    mkdir -p "$DIR"
    git -C "$DIR" init --quiet
    git -C "$DIR" remote add origin "$REPO"
    git -C "$DIR" fetch --quiet --tags --depth 50 origin "$ref" || { rm -rf "$DIR"; die "could not fetch $ref from $REPO."; }
    git -C "$DIR" checkout --quiet --detach FETCH_HEAD
fi

head=$(git -C "$DIR" rev-parse HEAD)
if [[ -n $COMMIT && $head != "$COMMIT"* ]]; then
    die "the source is at ${head:0:12}, not the commit you asked for ($COMMIT). Nothing was installed."
fi
say "Source at ${head:0:12} (${ref})"
[[ -x $DIR/installer/install.sh ]] || die "$DIR/installer/install.sh is missing: this is not a NimVor source tree."

if ((FETCH_ONLY)); then
    say "Fetched. Install with: sudo $DIR/installer/install.sh ${PASS[*]:-}"
    exit 0
fi
say "Running the installer"
cd "$DIR"
exec ./installer/install.sh ${PASS[@]+"${PASS[@]}"}
