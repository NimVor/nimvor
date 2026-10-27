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
'session'=>['path'=>'$WORK/sessions','secure_cookie'=>false],'agent'=>['socket'=>'$WORK/agent.sock','token_file'=>'$WORK/token'],
'security'=>['key_file'=>'$WORK/panel.key']];
PHP
export NIMVOR_CONFIG="$WORK/panel.php"

echo "Building agent"
(cd "$ROOT/agent" && go build -o "$WORK/nimvor-agent" ./cmd/nimvor-agent)
"$WORK/nimvor-agent" -config "$WORK/agent.json" > "$WORK/agent.log" 2>&1 & PIDS+=($!)
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

php "$ROOT/panel/bin/nimvor" migrate >/dev/null
echo "correct-horse-staple-42" | php "$ROOT/panel/bin/nimvor" user:create smoke smoke@example.test >/dev/null
php "$ROOT/panel/bin/nimvor" agent:check >/dev/null || fail "panel cannot reach agent"
doctor=$(php "$ROOT/panel/bin/nimvor" doctor) || fail "doctor reports failures: $doctor"
grep -q '^\[PASS\] Agent ' <<<"$doctor" || fail "doctor: agent check: $doctor"
pass "doctor self-checks"
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

# Remote server: a second agent listening on TLS, reached through the
# panel's pinned transport (no socket), and a wrong pin must be refused.
RPORT="${SMOKE_REMOTE_PORT:-18443}"
(umask 077; openssl rand -hex 32 > "$WORK/token2")
cat > "$WORK/agent2.json" <<JSON
{"socket_path":"$WORK/agent2.sock","socket_group":"","token_file":"$WORK/token2","root_dir":"$FIXTURE","metrics_interval":"1s",
 "listen_tcp":"127.0.0.1:$RPORT","allowed_clients":["127.0.0.1"],"tls_cert":"$WORK/agent2.crt","tls_key":"$WORK/agent2.key"}
JSON
PIN=$("$WORK/nimvor-agent" -config "$WORK/agent2.json" -pin)
[[ $PIN == sha256//* ]] || fail "no pin printed: $PIN"
"$WORK/nimvor-agent" -config "$WORK/agent2.json" > "$WORK/agent2.log" 2>&1 & PIDS+=($!)
for _ in $(seq 1 50); do curl -sk -o /dev/null "https://127.0.0.1:$RPORT/v1/health" && break; sleep 0.1; done
cat > "$WORK/remote.php" <<'PHP'
<?php
require $argv[1] . '/panel/vendor/autoload.php';
$app = NimVor\Core\Application::fromEnvironment();
$c = $app->container;
$c->get(NimVor\Services\AuditLogger::class)->setActor(null, 'smoke');
$svc = $c->get(NimVor\Services\ServerService::class);
try {
    $svc->add(['name' => 'bad-pin', 'url' => $argv[2], 'pin' => 'sha256//AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=', 'token' => $argv[4]]);
    fwrite(STDERR, "wrong pin accepted\n");
    exit(1);
} catch (NimVor\Agent\AgentException $e) {
    if ($e->errorCode() !== 'agent_pin_mismatch') { fwrite(STDERR, 'unexpected: ' . $e->errorCode() . "\n"); exit(1); }
}
$id = $svc->add(['name' => 'remote', 'url' => $argv[2], 'pin' => $argv[3], 'token' => $argv[4]]);
$ctx = $c->get(NimVor\Services\ServerContext::class);
$ctx->force($id);
$info = $c->get(NimVor\Agent\AgentClient::class)->call('system.info');
$ctx->force(1);
echo $id, ' ', $info['agent_version'] ?? '?', "\n";
PHP
rejected_before=$(grep -c "rejected request with invalid token" "$WORK/agent2.log" || true)
out=$(php "$WORK/remote.php" "$ROOT" "https://127.0.0.1:$RPORT" "$PIN" "$(cat "$WORK/token2")") || fail "remote server: $out"
[[ $out == "2 "* ]] || fail "remote server: $out"
pass "remote agent over pinned TLS (wrong pin refused before sending the token)"
rejected_after=$(grep -c "rejected request with invalid token" "$WORK/agent2.log" || true)
[[ $rejected_before == "$rejected_after" ]] || fail "a request reached the agent during the wrong-pin attempt"
pass "no token sent to an unpinned peer"

# The fixture must be byte-for-byte unchanged (discovery is read-only).
[[ $(fixture_sum) == "$BEFORE" ]] || fail "fixture tree was modified"
pass "fixture server unchanged (contents, modes and mtimes)"
echo "Smoke test passed."
