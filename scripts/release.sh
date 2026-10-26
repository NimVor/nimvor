#!/usr/bin/env bash
#
# Build release archives: dist/release/whacp-<version>-linux-<arch>.tar.gz
# with prebuilt whacp-agent + whacp (server) binaries, plus SHA256SUMS.
# Archives are reproducible for a given commit (fixed owners, order and
# timestamps). Run from a clean checkout:
#
#   scripts/release.sh            # amd64 and arm64
#   ARCHES=amd64 scripts/release.sh
set -Eeuo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
VERSION=$(cat VERSION)
[[ $VERSION =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || { echo "invalid VERSION: $VERSION" >&2; exit 1; }
[[ -z $(git status --porcelain --untracked-files=no) ]] || { echo "the working tree has uncommitted changes" >&2; exit 1; }
ARCHES=${ARCHES:-amd64 arm64}
OUT=$ROOT/dist/release
export SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct)}
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$OUT"
rm -f "$OUT"/whacp-"$VERSION"-*.tar.gz "$OUT/SHA256SUMS"

# Tracked files only, so nothing local leaks into a release.
mkdir -p "$WORK/src"
git archive --format=tar HEAD | tar -x -C "$WORK/src"
rm -rf "$WORK/src/.github"
# Tests, fixtures (which contain sample credentials) and development files
# are not part of a release.
rm -rf "$WORK/src/tests" "$WORK/src/agent/tests" "$WORK/src/server/*/testdata" "$WORK/src/scripts/dev.sh"
find "$WORK/src" -name '*_test.go' -delete

for arch in $ARCHES; do
    name=whacp-$VERSION
    stage=$WORK/$arch/$name
    mkdir -p "$WORK/$arch"
    cp -a "$WORK/src" "$stage"
    (cd agent && CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -buildvcs=false \
        -ldflags "-s -w -X main.version=$VERSION" -o "$stage/dist/whacp-agent" ./cmd/whacp-agent)
    (cd server && CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -buildvcs=false \
        -ldflags "-s -w -X main.version=$VERSION" -o "$stage/dist/whacp" ./cmd/whacp)
    if [[ $arch == "$(go env GOARCH)" ]]; then
        got=$("$stage/dist/whacp-agent" -version)
        [[ $got == *"$VERSION"* ]] || { echo "agent reports '$got', expected $VERSION" >&2; exit 1; }
    fi
    tar -C "$WORK/$arch" --sort=name --owner=0 --group=0 --numeric-owner \
        --mtime="@$SOURCE_DATE_EPOCH" --pax-option=exthdr.name=%d/PaxHeaders/%f,delete=atime,delete=ctime \
        -cf - "$name" | gzip -n -9 > "$OUT/$name-linux-$arch.tar.gz"
    echo "built dist/release/$name-linux-$arch.tar.gz"
done
# Refuse to publish anything that looks like a secret, a test or a local file.
for f in "$OUT"/whacp-"$VERSION"-linux-*.tar.gz; do
    listing=$(tar -tzf "$f")
    if grep -qE '/(tests|\.dev|\.git|node_modules)/|_test\.go$|phpunit|\.sqlite$|\.env$|/dist/release/' <<<"$listing"; then
        echo "$f contains test or development files:" >&2
        grep -E '/(tests|\.dev|\.git|node_modules)/|_test\.go$|phpunit|\.sqlite$|\.env$|/dist/release/' <<<"$listing" | head >&2
        exit 1
    fi
    if tar -xzOf "$f" --wildcards '*.pem' '*.key' '*.token' 2>/dev/null | grep -q 'PRIVATE KEY\|[0-9a-f]\{64\}'; then
        echo "$f contains a private key or token file" >&2
        exit 1
    fi
done
(cd "$OUT" && sha256sum whacp-"$VERSION"-linux-*.tar.gz > SHA256SUMS && cat SHA256SUMS)
