#!/usr/bin/env bash
#
# End-to-end smoke test: builds the agent, runs it against the fixture
# server, starts the panel, signs in over HTTP and checks that the existing
# websites are discovered and displayed. Nothing outside a temp dir is touched.

set -Eeuo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
PORT="${SMOKE_PORT:-18090}"
PIDS=()
cleanup() { for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null || true; done; rm -rf "$WORK"; }
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; [[ -f $WORK/panel.log ]] && tail -5 "$WORK/panel.log" >&2; exit 1; }
pass() { echo "  ok  $*"; }

FIXTURE="$ROOT/agent/tests/fixtures/ubuntu-host"
fixture_sum() { tar -C "$FIXTURE" --sort=name -cf - . | sha256sum; }
BEFORE=$(fixture_sum)
(umask 077; openssl rand -hex 32 > "$WORK/token")
cat > "$WORK/agent.json" <<JSON
{"socket_path":"$WORK/agent.sock","socket_group":"","token_file":"$WORK/token","root_dir":"$FIXTURE","metrics_interval":"1s"}
JSON
cat > "$WORK/panel.php" <<PHP
<?php return ['database'=>['path'=>'$WORK/db.sqlite'],'log'=>['file'=>'$WORK/panel.log'],
'session'=>['path'=>'$WORK/sessions','secure_cookie'=>false],'agent'=>['socket'=>'$WORK/agent.sock','token_file'=>'$WORK/token']];
PHP
export WHACP_CONFIG="$WORK/panel.php"

echo "Building agent"
(cd "$ROOT/agent" && go build -o "$WORK/whacp-agent" ./cmd/whacp-agent)
"$WORK/whacp-agent" -config "$WORK/agent.json" > "$WORK/agent.log" 2>&1 & PIDS+=($!)
for _ in $(seq 1 50); do [[ -S $WORK/agent.sock ]] && break; sleep 0.1; done
[[ -S $WORK/agent.sock ]] || fail "agent socket not created"
pass "agent listening"

T=$(cat "$WORK/token")
code=$(curl -s -o /dev/null -w '%{http_code}' --unix-socket "$WORK/agent.sock" http://agent/v1/health)
[[ $code == 401 ]] || fail "unauthenticated agent request returned $code"
pass "agent rejects requests without token"
code=$(curl -s -o /dev/null -w '%{http_code}' --unix-socket "$WORK/agent.sock" -H "Authorization: Bearer $T" -X POST http://agent/v1/actions/does.not.exist)
[[ $code == 404 ]] || fail "unknown action returned $code"
pass "agent rejects unknown actions"

php "$ROOT/panel/bin/whacp" migrate >/dev/null
echo "correct-horse-staple-42" | php "$ROOT/panel/bin/whacp" user:create smoke smoke@example.test >/dev/null
php "$ROOT/panel/bin/whacp" agent:check >/dev/null || fail "panel cannot reach agent"
pass "panel CLI reaches agent"

php -S "127.0.0.1:$PORT" -t "$ROOT/panel/public" "$ROOT/panel/public/router.php" > "$WORK/php.log" 2>&1 & PIDS+=($!)
for _ in $(seq 1 50); do curl -fs "http://127.0.0.1:$PORT/healthz" >/dev/null && break; sleep 0.1; done
pass "panel listening"

J="$WORK/cookies"
B="http://127.0.0.1:$PORT"
tok=$(curl -s -c "$J" -b "$J" "$B/login" | grep -o 'name="_token" value="[^"]*"' | sed 's/.*value="//;s/"//')
[[ ${#tok} == 64 ]] || fail "no CSRF token on login page"
loc=$(curl -s -o /dev/null -w '%{redirect_url}' -c "$J" -b "$J" --data-urlencode "_token=$tok" -d 'username=smoke&password=correct-horse-staple-42' "$B/login")
[[ $loc == "$B/" ]] || fail "login redirected to '$loc'"
pass "login"

page=$(curl -s -c "$J" -b "$J" "$B/websites")
for d in example.com shop.example.com api.example.com old.example.com; do
    grep -q "$d" <<<"$page" || fail "website $d not listed"
done
grep -q 'PHP 8.3' <<<"$page" || fail "PHP version not shown"
pass "existing websites discovered and listed"

live=$(curl -s -c "$J" -b "$J" -H 'Accept: application/json' "$B/dashboard/live")
grep -q '"success":true' <<<"$live" || fail "live metrics failed: $live"
pass "live metrics"

for secret in super-secret-redis-password S3cretPass abc123def must-not-leak; do
    for p in /cron /databases /discovery; do
        curl -s -c "$J" -b "$J" "$B$p" | grep -q "$secret" && fail "secret '$secret' leaked on $p"
    done
done
pass "no secrets rendered"

# The fixture must be byte-for-byte unchanged (discovery is read-only).
[[ $(fixture_sum) == "$BEFORE" ]] || fail "fixture tree was modified"
pass "fixture server unchanged (contents, modes and mtimes)"
echo "Smoke test passed."
